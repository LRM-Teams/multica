package daemon

import "strings"

func (d *Daemon) recordProviderSession(agentID, runtimeID, sessionID string) {
	if d == nil || d.agentRuntimeSessions == nil {
		return
	}
	agentID = strings.TrimSpace(agentID)
	runtimeID = strings.TrimSpace(runtimeID)
	if agentID == "" || runtimeID == "" {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	if err := d.agentRuntimeSessions.Put(agentID, runtimeID, sessionID); err != nil && d.logger != nil {
		d.logger.Warn("record provider session failed", "agent_id", agentID, "error", err)
	}
}
