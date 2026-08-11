package daemon

// agentProcessAdmission is the machine-wide capacity seam for a managed
// launch. Queue policy is intentionally hidden behind this small interface.
type agentProcessAdmission interface {
	Acquire(agentProcessCapacityRequest) (agentProcessCapacityGrant, bool)
	Cancel(agentProcessCapacityGrant)
	Release(agentProcessCapacityGrant)
	Active(agentProcessCapacityGrant) bool
}

type agentProcessCapacityRequest struct {
	WorkspaceID string
	AgentID     string
	RuntimeID   string
	LaunchID    string
	Waiter      agentProcessCapacityWaiter
}

type agentProcessCapacityGrant struct {
	ID        string
	LaunchID  string
	AgentID   string
	RuntimeID string
}

// agentProcessCapacityWaiter is registered by a Runner while it owns a queued
// launch. The Pool invokes it only after atomically recording the grant.
// #2701 uses this to make release progress independent of the releasing
// Runner's Workspace.
type agentProcessCapacityWaiter func(agentProcessCapacityGrant)

type canonicalProcessAdmission struct{ pool *canonicalAgentRuntimePool }

func (p *canonicalAgentRuntimePool) managedProcessAdmission() agentProcessAdmission {
	return canonicalProcessAdmission{pool: p}
}

func (a canonicalProcessAdmission) Acquire(request agentProcessCapacityRequest) (agentProcessCapacityGrant, bool) {
	if a.pool == nil || request.LaunchID == "" {
		return agentProcessCapacityGrant{}, false
	}
	a.pool.mu.Lock()
	defer a.pool.mu.Unlock()
	if grant, ok := a.pool.managedProcessGrants[request.LaunchID]; ok {
		return grant, true
	}
	if pending, ok := a.pool.pendingManagedProcesses[request.LaunchID]; ok {
		return pending.grant, false
	}
	grant := agentProcessCapacityGrant{ID: request.LaunchID, LaunchID: request.LaunchID, AgentID: request.AgentID, RuntimeID: request.RuntimeID}
	if a.pool.grantManagedProcessLocked(grant) {
		return grant, true
	}
	a.pool.pendingManagedProcesses[request.LaunchID] = pendingManagedProcess{grant: grant, waiter: request.Waiter}
	a.pool.pendingManagedOrder = append(a.pool.pendingManagedOrder, request.LaunchID)
	return grant, false
}

func (a canonicalProcessAdmission) Cancel(grant agentProcessCapacityGrant) {
	if a.pool == nil || grant.LaunchID == "" {
		return
	}
	var waiters []managedProcessGrantWakeup
	a.pool.mu.Lock()
	if pending, ok := a.pool.pendingManagedProcesses[grant.LaunchID]; ok && pending.grant == grant {
		delete(a.pool.pendingManagedProcesses, grant.LaunchID)
	}
	if current, ok := a.pool.managedProcessGrants[grant.LaunchID]; ok && current == grant {
		delete(a.pool.managedProcessGrants, grant.LaunchID)
		waiters = a.pool.promoteManagedProcessesLocked()
	}
	a.pool.mu.Unlock()
	invokeManagedProcessGrantWakeups(waiters)
}

func (a canonicalProcessAdmission) Release(grant agentProcessCapacityGrant) {
	a.Cancel(grant)
}

func (a canonicalProcessAdmission) Active(grant agentProcessCapacityGrant) bool {
	if a.pool == nil || grant.LaunchID == "" {
		return false
	}
	a.pool.mu.Lock()
	defer a.pool.mu.Unlock()
	current, ok := a.pool.managedProcessGrants[grant.LaunchID]
	return ok && current == grant
}

// pendingManagedProcess stays entirely in the pool: a Runner retains only the
// opaque grant token needed to cancel its exact launch.  This keeps queue
// ownership machine-wide while preserving launch fencing at the Runner edge.
type pendingManagedProcess struct {
	grant  agentProcessCapacityGrant
	waiter agentProcessCapacityWaiter
}

type managedProcessGrantWakeup struct {
	grant  agentProcessCapacityGrant
	waiter agentProcessCapacityWaiter
}

func (p *canonicalAgentRuntimePool) grantManagedProcessLocked(grant agentProcessCapacityGrant) bool {
	if existing, ok := p.managedProcessGrants[grant.LaunchID]; ok {
		return existing == grant
	}
	if p.maxAgentProcesses > 0 && !p.agentCountsTowardCapLocked(grant.AgentID) {
		for p.countCapAgentsLocked() >= p.maxAgentProcesses {
			if !p.evictOldestIdleForCapacityLocked(grant.AgentID) {
				return false
			}
			p.evictForCapTotal.Add(1)
		}
	}
	p.managedProcessGrants[grant.LaunchID] = grant
	return true
}

// promoteManagedProcessesLocked atomically records every granted launch
// before exposing it to its Runner.  It intentionally grants at most the
// eligible FIFO prefix; a blocked head cannot be bypassed by a later request.
func (p *canonicalAgentRuntimePool) promoteManagedProcessesLocked() []managedProcessGrantWakeup {
	var wakeups []managedProcessGrantWakeup
	for len(p.pendingManagedOrder) > 0 {
		launchID := p.pendingManagedOrder[0]
		pending, ok := p.pendingManagedProcesses[launchID]
		if !ok {
			p.pendingManagedOrder = p.pendingManagedOrder[1:]
			continue
		}
		if !p.grantManagedProcessLocked(pending.grant) {
			break
		}
		delete(p.pendingManagedProcesses, launchID)
		p.pendingManagedOrder = p.pendingManagedOrder[1:]
		if pending.waiter != nil {
			wakeups = append(wakeups, managedProcessGrantWakeup{grant: pending.grant, waiter: pending.waiter})
		}
	}
	return wakeups
}

// invokeManagedProcessGrantWakeups never calls back while pool or Runner
// locks can be held. Each grant was recorded before this handoff, so a stale
// callback cannot revive a launch that was cancelled in the meantime.
func invokeManagedProcessGrantWakeups(wakeups []managedProcessGrantWakeup) {
	for _, wakeup := range wakeups {
		wakeup := wakeup
		go wakeup.waiter(wakeup.grant)
	}
}
