package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func (h *Handler) createOrCoalesceChannelAmbientWakeTx(ctx context.Context, tx pgx.Tx, ch ChannelResponse, agent db.Agent, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID) (db.AgentInboxEvent, bool, bool) {
	conversationID, workspaceID, cursorSeq, pendingToSeq, ok := h.channelAmbientWakeCursorTx(ctx, tx, ch, agent, trigger)
	if !ok {
		return db.AgentInboxEvent{}, false, false
	}

	var existingTaskID pgtype.UUID
	var existingTaskStatus string
	var existingPendingToSeq int64
	err := tx.QueryRow(ctx, `
		SELECT w.task_id, w.pending_to_seq, COALESCE(atq.status, '')
		FROM channel_ambient_pending_wake w
		LEFT JOIN agent_inbox_event atq ON atq.id = w.task_id
		WHERE w.conversation_id = $1 AND w.agent_id = $2
		FOR UPDATE OF w`, conversationID, agent.ID).Scan(&existingTaskID, &existingPendingToSeq, &existingTaskStatus)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		slog.Warn("channel ambient wake: load pending row failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
		return db.AgentInboxEvent{}, false, false
	}
	if err == nil && isActiveTaskStatus(existingTaskStatus) {
		if _, updateErr := tx.Exec(ctx, `
			UPDATE channel_ambient_pending_wake
			SET pending_to_seq = GREATEST(pending_to_seq, $3),
			    last_trigger_message_id = COALESCE($4, last_trigger_message_id),
			    last_decision = $5,
			    updated_at = now()
			WHERE conversation_id = $1 AND agent_id = $2`,
			conversationID, agent.ID, pendingToSeq, nullableUUID(channelAmbientTriggerID(trigger)), channelAmbientGateReasonAgentActive); updateErr != nil {
			slog.Warn("channel ambient wake: coalesce failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", updateErr)
			return db.AgentInboxEvent{}, false, false
		}
		h.recordChannelAmbientGateDecision(channelAmbientGateActionCoalesced, channelAmbientGateReasonAgentActive, ch, agent, trigger)
		return db.AgentInboxEvent{}, false, true
	}

	if existingPendingToSeq > pendingToSeq {
		pendingToSeq = existingPendingToSeq
	}
	// LRM-1079: ambient wakes are channel-only (no forced chat_session).
	txQueries := h.Queries.WithTx(tx)
	promptResult, err := h.enqueueOrCoalesceChannelMessageWakeWithTx(
		ctx, txQueries, tx, ch, agent, trigger, initiatorUserID, conversationID, workspaceID, cursorSeq, pendingToSeq,
	)
	if err != nil {
		slog.Warn("channel ambient wake: enqueue channel-only wake failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
		return db.AgentInboxEvent{}, false, false
	}
	task := promptResult.Event
	lastTriggerID := channelAmbientTriggerID(trigger)
	if _, err := tx.Exec(ctx, `
		INSERT INTO channel_ambient_pending_wake (
		  conversation_id, channel_id, workspace_id, agent_id, chat_session_id, task_id,
		  status, pending_from_seq, pending_to_seq, delivered_to_seq, last_trigger_message_id,
		  last_decision, updated_at, completed_at
		)
		VALUES ($1, $2, $3, $4, NULL, $5, 'queued', $6, $7, $7, $8, $9, now(), NULL)
		ON CONFLICT (conversation_id, agent_id)
		DO UPDATE SET channel_id = EXCLUDED.channel_id,
		              workspace_id = EXCLUDED.workspace_id,
		              chat_session_id = NULL,
		              task_id = EXCLUDED.task_id,
		              status = 'queued',
		              pending_from_seq = EXCLUDED.pending_from_seq,
		              pending_to_seq = GREATEST(channel_ambient_pending_wake.pending_to_seq, EXCLUDED.pending_to_seq),
		              delivered_to_seq = EXCLUDED.delivered_to_seq,
		              last_trigger_message_id = EXCLUDED.last_trigger_message_id,
		              last_decision = EXCLUDED.last_decision,
		              updated_at = now(),
		              completed_at = NULL`,
		conversationID, parseUUID(ch.ID), workspaceID, agent.ID, task.ID, cursorSeq+1, pendingToSeq, nullableUUID(lastTriggerID), channelAmbientGateReasonAccepted); err != nil {
		slog.Warn("channel ambient wake: upsert pending row failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "task", uuidToString(task.ID), "error", err)
		return db.AgentInboxEvent{}, false, false
	}
	return task, !promptResult.Coalesced, true
}

func (h *Handler) channelAmbientWakeCursorTx(ctx context.Context, tx pgx.Tx, ch ChannelResponse, agent db.Agent, trigger ChannelMessageResponse) (pgtype.UUID, pgtype.UUID, int64, int64, bool) {
	var conversationID, workspaceID pgtype.UUID
	var lastSeq, lastReadSeq, lastDeliveredSeq, lastDrainedSeq int64
	err := tx.QueryRow(ctx, `
		WITH conv AS (
		  SELECT id, workspace_id, last_seq
		  FROM conversation
		  WHERE channel_id = $1
		),
		member_state AS (
		  INSERT INTO conversation_member (
		    conversation_id, workspace_id, member_type, member_id, wake_state, followed_at, created_at, updated_at
		  )
		  SELECT conv.id, conv.workspace_id, 'agent', $2, 'active', now(), now(), now()
		  FROM conv
		  ON CONFLICT (conversation_id, member_type, member_id)
		  DO UPDATE SET wake_state = 'active', updated_at = now()
		  RETURNING conversation_id, last_read_seq, last_delivered_seq
		)
		SELECT conv.id,
		       conv.workspace_id,
		       conv.last_seq,
		       member_state.last_read_seq,
		       member_state.last_delivered_seq,
		       COALESCE(session_state.last_drained_seq, 0)
		FROM conv
		JOIN member_state ON member_state.conversation_id = conv.id
		LEFT JOIN agent_session session_state
		  ON session_state.workspace_id = conv.workspace_id
		 AND session_state.conversation_id = conv.id
		 AND session_state.agent_id = $2`,
		parseUUID(ch.ID), agent.ID).Scan(&conversationID, &workspaceID, &lastSeq, &lastReadSeq, &lastDeliveredSeq, &lastDrainedSeq)
	if err != nil {
		slog.Warn("channel ambient wake: load cursor failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
		return pgtype.UUID{}, pgtype.UUID{}, 0, 0, false
	}
	cursorSeq := lastReadSeq
	if lastDeliveredSeq > cursorSeq {
		cursorSeq = lastDeliveredSeq
	}
	if lastDrainedSeq > cursorSeq {
		cursorSeq = lastDrainedSeq
	}
	pendingToSeq := lastSeq
	if trigger.Seq > pendingToSeq {
		pendingToSeq = trigger.Seq
	}
	if pendingToSeq <= cursorSeq {
		pendingToSeq = cursorSeq
	}
	return conversationID, workspaceID, cursorSeq, pendingToSeq, true
}

func (h *Handler) markConversationMemberDeliveredTx(ctx context.Context, tx pgx.Tx, conversationID pgtype.UUID, memberType string, memberID pgtype.UUID, deliveredToSeq int64) {
	if deliveredToSeq <= 0 {
		return
	}
	if _, err := tx.Exec(ctx, `
		UPDATE conversation_member
		SET last_delivered_seq = GREATEST(last_delivered_seq, $4),
		    updated_at = now()
		WHERE conversation_id = $1
		  AND member_type = $2
		  AND member_id = $3`,
		conversationID, memberType, memberID, deliveredToSeq); err != nil {
		slog.Warn("conversation member delivered cursor update failed", "conversation", uuidToString(conversationID), "member_type", memberType, "member_id", uuidToString(memberID), "error", err)
	}
}

func (h *Handler) buildChannelAmbientUnreadPromptWithDB(ctx context.Context, exec db.DBTX, ch ChannelResponse, agent db.Agent, trigger ChannelMessageResponse, cursorSeq, pendingToSeq int64) string {
	messages := h.channelAmbientUnreadMessages(ctx, exec, ch.WorkspaceID, ch.ID, cursorSeq, pendingToSeq, channelContextMessageLimit)
	if len(messages) == 0 && strings.TrimSpace(trigger.Content) != "" {
		messages = []ChannelMessageResponse{trigger}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "You are a member of the Multica group chat #%s. New non-directed messages arrived since your ambient cursor.\n", ch.Name)
	b.WriteString("Use ONLY the unread bundle below for this ambient observation. Do not assume older channel context unless you explicitly fetch/search it.\n")
	b.WriteString(channelOutputContractInstruction)
	b.WriteString("\n")
	b.WriteString(channelAmbientNoReplyInstruction)
	b.WriteString("\n")
	b.WriteString(channelAmbientGreetingReactionInstruction)
	b.WriteString("\n")
	b.WriteString(channelAmbientAlreadyDelegatedInstruction)
	b.WriteString("\n")
	b.WriteString("Decide whether your own role/profile makes a response useful. If it is not clearly relevant to you, finish without visible output; do not print no_reply or protocol text.\n")
	b.WriteString("If the unread bundle directly addresses your agent name, role, description, instructions, or an unmistakable task for you, treat it as directed to you: write a visible reply or acknowledgement using the requested supported delivery modality, and do not return no_reply.\n")
	b.WriteString("If a lightweight acknowledgement is enough outside an all-hands welcome/greeting request, use a reaction when the runtime brief supports reactions and a reaction is sufficient; otherwise send a short acknowledgement.\n")
	b.WriteString(channelStickerReplyInstruction)
	b.WriteString("\n")
	b.WriteString(channelContinuationInstruction)
	if instruction := channelVoiceReplyInstruction(trigger); instruction != "" {
		b.WriteString("\n")
		b.WriteString(instruction)
	}
	b.WriteString("\nDo not @-mention anyone from this ambient observation.\n\n")
	fmt.Fprintf(&b, "Reaction target message id: %s\n", trigger.ID)
	fmt.Fprintf(&b, "Ambient cursor range: seq > %d and seq <= %d\n", cursorSeq, pendingToSeq)
	fmt.Fprintf(&b, "Your agent name: %s\n", agentDisplayName(agent))
	if strings.TrimSpace(agent.Description) != "" {
		fmt.Fprintf(&b, "Your agent description: %s\n", strings.TrimSpace(agent.Description))
	}
	if strings.TrimSpace(agent.Instructions) != "" {
		fmt.Fprintf(&b, "Your agent instructions: %s\n", strings.TrimSpace(agent.Instructions))
	}
	if len(messages) > 0 {
		b.WriteString("\nUnread bundle:\n")
		for _, msg := range messages {
			fmt.Fprintf(&b, "%s\n", formatChannelMessageLine(msg))
		}
	}
	return b.String()
}

func (h *Handler) channelAmbientUnreadMessages(ctx context.Context, exec db.DBTX, workspaceID, channelID string, cursorSeq, pendingToSeq int64, limit int) []ChannelMessageResponse {
	if limit <= 0 {
		limit = channelContextMessageLimit
	}
	rows, err := exec.Query(ctx, `
		SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		FROM (
		  SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		  FROM channel_message
		  WHERE channel_id = $1
		    AND workspace_id = $2
		    AND thread_root_message_id IS NULL
		    AND seq > $3
		    AND seq <= $4
		  ORDER BY seq DESC
		  LIMIT $5
		) recent
		ORDER BY seq ASC`, parseUUID(channelID), parseUUID(workspaceID), cursorSeq, pendingToSeq, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []ChannelMessageResponse{}
	for rows.Next() {
		msg, err := scanChannelMessage(rows)
		if err != nil {
			continue
		}
		out = append(out, msg)
	}
	return out
}

func (h *Handler) settleChannelAmbientWakeForTask(ctx context.Context, taskID pgtype.UUID, success bool) {
	if !taskID.Valid || h.TxStarter == nil || h.TaskService == nil {
		return
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		slog.Warn("channel ambient wake: settle begin failed", "task", uuidToString(taskID), "error", err)
		return
	}
	defer tx.Rollback(ctx)

	var conversationID, channelID, workspaceID, agentID, lastTriggerID pgtype.UUID
	var pendingToSeq, deliveredToSeq int64
	err = tx.QueryRow(ctx, `
		SELECT conversation_id, channel_id, workspace_id, agent_id, last_trigger_message_id, pending_to_seq, delivered_to_seq
		FROM channel_ambient_pending_wake
		WHERE task_id = $1
		FOR UPDATE`, taskID).Scan(&conversationID, &channelID, &workspaceID, &agentID, &lastTriggerID, &pendingToSeq, &deliveredToSeq)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("channel ambient wake: settle load failed", "task", uuidToString(taskID), "error", err)
		}
		return
	}
	if !success {
		if _, err := tx.Exec(ctx, `
			UPDATE channel_ambient_pending_wake
			SET status = 'failed', completed_at = now(), updated_at = now()
			WHERE task_id = $1`, taskID); err != nil {
			slog.Warn("channel ambient wake: mark failed failed", "task", uuidToString(taskID), "error", err)
		}
		_ = tx.Commit(ctx)
		return
	}
	if pendingToSeq <= deliveredToSeq {
		h.markConversationMemberDeliveredTx(ctx, tx, conversationID, "agent", agentID, pendingToSeq)
		if _, err := tx.Exec(ctx, `
			UPDATE channel_ambient_pending_wake
			SET status = 'completed', completed_at = now(), updated_at = now()
			WHERE task_id = $1`, taskID); err != nil {
			slog.Warn("channel ambient wake: mark completed failed", "task", uuidToString(taskID), "error", err)
		}
		_ = tx.Commit(ctx)
		return
	}

	ch, found := h.getChannel(ctx, uuidToString(workspaceID), channelID)
	if !found {
		return
	}
	agent, err := h.Queries.GetAgent(ctx, agentID)
	if err != nil {
		slog.Warn("channel ambient wake: load agent for drain failed", "task", uuidToString(taskID), "agent", uuidToString(agentID), "error", err)
		return
	}
	trigger := ChannelMessageResponse{ID: uuidToString(lastTriggerID), ChannelID: uuidToString(channelID), WorkspaceID: uuidToString(workspaceID), Seq: pendingToSeq, Type: "user"}
	if lastTriggerID.Valid {
		if msg, ok := h.channelMessageByID(ctx, uuidToString(workspaceID), uuidToString(channelID), uuidToString(lastTriggerID)); ok {
			trigger = msg
		}
	}
	prompt := h.buildChannelAmbientUnreadPromptWithDB(ctx, tx, ch, agent, trigger, deliveredToSeq, pendingToSeq)
	// LRM-1079: ambient drain follow-ups are channel-only (no chat_session bridge).
	txQueries := h.Queries.WithTx(tx)
	promptResult, err := h.enqueueChannelAgentPromptRangeWithTx(
		ctx, txQueries, tx, ch, agent, trigger, pgtype.UUID{}, prompt,
		channelMessageWakeReason, channelMessageWakePriority, deliveredToSeq+1, pendingToSeq,
	)
	if err != nil {
		slog.Warn("channel ambient wake: enqueue channel-only drain failed", "task", uuidToString(taskID), "error", err)
		return
	}
	newTask := promptResult.Event
	if _, err := tx.Exec(ctx, `
		UPDATE channel_ambient_pending_wake
		SET task_id = $2,
		    chat_session_id = NULL,
		    status = 'queued',
		    pending_from_seq = $3,
		    delivered_to_seq = $4,
		    updated_at = now(),
		    completed_at = NULL
		WHERE task_id = $1`,
		taskID, newTask.ID, deliveredToSeq+1, pendingToSeq); err != nil {
		slog.Warn("channel ambient wake: advance drain row failed", "task", uuidToString(taskID), "new_task", uuidToString(newTask.ID), "error", err)
		return
	}
	h.markConversationMemberDeliveredTx(ctx, tx, conversationID, "agent", agentID, deliveredToSeq)
	if err := tx.Commit(ctx); err != nil {
		slog.Warn("channel ambient wake: drain commit failed", "task", uuidToString(taskID), "new_task", uuidToString(newTask.ID), "error", err)
		return
	}
	h.TaskService.PublishChatTaskQueued(ctx, newTask, false)
}

func channelAmbientTriggerID(trigger ChannelMessageResponse) pgtype.UUID {
	if strings.TrimSpace(trigger.ID) == "" {
		return pgtype.UUID{}
	}
	id, err := util.ParseUUID(trigger.ID)
	if err != nil {
		return pgtype.UUID{}
	}
	return id
}
