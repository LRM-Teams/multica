package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	agentDMDefaultRoundLimit = 3
	agentDMFrequencyWindow   = 5 * time.Minute
)

const (
	agentDMSystemEventPausedBudget    = "agent_dm_paused_budget"
	agentDMSystemEventPausedFrequency = "agent_dm_paused_frequency"
	agentDMSystemEventPausedPair      = "agent_dm_paused_pair"
	agentDMSystemEventPausedGlobal    = "agent_dm_paused_global"
	agentDMSystemEventResumed         = "agent_dm_resumed"
)

type agentDMSendReservation struct {
	ExchangeID pgtype.UUID
	Turn       int
	Round      int
	RoundLimit int
	Recipient  pgtype.UUID
}

type agentDMPausedError struct {
	ExchangeID pgtype.UUID
	ChannelID  pgtype.UUID
	State      string
	Reason     string
	Notify     bool
}

type agentDMTurnError struct {
	ExpectedSender pgtype.UUID
}

type AgentDMControlResponse struct {
	State          string   `json:"state"`
	ExchangeID     *string  `json:"exchange_id,omitempty"`
	MatterID       *string  `json:"matter_id,omitempty"`
	Round          int      `json:"round"`
	RoundLimit     int      `json:"round_limit"`
	PauseReason    *string  `json:"pause_reason,omitempty"`
	CanGrantRounds bool     `json:"can_grant_rounds"`
	CanPausePair   bool     `json:"can_pause_pair"`
	CanPauseGlobal bool     `json:"can_pause_global"`
	Actions        []string `json:"actions"`
}

type AgentDMGlobalControlResponse struct {
	State          string   `json:"state"`
	Paused         bool     `json:"paused"`
	CanPauseGlobal bool     `json:"can_pause_global"`
	Actions        []string `json:"actions"`
}

type agentDMControlRequest struct {
	Action     string `json:"action"`
	ExchangeID string `json:"exchange_id,omitempty"`
	Rounds     int    `json:"rounds,omitempty"`
}

type agentTransportDMControlRequest struct {
	Target     string `json:"target"`
	Action     string `json:"action"`
	ExchangeID string `json:"exchange_id,omitempty"`
	Rounds     int    `json:"rounds,omitempty"`
}

type agentTransportDMControlResponse struct {
	Target  string                  `json:"target"`
	Control *AgentDMControlResponse `json:"control"`
}

type agentDMSystemEventParams struct {
	ExchangeID      string   `json:"exchange_id"`
	DMChannelID     string   `json:"dm_channel_id"`
	SourceChannelID *string  `json:"source_channel_id,omitempty"`
	MatterID        string   `json:"matter_id"`
	Matter          string   `json:"matter"`
	State           string   `json:"state"`
	PauseReason     string   `json:"pause_reason,omitempty"`
	Round           int      `json:"round"`
	RoundLimit      int      `json:"round_limit"`
	AgentAID        string   `json:"agent_a_id"`
	AgentAHandle    string   `json:"agent_a_handle"`
	AgentAName      string   `json:"agent_a_name"`
	AgentBID        string   `json:"agent_b_id"`
	AgentBHandle    string   `json:"agent_b_handle"`
	AgentBName      string   `json:"agent_b_name"`
	Actions         []string `json:"actions"`
}

type agentDMPauseInboxDetails struct {
	agentDMSystemEventParams
	Kind        string `json:"kind"`
	SystemEvent string `json:"system_event"`
}

func (e *agentDMPausedError) Error() string {
	if strings.TrimSpace(e.Reason) != "" {
		return e.Reason
	}
	return "agent direct message is paused"
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

	var pairState string
	var pairPauseReason pgtype.Text
	var windowStartedAt pgtype.Timestamptz
	var windowMessageCount int
	if err := exec.QueryRow(ctx, `
		SELECT state, pause_reason, window_started_at, window_message_count
		FROM agent_dm_pair_control
		WHERE workspace_id = $1 AND agent_low_id = $2 AND agent_high_id = $3
		FOR UPDATE`, workspaceID, lowID, highID).Scan(
		&pairState, &pairPauseReason, &windowStartedAt, &windowMessageCount,
	); err != nil {
		return agentDMSendReservation{}, fmt.Errorf("lock agent dm pair control: %w", err)
	}

	matterID := source.inboxEventID
	if !matterID.Valid {
		matterID = source.task.ID
	}
	exchangeID := source.task.AgentDmExchangeID
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
			nullableUUID(source.task.ChannelID), nullableUUID(source.task.SourceMessageID),
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

	var globalPaused bool
	var globalPauseReason pgtype.Text
	err := exec.QueryRow(ctx, `
		SELECT true, string_agg(DISTINCT COALESCE(control.pause_reason, ''), '; ')
		FROM agent_dm_owner_control control
		JOIN agent owned
		  ON owned.workspace_id = control.workspace_id
		 AND owned.owner_id = control.owner_id
		 AND owned.id = ANY($2::uuid[])
		WHERE control.workspace_id = $1
		  AND control.paused = true
		HAVING count(*) > 0`, workspaceID, []pgtype.UUID{lowID, highID}).Scan(&globalPaused, &globalPauseReason)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return agentDMSendReservation{}, fmt.Errorf("load workspace agent dm control: %w", err)
	}
	if globalPaused {
		return h.pauseAgentDMExchangeTx(ctx, exec, exchangeID, channelID, "paused_global", firstNonEmpty(globalPauseReason.String, "an owner paused direct messages involving their agent"))
	}
	if pairState != "active" {
		return h.pauseAgentDMExchangeTx(ctx, exec, exchangeID, channelID, pairState, firstNonEmpty(pairPauseReason.String, "this agent pair is paused"))
	}
	if exchangeState != "active" {
		return agentDMSendReservation{}, &agentDMPausedError{
			ExchangeID: exchangeID,
			ChannelID:  channelID,
			State:      exchangeState,
			Reason:     "this agent direct-message exchange is paused",
		}
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

func (h *Handler) pauseAgentDMExchangeTx(ctx context.Context, exec db.DBTX, exchangeID, channelID pgtype.UUID, state, reason string) (agentDMSendReservation, error) {
	tag, err := exec.Exec(ctx, `
		UPDATE agent_dm_exchange
		SET state = $2,
		    pause_reason = $3,
		    notification_epoch = notification_epoch + 1,
		    notified_at = COALESCE(notified_at, now()),
		    updated_at = now()
		WHERE id = $1 AND state = 'active'`,
		exchangeID, state, reason,
	)
	if err != nil {
		return agentDMSendReservation{}, fmt.Errorf("pause agent dm exchange: %w", err)
	}
	return agentDMSendReservation{}, &agentDMPausedError{
		ExchangeID: exchangeID,
		ChannelID:  channelID,
		State:      state,
		Reason:     reason,
		Notify:     tag.RowsAffected() == 1,
	}
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
	b.WriteString("Owner control: only when your owner explicitly asks in this task, use `multica message a2a-control --target dm:@<peer> --action <pause_pair|resume_pair|grant_rounds|pause_global|resume_global>`; add `--rounds N` for grant_rounds. The server rejects peer-only self-extension.\n")
	fmt.Fprintf(&b, "This exchange is at round %d of %d. The server will pause it at the limit.\n", reservation.Round, reservation.RoundLimit)
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

type agentDMPauseNotificationResult struct {
	Created              bool
	ExchangeID           pgtype.UUID
	WorkspaceID          pgtype.UUID
	ChannelID            pgtype.UUID
	LowID                pgtype.UUID
	HighID               pgtype.UUID
	State                string
	TurnCount            int
	RoundLimit           int
	Message              ChannelMessageResponse
	RearmedManagedPatrol *agentReminder
	InboxItems           []db.InboxItem
}

func (h *Handler) notifyAgentDMPause(ctx context.Context, exchangeID pgtype.UUID) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		slog.Warn("agent dm pause notification begin failed", "exchange_id", uuidToString(exchangeID), "error", err)
		return
	}
	defer tx.Rollback(ctx)
	result, err := h.persistAgentDMPauseNotificationTx(ctx, tx, exchangeID)
	if err != nil {
		slog.Warn("agent dm pause notification persist failed", "exchange_id", uuidToString(exchangeID), "error", err)
		return
	}
	if !result.Created {
		return
	}
	if err := tx.Commit(ctx); err != nil {
		slog.Warn("agent dm pause notification commit failed", "exchange_id", uuidToString(exchangeID), "error", err)
		return
	}
	h.publishAgentDMPauseNotification(ctx, result)
}

func (h *Handler) publishAgentDMPauseNotification(
	ctx context.Context,
	result agentDMPauseNotificationResult,
) {
	if !result.Created {
		return
	}
	if dmChannel, found := h.getChannel(ctx, uuidToString(result.WorkspaceID), result.ChannelID); found {
		h.publishAgentDMToOwners(
			ctx, dmChannel, result.LowID, result.HighID, protocol.EventChannelMessage, result.Message,
		)
	}
	for _, item := range result.InboxItems {
		h.publishToUsers(
			protocol.EventInboxNew,
			uuidToString(result.WorkspaceID),
			"system",
			"",
			[]string{uuidToString(item.RecipientID)},
			map[string]any{"item": inboxToResponse(item)},
		)
	}
	slog.Info("agent dm exchange paused",
		"workspace_id", uuidToString(result.WorkspaceID),
		"channel_id", uuidToString(result.ChannelID),
		"exchange_id", uuidToString(result.ExchangeID),
		"agent_low_id", uuidToString(result.LowID),
		"agent_high_id", uuidToString(result.HighID),
		"state", result.State,
		"turn_count", result.TurnCount,
		"round_limit", result.RoundLimit,
	)
}

// persistAgentDMPauseNotificationTx makes the durable DM system row, every
// owner inbox item, and the exchange receipt one transaction. The exchange row
// lock serializes attempts; agent_dm_pause_notification persists
// exchange_id + notification_epoch as a stable idempotency key so an uncertain
// retry cannot duplicate either user-visible surface.
func (h *Handler) persistAgentDMPauseNotificationTx(
	ctx context.Context,
	exec db.DBTX,
	exchangeID pgtype.UUID,
) (agentDMPauseNotificationResult, error) {
	var workspaceID, channelID, sourceChannelID, sourceMessageID, matterID, lowID, highID pgtype.UUID
	var state, reason, lowHandle, lowName, highHandle, highName string
	var turnCount, roundLimit, grantedRounds, notificationEpoch int
	err := exec.QueryRow(ctx, `
		SELECT exchange.workspace_id, exchange.channel_id, exchange.source_channel_id,
		       exchange.source_message_id, exchange.matter_id,
		       exchange.agent_low_id, exchange.agent_high_id,
		       exchange.state, COALESCE(exchange.pause_reason, ''),
		       exchange.turn_count, exchange.round_limit, exchange.granted_rounds,
		       exchange.notification_epoch,
		       low.name, COALESCE(NULLIF(low.display_name, ''), low.name),
		       high.name, COALESCE(NULLIF(high.display_name, ''), high.name)
		FROM agent_dm_exchange exchange
		JOIN agent low ON low.id = exchange.agent_low_id
		JOIN agent high ON high.id = exchange.agent_high_id
		WHERE exchange.id = $1
		  AND exchange.state <> 'active'
		  AND exchange.notification_sent_at IS NULL
		FOR UPDATE OF exchange`,
		exchangeID,
	).Scan(
		&workspaceID, &channelID, &sourceChannelID,
		&sourceMessageID, &matterID,
		&lowID, &highID, &state, &reason,
		&turnCount, &roundLimit, &grantedRounds,
		&notificationEpoch,
		&lowHandle, &lowName, &highHandle, &highName,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return agentDMPauseNotificationResult{}, nil
		}
		return agentDMPauseNotificationResult{}, fmt.Errorf("lock agent dm pause notification: %w", err)
	}
	round := (turnCount + 1) / 2
	totalLimit := roundLimit + grantedRounds
	matter := h.agentDMMatterLabelWithExec(ctx, exec, workspaceID, sourceMessageID)
	var content string
	switch state {
	case "paused_budget":
		content = fmt.Sprintf(
			"%s 和 %s 就「%s」来回到了上限（%d/%d 轮），已暂停。",
			lowName, highName, matter, round, totalLimit,
		)
	case "paused_frequency":
		content = fmt.Sprintf("%s 和 %s 私聊太频繁，已暂停这对。", lowName, highName)
	case "paused_global":
		content = fmt.Sprintf("%s 和 %s 的智能体私聊因 owner 全局暂停而停止。", lowName, highName)
	default:
		content = fmt.Sprintf(
			"%s 和 %s 的智能体私聊已暂停：%s。",
			lowName, highName, firstNonEmpty(reason, state),
		)
	}
	event := agentDMSystemEventForState(state)
	params := agentDMSystemEventParams{
		ExchangeID:      uuidToString(exchangeID),
		DMChannelID:     uuidToString(channelID),
		SourceChannelID: uuidToPtr(sourceChannelID),
		MatterID:        uuidToString(matterID),
		Matter:          matter,
		State:           state,
		PauseReason:     reason,
		Round:           round,
		RoundLimit:      totalLimit,
		AgentAID:        uuidToString(lowID),
		AgentAHandle:    lowHandle,
		AgentAName:      lowName,
		AgentBID:        uuidToString(highID),
		AgentBHandle:    highHandle,
		AgentBName:      highName,
		Actions:         []string{"view_dm", "grant_rounds", "pause_pair", "pause_global"},
	}
	paramsJSON, marshalErr := json.Marshal(params)
	if marshalErr != nil {
		return agentDMPauseNotificationResult{}, fmt.Errorf("marshal agent dm pause event: %w", marshalErr)
	}
	inserted, err := insertChannelMessageWithPartsExec(
		ctx, exec, channelID, workspaceID, "system", pgtype.UUID{}, "",
		content,
		[]protocol.MessagePart{{
			Type:        protocol.MessagePartTypeSystemEvent,
			Event:       event,
			EventParams: paramsJSON,
		}},
		"multica", nil, nil,
		pgtype.UUID{}, pgtype.UUID{}, nil, pgtype.UUID{}, nil, 0,
	)
	if err != nil {
		return agentDMPauseNotificationResult{}, fmt.Errorf("insert agent dm pause system row: %w", err)
	}
	recipients, err := agentDMOwnerIDsWithExec(ctx, exec, workspaceID, lowID, highID)
	if err != nil {
		return agentDMPauseNotificationResult{}, fmt.Errorf("load agent dm pause owners: %w", err)
	}
	details, err := json.Marshal(agentDMPauseInboxDetails{
		agentDMSystemEventParams: params,
		Kind:                     "agent_dm_paused",
		SystemEvent:              event,
	})
	if err != nil {
		return agentDMPauseNotificationResult{}, fmt.Errorf("marshal agent dm pause inbox details: %w", err)
	}
	title := fmt.Sprintf("%s 和 %s 的智能体私聊已暂停", params.AgentAName, params.AgentBName)
	qtx := db.New(exec)
	inboxItems := make([]db.InboxItem, 0, len(recipients))
	for _, recipientID := range recipients {
		item, err := qtx.CreateInboxItem(ctx, db.CreateInboxItemParams{
			WorkspaceID:   workspaceID,
			RecipientType: "member",
			RecipientID:   recipientID,
			Type:          "agent_dm_paused",
			Severity:      "action_required",
			Title:         title,
			Body:          strToText(content),
			ActorType:     strToText("system"),
			Details:       details,
		})
		if err != nil {
			return agentDMPauseNotificationResult{}, fmt.Errorf(
				"insert agent dm pause owner inbox for %s: %w",
				uuidToString(recipientID), err,
			)
		}
		inboxItems = append(inboxItems, item)
	}
	if _, err := exec.Exec(ctx, `
		INSERT INTO agent_dm_pause_notification (
		  exchange_id, notification_epoch, channel_message_id
		)
		VALUES ($1, $2, $3)`,
		exchangeID, notificationEpoch, parseUUID(inserted.Message.ID),
	); err != nil {
		return agentDMPauseNotificationResult{}, fmt.Errorf("record agent dm pause notification receipt: %w", err)
	}
	tag, err := exec.Exec(ctx, `
		UPDATE agent_dm_exchange
		SET notification_sent_at = now(),
		    updated_at = now()
		WHERE id = $1
		  AND notification_epoch = $2
		  AND notification_sent_at IS NULL`,
		exchangeID, notificationEpoch,
	)
	if err != nil {
		return agentDMPauseNotificationResult{}, fmt.Errorf("confirm agent dm pause notification: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return agentDMPauseNotificationResult{}, errors.New("agent dm pause notification changed during persistence")
	}
	return agentDMPauseNotificationResult{
		Created:              true,
		ExchangeID:           exchangeID,
		WorkspaceID:          workspaceID,
		ChannelID:            channelID,
		LowID:                lowID,
		HighID:               highID,
		State:                state,
		TurnCount:            turnCount,
		RoundLimit:           totalLimit,
		Message:              inserted.Message,
		RearmedManagedPatrol: inserted.RearmedManagedPatrol,
		InboxItems:           inboxItems,
	}, nil
}

func agentDMSystemEventForState(state string) string {
	switch state {
	case "paused_frequency":
		return agentDMSystemEventPausedFrequency
	case "paused_pair":
		return agentDMSystemEventPausedPair
	case "paused_global":
		return agentDMSystemEventPausedGlobal
	default:
		return agentDMSystemEventPausedBudget
	}
}

func (h *Handler) agentDMMatterLabel(ctx context.Context, workspaceID, sourceMessageID pgtype.UUID) string {
	return h.agentDMMatterLabelWithExec(ctx, h.DB, workspaceID, sourceMessageID)
}

func (h *Handler) agentDMMatterLabelWithExec(
	ctx context.Context,
	exec db.DBTX,
	workspaceID, sourceMessageID pgtype.UUID,
) string {
	if !sourceMessageID.Valid {
		return "当前事项"
	}
	var content string
	if err := exec.QueryRow(ctx, `
		SELECT content
		FROM channel_message
		WHERE id = $1 AND workspace_id = $2`,
		sourceMessageID, workspaceID).Scan(&content); err != nil {
		return "当前事项"
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "当前事项"
	}
	runes := []rune(content)
	if len(runes) > 120 {
		content = string(runes[:120]) + "…"
	}
	return content
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

func (h *Handler) GetAgentDMControl(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.channelUserIsAgentDMSupervisor(r.Context(), workspaceID, channelID, parseUUID(userID)) {
		writeError(w, http.StatusForbidden, "only an agent owner may manage this direct message")
		return
	}
	control, ok := h.agentDMControlForOwner(r.Context(), parseUUID(workspaceID), channelID, parseUUID(userID))
	if !ok {
		writeError(w, http.StatusNotFound, "agent direct message not found")
		return
	}
	writeJSON(w, http.StatusOK, control)
}

func (h *Handler) GetAgentDMGlobalControl(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := parseUUID(ctxWorkspaceID(r.Context()))
	ownerID := parseUUID(userID)
	if !h.requireAgentDMOwner(w, r.Context(), workspaceID, ownerID) {
		return
	}
	writeJSON(w, http.StatusOK, h.agentDMGlobalControl(r.Context(), workspaceID, ownerID))
}

func (h *Handler) UpdateAgentDMGlobalControl(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := parseUUID(ctxWorkspaceID(r.Context()))
	ownerID := parseUUID(userID)
	if !h.requireAgentDMOwner(w, r.Context(), workspaceID, ownerID) {
		return
	}
	var req agentDMControlRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	action := strings.TrimSpace(req.Action)
	if action != "pause_global" && action != "resume_global" {
		writeError(w, http.StatusBadRequest, "action must be pause_global or resume_global")
		return
	}
	changed, err := h.updateAgentDMOwnerGlobalControl(
		r.Context(), workspaceID, ownerID, action == "pause_global",
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update agent dm global control")
		return
	}
	if changed {
		event := agentDMSystemEventResumed
		state := "active"
		if action == "pause_global" {
			event = agentDMSystemEventPausedGlobal
			state = "paused_global"
		}
		h.insertAgentDMOwnerControlSystemEvents(r.Context(), workspaceID, ownerID, event, state)
	}
	writeJSON(w, http.StatusOK, h.agentDMGlobalControl(r.Context(), workspaceID, ownerID))
}

func (h *Handler) requireAgentDMOwner(w http.ResponseWriter, ctx context.Context, workspaceID, ownerID pgtype.UUID) bool {
	var ownsAgent bool
	err := h.DB.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1
		  FROM agent
		  WHERE workspace_id = $1
		    AND owner_id = $2
		    AND archived_at IS NULL
		)`, workspaceID, ownerID).Scan(&ownsAgent)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to authorize agent owner")
		return false
	}
	if !ownsAgent {
		writeError(w, http.StatusForbidden, "only an agent owner may manage global agent direct messages")
		return false
	}
	return true
}

func (h *Handler) agentDMGlobalControl(ctx context.Context, workspaceID, ownerID pgtype.UUID) AgentDMGlobalControlResponse {
	var paused bool
	_ = h.DB.QueryRow(ctx, `
		SELECT paused
		FROM agent_dm_owner_control
		WHERE workspace_id = $1 AND owner_id = $2`,
		workspaceID, ownerID).Scan(&paused)
	state := "active"
	actions := []string{"pause_global"}
	if paused {
		state = "paused_global"
		actions = []string{"resume_global"}
	}
	return AgentDMGlobalControlResponse{
		State:          state,
		Paused:         paused,
		CanPauseGlobal: true,
		Actions:        actions,
	}
}

func (h *Handler) UpdateAgentDMControl(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	ownerID := parseUUID(userID)
	if !h.channelUserIsAgentDMSupervisor(r.Context(), workspaceID, channelID, ownerID) {
		writeError(w, http.StatusForbidden, "only an agent owner may manage this direct message")
		return
	}
	var req agentDMControlRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.updateAgentDMControl(r.Context(), parseUUID(workspaceID), channelID, ownerID, req); err != nil {
		if errors.Is(err, errChatOutputInvalidTarget) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	control, ok := h.agentDMControlForOwner(r.Context(), parseUUID(workspaceID), channelID, ownerID)
	if !ok {
		writeError(w, http.StatusNotFound, "agent direct message not found")
		return
	}
	writeJSON(w, http.StatusOK, control)
}

// AgentTransportUpdateDMControl is the speech-to-control bridge: an owner may
// tell an owned agent to pause/resume/extend an existing A2A DM, and that
// running task can execute the same control contract as the owner UI. The
// initiator must be the source agent's owner; ordinary peer wakes cannot grant
// themselves more budget.
func (h *Handler) AgentTransportUpdateDMControl(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
		return
	}
	var req agentTransportDMControlRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ownerID := h.agentOwnerID(r.Context(), source.origin.workspaceID, source.origin.agentID)
	if !ownerID.Valid || source.task.InitiatorUserID != ownerID {
		writeError(w, http.StatusForbidden, "agent dm control requires an explicit task from this agent's owner")
		return
	}
	target, err := h.resolveAgentTransportTarget(
		r.Context(), source.task, source.origin, req.Target, false,
	)
	if err != nil || target.kind != chatOutputTargetDM || target.recipientType != "agent" {
		writeError(w, http.StatusBadRequest, "target must be an existing agent direct message")
		return
	}
	if !h.channelHasAgentMember(
		r.Context(), source.origin.workspaceID, parseUUID(target.channel.ID), source.origin.agentID,
	) {
		writeError(w, http.StatusForbidden, "source agent is not a direct-message participant")
		return
	}
	controlRequest := agentDMControlRequest{
		Action:     req.Action,
		ExchangeID: req.ExchangeID,
		Rounds:     req.Rounds,
	}
	if err := h.updateAgentDMControl(
		r.Context(),
		source.origin.workspaceID,
		parseUUID(target.channel.ID),
		ownerID,
		controlRequest,
	); err != nil {
		if errors.Is(err, errChatOutputInvalidTarget) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	control, ok := h.agentDMControlForOwner(
		r.Context(), source.origin.workspaceID, parseUUID(target.channel.ID), ownerID,
	)
	if !ok {
		writeError(w, http.StatusNotFound, "agent direct message not found")
		return
	}
	writeJSON(w, http.StatusOK, agentTransportDMControlResponse{
		Target:  strings.TrimSpace(req.Target),
		Control: control,
	})
}

func (h *Handler) updateAgentDMControl(ctx context.Context, workspaceID, channelID, ownerID pgtype.UUID, req agentDMControlRequest) error {
	lowID, highID, ok := h.agentDMPairForChannel(ctx, workspaceID, channelID)
	if !ok {
		return errChatOutputInvalidTarget
	}
	switch strings.TrimSpace(req.Action) {
	case "pause_pair":
		var previousState string
		_ = h.DB.QueryRow(ctx, `
			SELECT state
			FROM agent_dm_pair_control
			WHERE workspace_id = $1 AND agent_low_id = $2 AND agent_high_id = $3`,
			workspaceID, lowID, highID).Scan(&previousState)
		_, err := h.DB.Exec(ctx, `
			INSERT INTO agent_dm_pair_control (
			  workspace_id, agent_low_id, agent_high_id, state, pause_reason
			)
			VALUES ($1, $2, $3, 'paused_pair', 'paused by owner')
			ON CONFLICT (workspace_id, agent_low_id, agent_high_id) DO UPDATE
			SET state = 'paused_pair', pause_reason = 'paused by owner', updated_at = now()`,
			workspaceID, lowID, highID)
		if err == nil {
			_, err = h.DB.Exec(ctx, `
				UPDATE agent_dm_exchange
				SET state = 'paused_pair', pause_reason = 'paused by owner',
				    notification_epoch = notification_epoch + 1,
				    notified_at = COALESCE(notified_at, now()), updated_at = now()
				WHERE workspace_id = $1 AND agent_low_id = $2 AND agent_high_id = $3
				  AND state = 'active'`,
				workspaceID, lowID, highID)
		}
		if err == nil && previousState != "paused_pair" {
			h.insertAgentDMControlSystemEvent(
				ctx, workspaceID, channelID, agentDMSystemEventPausedPair, "paused_pair",
			)
		}
		return err
	case "resume_pair":
		tx, err := h.TxStarter.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		var previousState string
		_ = tx.QueryRow(ctx, `
			SELECT state
			FROM agent_dm_pair_control
			WHERE workspace_id = $1 AND agent_low_id = $2 AND agent_high_id = $3
			FOR UPDATE`, workspaceID, lowID, highID).Scan(&previousState)
		if _, err := tx.Exec(ctx, `
			INSERT INTO agent_dm_pair_control (
			  workspace_id, agent_low_id, agent_high_id, state,
			  window_started_at, window_message_count
			)
			VALUES ($1, $2, $3, 'active', now(), 0)
			ON CONFLICT (workspace_id, agent_low_id, agent_high_id) DO UPDATE
			SET state = 'active', pause_reason = NULL,
			    window_started_at = now(), window_message_count = 0,
			    updated_at = now()`, workspaceID, lowID, highID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE agent_dm_exchange
			SET state = CASE
			      WHEN turn_count >= (round_limit + granted_rounds) * 2 THEN 'paused_budget'
			      ELSE 'active'
			    END,
			    pause_reason = CASE
			      WHEN turn_count >= (round_limit + granted_rounds) * 2 THEN pause_reason
			      ELSE NULL
			    END,
			    notification_sent_at = CASE
			      WHEN turn_count >= (round_limit + granted_rounds) * 2 THEN notification_sent_at
			      ELSE NULL
			    END,
			    updated_at = now()
			WHERE workspace_id = $1 AND agent_low_id = $2 AND agent_high_id = $3
			  AND state IN ('paused_pair', 'paused_frequency')`,
			workspaceID, lowID, highID); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		if previousState != "" && previousState != "active" {
			h.insertAgentDMControlSystemEvent(
				ctx, workspaceID, channelID, agentDMSystemEventResumed, "active",
			)
		}
		return nil
	case "grant_rounds":
		if req.Rounds <= 0 || req.Rounds > 20 {
			return errors.New("rounds must be between 1 and 20")
		}
		rawExchangeID := strings.TrimSpace(req.ExchangeID)
		var parsed pgtype.UUID
		if rawExchangeID == "" {
			if err := h.DB.QueryRow(ctx, `
				SELECT id
				FROM agent_dm_exchange
				WHERE workspace_id = $1 AND channel_id = $2
				  AND agent_low_id = $3 AND agent_high_id = $4
				ORDER BY updated_at DESC
				LIMIT 1`, workspaceID, channelID, lowID, highID).Scan(&parsed); err != nil {
				return errors.New("no agent direct-message exchange to extend")
			}
		} else {
			var err error
			parsed, err = util.ParseUUID(rawExchangeID)
			if err != nil {
				return errChatOutputInvalidTarget
			}
		}
		tag, err := h.DB.Exec(ctx, `
			UPDATE agent_dm_exchange
			SET granted_rounds = granted_rounds + $2,
			    state = 'active',
			    pause_reason = NULL,
			    notification_sent_at = NULL,
			    updated_at = now()
			WHERE id = $1 AND workspace_id = $3 AND channel_id = $4
			  AND agent_low_id = $5 AND agent_high_id = $6`,
			parsed, req.Rounds, workspaceID, channelID, lowID, highID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return errors.New("agent direct-message exchange not found")
		}
		return nil
	case "pause_global", "resume_global":
		paused := strings.TrimSpace(req.Action) == "pause_global"
		changed, err := h.updateAgentDMOwnerGlobalControl(ctx, workspaceID, ownerID, paused)
		if err != nil {
			return err
		}
		if changed {
			event := agentDMSystemEventResumed
			state := "active"
			if paused {
				event = agentDMSystemEventPausedGlobal
				state = "paused_global"
			}
			h.insertAgentDMOwnerControlSystemEvents(ctx, workspaceID, ownerID, event, state)
		}
		return nil
	default:
		return errors.New("action must be pause_pair, resume_pair, grant_rounds, pause_global, or resume_global")
	}
}

func (h *Handler) updateAgentDMOwnerGlobalControl(ctx context.Context, workspaceID, ownerID pgtype.UUID, paused bool) (bool, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	var previousPaused bool
	err = tx.QueryRow(ctx, `
		SELECT paused
		FROM agent_dm_owner_control
		WHERE workspace_id = $1 AND owner_id = $2
		FOR UPDATE`,
		workspaceID, ownerID).Scan(&previousPaused)
	if errors.Is(err, pgx.ErrNoRows) && !paused {
		return false, nil
	}
	if err == nil && previousPaused == paused {
		return false, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO agent_dm_owner_control (
		  workspace_id, owner_id, paused, pause_reason
		)
		VALUES ($1, $2, $3, CASE WHEN $3 THEN 'paused by owner' ELSE NULL END)
		ON CONFLICT (workspace_id, owner_id) DO UPDATE
		SET paused = EXCLUDED.paused,
		    pause_reason = EXCLUDED.pause_reason,
		    updated_at = now()`,
		workspaceID, ownerID, paused); err != nil {
		return false, err
	}
	if paused {
		_, err = tx.Exec(ctx, `
			UPDATE agent_dm_exchange e
			SET state = 'paused_global',
			    pause_reason = 'paused by owner',
			    notification_epoch = notification_epoch + 1,
			    notified_at = COALESCE(notified_at, now()),
			    updated_at = now()
			WHERE e.workspace_id = $1
			  AND e.state = 'active'
			  AND EXISTS (
			    SELECT 1
			    FROM agent a
			    WHERE a.workspace_id = e.workspace_id
			      AND a.id = ANY(ARRAY[e.agent_low_id, e.agent_high_id])
			      AND a.owner_id = $2
			  )`,
			workspaceID, ownerID)
	} else {
		_, err = tx.Exec(ctx, `
			UPDATE agent_dm_exchange e
			SET state = CASE
			      WHEN e.turn_count >= (e.round_limit + e.granted_rounds) * 2 THEN 'paused_budget'
			      ELSE 'active'
			    END,
			    pause_reason = CASE
			      WHEN e.turn_count >= (e.round_limit + e.granted_rounds) * 2 THEN e.pause_reason
			      ELSE NULL
			    END,
			    notification_sent_at = CASE
			      WHEN e.turn_count >= (e.round_limit + e.granted_rounds) * 2 THEN e.notification_sent_at
			      ELSE NULL
			    END,
			    updated_at = now()
			WHERE e.workspace_id = $1
			  AND e.state = 'paused_global'
			  AND EXISTS (
			    SELECT 1
			    FROM agent a
			    WHERE a.workspace_id = e.workspace_id
			      AND a.id = ANY(ARRAY[e.agent_low_id, e.agent_high_id])
			      AND a.owner_id = $2
			  )
			  AND NOT EXISTS (
			    SELECT 1
			    FROM agent a
			    JOIN agent_dm_owner_control oc
			      ON oc.workspace_id = a.workspace_id
			     AND oc.owner_id = a.owner_id
			     AND oc.paused
			    WHERE a.workspace_id = e.workspace_id
			      AND a.id = ANY(ARRAY[e.agent_low_id, e.agent_high_id])
			  )
			  AND COALESCE((
			    SELECT pc.state
			    FROM agent_dm_pair_control pc
			    WHERE pc.workspace_id = e.workspace_id
			      AND pc.agent_low_id = e.agent_low_id
			      AND pc.agent_high_id = e.agent_high_id
			  ), 'active') = 'active'`,
			workspaceID, ownerID)
	}
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (h *Handler) insertAgentDMOwnerControlSystemEvents(
	ctx context.Context,
	workspaceID, ownerID pgtype.UUID,
	event, state string,
) {
	rows, err := h.DB.Query(ctx, `
		SELECT ch.id
		FROM channel ch
		JOIN channel_member cm
		  ON cm.workspace_id = ch.workspace_id
		 AND cm.channel_id = ch.id
		LEFT JOIN agent a
		  ON cm.member_type = 'agent'
		 AND a.workspace_id = cm.workspace_id
		 AND a.id = cm.member_id
		WHERE ch.workspace_id = $1 AND ch.kind = 'dm'
		GROUP BY ch.id
		HAVING count(*) FILTER (WHERE cm.member_type = 'agent') = 2
		   AND count(*) FILTER (WHERE cm.member_type = 'user') = 0
		   AND bool_or(cm.member_type = 'agent' AND a.owner_id = $2)`,
		workspaceID, ownerID)
	if err != nil {
		return
	}
	defer rows.Close()
	var channelIDs []pgtype.UUID
	for rows.Next() {
		var channelID pgtype.UUID
		if rows.Scan(&channelID) == nil {
			channelIDs = append(channelIDs, channelID)
		}
	}
	for _, channelID := range channelIDs {
		h.insertAgentDMControlSystemEvent(ctx, workspaceID, channelID, event, state)
	}
}

func (h *Handler) insertAgentDMControlSystemEvent(
	ctx context.Context,
	workspaceID, channelID pgtype.UUID,
	event, state string,
) {
	var exchangeID, sourceChannelID, sourceMessageID, matterID pgtype.UUID
	var exchangeState string
	var turnCount, roundLimit, grantedRounds int
	var lowID, highID pgtype.UUID
	var lowHandle, lowName, highHandle, highName string
	var globallyPaused bool
	err := h.DB.QueryRow(ctx, `
		SELECT e.id, e.source_channel_id, e.source_message_id, e.matter_id,
		       e.state, e.turn_count, e.round_limit, e.granted_rounds,
		       e.agent_low_id, e.agent_high_id,
		       low.name, low.display_name, high.name, high.display_name,
		       EXISTS (
		         SELECT 1
		         FROM agent a
		         JOIN agent_dm_owner_control oc
		           ON oc.workspace_id = a.workspace_id
		          AND oc.owner_id = a.owner_id
		          AND oc.paused
		         WHERE a.workspace_id = e.workspace_id
		           AND a.id = ANY(ARRAY[e.agent_low_id, e.agent_high_id])
		       )
		FROM agent_dm_exchange e
		JOIN agent low ON low.id = e.agent_low_id AND low.workspace_id = e.workspace_id
		JOIN agent high ON high.id = e.agent_high_id AND high.workspace_id = e.workspace_id
		WHERE e.workspace_id = $1 AND e.channel_id = $2
		ORDER BY e.updated_at DESC
		LIMIT 1`,
		workspaceID, channelID).Scan(
		&exchangeID, &sourceChannelID, &sourceMessageID, &matterID,
		&exchangeState, &turnCount, &roundLimit, &grantedRounds,
		&lowID, &highID,
		&lowHandle, &lowName, &highHandle, &highName,
		&globallyPaused,
	)
	if err != nil {
		return
	}
	if event == agentDMSystemEventResumed && (exchangeState != "active" || globallyPaused) {
		return
	}
	matter := h.agentDMMatterLabel(ctx, workspaceID, sourceMessageID)
	totalLimit := roundLimit + grantedRounds
	params := agentDMSystemEventParams{
		ExchangeID:      uuidToString(exchangeID),
		DMChannelID:     uuidToString(channelID),
		SourceChannelID: uuidToPtr(sourceChannelID),
		MatterID:        uuidToString(matterID),
		Matter:          matter,
		State:           state,
		Round:           (turnCount + 1) / 2,
		RoundLimit:      totalLimit,
		AgentAID:        uuidToString(lowID),
		AgentAHandle:    lowHandle,
		AgentAName:      lowName,
		AgentBID:        uuidToString(highID),
		AgentBHandle:    highHandle,
		AgentBName:      highName,
		Actions:         []string{"view_dm", "pause_pair", "pause_global"},
	}
	var content string
	switch event {
	case agentDMSystemEventPausedPair:
		params.PauseReason = "paused by owner"
		params.Actions = []string{"view_dm", "resume_pair", "pause_global"}
		content = "你已暂停这对智能体的私聊——他们不再互发，直到你恢复。"
	case agentDMSystemEventPausedGlobal:
		params.PauseReason = "paused by owner"
		params.Actions = []string{"view_dm", "resume_global"}
		content = "你暂停了涉及你智能体的所有私聊——它们暂时不再和任何智能体互发，直到你恢复。"
	default:
		content = "已恢复，你的智能体可以继续私聊了。"
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return
	}
	dmChannel, found := h.getChannel(ctx, uuidToString(workspaceID), channelID)
	if !found {
		return
	}
	msg, err := h.insertChannelMessageWithParts(
		ctx, channelID, workspaceID, "system", pgtype.UUID{}, "",
		content,
		[]protocol.MessagePart{{
			Type:        protocol.MessagePartTypeSystemEvent,
			Event:       event,
			EventParams: paramsJSON,
		}},
		"multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0,
	)
	if err != nil {
		return
	}
	h.publishAgentDMToOwners(ctx, dmChannel, lowID, highID, protocol.EventChannelMessage, msg)
}

func (h *Handler) agentDMPairForChannel(ctx context.Context, workspaceID, channelID pgtype.UUID) (pgtype.UUID, pgtype.UUID, bool) {
	rows, err := h.DB.Query(ctx, `
		SELECT member_id
		FROM channel_member
		WHERE workspace_id = $1 AND channel_id = $2 AND member_type = 'agent'
		ORDER BY member_id::text`, workspaceID, channelID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, false
	}
	defer rows.Close()
	var ids []pgtype.UUID
	for rows.Next() {
		var id pgtype.UUID
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	if len(ids) != 2 {
		return pgtype.UUID{}, pgtype.UUID{}, false
	}
	return ids[0], ids[1], true
}

func agentDMControlActions(state string, exchangeExists, ownerPaused bool) []string {
	actions := []string{"view_dm"}
	if ownerPaused {
		return append(actions, "resume_global")
	}

	switch firstNonEmpty(state, "active") {
	case "active":
		actions = append(actions, "pause_pair")
	case "paused_budget":
		if exchangeExists {
			actions = append(actions, "grant_rounds")
		}
		actions = append(actions, "pause_pair")
	case "paused_frequency", "paused_pair":
		actions = append(actions, "resume_pair")
	}
	return append(actions, "pause_global")
}

func hasAgentDMControlAction(actions []string, action string) bool {
	for _, candidate := range actions {
		if candidate == action {
			return true
		}
	}
	return false
}

func (h *Handler) agentDMControlForOwner(ctx context.Context, workspaceID, channelID, ownerID pgtype.UUID) (*AgentDMControlResponse, bool) {
	lowID, highID, ok := h.agentDMPairForChannel(ctx, workspaceID, channelID)
	if !ok {
		return nil, false
	}
	var pairState string
	_ = h.DB.QueryRow(ctx, `
		SELECT state
		FROM agent_dm_pair_control
		WHERE workspace_id = $1 AND agent_low_id = $2 AND agent_high_id = $3`,
		workspaceID, lowID, highID).Scan(&pairState)
	var ownerPaused bool
	_ = h.DB.QueryRow(ctx, `
		SELECT paused
		FROM agent_dm_owner_control
		WHERE workspace_id = $1 AND owner_id = $2`,
		workspaceID, ownerID).Scan(&ownerPaused)
	var exchangeID, matterID pgtype.UUID
	var exchangeState string
	var pauseReason pgtype.Text
	var turnCount, roundLimit, grantedRounds int
	err := h.DB.QueryRow(ctx, `
		SELECT id, matter_id, state, pause_reason, turn_count, round_limit, granted_rounds
		FROM agent_dm_exchange
		WHERE workspace_id = $1 AND channel_id = $2
		ORDER BY updated_at DESC
		LIMIT 1`, workspaceID, channelID).Scan(
		&exchangeID, &matterID, &exchangeState, &pauseReason,
		&turnCount, &roundLimit, &grantedRounds,
	)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, false
	}
	state := firstNonEmpty(exchangeState, "active")
	if pairState != "" && pairState != "active" {
		state = pairState
	}
	if ownerPaused {
		state = "paused_global"
	}
	actions := agentDMControlActions(state, exchangeID.Valid, ownerPaused)
	response := &AgentDMControlResponse{
		State:          state,
		Round:          (turnCount + 1) / 2,
		RoundLimit:     roundLimit + grantedRounds,
		CanGrantRounds: hasAgentDMControlAction(actions, "grant_rounds"),
		CanPausePair:   hasAgentDMControlAction(actions, "pause_pair"),
		CanPauseGlobal: hasAgentDMControlAction(actions, "pause_global"),
		Actions:        actions,
	}
	if exchangeID.Valid {
		value := uuidToString(exchangeID)
		response.ExchangeID = &value
	}
	if matterID.Valid {
		value := uuidToString(matterID)
		response.MatterID = &value
	}
	if pauseReason.Valid && strings.TrimSpace(pauseReason.String) != "" {
		value := pauseReason.String
		response.PauseReason = &value
	}
	return response, true
}
