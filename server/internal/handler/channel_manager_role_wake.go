package handler

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	channelRoleChangedWakeReason   = protocol.ChannelRoleChangedReason
	channelRoleChangedWakePriority = 10
)

func insertChannelManagerRoleWakeExec(
	ctx context.Context,
	exec dbExecutor,
	workspaceID, channelID, agentID, initiatorUserID pgtype.UUID,
) (pgtype.UUID, error) {
	var eventID pgtype.UUID
	err := exec.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
		  workspace_id,
		  agent_session_id,
		  channel_id,
		  agent_id,
		  runtime_id,
		  execution_config,
		  reason,
		  delivery_mode,
		  response_mode,
		  requires_wake,
		  status,
		  priority,
		  context,
		  trigger_summary,
		  initiator_user_id
		)
		SELECT
		  agent.workspace_id,
		  ensure_agent_wake_session(agent.id),
		  $2::uuid,
		  agent.id,
		  agent.runtime_id,
		  jsonb_build_object('execution_config', jsonb_build_object(
		    'model', COALESCE(agent.model, ''),
		    'thinking_level', COALESCE(agent.thinking_level, ''),
		    'execution_profile', 'full',
		    'snapshotted', true
		  )),
		  $4::text,
		  'execute',
		  'public_response',
		  true,
		  'pending',
		  $5::integer,
		  jsonb_build_object(
		    'type', $4::text,
		    'channel_id', $2::text
		  ),
		  'Channel manager role changed',
		  $3::uuid
		FROM agent
		WHERE agent.id = $1::uuid
		  AND agent.workspace_id = $6::uuid
		RETURNING id`,
		agentID,
		channelID,
		initiatorUserID,
		channelRoleChangedWakeReason,
		channelRoleChangedWakePriority,
		workspaceID,
	).Scan(&eventID)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("insert channel manager role wake: %w", err)
	}
	return eventID, nil
}

func (h *Handler) publishChannelManagerRoleWake(
	ctx context.Context,
	eventID pgtype.UUID,
) {
	event, err := h.Queries.GetAgentInboxEvent(ctx, eventID)
	if err != nil {
		return
	}
	h.publishAgentInboxTaskLifecycle(
		protocol.EventTaskQueued,
		event,
		event.RuntimeID,
		"queued",
	)
	if h.TaskService != nil {
		h.TaskService.NotifyTaskEnqueued(ctx, db.AgentInboxEvent(event))
	}
}

func (h *Handler) isChannelAgentManager(
	ctx context.Context,
	workspaceID, channelID, agentID pgtype.UUID,
) bool {
	var exists bool
	err := h.DB.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1
		  FROM channel_member membership
		  JOIN channel channel_row
		    ON channel_row.id = membership.channel_id
		   AND channel_row.workspace_id = membership.workspace_id
		  JOIN agent manager
		    ON manager.id = membership.member_id
		   AND manager.workspace_id = membership.workspace_id
		  WHERE membership.workspace_id = $1
		    AND membership.channel_id = $2
		    AND membership.member_type = 'agent'
		    AND membership.member_id = $3
		    AND membership.role = 'manager'
		    AND channel_row.archived_at IS NULL
		    AND manager.archived_at IS NULL
		)`,
		workspaceID,
		channelID,
		agentID,
	).Scan(&exists)
	return err == nil && exists
}
