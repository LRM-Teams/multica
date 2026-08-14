package computer

import "sync"

// ProcessCapacityGrant is the opaque Host-owned lease for one managed launch.
type ProcessCapacityGrant struct {
	ID        string `json:"id"`
	LaunchID  string `json:"launch_id"`
	AgentID   string `json:"agent_id"`
	RuntimeID string `json:"runtime_id"`
}

// ProcessCapacityRequest identifies one child-owned process that needs a
// machine-wide admission lease.
type ProcessCapacityRequest struct {
	WorkspaceID string
	AgentID     string
	RuntimeID   string
	LaunchID    string
	Waiter      func(ProcessCapacityGrant)
}

// ProcessCapacity is the Computer Host's machine-wide admission ledger. It
// deliberately knows nothing about WorkspaceRunner or provider implementations.
type ProcessCapacity struct {
	mu      sync.Mutex
	max     int
	grants  map[string]ProcessCapacityGrant
	pending map[string]pendingProcess
	order   []string
}

type pendingProcess struct {
	grant  ProcessCapacityGrant
	waiter func(ProcessCapacityGrant)
}

type processGrantWakeup struct {
	grant  ProcessCapacityGrant
	waiter func(ProcessCapacityGrant)
}

func NewProcessCapacity(max int) *ProcessCapacity {
	if max < 0 {
		max = 0
	}
	return &ProcessCapacity{
		max: max, grants: make(map[string]ProcessCapacityGrant), pending: make(map[string]pendingProcess),
	}
}

func (c *ProcessCapacity) Acquire(request ProcessCapacityRequest) (ProcessCapacityGrant, bool) {
	if c == nil || request.LaunchID == "" {
		return ProcessCapacityGrant{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if grant, ok := c.grants[request.LaunchID]; ok {
		return grant, true
	}
	if pending, ok := c.pending[request.LaunchID]; ok {
		return pending.grant, false
	}
	grant := ProcessCapacityGrant{ID: request.LaunchID, LaunchID: request.LaunchID, AgentID: request.AgentID, RuntimeID: request.RuntimeID}
	if c.canGrantLocked(grant.AgentID) {
		c.grants[grant.LaunchID] = grant
		return grant, true
	}
	c.pending[grant.LaunchID] = pendingProcess{grant: grant, waiter: request.Waiter}
	c.order = append(c.order, grant.LaunchID)
	return grant, false
}

func (c *ProcessCapacity) Cancel(grant ProcessCapacityGrant) {
	if c == nil || grant.LaunchID == "" {
		return
	}
	c.mu.Lock()
	if pending, ok := c.pending[grant.LaunchID]; ok && pending.grant == grant {
		delete(c.pending, grant.LaunchID)
	}
	if current, ok := c.grants[grant.LaunchID]; ok && current == grant {
		delete(c.grants, grant.LaunchID)
	}
	wakeups := c.promoteLocked()
	c.mu.Unlock()
	for _, wakeup := range wakeups {
		wakeup := wakeup
		go wakeup.waiter(wakeup.grant)
	}
}

func (c *ProcessCapacity) Release(grant ProcessCapacityGrant) { c.Cancel(grant) }

func (c *ProcessCapacity) Active(grant ProcessCapacityGrant) bool {
	if c == nil || grant.LaunchID == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	current, ok := c.grants[grant.LaunchID]
	return ok && current == grant
}

func (c *ProcessCapacity) canGrantLocked(agentID string) bool {
	if c.max <= 0 {
		return true
	}
	agents := make(map[string]struct{}, len(c.grants))
	for _, grant := range c.grants {
		agents[grant.AgentID] = struct{}{}
	}
	if _, alreadyCounted := agents[agentID]; alreadyCounted {
		return true
	}
	return len(agents) < c.max
}

func (c *ProcessCapacity) promoteLocked() []processGrantWakeup {
	var wakeups []processGrantWakeup
	for len(c.order) > 0 {
		launchID := c.order[0]
		pending, ok := c.pending[launchID]
		if !ok {
			c.order = c.order[1:]
			continue
		}
		if !c.canGrantLocked(pending.grant.AgentID) {
			break
		}
		c.grants[launchID] = pending.grant
		delete(c.pending, launchID)
		c.order = c.order[1:]
		if pending.waiter != nil {
			wakeups = append(wakeups, processGrantWakeup{grant: pending.grant, waiter: pending.waiter})
		}
	}
	return wakeups
}
