package daemon

import (
	"context"
	"os"
	"strings"

	"github.com/multica-ai/multica/server/internal/agentworkspace"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// handleAgentStartIntent applies the durable first-start delivery locally and
// then acknowledges receipt. handleDaemonAgentStart is idempotent for a stable
// placement generation, which makes a replay after an ACK loss safe. Ready is
// only reported after separately re-observing the local residency, Agent root,
// and Message coordinator installed by that operation. Neither transition
// starts a provider process or schedules a retry after a terminal failure.
func (d *Daemon) handleAgentStartIntent(ctx context.Context, pending protocol.DaemonHeartbeatPendingAgentStartIntent) {
	if d == nil || d.client == nil {
		return
	}
	if !d.ownsAgentStartIntent(pending) {
		d.reportAgentStartIntent(ctx, pending, map[string]any{
			"status": "failed", "lifecycle_seq": 1, "failure_code": "local_runtime_unavailable",
		}, "failure")
		return
	}
	if err := d.handleDaemonAgentStartFrame(protocol.DaemonAgentStartPayload{
		AgentID:             pending.AgentID,
		RuntimeID:           pending.RuntimeID,
		WorkspaceID:         pending.WorkspaceID,
		PlacementGeneration: 1,
	}); err != nil {
		d.logger.Warn("agent start intent apply failed", "start_dispatch_id", pending.StartDispatchID, "agent_id", pending.AgentID, "error", err)
		d.reportAgentStartIntent(ctx, pending, map[string]any{
			"status": "failed", "lifecycle_seq": 1, "failure_code": "local_start_apply_failed",
		}, "failure")
		return
	}
	d.reportAgentStartIntent(ctx, pending, map[string]any{
		"status": "accepted", "lifecycle_seq": 1,
	}, "acceptance")

	// Keep ready distinct from accepted: this examines the persisted local
	// residency and independently-created coordinator/root after the accept
	// operation completed. A failure to observe readiness is intentionally not
	// converted into a retry loop; a later daemon/runtime observation can report
	// failed with a higher sequence for explicit human correction.
	if d.agentStartIntentReady(pending) {
		d.reportAgentStartIntent(ctx, pending, map[string]any{
			"status": "ready", "lifecycle_seq": 2,
		}, "ready")
	}
}

func (d *Daemon) reportAgentStartIntent(ctx context.Context, pending protocol.DaemonHeartbeatPendingAgentStartIntent, result map[string]any, observation string) {
	if err := d.client.ReportAgentStartIntent(ctx, pending.RuntimeID, pending.StartDispatchID, result); err != nil {
		d.logger.Warn("agent start intent report failed", "observation", observation, "start_dispatch_id", pending.StartDispatchID, "error", err)
	}
}

// ownsAgentStartIntent is the daemon-local Computer ownership proof. The
// durable server row alone never authorizes this machine to make an Agent
// resident, unlike legacy lifecycle frames which remain deliberately ignored
// when a runtime is no longer locally registered.
func (d *Daemon) ownsAgentStartIntent(pending protocol.DaemonHeartbeatPendingAgentStartIntent) bool {
	if d == nil || strings.TrimSpace(pending.AgentID) == "" || strings.TrimSpace(pending.RuntimeID) == "" || strings.TrimSpace(pending.WorkspaceID) == "" {
		return false
	}
	d.mu.Lock()
	runtime, ok := d.runtimeIndex[pending.RuntimeID]
	d.mu.Unlock()
	return ok && runtime.WorkspaceID == pending.WorkspaceID
}

// agentStartIntentReady observes the local durable prerequisites for an idle
// Agent. This is intentionally narrower than provider execution readiness:
// providers start only when work is dispatched, and a later runtime failure is
// reported through the same sequence-guarded endpoint rather than re-running
// the first-start provisioning work.
func (d *Daemon) agentStartIntentReady(pending protocol.DaemonHeartbeatPendingAgentStartIntent) bool {
	if !d.ownsAgentStartIntent(pending) || d.reminderAgents == nil {
		return false
	}
	resident, ok := d.reminderAgents.get(pending.AgentID)
	if !ok || resident.RuntimeID != pending.RuntimeID || resident.WorkspaceID != pending.WorkspaceID || resident.PlacementGeneration < 1 {
		return false
	}
	root := agentworkspace.Root(d.cfg.WorkspacesRoot, pending.WorkspaceID, pending.AgentID)
	if _, err := os.Stat(root); err != nil {
		return false
	}
	d.messageCoordinatorMu.RLock()
	coordinator := d.messageCoordinators[pending.AgentID]
	runtimeID := d.messageRuntimeIDs[pending.AgentID]
	d.messageCoordinatorMu.RUnlock()
	return coordinator != nil && runtimeID == pending.RuntimeID
}
