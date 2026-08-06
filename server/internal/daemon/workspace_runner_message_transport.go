package daemon

// workspaceRunnerMessageTransport is a connection-scoped sender. It carries
// canonical Message protocol frames only; lifecycle and reminder frames retain
// their own bounded channels.
type workspaceRunnerMessageTransport struct {
	generation uint64
	send       func(string, any) error
}

func (d *Daemon) attachWorkspaceRunnerMessageTransport(workspaceID string, send func(string, any) error) uint64 {
	if d == nil || workspaceID == "" || send == nil {
		return 0
	}
	d.mu.Lock()
	if d.runnerMessageGeneration == nil {
		d.runnerMessageGeneration = make(map[string]uint64)
	}
	if d.runnerMessageTransports == nil {
		d.runnerMessageTransports = make(map[string]workspaceRunnerMessageTransport)
	}
	d.runnerMessageGeneration[workspaceID]++
	generation := d.runnerMessageGeneration[workspaceID]
	d.runnerMessageTransports[workspaceID] = workspaceRunnerMessageTransport{generation: generation, send: send}
	d.mu.Unlock()
	return generation
}

func (d *Daemon) detachWorkspaceRunnerMessageTransport(workspaceID string, generation uint64) {
	if d == nil || workspaceID == "" || generation == 0 {
		return
	}
	d.mu.Lock()
	if current := d.runnerMessageTransports[workspaceID]; current.generation == generation {
		delete(d.runnerMessageTransports, workspaceID)
	}
	d.mu.Unlock()
}

func (d *Daemon) sendAgentMessageRunnerFrame(agentID, eventType string, payload any) bool {
	if d == nil || agentID == "" {
		return false
	}
	d.messageCoordinatorMu.RLock()
	runtimeID := d.messageRuntimeIDs[agentID]
	d.messageCoordinatorMu.RUnlock()
	if runtimeID == "" {
		return false
	}
	d.mu.Lock()
	workspaceID := d.runtimeIndex[runtimeID].WorkspaceID
	transport := d.runnerMessageTransports[workspaceID]
	d.mu.Unlock()
	if transport.send == nil {
		return false
	}
	return transport.send(eventType, payload) == nil
}
