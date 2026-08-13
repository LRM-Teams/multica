package daemon

import (
	"strings"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// sendAgentStatus is Raft sendAgentStatus(agentId, active|inactive, launchId).
// It reports process residency only. It is not Computer heartbeat and not
// Idle/Working Activity.
func (runner *WorkspaceRunner) sendAgentStatus(agentID, status, launchID string) error {
	if runner == nil {
		return nil
	}
	if strings.TrimSpace(agentID) == "" || strings.TrimSpace(launchID) == "" {
		return nil
	}
	return runner.sendOnCurrentConnection(protocol.EventAgentStatus, protocol.AgentStatusPayload{
		AgentID:  agentID,
		LaunchID: launchID,
		Status:   status,
	})
}

// broadcastActivity is Raft broadcastActivity(..., detailKind). Starting and
// Stopped are the only lifecycle words this helper owns.
func (runner *WorkspaceRunner) broadcastActivity(agentID, runtimeID, detailKind string) {
	if runner == nil {
		return
	}
	launch, found := runner.managedLaunch(agentID, runtimeID)
	if !found {
		return
	}
	var kind AgentObservationKind
	switch detailKind {
	case protocol.ActivityDetailStarting:
		kind = AgentObservationRuntimeStarting
	case protocol.ActivityDetailStopped:
		kind = AgentObservationRuntimeStopped
	default:
		return
	}
	runner.observeActivity(AgentObservation{
		AgentID:  agentID,
		LaunchID: launch.LaunchID,
		Kind:     kind,
		Data:     AgentRuntimeStageObservationData{RuntimeID: launch.RuntimeID},
		At:       time.Now().UTC(),
	}, detailKind)
}
