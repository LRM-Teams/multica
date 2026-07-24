package handler

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// channelAgentWake carries everything recordChannelAgentPromptWake needs to
// publish/activity-log a queued agent prompt. Shared by Collaboration turn
// grants and the legacy Andong wake-all ambient dispatch.
type channelAgentWake struct {
	channel ChannelResponse
	agent   db.Agent
	trigger ChannelMessageResponse
	reason  string
	result  channelAgentPromptTxResult
}

var channelAllMentionPattern = regexp.MustCompile(`(?i)(^|[\s，。！？、,:;])[@＠]all(?:$|[\s，。！？、,:;])`)

// channelMessageTriggerCreatorID resolves the human initiator (if any) behind
// a channel message trigger, used to attribute agent-queued work.
func channelMessageTriggerCreatorID(trigger ChannelMessageResponse) pgtype.UUID {
	if trigger.Type == "user" && trigger.AuthorID != nil {
		return parseUUID(*trigger.AuthorID)
	}
	return pgtype.UUID{}
}

func (h *Handler) recordChannelUnmentionedMessage() {
	if h == nil {
		return
	}
	denominator := atomic.AddUint64(&h.channelUnmentionedMessages, 1)
	if h.Metrics != nil {
		h.Metrics.SetChannelFullExecutionAmplificationRatio(float64(atomic.LoadUint64(&h.channelUnmentionedFullWakes)) / float64(denominator))
	}
}

func (h *Handler) recordChannelUnmentionedFullWake() {
	if h == nil {
		return
	}
	numerator := atomic.AddUint64(&h.channelUnmentionedFullWakes, 1)
	if denominator := atomic.LoadUint64(&h.channelUnmentionedMessages); denominator > 0 && h.Metrics != nil {
		h.Metrics.SetChannelFullExecutionAmplificationRatio(float64(numerator) / float64(denominator))
	}
}

func channelMessageHasAgentMention(content string, parts []protocol.MessagePart) bool {
	for _, mention := range util.ParseMentionsFromContentAndParts(content, parts) {
		if mention.Type == "agent" {
			return true
		}
	}
	return false
}

func channelMessageIsGroupCommand(content string, parts []protocol.MessagePart) bool {
	for _, part := range parts {
		if part.Type == protocol.MessagePartTypeReference && part.RefType == "mention" && part.RefSubType == "all" {
			return true
		}
	}
	trimmed := strings.TrimSpace(content)
	return channelAllMentionPattern.MatchString(trimmed) || strings.Contains(trimmed, "大家")
}

func channelMessageIsHumanAuthored(authorType string) bool {
	switch strings.ToLower(strings.TrimSpace(authorType)) {
	case "user", "lark":
		return true
	default:
		return false
	}
}

func (h *Handler) channelAgentMembersWithDB(ctx context.Context, exec db.DBTX, workspaceID, channelID string) ([]db.Agent, error) {
	rows, err := exec.Query(ctx, `
		SELECT a.id, a.workspace_id, a.name, a.avatar_url, a.runtime_mode, a.runtime_config, a.visibility, a.status,
		       a.max_concurrent_tasks, a.owner_id, a.created_at, a.updated_at, a.description, a.runtime_id,
		       a.instructions, a.archived_at, a.display_name
		FROM channel_member cm
		JOIN agent a ON cm.member_type = 'agent' AND a.id = cm.member_id
		WHERE cm.channel_id = $1 AND cm.workspace_id = $2 AND a.archived_at IS NULL
		ORDER BY a.id`, parseUUID(channelID), parseUUID(workspaceID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []db.Agent
	for rows.Next() {
		var agent db.Agent
		if err := rows.Scan(&agent.ID, &agent.WorkspaceID, &agent.Name, &agent.AvatarUrl, &agent.RuntimeMode, &agent.RuntimeConfig, &agent.Visibility, &agent.Status, &agent.MaxConcurrentTasks, &agent.OwnerID, &agent.CreatedAt, &agent.UpdatedAt, &agent.Description, &agent.RuntimeID, &agent.Instructions, &agent.ArchivedAt, &agent.DisplayName); err != nil {
			return nil, err
		}
		out = append(out, agent)
	}
	return out, rows.Err()
}

// upsertChannelObserveInboxEventTx queues (or refreshes) a low-priority,
// non-waking "observe" inbox event so an agent's context stays current even
// when it isn't otherwise woken for a channel message (for example, a group
// member who wasn't @-mentioned).
func upsertChannelObserveInboxEventTx(ctx context.Context, tx pgx.Tx, workspaceID, channelID, agentID, agentSessionID, conversationID, sourceMessageID pgtype.UUID, seqFrom, seqTo int64) error {
	var eventID pgtype.UUID
	return tx.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
		  workspace_id, agent_session_id, conversation_id, channel_id, agent_id,
		  runtime_id, execution_config, source_message_id, reason,
		  delivery_mode, response_mode, requires_wake, status, priority, seq_from, seq_to
		)
		SELECT $1, $2, $3, $4, agent.id, agent.runtime_id,
		       jsonb_build_object('execution_config', jsonb_build_object(
		         'model', COALESCE(agent.model, ''),
		         'thinking_level', COALESCE(agent.thinking_level, ''),
		         'execution_profile', 'full', 'snapshotted', true
		       )),
		       $6, 'ambient', 'observe', 'no_public_output', false, 'pending', 0, $7, $8
		FROM agent WHERE agent.id = $5
		ON CONFLICT (conversation_id, agent_id)
		  WHERE reason = 'ambient'
		    AND delivery_mode = 'observe'
		    AND status IN ('pending', 'failed')
		    AND conversation_id IS NOT NULL
		DO UPDATE SET
		  agent_session_id = EXCLUDED.agent_session_id,
		  channel_id = EXCLUDED.channel_id,
		  source_message_id = COALESCE(EXCLUDED.source_message_id, agent_inbox_event.source_message_id),
		  status = 'pending',
		  seq_from = LEAST(agent_inbox_event.seq_from, EXCLUDED.seq_from),
		  seq_to = GREATEST(agent_inbox_event.seq_to, EXCLUDED.seq_to),
		  updated_at = now()
		RETURNING id`, workspaceID, agentSessionID, conversationID, channelID, agentID,
		sourceMessageID, seqFrom, seqTo).Scan(&eventID)
}

// leaseAgentInboxEventForRuntime admits the oldest eligible inbox wake while
// sharing the same per-agent row lock as legacy task claims. Candidate
// discovery happens first; after locking the agent, a second statement
// revalidates every cross-source predicate against a fresh READ COMMITTED
// snapshot. That two-statement shape is what makes the exclusion exact across
// agent_inbox_event and agent_task_queue.
func (h *Handler) leaseAgentInboxEventForRuntime(ctx context.Context, runtime db.AgentRuntime) (db.AgentEventDelivery, error) {
	if h.TxStarter == nil {
		return db.AgentEventDelivery{}, errors.New("transaction starter unavailable")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return db.AgentEventDelivery{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('channel_agent_inbox_runtime'), hashtext($1))`, uuidToString(runtime.ID)); err != nil {
		return db.AgentEventDelivery{}, err
	}

	var eventID, agentID pgtype.UUID
	err = tx.QueryRow(ctx, `
		SELECT event.id, event.agent_id
		FROM agent_inbox_event event
		JOIN agent_session session ON session.id = event.agent_session_id
		WHERE COALESCE(event.runtime_id, session.runtime_id) = $1
		  AND session.status = 'active'
		  AND event.status IN ('pending', 'failed')
		  AND NOT EXISTS (
		    SELECT 1
		    FROM agent_event_delivery active_delivery
		    JOIN agent_session active_session ON active_session.id = active_delivery.agent_session_id
		    WHERE active_session.agent_id = event.agent_id
		      AND active_delivery.status IN ('leased', 'processing')
		      AND active_delivery.lease_expires_at > now()
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM agent_task_queue active_task
		    WHERE active_task.agent_id = event.agent_id
		      AND active_task.status IN ('dispatched', 'running', 'waiting_local_directory')
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM agent_task_queue queued_task
		    WHERE queued_task.agent_id = event.agent_id
		      AND queued_task.runtime_id = COALESCE(event.runtime_id, session.runtime_id)
		      AND queued_task.status = 'queued'
		      AND (
		        queued_task.context->>'type' IS DISTINCT FROM 'agent_radar'
		        OR workspace_radar_task_is_authorized(queued_task.id)
		      )
		      AND (queued_task.created_at, queued_task.id) < (event.created_at, event.id)
		  )
		ORDER BY event.created_at, event.id
		LIMIT 1`, runtime.ID).Scan(&eventID, &agentID)
	if err != nil {
		return db.AgentEventDelivery{}, err
	}
	if err := tx.QueryRow(ctx, `
		SELECT id
		FROM agent
		WHERE id = $1
		FOR UPDATE`, agentID).Scan(&agentID); err != nil {
		return db.AgentEventDelivery{}, err
	}
	err = tx.QueryRow(ctx, `
		SELECT event.id
		FROM agent_inbox_event event
		JOIN agent_session session ON session.id = event.agent_session_id
		WHERE event.id = $1
		  AND event.agent_id = $2
		  AND COALESCE(event.runtime_id, session.runtime_id) = $3
		  AND session.status = 'active'
		  AND event.status IN ('pending', 'failed')
		  AND NOT EXISTS (
		    SELECT 1
		    FROM agent_event_delivery active_delivery
		    JOIN agent_session active_session ON active_session.id = active_delivery.agent_session_id
		    WHERE active_session.agent_id = event.agent_id
		      AND active_delivery.status IN ('leased', 'processing')
		      AND active_delivery.lease_expires_at > now()
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM agent_task_queue active_task
		    WHERE active_task.agent_id = event.agent_id
		      AND active_task.status IN ('dispatched', 'running', 'waiting_local_directory')
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM agent_task_queue queued_task
		    WHERE queued_task.agent_id = event.agent_id
		      AND queued_task.runtime_id = COALESCE(event.runtime_id, session.runtime_id)
		      AND queued_task.status = 'queued'
		      AND (
		        queued_task.context->>'type' IS DISTINCT FROM 'agent_radar'
		        OR workspace_radar_task_is_authorized(queued_task.id)
		      )
		      AND (queued_task.created_at, queued_task.id) < (event.created_at, event.id)
		  )
		FOR UPDATE OF event`, eventID, agentID, runtime.ID).Scan(&eventID)
	if err != nil {
		return db.AgentEventDelivery{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_inbox_event
		SET status = 'draining', claimed_at = now(), attempt = attempt + 1, updated_at = now()
		WHERE id = $1`, eventID); err != nil {
		return db.AgentEventDelivery{}, err
	}
	var delivery db.AgentEventDelivery
	err = tx.QueryRow(ctx, `
		INSERT INTO agent_event_delivery (
		  workspace_id, agent_session_id, inbox_event_id, runtime_id, status
		)
		SELECT workspace_id, agent_session_id, id, $2, 'leased'
		FROM agent_inbox_event
		WHERE id = $1
		RETURNING id, workspace_id, agent_session_id, inbox_event_id, runtime_id,
		          status, lease_token, leased_at, lease_expires_at, acked_at,
		          last_error, created_at, updated_at`, eventID, runtime.ID).Scan(
		&delivery.ID, &delivery.WorkspaceID, &delivery.AgentSessionID, &delivery.InboxEventID,
		&delivery.RuntimeID, &delivery.Status, &delivery.LeaseToken, &delivery.LeasedAt,
		&delivery.LeaseExpiresAt, &delivery.AckedAt, &delivery.LastError,
		&delivery.CreatedAt, &delivery.UpdatedAt,
	)
	if err != nil {
		return db.AgentEventDelivery{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE channel_agent_onboarding onboarding
		SET status = 'claimed',
		    claimed_at = COALESCE(onboarding.claimed_at, now()),
		    updated_at = now()
		FROM agent_inbox_event event
		WHERE event.id = $1
		  AND event.channel_onboarding_id = onboarding.id
		  AND onboarding.status = 'pending'`, eventID); err != nil {
		return db.AgentEventDelivery{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return db.AgentEventDelivery{}, err
	}
	return delivery, nil
}

func (h *Handler) countReadyAgentInboxEventsForRuntime(ctx context.Context, runtime db.AgentRuntime) (int64, error) {
	var count int64
	err := h.DB.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_inbox_event event
		JOIN agent_session session ON session.id = event.agent_session_id
		WHERE COALESCE(event.runtime_id, session.runtime_id) = $1
		  AND session.status = 'active'
		  AND event.status IN ('pending', 'failed')`, runtime.ID).Scan(&count)
	return count, err
}
