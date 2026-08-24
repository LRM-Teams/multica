package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// waitForRuntimeSlot implements #35 for resident acquires:
//  1. Close idle other-runtime slots for this agent (rebind → one live process).
//  2. If this agent already has a live backend (or a pending create), no new
//     cap unit is needed.
//  3. Otherwise wait until live+pending < max, preferring idle-LRU eviction
//     over blocking; never evict a running turn.
//
// One-shot callers must not call this (they do not retain pool backends).
func (p *agentRuntimePool) waitForRuntimeSlot(ctx context.Context, agentID, runtimeID string) error {
	if p == nil {
		return fmt.Errorf("canonical agent runtime pool is nil")
	}
	agentID = strings.TrimSpace(agentID)
	runtimeID = strings.TrimSpace(runtimeID)
	if agentID == "" {
		return fmt.Errorf("agent process capacity requires agent_id")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	for {
		// Rebind: drop idle live processes on other runtimes for this agent.
		if n := p.closeIdleOtherRuntimesLocked(agentID, runtimeID); n > 0 {
			p.evictForCapTotal.Add(int64(n))
		}

		if p.agentCountsTowardCapLocked(agentID) {
			// Already live or already reserved by this/another in-flight create.
			return nil
		}

		max := p.maxAgentProcesses
		if max <= 0 {
			// Unlimited: still track pending so concurrent creates stay honest
			// if a cap is set later mid-flight (rare).
			p.pendingAgents[agentID] = struct{}{}
			return nil
		}

		if p.countCapAgentsLocked() < max {
			p.pendingAgents[agentID] = struct{}{}
			return nil
		}

		// Prefer freeing an idle resident over waiting on running turns.
		if p.evictOldestIdleForCapacityLocked(agentID) {
			p.evictForCapTotal.Add(1)
			continue
		}

		// All counted agents are running (or pending). Queue until a release
		// frees capacity or ctx cancels.
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("agent process capacity wait: %w", err)
		}
		// Wake Wait when ctx is done.
		stop := context.AfterFunc(ctx, func() {
			p.mu.Lock()
			p.capacityCond.Broadcast()
			p.mu.Unlock()
		})
		p.capacityCond.Wait()
		stop()
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("agent process capacity wait: %w", err)
		}
	}
}

func (p *agentRuntimePool) cancelRuntimeSlotWait(agentID string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return
	}
	if _, ok := p.pendingAgents[agentID]; ok {
		delete(p.pendingAgents, agentID)
		p.capacityCond.Broadcast()
	}
}

// finishRuntimeSlotWait drops a successful create's pending entry.
func (p *agentRuntimePool) finishRuntimeSlotWait(agentID string) {
	p.cancelRuntimeSlotWait(agentID)
}

// signalAgentProcessCapacityFreed publishes live count and wakes waiters after
// a live backend was closed (unhealthy release, cap eviction, rebind, invalidate).
func (p *agentRuntimePool) signalRuntimeSlotAvailable() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.publishLiveAgentProcessCountLocked()
	p.capacityCond.Broadcast()
	p.mu.Unlock()
}

func (p *agentRuntimePool) publishLiveAgentProcessCount() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.publishLiveAgentProcessCountLocked()
}

func (p *agentRuntimePool) publishLiveAgentProcessCountLocked() {
	p.liveAgentProcesses.Store(int64(p.countLiveAgentsLocked()))
}

// agentCountsTowardCapLocked is true when an Agent already has a live backend
// or pending create. Caller must hold p.mu.
func (p *agentRuntimePool) agentCountsTowardCapLocked(agentID string) bool {
	if _, ok := p.pendingAgents[agentID]; ok {
		return true
	}
	if p.agentHasLiveBackendLocked(agentID) {
		return true
	}
	return false
}

// countCapAgentsLocked is the physical-cap accounting set: an Agent is
// counted once even when its managed launch and resident backend coexist.
// Caller must hold p.mu.
func (p *agentRuntimePool) countCapAgentsLocked() int {
	agents := make(map[string]struct{})
	for agentID := range p.pendingAgents {
		agents[agentID] = struct{}{}
	}
	for key, slot := range p.slots {
		slot.mu.Lock()
		live := slot.backend != nil
		slot.mu.Unlock()
		if live {
			agentID, _ := splitCanonicalSlotKey(key)
			if agentID != "" {
				agents[agentID] = struct{}{}
			}
		}
	}
	return len(agents)
}

// agentHasLiveBackendLocked reports whether any slot for agentID holds a
// resident backend process. Caller must hold p.mu.
func (p *agentRuntimePool) agentHasLiveBackendLocked(agentID string) bool {
	for key, slot := range p.slots {
		aid, _ := splitCanonicalSlotKey(key)
		if aid != agentID {
			continue
		}
		slot.mu.Lock()
		live := slot.backend != nil
		slot.mu.Unlock()
		if live {
			return true
		}
	}
	return false
}

// countLiveAgentsLocked returns distinct agentIDs with backend != nil.
// Caller must hold p.mu.
func (p *agentRuntimePool) countLiveAgentsLocked() int {
	agents := make(map[string]struct{})
	for key, slot := range p.slots {
		slot.mu.Lock()
		live := slot.backend != nil
		slot.mu.Unlock()
		if !live {
			continue
		}
		aid, _ := splitCanonicalSlotKey(key)
		if aid != "" {
			agents[aid] = struct{}{}
		}
	}
	return len(agents)
}

// closeIdleOtherRuntimesLocked closes and deletes idle live slots for agentID
// whose runtimeID != keepRuntimeID (rebind). Running slots are left alone.
// Caller must hold p.mu. Returns number of processes closed.
func (p *agentRuntimePool) closeIdleOtherRuntimesLocked(agentID, keepRuntimeID string) int {
	closed := 0
	for key, slot := range p.slots {
		aid, rid := splitCanonicalSlotKey(key)
		if aid != agentID || rid == keepRuntimeID {
			continue
		}
		slot.mu.Lock()
		if slot.running {
			slot.mu.Unlock()
			continue
		}
		if slot.backend != nil {
			slot.closeBackend()
			closed++
		}
		slot.idleSince = time.Time{}
		slot.mu.Unlock()
		delete(p.slots, key)
	}
	if closed > 0 {
		p.publishLiveAgentProcessCountLocked()
		p.capacityCond.Broadcast()
	}
	return closed
}

// evictOldestIdleForCapacityLocked picks the idle live resident with the
// oldest idleSince (LRU), closes it, and deletes the slot. Never touches
// running slots or excludeAgentID. Caller must hold p.mu. Returns true if
// something was evicted.
func (p *agentRuntimePool) evictOldestIdleForCapacityLocked(excludeAgentID string) bool {
	var (
		bestKey  string
		bestSlot *agentRuntimeSlot
		bestIdle time.Time
		found    bool
	)
	for key, slot := range p.slots {
		aid, _ := splitCanonicalSlotKey(key)
		if aid == excludeAgentID {
			continue
		}
		slot.mu.Lock()
		eligible := !slot.running && slot.backend != nil
		idleSince := slot.idleSince
		slot.mu.Unlock()
		if !eligible {
			continue
		}
		if !found || idleSince.Before(bestIdle) {
			found = true
			bestKey = key
			bestSlot = slot
			bestIdle = idleSince
		}
	}
	if !found || bestSlot == nil {
		return false
	}
	bestSlot.mu.Lock()
	// Re-check under slot lock.
	if bestSlot.running || bestSlot.backend == nil {
		bestSlot.mu.Unlock()
		return false
	}
	bestSlot.closeBackend()
	bestSlot.idleSince = time.Time{}
	bestSlot.mu.Unlock()
	delete(p.slots, bestKey)
	p.publishLiveAgentProcessCountLocked()
	p.capacityCond.Broadcast()
	return true
}
