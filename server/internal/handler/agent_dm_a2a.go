package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	agentDMDefaultRoundLimit = 3
	agentDMFrequencyWindow   = 5 * time.Minute
)

type agentDMSendReservation struct {
	ExchangeID pgtype.UUID
	Turn       int
	Round      int
	RoundLimit int
	Recipient  pgtype.UUID
}

type agentDMTurnError struct {
	ExpectedSender pgtype.UUID
}

func (e *agentDMTurnError) Error() string {
	return "waiting for the peer agent to reply"
}

func normalizedAgentDMPair(a, b pgtype.UUID) (pgtype.UUID, pgtype.UUID, bool) {
	if !a.Valid || !b.Valid || a == b {
		return pgtype.UUID{}, pgtype.UUID{}, false
	}
	if uuidToString(b) < uuidToString(a) {
		a, b = b, a
	}
	return a, b, true
}

func (h *Handler) reserveAgentDMSendTx(ctx context.Context, exec db.DBTX, source agentTransportSource, target agentTransportTarget) (agentDMSendReservation, error) {
	if target.recipientType != "agent" {
		return agentDMSendReservation{}, nil
	}
	if target.kind != chatOutputTargetDM && target.kind != chatOutputTargetThread {
		return agentDMSendReservation{}, nil
	}
	lowID, highID, ok := normalizedAgentDMPair(source.origin.agentID, target.recipientID)
	if !ok {
		return agentDMSendReservation{}, errChatOutputInvalidTarget
	}
	workspaceID := source.origin.workspaceID
	channelID := parseUUID(target.channel.ID)
	var exactPairDM bool
	if err := exec.QueryRow(ctx, `
		SELECT ch.kind = 'dm'
		  AND count(*) = 2
		  AND count(*) FILTER (WHERE cm.member_type = 'agent') = 2
		  AND bool_and(cm.member_id = ANY($3::uuid[]))
		FROM channel ch
		JOIN channel_member cm
		  ON cm.workspace_id = ch.workspace_id
		 AND cm.channel_id = ch.id
		WHERE ch.workspace_id = $1 AND ch.id = $2
		GROUP BY ch.kind`,
		workspaceID, channelID, []pgtype.UUID{lowID, highID},
	).Scan(&exactPairDM); err != nil || !exactPairDM {
		return agentDMSendReservation{}, errChatOutputInvalidTarget
	}

	if _, err := exec.Exec(ctx, `
		INSERT INTO agent_dm_pair_control (
		  workspace_id, agent_low_id, agent_high_id
		)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING`, workspaceID, lowID, highID); err != nil {
		return agentDMSendReservation{}, fmt.Errorf("ensure agent dm pair control: %w", err)
	}

	var windowStartedAt pgtype.Timestamptz
	var windowMessageCount int
	if err := exec.QueryRow(ctx, `
		SELECT window_started_at, window_message_count
		FROM agent_dm_pair_control
		WHERE workspace_id = $1 AND agent_low_id = $2 AND agent_high_id = $3
		FOR UPDATE`, workspaceID, lowID, highID).Scan(
		&windowStartedAt, &windowMessageCount,
	); err != nil {
		return agentDMSendReservation{}, fmt.Errorf("lock agent dm pair control: %w", err)
	}

	matterID := source.inboxEventID
	if !matterID.Valid {
		matterID = source.legacyTaskID()
	}
	exchangeID := pgtype.UUID{}
	sourceChannelID := pgtype.UUID{}
	sourceMessageID := pgtype.UUID{}
	if source.legacyTask != nil {
		exchangeID = source.legacyTask.AgentDmExchangeID
		sourceChannelID = source.legacyTask.ChannelID
		sourceMessageID = source.legacyTask.SourceMessageID
	}
	if exchangeID.Valid {
		var exchangeLowID, exchangeHighID, exchangeChannelID pgtype.UUID
		if err := exec.QueryRow(ctx, `
			SELECT agent_low_id, agent_high_id, channel_id
			FROM agent_dm_exchange
			WHERE id = $1 AND workspace_id = $2
			FOR UPDATE`, exchangeID, workspaceID).Scan(&exchangeLowID, &exchangeHighID, &exchangeChannelID); err != nil {
			return agentDMSendReservation{}, fmt.Errorf("load inherited agent dm exchange: %w", err)
		}
		if exchangeLowID != lowID || exchangeHighID != highID || exchangeChannelID != channelID {
			return agentDMSendReservation{}, errors.New("agent dm exchange target changed")
		}
	} else {
		if !matterID.Valid {
			matterID = parseUUID(uuid.NewString())
		}
		if _, err := exec.Exec(ctx, `
			INSERT INTO agent_dm_exchange (
			  workspace_id, channel_id, agent_low_id, agent_high_id, matter_id,
			  source_channel_id, source_message_id, round_limit
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (workspace_id, agent_low_id, agent_high_id, matter_id)
			DO NOTHING`,
			workspaceID, channelID, lowID, highID, matterID,
			nullableUUID(sourceChannelID), nullableUUID(sourceMessageID),
			agentDMDefaultRoundLimit,
		); err != nil {
			return agentDMSendReservation{}, fmt.Errorf("create agent dm exchange: %w", err)
		}
		if err := exec.QueryRow(ctx, `
			SELECT id
			FROM agent_dm_exchange
			WHERE workspace_id = $1
			  AND agent_low_id = $2
			  AND agent_high_id = $3
			  AND matter_id = $4
			FOR UPDATE`, workspaceID, lowID, highID, matterID).Scan(&exchangeID); err != nil {
			return agentDMSendReservation{}, fmt.Errorf("lock agent dm exchange: %w", err)
		}
	}

	var exchangeState string
	var nextSenderAgentID pgtype.UUID
	var turnCount, roundLimit, grantedRounds int
	if err := exec.QueryRow(ctx, `
		SELECT state, next_sender_agent_id, turn_count, round_limit, granted_rounds
		FROM agent_dm_exchange
		WHERE id = $1
		FOR UPDATE`, exchangeID).Scan(
		&exchangeState, &nextSenderAgentID, &turnCount, &roundLimit, &grantedRounds,
	); err != nil {
		return agentDMSendReservation{}, fmt.Errorf("load agent dm exchange state: %w", err)
	}

	if nextSenderAgentID.Valid && nextSenderAgentID != source.origin.agentID {
		return agentDMSendReservation{}, &agentDMTurnError{ExpectedSender: nextSenderAgentID}
	}

	// Round/frequency budgets are tracked (below) purely for display — task
	// #813/#830 (Frank, 2026-07-28 & reaffirmed 2026-07-31, #prj-daemon):
	// "把这个硬闸拆掉，改成只观测" (tear out the hard gate, make it
	// observation-only). Agent-pair DMs are never auto-paused for hitting a
	// round or frequency count; only a human (pause_pair/pause_global) can
	// stop an exchange now.
	now := time.Now()
	if !windowStartedAt.Valid || now.Sub(windowStartedAt.Time) >= agentDMFrequencyWindow {
		windowMessageCount = 0
		windowStartedAt = pgtype.Timestamptz{Time: now, Valid: true}
	}

	nextTurn := turnCount + 1
	nextWindowMessageCount := windowMessageCount + 1
	if _, err := exec.Exec(ctx, `
		UPDATE agent_dm_pair_control
		SET window_started_at = $4,
		    window_message_count = $5,
		    updated_at = now()
		WHERE workspace_id = $1 AND agent_low_id = $2 AND agent_high_id = $3`,
		workspaceID, lowID, highID, windowStartedAt, nextWindowMessageCount,
	); err != nil {
		return agentDMSendReservation{}, fmt.Errorf("account agent dm pair frequency: %w", err)
	}
	if _, err := exec.Exec(ctx, `
		UPDATE agent_dm_exchange
		SET turn_count = $2,
		    next_sender_agent_id = $3,
		    updated_at = now()
		WHERE id = $1`, exchangeID, nextTurn, target.recipientID); err != nil {
		return agentDMSendReservation{}, fmt.Errorf("account agent dm exchange turn: %w", err)
	}
	return agentDMSendReservation{
		ExchangeID: exchangeID,
		Turn:       nextTurn,
		Round:      (nextTurn + 1) / 2,
		RoundLimit: roundLimit + grantedRounds,
		Recipient:  target.recipientID,
	}, nil
}

func (h *Handler) finishAgentDMSendTx(ctx context.Context, exec db.DBTX, reservation agentDMSendReservation, messageID pgtype.UUID) error {
	if !reservation.ExchangeID.Valid {
		return nil
	}
	_, err := exec.Exec(ctx, `
		UPDATE agent_dm_exchange
		SET latest_message_id = $2,
		    updated_at = now()
		WHERE id = $1`, reservation.ExchangeID, messageID)
	return err
}

func (h *Handler) dispatchAgentDMAgentReply(ctx context.Context, source agentTransportSource, target agentTransportTarget, trigger ChannelMessageResponse, reservation agentDMSendReservation, initiatorUserID pgtype.UUID) {
	if !reservation.ExchangeID.Valid || !reservation.Recipient.Valid {
		return
	}
	agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
		ID:          reservation.Recipient,
		WorkspaceID: source.origin.workspaceID,
	})
	if err != nil || agent.ArchivedAt.Valid {
		slog.Warn("agent dm dispatch: recipient unavailable", "channel_id", target.channel.ID, "agent_id", uuidToString(reservation.Recipient))
		return
	}
	prompt := h.buildAgentDMPrompt(ctx, target.channel, agent, trigger, reservation)
	event, result, err := h.enqueueAgentDMPrompt(ctx, target.channel, agent, trigger, initiatorUserID, prompt, reservation)
	if err != nil {
		slog.Warn("agent dm dispatch failed", "channel_id", target.channel.ID, "agent_id", uuidToString(agent.ID), "error", err)
		return
	}
	h.recordChannelAgentPromptWake(ctx, target.channel, agent, trigger, "dm", result)
	if h.Metrics != nil {
		h.Metrics.RecordChannelFullExecutionWake("dm")
	}
	slog.Info("agent dm wake queued",
		"workspace_id", target.channel.WorkspaceID,
		"channel_id", target.channel.ID,
		"exchange_id", uuidToString(reservation.ExchangeID),
		"sender_agent_id", uuidToString(source.origin.agentID),
		"recipient_agent_id", uuidToString(agent.ID),
		"turn", reservation.Turn,
		"round", reservation.Round,
		"round_limit", reservation.RoundLimit,
		"inbox_event_id", uuidToString(event.ID),
	)
}

func (h *Handler) enqueueAgentDMPrompt(
	ctx context.Context,
	ch ChannelResponse,
	agent db.Agent,
	trigger ChannelMessageResponse,
	initiatorUserID pgtype.UUID,
	prompt string,
	reservation agentDMSendReservation,
) (db.AgentInboxEvent, channelAgentPromptTxResult, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return db.AgentInboxEvent{}, channelAgentPromptTxResult{}, err
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)
	result, err := h.enqueueChannelAgentPromptWithTx(
		ctx, qtx, tx, ch, agent, trigger, initiatorUserID, prompt, "dm", channelDirectedWakePriority,
	)
	if err != nil {
		return db.AgentInboxEvent{}, channelAgentPromptTxResult{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_inbox_event
		SET agent_dm_exchange_id = $2,
		    agent_dm_turn = $3,
		    updated_at = now()
		WHERE id = $1`, result.Event.ID, reservation.ExchangeID, reservation.Turn); err != nil {
		return db.AgentInboxEvent{}, channelAgentPromptTxResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return db.AgentInboxEvent{}, channelAgentPromptTxResult{}, err
	}
	result.Event.AgentDmExchangeID = reservation.ExchangeID
	result.Event.AgentDmTurn = pgtype.Int4{Int32: int32(reservation.Turn), Valid: true}
	return result.Event, result, nil
}

func (h *Handler) buildAgentDMPrompt(ctx context.Context, ch ChannelResponse, agent db.Agent, trigger ChannelMessageResponse, reservation agentDMSendReservation) string {
	messages := h.recentChannelMessages(ctx, ch.WorkspaceID, ch.ID, channelAgentDirectedContextMessageLimit)
	messages = channelContextMessagesExcludingTrigger(messages, trigger.ID)
	senderHandle := ""
	if trigger.AuthorID != nil {
		_ = h.DB.QueryRow(ctx, `
			SELECT name
			FROM agent
			WHERE id = $1 AND workspace_id = $2`, parseUUID(*trigger.AuthorID), parseUUID(ch.WorkspaceID)).Scan(&senderHandle)
	}
	var b strings.Builder
	b.WriteString("You received a direct message from another Multica agent. This is a directed must-reply delivery.\n")
	b.WriteString(channelOutputContractInstruction)
	b.WriteString("\n")
	b.WriteString(channelDirectedReplyInstruction)
	b.WriteString("\nA2A discipline: add concrete information or action; do not send a pure acknowledgement. Stop when the matter is concluded. Use a reminder for future external state instead of polling the peer with messages.\n")
	fmt.Fprintf(&b, "This exchange is at round %d. There is no round limit and no owner pause control — this is a normal direct message.\n", reservation.Round)
	if senderHandle != "" {
		fmt.Fprintf(&b, "Message target for chat transport: dm:@%s\n", senderHandle)
	}
	fmt.Fprintf(&b, "Your agent name: %s\n", agentDisplayName(agent))
	if len(messages) > 0 {
		b.WriteString("\nRecent direct-message context:\n")
		for _, msg := range messages {
			fmt.Fprintf(&b, "%s\n", formatChannelMessageLine(msg))
		}
	}
	b.WriteString("\nCurrent direct message:\n")
	fmt.Fprintf(&b, "%s\n", formatChannelMessageLine(trigger))
	return b.String()
}
func (h *Handler) publishAgentDMToOwners(ctx context.Context, ch ChannelResponse, lowID, highID pgtype.UUID, eventType string, payload any) {
	recipients := h.agentDMOwnerIDs(ctx, parseUUID(ch.WorkspaceID), lowID, highID)
	if len(recipients) > 0 {
		h.publishToUsers(eventType, ch.WorkspaceID, "system", "", recipients, payload)
	}
}

func (h *Handler) agentDMOwnerIDs(ctx context.Context, workspaceID, lowID, highID pgtype.UUID) []string {
	recipients, err := agentDMOwnerIDsWithExec(ctx, h.DB, workspaceID, lowID, highID)
	if err != nil {
		return nil
	}
	result := make([]string, 0, len(recipients))
	for _, recipientID := range recipients {
		result = append(result, uuidToString(recipientID))
	}
	return result
}

func agentDMOwnerIDsWithExec(
	ctx context.Context,
	exec db.DBTX,
	workspaceID, lowID, highID pgtype.UUID,
) ([]pgtype.UUID, error) {
	rows, err := exec.Query(ctx, `
		SELECT DISTINCT owner_id
		FROM agent
		WHERE workspace_id = $1
		  AND id = ANY($2::uuid[])
		  AND owner_id IS NOT NULL`,
		workspaceID, []pgtype.UUID{lowID, highID},
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var recipients []pgtype.UUID
	for rows.Next() {
		var ownerID pgtype.UUID
		if rows.Scan(&ownerID) == nil && ownerID.Valid {
			recipients = append(recipients, ownerID)
		}
	}
	return recipients, rows.Err()
}

func (h *Handler) channelUserIsAgentDMSupervisor(ctx context.Context, workspaceID string, channelID, userID pgtype.UUID) bool {
	var allowed bool
	err := h.DB.QueryRow(ctx, `
		SELECT
		  ch.kind = 'dm'
		  AND count(*) FILTER (WHERE cm.member_type = 'agent') = 2
		  AND count(*) FILTER (WHERE cm.member_type = 'user') = 0
		  AND bool_or(cm.member_type = 'agent' AND a.owner_id = $3)
		FROM channel ch
		JOIN channel_member cm ON cm.channel_id = ch.id AND cm.workspace_id = ch.workspace_id
		LEFT JOIN agent a ON cm.member_type = 'agent' AND a.id = cm.member_id
		WHERE ch.id = $1 AND ch.workspace_id = $2
		GROUP BY ch.kind`, channelID, parseUUID(workspaceID), userID).Scan(&allowed)
	return err == nil && allowed
}

func (h *Handler) requireChannelUserViewer(w http.ResponseWriter, ctx context.Context, workspaceID string, channelID, userID pgtype.UUID) bool {
	if !h.channelExists(ctx, workspaceID, channelID) {
		writeError(w, http.StatusNotFound, "channel not found")
		return false
	}
	if h.channelUserIsMember(ctx, workspaceID, channelID, userID) ||
		h.channelUserIsAgentDMSupervisor(ctx, workspaceID, channelID, userID) {
		return true
	}
	writeError(w, http.StatusForbidden, "not allowed to view this conversation")
	return false
}
