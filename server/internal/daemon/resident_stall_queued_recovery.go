package daemon

import (
	"errors"
	"strings"
	"time"
)

// recoverStalledSlotForQueuedMessage is the port of Raft 1.0.17's
// recoverStaleProcessForQueuedMessageIfNeeded onto the canonical resident
// pool. It closes a gap the existing stall watchdog
// (resident_stall_watch.go) cannot cover: the watchdog only runs while a
// Message turn is in flight (it starts after AcceptMessageBatch succeeds and
// dies on <-turnDone) and it deliberately refuses to act unless the provider
// process is CONFIRMED DEAD. A process that is alive but has stopped reading
// its control channel — the actual failure this recovers — never trips the
// watchdog and never gets force-killed, so queued Messages redeliver every
// ~20s forever with nothing ever restarting the wedged provider.
//
// This method runs on the deferred-delivery path instead: every time a
// Message is queued because the slot is busy (ErrCanonicalAgentRuntimeBusy),
// the caller asks whether the runtime has been silent past the stall window
// and, if so, terminates it for restart. It deliberately does NOT check
// process liveness — that check is what makes the existing watchdog useless
// here, since a hung-but-alive process is exactly the case that needs
// killing. Liveness-gated recovery remains the watchdog's job for the
// in-flight-turn case; this is the idle-queue-side complement.
//
// The tool-call and compacting gates exist because both are legitimate
// reasons for a live resident to be silent on its control channel for a long
// time: a long-running tool call, or an in-progress context compaction. A
// silence-only heuristic would kill those runtimes mid-work.
func (p *canonicalAgentRuntimePool) recoverStalledSlotForQueuedMessage(agentID, runtimeID string) (bool, error) {
	if p == nil {
		return false, nil
	}
	if p.residentStallWatchdog <= 0 {
		return false, nil
	}
	agentID = strings.TrimSpace(agentID)
	runtimeID = strings.TrimSpace(runtimeID)
	if agentID == "" || runtimeID == "" {
		return false, errors.New("canonical runtime agent_id and runtime_id are required")
	}
	key := agentID + "\x00" + runtimeID

	p.mu.Lock()
	slot := p.slots[key]
	if slot == nil {
		p.mu.Unlock()
		return false, nil
	}
	slot.mu.Lock()
	p.mu.Unlock()

	if slot.backend == nil {
		slot.mu.Unlock()
		return false, nil
	}
	if slot.stalledRecovering || slot.invalidateAfterInput {
		slot.mu.Unlock()
		return false, nil
	}
	if slot.compacting {
		slot.mu.Unlock()
		return false, nil
	}
	if len(slot.outstandingToolCalls) > 0 {
		slot.mu.Unlock()
		return false, nil
	}
	staleFor, due := slot.stalledRecoveryDueLocked(p.residentStallWatchdog)
	if !due {
		slot.mu.Unlock()
		return false, nil
	}
	slot.stalledRecovering = true
	// Release slot.mu before beginResidentTermination: it re-takes p.mu then
	// slot.mu itself, and holding slot.mu here would deadlock against that.
	slot.mu.Unlock()

	// Run async: this executes on the synchronous deferred-delivery path and
	// emission may run subscriber I/O (observeResidentRuntimeStalled
	// publishes Activity). Never block delivery on it, and never call it
	// while holding slot.mu.
	go p.emitResidentProcessEvent(residentProcessEvent{
		AgentID: agentID, RuntimeID: runtimeID, Kind: residentProcessStalled,
		SilentFor: staleFor, At: time.Now(),
	})

	if err := p.beginResidentTermination(agentID, runtimeID); err != nil {
		slot.mu.Lock()
		slot.stalledRecovering = false
		slot.mu.Unlock()
		return false, err
	}
	// stalledRecovering is cleared only by closeBackend(), reached through
	// both branches of beginResidentTermination (the idle close-now path and the
	// force-killed turn's eventual finishResidentMessageInput/failResident-
	// MessageInputAttempt teardown). No second clearing path is added here.
	return true, nil
}

// stalledRecoveryDueLocked reports whether this slot has been silent for at
// least window and, if so, for how long. slot.mu must already be held. It
// reads the shared silentForLocked clock (agent_runtime_pool.go) rather than
// lastRuntimeActivityAt directly, so this stays the only staleness math for
// both recovery policies. "Unknown" silence (no activity stamp yet) never
// recovers — we only ever kill a runtime whose silence we can measure.
func (slot *canonicalAgentRuntimeSlot) stalledRecoveryDueLocked(window time.Duration) (time.Duration, bool) {
	staleFor, known := slot.silentForLocked(time.Now())
	if !known || staleFor < window {
		return staleFor, false
	}
	return staleFor, true
}
