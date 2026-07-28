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

// leaseAgentInboxEventForRuntime admits one eligible per-agent priority head.
// Pending events use priority DESC and FIFO within equal priority. After
// locking the chosen agent, a second statement revalidates that same ordering
// and the active-delivery predicate against a fresh READ COMMITTED snapshot.
//
// The pending→draining transition is hard-gated on UPDATE ... RETURNING: a
// BEFORE trigger (Radar authorization guard) may suppress the row change with
// RETURN NULL. In that case we must never INSERT a delivery. Permanently
// unauthorized Radar heads are terminalized so they cannot livelock FIFO.
// Any poison rows terminalized in this transaction are committed even when no
// delivery is leased (poison-only / drained-to-empty / hit max), so cleanup is
// durable across drain polls.
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

	// Clear up to a few unauthorized Radar poison heads in one transaction so
	// a directed wake immediately behind them can be leased without waiting
	// for another drain poll. Bound the loop so a pathological backlog cannot
	// monopolize the drain request.
	const maxPoisonTerminalizations = 8
	terminalizedCount := 0
	// commitNoDelivery makes any in-tx poison cleanup durable before reporting
	// no leasable event. Without this, poison-only / empty-tail / max-cap paths
	// would return ErrNoRows with defer Rollback and leave poisons pending.
	commitNoDelivery := func() error {
		if terminalizedCount == 0 {
			return pgx.ErrNoRows
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return pgx.ErrNoRows
	}

	for attempt := 0; attempt <= maxPoisonTerminalizations; attempt++ {
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
			    FROM agent_inbox_event blocking_event
			    JOIN agent_session blocking_session
			      ON blocking_session.id = blocking_event.agent_session_id
			    WHERE blocking_event.agent_id = event.agent_id
			      AND blocking_session.status = 'active'
			      AND blocking_event.status IN ('pending', 'failed')
			      AND (
			        blocking_event.priority > event.priority
			        OR (
			          blocking_event.priority = event.priority
			          AND (blocking_event.created_at, blocking_event.id) < (event.created_at, event.id)
			        )
			      )
			  )
			  AND NOT EXISTS (
			    SELECT 1
			    FROM agent_event_delivery active_delivery
			    JOIN agent_session active_session ON active_session.id = active_delivery.agent_session_id
			    WHERE active_session.agent_id = event.agent_id
			      AND active_delivery.status IN ('leased', 'processing')
			      AND active_delivery.lease_expires_at > now()
			  )
			ORDER BY event.priority DESC, event.requires_wake DESC, event.created_at, event.id
			LIMIT 1`, runtime.ID).Scan(&eventID, &agentID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return db.AgentEventDelivery{}, commitNoDelivery()
			}
			return db.AgentEventDelivery{}, err
		}

		// #801 lock order (shared with remove):
		// 1) read channel_id (no row lock yet)
		// 2) advisory(channel×agent)
		// 3) FOR UPDATE agent + event
		// 4) membership check on same tx
		// Taking advisory after FOR UPDATE event deadlocks with remove.
		var eventChannelID pgtype.UUID
		var eventWorkspaceID pgtype.UUID
		if err := tx.QueryRow(ctx, `
			SELECT workspace_id, channel_id FROM agent_inbox_event WHERE id = $1`, eventID).
			Scan(&eventWorkspaceID, &eventChannelID); err != nil {
			return db.AgentEventDelivery{}, err
		}
		if eventChannelID.Valid {
			if _, err := tx.Exec(ctx, `
				SELECT pg_advisory_xact_lock(
					hashtext('agent_channel_membership_revoke'),
					hashtext($1 || ':' || $2)
				)`, uuidToString(eventChannelID), uuidToString(agentID)); err != nil {
				return db.AgentEventDelivery{}, err
			}
		}

		if err := tx.QueryRow(ctx, `
			SELECT id
			FROM agent
			WHERE id = $1
			FOR UPDATE`, agentID).Scan(&agentID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return db.AgentEventDelivery{}, commitNoDelivery()
			}
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
			    FROM agent_inbox_event blocking_event
			    JOIN agent_session blocking_session
			      ON blocking_session.id = blocking_event.agent_session_id
			    WHERE blocking_event.agent_id = event.agent_id
			      AND blocking_session.status = 'active'
			      AND blocking_event.status IN ('pending', 'failed')
			      AND (
			        blocking_event.priority > event.priority
			        OR (
			          blocking_event.priority = event.priority
			          AND (blocking_event.created_at, blocking_event.id) < (event.created_at, event.id)
			        )
			      )
			  )
			  AND NOT EXISTS (
			    SELECT 1
			    FROM agent_event_delivery active_delivery
			    JOIN agent_session active_session ON active_session.id = active_delivery.agent_session_id
			    WHERE active_session.agent_id = event.agent_id
			      AND active_delivery.status IN ('leased', 'processing')
			      AND active_delivery.lease_expires_at > now()
			  )
			FOR UPDATE OF event`, eventID, agentID, runtime.ID).Scan(&eventID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return db.AgentEventDelivery{}, commitNoDelivery()
			}
			return db.AgentEventDelivery{}, err
		}

		if eventChannelID.Valid {
			if !agentHasDirectChannelMembership(ctx, tx, eventWorkspaceID, agentID, eventChannelID) {
				terminalized, termErr := terminalizeUnauthorizedMembershipInboxEventTx(ctx, tx, eventID)
				if termErr != nil {
					return db.AgentEventDelivery{}, termErr
				}
				if terminalized {
					terminalizedCount++
				}
				if attempt == maxPoisonTerminalizations {
					return db.AgentEventDelivery{}, commitNoDelivery()
				}
				continue
			}
		}

		// Hard gate: only proceed to delivery INSERT when the row actually
		// transitioned to draining. Radar's BEFORE UPDATE guard returns NULL
		// when unauthorized; Exec would succeed with 0 rows and silently leak
		// a delivery while the event stays pending (FIFO livelock).
		err = tx.QueryRow(ctx, `
			UPDATE agent_inbox_event
			SET status = 'draining',
			    claimed_at = now(),
			    dispatched_at = now(),
			    updated_at = now()
			WHERE id = $1
			  AND status IN ('pending', 'failed')
			RETURNING id`, eventID).Scan(&eventID)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				return db.AgentEventDelivery{}, err
			}
			// Transition suppressed (guard) or raced. Terminalize unauthorized
			// Radar poison heads so they leave FIFO; then retry candidate
			// selection for a later wake on the same runtime.
			terminalized, termErr := terminalizeUnauthorizedRadarInboxEventTx(ctx, tx, eventID)
			if termErr != nil {
				return db.AgentEventDelivery{}, termErr
			}
			if !terminalized {
				// Race or non-Radar suppression: keep any prior cleanups durable.
				return db.AgentEventDelivery{}, commitNoDelivery()
			}
			terminalizedCount++
			if attempt == maxPoisonTerminalizations {
				// Cap reached after this durable cleanup; later polls continue.
				return db.AgentEventDelivery{}, commitNoDelivery()
			}
			continue
		}

		var delivery db.AgentEventDelivery
		err = tx.QueryRow(ctx, `
			INSERT INTO agent_event_delivery (
			  workspace_id, agent_session_id, inbox_event_id, runtime_id, status
			)
			SELECT workspace_id, agent_session_id, id, $2, 'leased'
			FROM agent_inbox_event
			WHERE id = $1
			  AND status = 'draining'
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
	return db.AgentEventDelivery{}, commitNoDelivery()
}

// terminalizeUnauthorizedMembershipInboxEventTx permanently fails a pending/
// failed inbox event whose agent is no longer a channel member (#801).
func terminalizeUnauthorizedMembershipInboxEventTx(ctx context.Context, tx pgx.Tx, eventID pgtype.UUID) (bool, error) {
	var terminalizedID pgtype.UUID
	err := tx.QueryRow(ctx, `
		UPDATE agent_inbox_event e
		SET status = 'acked',
		    completed_at = now(),
		    terminal_at = now(),
		    acked_at = now(),
		    terminal_outcome = 'failed',
		    error = 'agent is not a channel member',
		    failure_reason = 'membership_revoked',
		    updated_at = now()
		WHERE e.id = $1
		  AND e.status IN ('pending', 'failed', 'draining')
		  AND e.terminal_outcome IS NULL
		RETURNING e.id`, eventID).Scan(&terminalizedID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_event_delivery d
		SET status = 'failed',
		    last_error = 'agent is not a channel member',
		    updated_at = now()
		WHERE d.inbox_event_id = $1
		  AND d.status IN ('leased', 'processing')`, eventID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_execution x
		SET status = 'failed',
		    completed_at = COALESCE(completed_at, now())
		WHERE x.source_kind = 'inbox'
		  AND x.source_event_id = $1
		  AND x.status = 'running'`, eventID); err != nil {
		return false, err
	}
	return true, nil
}

// terminalizeUnauthorizedRadarInboxEventTx permanently fails a still-pending
// agent_radar event whose dispatch guard will never admit it. Returns true
// when a row was terminalized so the caller may reselect a later FIFO head.
func terminalizeUnauthorizedRadarInboxEventTx(ctx context.Context, tx pgx.Tx, eventID pgtype.UUID) (bool, error) {
	var terminalizedID pgtype.UUID
	err := tx.QueryRow(ctx, `
		UPDATE agent_inbox_event e
		SET status = 'acked',
		    completed_at = now(),
		    terminal_at = now(),
		    acked_at = now(),
		    terminal_outcome = 'failed',
		    error = 'radar task not authorized for dispatch',
		    failure_reason = 'radar_unauthorized',
		    updated_at = now()
		WHERE e.id = $1
		  AND e.status IN ('pending', 'failed')
		  AND e.context->>'type' = 'agent_radar'
		  AND NOT workspace_radar_task_is_authorized(e.id)
		RETURNING e.id`, eventID).Scan(&terminalizedID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_radar_run run
		SET status = 'failed',
		    finished_at = COALESCE(finished_at, now()),
		    updated_at = now()
		WHERE run.task_id = $1
		  AND run.status IN ('planned', 'queued', 'running')`, eventID); err != nil {
		return false, err
	}
	return true, nil
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
