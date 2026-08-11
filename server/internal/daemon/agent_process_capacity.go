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
}

type agentProcessCapacityGrant struct {
	ID       string
	LaunchID string
}

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
	grant := agentProcessCapacityGrant{ID: request.LaunchID, LaunchID: request.LaunchID}
	a.pool.managedProcessGrants[request.LaunchID] = grant
	return grant, true
}

func (a canonicalProcessAdmission) Cancel(grant agentProcessCapacityGrant) { a.Release(grant) }

func (a canonicalProcessAdmission) Release(grant agentProcessCapacityGrant) {
	if a.pool == nil || grant.LaunchID == "" {
		return
	}
	a.pool.mu.Lock()
	defer a.pool.mu.Unlock()
	if current, ok := a.pool.managedProcessGrants[grant.LaunchID]; ok && current == grant {
		delete(a.pool.managedProcessGrants, grant.LaunchID)
	}
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
