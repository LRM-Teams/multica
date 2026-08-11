package handler

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// dispatchPendingRunnerLaunches bridges the durable first-launch outbox to a
// capable current Workspace Runner. The intent is not Attachment authority:
// every command rechecks the server-owned current Attachment before sending.
// Older daemons keep receiving the same pending intent through heartbeat.
func (h *Handler) dispatchPendingRunnerLaunches(ctx context.Context, identity daemonws.ClientIdentity) error {
	if h == nil || h.DB == nil || h.DaemonHub == nil || !h.DaemonHub.WorkspaceRunnerSupportsCapability(identity.DaemonID, identity.WorkspaceID, protocol.DaemonCapabilityWorkspaceRunnerAttachment) {
		return nil
	}
	allowed := runnerAttachmentRuntimeScope(identity)
	rows, err := h.DB.Query(ctx, `
		SELECT intent.start_dispatch_id::text, intent.agent_id::text, intent.runtime_id::text
		FROM agent_start_intent intent
		JOIN agent_attachment_projection attachment
		  ON attachment.agent_id = intent.agent_id
		 AND attachment.workspace_id = intent.workspace_id
		 AND attachment.runtime_id = intent.runtime_id
		WHERE intent.workspace_id::text = $1 AND intent.status = 'pending'
		ORDER BY intent.created_at, intent.start_dispatch_id
		LIMIT $2`, identity.WorkspaceID, maxAgentStartIntentBatch)
	if err != nil {
		return fmt.Errorf("list pending Workspace Runner launches: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var dispatchID, agentID, runtimeID string
		if err := rows.Scan(&dispatchID, &agentID, &runtimeID); err != nil {
			return fmt.Errorf("scan pending Workspace Runner launch: %w", err)
		}
		if _, ok := allowed[runtimeID]; !ok {
			continue
		}
		if !h.DaemonHub.NotifyWorkspaceRunner(identity.DaemonID, identity.WorkspaceID, protocol.EventDaemonAgentStart, protocol.WorkspaceRunnerAgentStartPayload{
			AgentID: agentID, RuntimeID: runtimeID, StartDispatchID: dispatchID,
		}) {
			return fmt.Errorf("current Workspace Runner unavailable while dispatching launch")
		}
		if _, err := h.DB.Exec(ctx, `
			UPDATE agent_start_intent
			SET dispatch_attempts = dispatch_attempts + 1, last_dispatched_at = now(), updated_at = now()
			WHERE start_dispatch_id::text = $1 AND status = 'pending'`, dispatchID); err != nil {
			return fmt.Errorf("record Workspace Runner launch dispatch: %w", err)
		}
	}
	return rows.Err()
}

// dispatchPendingRunnerStops projects a durable lifecycle request onto the
// current managed launch. The lifecycle operation remains the sole durable
// intent and keeps its legacy executor for reset-session/full-reset work;
// this command only releases the matching Runner-owned launch. Repeating the
// projection is safe because the command is fenced by the immutable LaunchID.
func (h *Handler) dispatchPendingRunnerStops(ctx context.Context, identity daemonws.ClientIdentity) error {
	if h == nil || h.DB == nil || h.DaemonHub == nil || !h.DaemonHub.WorkspaceRunnerSupportsCapability(identity.DaemonID, identity.WorkspaceID, protocol.DaemonCapabilityWorkspaceRunnerAttachment) {
		return nil
	}
	allowed := runnerAttachmentRuntimeScope(identity)
	rows, err := h.DB.Query(ctx, `
		SELECT launch.agent_id::text, launch.runtime_id::text, launch.launch_id
		FROM agent_activity_launch launch
		JOIN agent_lifecycle_operation operation
		  ON operation.workspace_id = launch.workspace_id
		 AND operation.agent_id = launch.agent_id
		 AND operation.runtime_id = launch.runtime_id
		WHERE launch.workspace_id::text = $1
		  AND launch.daemon_id = $2
		  AND launch.status IN ('accepted', 'active')
		  AND operation.status = 'running'
		ORDER BY operation.created_at, launch.agent_id
		LIMIT $3`, identity.WorkspaceID, identity.DaemonID, maxAgentStartIntentBatch)
	if err != nil {
		return fmt.Errorf("list pending Workspace Runner stops: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var agentID, runtimeID, launchID string
		if err := rows.Scan(&agentID, &runtimeID, &launchID); err != nil {
			return fmt.Errorf("scan pending Workspace Runner stop: %w", err)
		}
		if _, ok := allowed[runtimeID]; !ok {
			continue
		}
		if !h.DaemonHub.NotifyWorkspaceRunner(identity.DaemonID, identity.WorkspaceID, protocol.EventDaemonAgentStop, protocol.WorkspaceRunnerAgentStopPayload{AgentID: agentID, LaunchID: launchID}) {
			return fmt.Errorf("current Workspace Runner unavailable while dispatching stop")
		}
		slog.Debug("Workspace Runner stop dispatched", "workspace_id", identity.WorkspaceID, "daemon_id", identity.DaemonID, "agent_id", agentID, "runtime_id", runtimeID, "launch_id", launchID, "outcome", "sent", "reason", "lifecycle_operation")
	}
	return rows.Err()
}
