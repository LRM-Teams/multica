package handler

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const maxRunnerLifecycleBatch = 50

// dispatchPendingRunnerLaunches is the compatibility call-site used by
// heartbeat and Attachment receipts. All capable Workspace Runners now share
// the same desired-vs-observed reconcile used by ready/setup/reconnect.
func (h *Handler) dispatchPendingRunnerLaunches(ctx context.Context, identity daemonws.ClientIdentity) error {
	return h.reconcileWorkspaceRunnerLaunches(ctx, identity)
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
	allowed, err := h.runnerAttachmentRuntimeScope(ctx, identity)
	if err != nil {
		return err
	}
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
		LIMIT $3`, identity.WorkspaceID, identity.DaemonID, maxRunnerLifecycleBatch)
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
