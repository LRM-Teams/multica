package handler

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

// channelAgentWake carries everything needed to publish and activity-log a
// Collaboration turn grant.
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

func channelMessageTriggerID(trigger ChannelMessageResponse) pgtype.UUID {
	if strings.TrimSpace(trigger.ID) == "" {
		return pgtype.UUID{}
	}
	return parseUUID(trigger.ID)
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

// leaseAgentInboxEventForRuntime admits one eligible per-agent priority head.
// Pending events use priority DESC and FIFO within equal priority. After
// locking the chosen agent, a second statement revalidates that same ordering
// and the active-delivery predicate against a fresh READ COMMITTED snapshot.
//
// Any membership-poison rows terminalized in this transaction are committed
// even when no delivery is leased (poison-only / drained-to-empty / hit max),
// so cleanup is durable across drain polls.
func (h *Handler) channelAgentMembersWithDB(ctx context.Context, exec db.DBTX, workspaceID, channelID string) ([]db.Agent, error) {
	rows, err := exec.Query(ctx, `
		SELECT a.id, a.workspace_id, a.name, a.avatar_url, a.runtime_mode, a.runtime_config, a.status,
		       a.owner_id, a.created_at, a.updated_at, a.description, a.runtime_id,
		       a.instructions, a.archived_at, a.display_name, a.model, a.thinking_level
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
		if err := rows.Scan(&agent.ID, &agent.WorkspaceID, &agent.Name, &agent.AvatarUrl, &agent.RuntimeMode, &agent.RuntimeConfig, &agent.Status, &agent.OwnerID, &agent.CreatedAt, &agent.UpdatedAt, &agent.Description, &agent.RuntimeID, &agent.Instructions, &agent.ArchivedAt, &agent.DisplayName, &agent.Model, &agent.ThinkingLevel); err != nil {
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
// Any membership-poison rows terminalized in this transaction are committed
// even when no delivery is leased (poison-only / drained-to-empty / hit max),
// so cleanup is durable across drain polls.

// agentInboxConversationFoldMax caps same-conversation events leased in one
// drain so a pathological backlog cannot monopolize one turn. Primary + extras.
const agentInboxConversationFoldMax = 32

// leaseAgentInboxEventForRuntime leases the next ready event for a runtime.
// Kept as the single-event API used by unit tests and heal paths.
func (h *Handler) leaseAgentInboxEventForRuntime(ctx context.Context, runtime db.AgentRuntime) (db.AgentEventDelivery, error) {
	deliveries, err := h.leaseAgentInboxConversationBatchForRuntime(ctx, runtime, 1)
	if err != nil {
		return db.AgentEventDelivery{}, err
	}
	if len(deliveries) == 0 {
		return db.AgentEventDelivery{}, pgx.ErrNoRows
	}
	return deliveries[0], nil
}

// leaseAgentInboxConversationBatchForRuntime leases the head ready event and,
// when maxEvents > 1 and the head has a conversation_id, also leases other
// pending/failed events for the same (agent, conversation) on this runtime.
// All leases are created in one transaction so nothing is parked across turns
// (Alice boundary: no lease-race from drain-ahead of a different conversation).
func (h *Handler) leaseAgentInboxConversationBatchForRuntime(ctx context.Context, runtime db.AgentRuntime, maxEvents int) ([]db.AgentEventDelivery, error) {
	if maxEvents <= 0 {
		maxEvents = 1
	}
	if maxEvents > agentInboxConversationFoldMax {
		maxEvents = agentInboxConversationFoldMax
	}
	if h.TxStarter == nil {
		return nil, errors.New("transaction starter unavailable")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('channel_agent_inbox_runtime'), hashtext($1))`, uuidToString(runtime.ID)); err != nil {
		return nil, err
	}

	// Clear up to a few membership-poison / stale-runtime heads in one
	// transaction so a directed wake immediately behind them can be leased
	// without waiting for another drain poll. Bound the loop so a pathological
	// backlog cannot monopolize the drain request.
	const maxPoisonTerminalizations = 8
	terminalizedCount := 0
	healedRuntimeIDs := map[string]struct{}{}
	// commitNoDelivery makes any in-tx poison cleanup / runtime heal durable
	// before reporting no leasable event. Without this, poison-only /
	// empty-tail / max-cap paths would return ErrNoRows with defer Rollback
	// and leave poisons pending (or leave events pinned to a stale runtime).
	commitNoDelivery := func() error {
		if terminalizedCount == 0 && len(healedRuntimeIDs) == 0 {
			return pgx.ErrNoRows
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		h.notifyRuntimesAfterInboxHeal(healedRuntimeIDs)
		return pgx.ErrNoRows
	}

	for attempt := 0; attempt <= maxPoisonTerminalizations; attempt++ {
		var eventID, agentID pgtype.UUID
		err = tx.QueryRow(ctx, `
			SELECT event.id, event.agent_id
			FROM agent_inbox_event event
			JOIN agent_session session ON session.id = event.agent_session_id
			JOIN agent agent_row ON agent_row.id = event.agent_id
			WHERE COALESCE(event.runtime_id, session.runtime_id) = $1
			  AND session.status = 'active'
			  AND event.status IN ('pending', 'failed')
			  AND NOT (
			    btrim(COALESCE(agent_row.provider_block_detail, '')) <> ''
			    AND lower(btrim(agent_row.provider_block_detail)) NOT IN ('{}', '[]', 'null', 'undefined', '""')
			    AND (agent_row.provider_blocked_until IS NULL OR agent_row.provider_blocked_until > now())
			  )
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
			    JOIN agent_inbox_event active_event ON active_event.id = active_delivery.inbox_event_id
			    WHERE active_session.agent_id = event.agent_id
			      AND active_event.status = 'draining'
			      AND active_delivery.status IN ('leased', 'processing')
			      AND active_delivery.lease_expires_at > now()
			  )
			ORDER BY event.priority DESC, event.requires_wake DESC, event.created_at, event.id
			LIMIT 1`, runtime.ID).Scan(&eventID, &agentID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, commitNoDelivery()
			}
			return nil, err
		}

		// #801 lock order (shared with remove):
		// 1) read channel_id (no row lock yet)
		// 2) advisory(channel×agent)
		// 3) FOR UPDATE agent + event
		// 4) membership check on same tx
		// Taking advisory after FOR UPDATE event deadlocks with remove.
		var eventChannelID pgtype.UUID
		var eventWorkspaceID pgtype.UUID
		var eventConversationID pgtype.UUID
		if err := tx.QueryRow(ctx, `
			SELECT workspace_id, channel_id, conversation_id FROM agent_inbox_event WHERE id = $1`, eventID).
			Scan(&eventWorkspaceID, &eventChannelID, &eventConversationID); err != nil {
			return nil, err
		}
		if eventChannelID.Valid {
			if _, err := tx.Exec(ctx, `
				SELECT pg_advisory_xact_lock(
					hashtext('agent_channel_membership_revoke'),
					hashtext($1 || ':' || $2)
				)`, uuidToString(eventChannelID), uuidToString(agentID)); err != nil {
				return nil, err
			}
		}

		var agentRuntimeID pgtype.UUID
		var agentArchivedAt pgtype.Timestamptz
		var providerBlockedUntil pgtype.Timestamptz
		var providerBlockDetail string
		if err := tx.QueryRow(ctx, `
			SELECT id, runtime_id, archived_at, provider_blocked_until, provider_block_detail
			FROM agent
			WHERE id = $1
			FOR UPDATE`, agentID).Scan(&agentID, &agentRuntimeID, &agentArchivedAt, &providerBlockedUntil, &providerBlockDetail); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, commitNoDelivery()
			}
			return nil, err
		}
		// Sticky provider-quota lock (task #92): skip without terminalizing —
		// wakes stay pending until the lock clears (until elapses or detail cleared).
		if taskfailure.ProviderLockActive(providerBlockDetail, providerBlockedUntil.Time, providerBlockedUntil.Valid, time.Now()) {
			return nil, commitNoDelivery()
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
			    JOIN agent_inbox_event active_event ON active_event.id = active_delivery.inbox_event_id
			    WHERE active_session.agent_id = event.agent_id
			      AND active_event.status = 'draining'
			      AND active_delivery.status IN ('leased', 'processing')
			      AND active_delivery.lease_expires_at > now()
			  )
			FOR UPDATE OF event`, eventID, agentID, runtime.ID).Scan(&eventID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, commitNoDelivery()
			}
			return nil, err
		}

		// Agent was reassigned (or archived) after the inbox event pinned this
		// runtime_id. UpdateAgent does not rewrite historical event.runtime_id
		// (see CancelAgentTasksByRuntimeOrAgent), so without this heal the old
		// daemon leases the event, ensure-credential 403s ("agent is not bound
		// to this runtime"), and Activity fills with credential_unavailable.
		// Move claimable events onto the agent's current runtime and wake it.
		if agentArchivedAt.Valid || !agentRuntimeID.Valid {
			terminalized, termErr := terminalizeStaleAgentInboxEventTx(ctx, tx, eventID,
				"agent is archived or has no runtime", "agent_unavailable")
			if termErr != nil {
				return nil, termErr
			}
			if terminalized {
				terminalizedCount++
			}
			if attempt == maxPoisonTerminalizations {
				return nil, commitNoDelivery()
			}
			continue
		}
		if agentRuntimeID != runtime.ID {
			healed, healErr := reassignStaleRuntimeInboxEventTx(ctx, tx, eventID, agentRuntimeID)
			if healErr != nil {
				return nil, healErr
			}
			if healed {
				healedRuntimeIDs[uuidToString(agentRuntimeID)] = struct{}{}
			}
			if attempt == maxPoisonTerminalizations {
				return nil, commitNoDelivery()
			}
			continue
		}

		if eventChannelID.Valid {
			if !agentHasDirectChannelMembership(ctx, tx, eventWorkspaceID, agentID, eventChannelID) {
				terminalized, termErr := terminalizeUnauthorizedMembershipInboxEventTx(ctx, tx, eventID)
				if termErr != nil {
					return nil, termErr
				}
				if terminalized {
					terminalizedCount++
				}
				if attempt == maxPoisonTerminalizations {
					return nil, commitNoDelivery()
				}
				continue
			}
		}

		// Only proceed to delivery INSERT when the row actually transitioned
		// to draining. A miss here means another concurrent lease already
		// claimed it; keep any prior poison cleanups durable and report no
		// delivery for this attempt.
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
				return nil, err
			}
			return nil, commitNoDelivery()
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
			return nil, err
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
			return nil, err
		}

		deliveries := []db.AgentEventDelivery{delivery}
		// Turn-fold: same-conversation siblings join this one turn. They are
		// leased in this same TX so no cross-conversation park/lease-race.
		if maxEvents > 1 && eventConversationID.Valid {
			siblings, sibErr := leaseSameConversationSiblingInboxEventsTx(
				ctx, tx, runtime, agentID, eventConversationID, eventID, maxEvents-1,
			)
			if sibErr != nil {
				return nil, sibErr
			}
			deliveries = append(deliveries, siblings...)
		}

		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		h.notifyRuntimesAfterInboxHeal(healedRuntimeIDs)
		return deliveries, nil
	}
	return nil, commitNoDelivery()
}

// leaseSameConversationSiblingInboxEventsTx claims additional pending/failed
// events for the same agent+conversation so one drain returns one conversation
// batch. Skips the "no active delivery" gate (primary already holds a lease)
// and does not cross conversation boundaries. Membership poisons are
// terminalized in-place; other failures abort the batch TX.
func leaseSameConversationSiblingInboxEventsTx(
	ctx context.Context,
	tx pgx.Tx,
	runtime db.AgentRuntime,
	agentID, conversationID, primaryEventID pgtype.UUID,
	maxExtra int,
) ([]db.AgentEventDelivery, error) {
	if maxExtra <= 0 || !conversationID.Valid || !agentID.Valid || !primaryEventID.Valid {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT event.id, event.workspace_id, event.channel_id
		FROM agent_inbox_event event
		JOIN agent_session session ON session.id = event.agent_session_id
		WHERE event.agent_id = $1
		  AND event.conversation_id = $2
		  AND event.id <> $3
		  AND COALESCE(event.runtime_id, session.runtime_id) = $4
		  AND session.status = 'active'
		  AND event.status IN ('pending', 'failed')
		ORDER BY event.priority DESC, event.requires_wake DESC, event.created_at, event.id
		LIMIT $5
		FOR UPDATE OF event SKIP LOCKED`,
		agentID, conversationID, primaryEventID, runtime.ID, maxExtra,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type siblingCandidate struct {
		id          pgtype.UUID
		workspaceID pgtype.UUID
		channelID   pgtype.UUID
	}
	var candidates []siblingCandidate
	for rows.Next() {
		var c siblingCandidate
		if err := rows.Scan(&c.id, &c.workspaceID, &c.channelID); err != nil {
			return nil, err
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var deliveries []db.AgentEventDelivery
	for _, c := range candidates {
		if c.channelID.Valid {
			if !agentHasDirectChannelMembership(ctx, tx, c.workspaceID, agentID, c.channelID) {
				if _, termErr := terminalizeUnauthorizedMembershipInboxEventTx(ctx, tx, c.id); termErr != nil {
					return nil, termErr
				}
				continue
			}
		}
		var claimedID pgtype.UUID
		err := tx.QueryRow(ctx, `
			UPDATE agent_inbox_event
			SET status = 'draining',
			    claimed_at = now(),
			    dispatched_at = now(),
			    updated_at = now()
			WHERE id = $1
			  AND status IN ('pending', 'failed')
			RETURNING id`, c.id).Scan(&claimedID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return nil, err
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
			          last_error, created_at, updated_at`, claimedID, runtime.ID).Scan(
			&delivery.ID, &delivery.WorkspaceID, &delivery.AgentSessionID, &delivery.InboxEventID,
			&delivery.RuntimeID, &delivery.Status, &delivery.LeaseToken, &delivery.LeasedAt,
			&delivery.LeaseExpiresAt, &delivery.AckedAt, &delivery.LastError,
			&delivery.CreatedAt, &delivery.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE channel_agent_onboarding onboarding
			SET status = 'claimed',
			    claimed_at = COALESCE(onboarding.claimed_at, now()),
			    updated_at = now()
			FROM agent_inbox_event event
			WHERE event.id = $1
			  AND event.channel_onboarding_id = onboarding.id
			  AND onboarding.status = 'pending'`, claimedID); err != nil {
			return nil, err
		}
		deliveries = append(deliveries, delivery)
	}
	return deliveries, nil
}

// reassignStaleRuntimeInboxEventTx moves a still-claimable inbox event (and its
// session) onto the agent's current runtime after a reassignment. Returns true
// when the event row was updated.
func reassignStaleRuntimeInboxEventTx(ctx context.Context, tx pgx.Tx, eventID, newRuntimeID pgtype.UUID) (bool, error) {
	if !newRuntimeID.Valid {
		return false, nil
	}
	var movedID pgtype.UUID
	err := tx.QueryRow(ctx, `
		UPDATE agent_inbox_event e
		SET runtime_id = $2,
		    updated_at = now(),
		    last_error = NULL
		WHERE e.id = $1
		  AND e.status IN ('pending', 'failed')
		  AND e.runtime_id IS DISTINCT FROM $2
		RETURNING e.id`, eventID, newRuntimeID).Scan(&movedID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_session s
		SET runtime_id = $2,
		    updated_at = now()
		FROM agent_inbox_event e
		WHERE e.id = $1
		  AND s.id = e.agent_session_id
		  AND s.runtime_id IS DISTINCT FROM $2`, eventID, newRuntimeID); err != nil {
		return false, err
	}
	return true, nil
}

func (h *Handler) notifyRuntimesAfterInboxHeal(runtimeIDs map[string]struct{}) {
	if h == nil || h.DaemonHub == nil || len(runtimeIDs) == 0 {
		return
	}
	for runtimeID := range runtimeIDs {
		if strings.TrimSpace(runtimeID) == "" {
			continue
		}
		h.DaemonHub.NotifyTaskAvailable(runtimeID, "")
	}
}

// reassignClaimableInboxEventsAfterAgentRuntimeMove rewrites still-claimable
// inbox events (and their sessions) that were snapshotted onto oldRuntimeID
// when the agent moves. Best-effort: UpdateAgent already committed; a heal
// miss is recovered on the next old-runtime drain via leaseAgentInboxEventForRuntime.
func (h *Handler) reassignClaimableInboxEventsAfterAgentRuntimeMove(ctx context.Context, agentID, oldRuntimeID, newRuntimeID pgtype.UUID) {
	if h == nil || h.DB == nil || !agentID.Valid || !oldRuntimeID.Valid || !newRuntimeID.Valid || oldRuntimeID == newRuntimeID {
		return
	}
	tag, err := h.DB.Exec(ctx, `
		UPDATE agent_inbox_event
		SET runtime_id = $3,
		    updated_at = now(),
		    last_error = NULL
		WHERE agent_id = $1
		  AND runtime_id = $2
		  AND status IN ('pending', 'failed')`, agentID, oldRuntimeID, newRuntimeID)
	if err != nil {
		slog.Warn("agent runtime move: reassign claimable inbox events failed",
			"agent_id", uuidToString(agentID),
			"old_runtime_id", uuidToString(oldRuntimeID),
			"new_runtime_id", uuidToString(newRuntimeID),
			"error", err)
		return
	}
	if _, err := h.DB.Exec(ctx, `
		UPDATE agent_session
		SET runtime_id = $3,
		    updated_at = now()
		WHERE agent_id = $1
		  AND runtime_id = $2
		  AND status = 'active'`, agentID, oldRuntimeID, newRuntimeID); err != nil {
		slog.Warn("agent runtime move: reassign agent sessions failed",
			"agent_id", uuidToString(agentID),
			"old_runtime_id", uuidToString(oldRuntimeID),
			"new_runtime_id", uuidToString(newRuntimeID),
			"error", err)
	}
	moved := tag.RowsAffected()
	if moved > 0 {
		slog.Info("agent runtime move: reassigned claimable inbox events",
			"agent_id", uuidToString(agentID),
			"old_runtime_id", uuidToString(oldRuntimeID),
			"new_runtime_id", uuidToString(newRuntimeID),
			"events_moved", moved)
		h.notifyRuntimesAfterInboxHeal(map[string]struct{}{uuidToString(newRuntimeID): {}})
	}
}

// terminalizeStaleAgentInboxEventTx permanently fails a still-claimable inbox
// event that can never succeed on the draining runtime (archived agent, etc.).
func terminalizeStaleAgentInboxEventTx(ctx context.Context, tx pgx.Tx, eventID pgtype.UUID, errMsg, failureReason string) (bool, error) {
	var terminalizedID pgtype.UUID
	err := tx.QueryRow(ctx, `
		UPDATE agent_inbox_event e
		SET status = 'acked',
		    completed_at = now(),
		    terminal_at = now(),
		    acked_at = now(),
		    terminal_outcome = 'failed',
		    error = $2,
		    failure_reason = $3,
		    updated_at = now()
		WHERE e.id = $1
		  AND e.status IN ('pending', 'failed', 'draining')
		  AND e.terminal_outcome IS NULL
		RETURNING e.id`, eventID, errMsg, failureReason).Scan(&terminalizedID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_event_delivery d
		SET status = 'failed',
		    last_error = $2,
		    updated_at = now()
		WHERE d.inbox_event_id = $1
		  AND d.status IN ('leased', 'processing')`, eventID, errMsg); err != nil {
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

// terminalizeUnauthorizedMembershipInboxEventTx permanently fails a pending/
// failed inbox event whose agent is no longer a channel member (#801).
func terminalizeUnauthorizedMembershipInboxEventTx(ctx context.Context, tx pgx.Tx, eventID pgtype.UUID) (bool, error) {
	return terminalizeStaleAgentInboxEventTx(ctx, tx, eventID,
		"agent is not a channel member", "membership_revoked")
}

// terminalizeUnauthorizedRadarInboxEventTx permanently fails a still-pending
// agent_radar event whose dispatch guard will never admit it. Returns true
// when a row was terminalized so the caller may reselect a later FIFO head.
func (h *Handler) countReadyAgentInboxEventsForRuntime(ctx context.Context, runtime db.AgentRuntime) (int64, error) {
	var count int64
	err := h.DB.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_inbox_event event
		JOIN agent_session session ON session.id = event.agent_session_id
		JOIN agent agent_row ON agent_row.id = event.agent_id
		WHERE COALESCE(event.runtime_id, session.runtime_id) = $1
		  AND session.status = 'active'
		  AND event.status IN ('pending', 'failed')
		  AND NOT (
		    btrim(COALESCE(agent_row.provider_block_detail, '')) <> ''
		    AND lower(btrim(agent_row.provider_block_detail)) NOT IN ('{}', '[]', 'null', 'undefined', '""')
		    AND (agent_row.provider_blocked_until IS NULL OR agent_row.provider_blocked_until > now())
		  )`, runtime.ID).Scan(&count)
	return count, err
}
