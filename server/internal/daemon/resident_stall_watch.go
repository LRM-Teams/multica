package daemon

import (
	"time"
)

const residentStallSettleGrace = 5 * time.Second

// startResidentStallWatchdog applies the Raft stalled-run rule to the
// canonical Message path. A resident turn is allowed to run indefinitely
// while it produces provider activity. Once it is silent, the watchdog first
// confirms that the provider process is actually dead and waits a bounded
// settle grace before fencing and force-killing the turn. The Message remains
// owned by the coordinator until the turn completion path releases it, so the
// next delivery can recreate the resident session without inventing a second
// spawn path.
//
// Staleness is read from slot.silentFor, the single activity clock shared
// with recoverStalledSlotForQueuedMessage (resident_stall_queued_recovery.go)
// — both policies watch the same lastRuntimeActivityAt stamp and differ only
// in what they do about it: this one suppresses on a confirmed-alive process
// and only ever force-invalidates a confirmed-dead one; the queued-message
// path never checks liveness at all.
func (p *canonicalAgentRuntimePool) startResidentStallWatchdog(
	agentID, runtimeID string,
	slot *canonicalAgentRuntimeSlot,
	turnDone <-chan struct{},
) {
	if p == nil || p.residentStallWatchdog <= 0 || slot == nil {
		return
	}
	window := p.residentStallWatchdog
	tick := window / 2
	if tick <= 0 {
		tick = time.Millisecond
	}
	go func() {
		ticker := time.NewTicker(tick)
		defer ticker.Stop()
		var deadSince time.Time
		stalledPublished := false
		for {
			select {
			case <-turnDone:
				return
			case <-ticker.C:
			}

			idleFor, known := slot.silentFor(time.Now())
			if !known || idleFor < window {
				deadSince = time.Time{}
				stalledPublished = false
				continue
			}
			if !stalledPublished {
				stalledPublished = true
				if p.residentStallObserver != nil {
					p.residentStallObserver(agentID, runtimeID, idleFor)
				}
			}

			alive, liveKnown := false, false
			slot.mu.Lock()
			if liveness, ok := slot.backend.(interface{ RuntimeAlive() (bool, bool) }); ok {
				alive, liveKnown = liveness.RuntimeAlive()
			}
			slot.mu.Unlock()
			// Silence alone is not enough: a live Pi may be running a long
			// tool. Only a confirmed-dead provider enters recovery.
			if !liveKnown || alive {
				deadSince = time.Time{}
				continue
			}
			if deadSince.IsZero() {
				deadSince = time.Now()
				continue
			}
			if time.Since(deadSince) < residentStallSettleGrace {
				continue
			}
			if err := p.forceInvalidateSession(agentID, runtimeID); err == nil {
				return
			}
		}
	}()
}
