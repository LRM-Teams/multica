package daemon

import (
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// reportMixedRunActivity durably records one mixed-run lifecycle transition and
// best-effort ships it on the workspace Runner connection. The durable outbox
// is written before any send attempt so a daemon restart cannot lose the
// counter delta; the server acknowledges with EventMixedRunActivityAck.
func (d *Daemon) reportMixedRunActivity(agentID, runtimeID, runID, runAgentID, transitionID, dimension string, delta int) bool {
	payload := protocol.MixedRunActivityTransitionPayload{
		AgentID: agentID, RuntimeID: runtimeID, RunID: runID, RunAgentID: runAgentID,
		TransitionID: transitionID, Dimension: dimension, Delta: delta,
	}
	if payload.Validate() != nil {
		return false
	}
	if d.mixedRunActivityReporter != nil {
		return d.mixedRunActivityReporter(payload)
	}
	if d.mixedRunActivityOutbox == nil {
		return false
	}
	d.mu.Lock()
	workspaceID := d.runtimeIndex[runtimeID].WorkspaceID
	d.mu.Unlock()
	if workspaceID == "" {
		return false
	}
	if err := d.mixedRunActivityOutbox.enqueue(workspaceID, payload); err != nil {
		if d.logger != nil {
			d.logger.Warn("queue mixed-run activity transition failed", "error", err, "run_id", runID, "transition_id", transitionID)
		}
		return false
	}
	if runner := d.currentWorkspaceDaemon(workspaceID); runner != nil {
		if err := runner.sendOnCurrentConnection(protocol.EventMixedRunActivityTransition, payload); err != nil && d.logger != nil {
			d.logger.Debug("mixed-run activity transition send deferred", "error", err, "workspace_id", workspaceID, "run_id", runID, "transition_id", transitionID)
		}
	}
	return true
}

// replayMixedRunActivity resends every unacknowledged transition for one
// Workspace after its Runner connection attaches. A failed send stops the
// replay; the remaining entries stay durable for the next attachment.
func (d *Daemon) replayMixedRunActivity(workspaceID string, send func(string, any) error) {
	if d == nil || d.mixedRunActivityOutbox == nil || workspaceID == "" || send == nil {
		return
	}
	pending, err := d.mixedRunActivityOutbox.pending(workspaceID)
	if err != nil {
		if d.logger != nil {
			d.logger.Warn("load mixed-run activity outbox failed", "error", err, "workspace_id", workspaceID)
		}
		return
	}
	for _, payload := range pending {
		if err := send(protocol.EventMixedRunActivityTransition, payload); err != nil {
			if d.logger != nil {
				d.logger.Debug("mixed-run activity replay deferred", "error", err, "workspace_id", workspaceID, "run_id", payload.RunID, "transition_id", payload.TransitionID)
			}
			return
		}
	}
}

func (d *Daemon) ackMixedRunActivity(ack protocol.MixedRunActivityTransitionAckPayload) error {
	if d == nil || d.mixedRunActivityOutbox == nil {
		return nil
	}
	return d.mixedRunActivityOutbox.acknowledge(ack)
}

func (d *Daemon) reportMixedRunMessageQueueActivity(agentID, runtimeID string, messages []protocol.AgentMessageProjection, delta int) {
	phase := "start"
	if delta < 0 {
		phase = "end"
	}
	for _, message := range messages {
		if message.RunID == "" || message.RunAgentID == "" {
			continue
		}
		d.reportMixedRunActivity(agentID, runtimeID, message.RunID, message.RunAgentID,
			"message:"+message.ID+":queued:"+phase, protocol.MixedRunActivityQueuedMessage, delta)
	}
}
