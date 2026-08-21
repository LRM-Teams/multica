package daemon

import (
	"log/slog"
	"time"
)

// residentProcessEventKind enumerates the three resident-process facts the
// canonicalAgentRuntimePool observes about a slot's underlying provider
// process. "spawned"/"ready" are still pushed by the Workspace Runner
// orchestrator directly (workspace_runner_agent_process.go) and are
// deliberately not part of this bus.
type residentProcessEventKind string

const (
	residentProcessExited    residentProcessEventKind = "exited"
	residentProcessRecovered residentProcessEventKind = "recovered"
	residentProcessStalled   residentProcessEventKind = "stalled"
)

// residentProcessEvent is the single fact type carried by every resident
// process channel out of canonicalAgentRuntimePool. It replaces the previous
// pair of parallel channels (ResidentRuntimeCrashEvent's crash subscribers
// and the recovered/stalled callbacks) with one event shape and one
// subscription point.
type residentProcessEvent struct {
	AgentID   string
	RuntimeID string
	Kind      residentProcessEventKind
	Provider  string        // exited only, where known
	SilentFor time.Duration // stalled only
	At        time.Time
}

// subscribeResidentProcess registers fn to run for every future resident
// process event, in registration order relative to other subscribers. It
// does not replay past events.
func (p *canonicalAgentRuntimePool) subscribeResidentProcess(fn func(residentProcessEvent)) {
	if p == nil || fn == nil {
		return
	}
	p.residentProcessMu.Lock()
	defer p.residentProcessMu.Unlock()
	p.residentProcessSubscribers = append(p.residentProcessSubscribers, fn)
}

// emitResidentProcessEvent delivers ev to every subscriber registered at the
// time of the call, sequentially, in registration order. residentProcessMu
// guards only the subscriber-list read — it is released before any
// subscriber runs. Delivery itself is deliberately unlocked: the exited
// route's subscriber chain runs crash backoff bookkeeping, then
// agentProcessManager.ProcessExited (which takes APM's own mutex and can
// reach back into this pool's p.mu/slot.mu through admission), then a
// blocking HTTP POST to report the crash to the server. Holding
// residentProcessMu across that chain would block every other emitter — the
// 20s liveness sweep, every per-turn stall watchdog goroutine, and acquire's
// recovered notice — for the duration of a slow or hung request, and would
// stack a residentProcessMu -> APM mu -> p.mu -> slot.mu lock chain across
// network I/O. So this only guarantees sequential, panic-isolated delivery
// of one event to its subscribers in order; two different events emitted
// concurrently may be delivered to subscribers concurrently with each
// other. That is safe because every subscriber here already guards its own
// state (agentProcessManager has its own mutex, the crash-backoff tracker
// has its own mutex).
//
// emitResidentProcessEvent itself never holds p.mu or slot.mu, and by the
// time a subscriber runs it holds no pool lock at all — every call site
// unlocks p.mu/slot.mu before calling in (see checkResidentLiveness,
// acquire, startResidentStallWatchdog, recoverStalledSlotForQueuedMessage),
// and that now also matters for lock ordering, not just for avoiding
// self-deadlock on this pool. A subscriber that panics is recovered and
// logged so it can neither kill the emitter nor prevent later subscribers
// from receiving the event.
func (p *canonicalAgentRuntimePool) emitResidentProcessEvent(ev residentProcessEvent) {
	if p == nil {
		return
	}
	p.residentProcessMu.Lock()
	subscribers := make([]func(residentProcessEvent), len(p.residentProcessSubscribers))
	copy(subscribers, p.residentProcessSubscribers)
	p.residentProcessMu.Unlock()

	for _, sub := range subscribers {
		deliverResidentProcessEvent(sub, ev)
	}
}

func deliverResidentProcessEvent(sub func(residentProcessEvent), ev residentProcessEvent) {
	defer func() {
		if r := recover(); r != nil {
			slog.Default().Error("resident process event subscriber panicked",
				"recovered", r, "kind", ev.Kind, "agent_id", ev.AgentID, "runtime_id", ev.RuntimeID)
		}
	}()
	sub(ev)
}
