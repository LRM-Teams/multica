package handler

import (
	"context"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	channelAmbientGateModeGate = "gate"
	channelAmbientGateModeOff  = "off"

	defaultChannelAmbientGateWindow              = time.Minute
	defaultChannelAmbientGateMaxRecentPerAgent   = 1
	defaultChannelAmbientGateMaxRecentPerChannel = 32
	defaultChannelAmbientGateMaxRecentPerRuntime = 64

	channelAmbientGateActionEnqueued         = "enqueued"
	channelAmbientGateActionCoalesced        = "coalesced"
	channelAmbientGateActionDropped          = "dropped"
	channelAmbientGateActionFused            = "fused"
	channelAmbientGateActionRelevanceSkipped = "relevance_skipped"

	channelAmbientGateReasonAccepted         = "accepted"
	channelAmbientGateReasonGateOff          = "gate_off"
	channelAmbientGateReasonGateError        = "gate_error"
	channelAmbientGateReasonNonTextNoise     = "non_text_noise"
	channelAmbientGateReasonAgentActive      = "agent_active_ambient"
	channelAmbientGateReasonAgentWindowCap   = "agent_window_cap"
	channelAmbientGateReasonChannelWindowCap = "channel_window_cap"
	channelAmbientGateReasonRuntimeWindowCap = "runtime_window_cap"
)

type channelAmbientGateConfig struct {
	mode                string
	window              time.Duration
	maxRecentPerAgent   int
	maxRecentPerChannel int
	maxRecentPerRuntime int
}

type channelAmbientGateStats struct {
	activeForAgent   int64
	recentForAgent   int64
	recentForChannel int64
	recentForRuntime int64
}

func (h *Handler) channelAmbientGateConfig() channelAmbientGateConfig {
	mode := strings.ToLower(strings.TrimSpace(h.cfg.ChannelAmbientGateMode))
	switch mode {
	case "", channelAmbientGateModeGate:
		mode = channelAmbientGateModeGate
	case channelAmbientGateModeOff:
	default:
		slog.Warn("invalid channel ambient gate mode, using gate", "value", h.cfg.ChannelAmbientGateMode)
		mode = channelAmbientGateModeGate
	}
	window := h.cfg.ChannelAmbientGateWindow
	if window <= 0 {
		window = defaultChannelAmbientGateWindow
	}
	maxRecentPerAgent := h.cfg.ChannelAmbientGateMaxRecentPerAgent
	if maxRecentPerAgent <= 0 {
		maxRecentPerAgent = defaultChannelAmbientGateMaxRecentPerAgent
	}
	maxRecentPerChannel := h.cfg.ChannelAmbientGateMaxRecentPerChannel
	if maxRecentPerChannel <= 0 {
		maxRecentPerChannel = defaultChannelAmbientGateMaxRecentPerChannel
	}
	maxRecentPerRuntime := h.cfg.ChannelAmbientGateMaxRecentPerRuntime
	if maxRecentPerRuntime <= 0 {
		maxRecentPerRuntime = defaultChannelAmbientGateMaxRecentPerRuntime
	}
	return channelAmbientGateConfig{
		mode:                mode,
		window:              window,
		maxRecentPerAgent:   maxRecentPerAgent,
		maxRecentPerChannel: maxRecentPerChannel,
		maxRecentPerRuntime: maxRecentPerRuntime,
	}
}

func (h *Handler) shouldDispatchChannelAmbientObservation(ctx context.Context, ch ChannelResponse, trigger ChannelMessageResponse, agent db.Agent) bool {
	return h.shouldDispatchChannelAmbientObservationWithDB(ctx, h.DB, ch, trigger, agent)
}

func (h *Handler) shouldDispatchChannelAmbientObservationWithDB(ctx context.Context, exec db.DBTX, ch ChannelResponse, trigger ChannelMessageResponse, agent db.Agent) bool {
	cfg := h.channelAmbientGateConfig()
	if cfg.mode == channelAmbientGateModeOff {
		h.recordChannelAmbientGateDecision(channelAmbientGateActionEnqueued, channelAmbientGateReasonGateOff, ch, agent, trigger)
		return true
	}
	if skip, reason := deterministicChannelAmbientRelevanceSkip(trigger.Content); skip {
		h.recordChannelAmbientGateDecision(channelAmbientGateActionRelevanceSkipped, reason, ch, agent, trigger)
		return false
	}
	stats, err := h.channelAmbientGateStatsWithDB(ctx, exec, parseUUID(ch.ID), agent.ID, agent.RuntimeID, cfg.window)
	if err != nil {
		slog.Warn("channel ambient gate: stats query failed; dropping ambient dispatch", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
		h.recordChannelAmbientGateDecision(channelAmbientGateActionDropped, channelAmbientGateReasonGateError, ch, agent, trigger)
		return false
	}
	if stats.activeForAgent > 0 {
		return true
	}
	if stats.recentForAgent >= int64(cfg.maxRecentPerAgent) {
		h.recordChannelAmbientGateDecision(channelAmbientGateActionDropped, channelAmbientGateReasonAgentWindowCap, ch, agent, trigger)
		return false
	}
	if stats.recentForChannel >= int64(cfg.maxRecentPerChannel) {
		h.recordChannelAmbientGateDecision(channelAmbientGateActionDropped, channelAmbientGateReasonChannelWindowCap, ch, agent, trigger)
		return false
	}
	if agent.RuntimeID.Valid && stats.recentForRuntime >= int64(cfg.maxRecentPerRuntime) {
		h.recordChannelAmbientGateDecision(channelAmbientGateActionFused, channelAmbientGateReasonRuntimeWindowCap, ch, agent, trigger)
		return false
	}
	h.recordChannelAmbientGateDecision(channelAmbientGateActionEnqueued, channelAmbientGateReasonAccepted, ch, agent, trigger)
	return true
}

func (h *Handler) dispatchSingleChannelAmbientObservation(ctx context.Context, ch ChannelResponse, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID, agent db.Agent) {
	cfg := h.channelAmbientGateConfig()
	if cfg.mode == channelAmbientGateModeOff {
		h.recordChannelAmbientGateDecision(channelAmbientGateActionEnqueued, channelAmbientGateReasonGateOff, ch, agent, trigger)
		h.recordChannelAmbientInboxEvent(ctx, ch, trigger, agent)
		return
	}
	if skip, reason := deterministicChannelAmbientRelevanceSkip(trigger.Content); skip {
		h.recordChannelAmbientGateDecision(channelAmbientGateActionRelevanceSkipped, reason, ch, agent, trigger)
		return
	}
	if h.TxStarter == nil {
		slog.Warn("channel ambient gate: missing transaction starter; dropping ambient dispatch", "channel", ch.ID, "agent", uuidToString(agent.ID))
		h.recordChannelAmbientGateDecision(channelAmbientGateActionDropped, channelAmbientGateReasonGateError, ch, agent, trigger)
		return
	}

	// Serialize the final ambient gate recheck and enqueue per (channel, agent).
	// This makes concurrent ordinary messages coalesce behind the first queued
	// ambient task instead of all passing the same pre-enqueue count snapshot.
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		slog.Warn("channel ambient gate: begin lock transaction failed; dropping ambient dispatch", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
		h.recordChannelAmbientGateDecision(channelAmbientGateActionDropped, channelAmbientGateReasonGateError, ch, agent, trigger)
		return
	}
	defer tx.Rollback(ctx)

	if err := h.lockChannelAmbientGate(ctx, tx, ch, agent); err != nil {
		slog.Warn("channel ambient gate: advisory lock failed; dropping ambient dispatch", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
		h.recordChannelAmbientGateDecision(channelAmbientGateActionDropped, channelAmbientGateReasonGateError, ch, agent, trigger)
		return
	}
	if !h.shouldDispatchChannelAmbientObservationWithDB(ctx, tx, ch, trigger, agent) {
		_ = tx.Commit(ctx)
		return
	}
	task, queued, ok := h.createOrCoalesceChannelAmbientWakeTx(ctx, tx, ch, agent, trigger, initiatorUserID)
	if !ok {
		return
	}
	if err := tx.Commit(ctx); err != nil {
		slog.Warn("channel ambient gate: advisory lock transaction commit failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
		return
	}
	if queued {
		h.TaskService.PublishChatTaskQueued(ctx, task, false)
	}
}

func (h *Handler) createChannelAmbientPromptTaskTx(ctx context.Context, tx pgx.Tx, ch ChannelResponse, agent db.Agent, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID) (db.AgentInboxEvent, bool) {
	task, queued, ok := h.createOrCoalesceChannelAmbientWakeTx(ctx, tx, ch, agent, trigger, initiatorUserID)
	if !ok || !queued {
		return db.AgentInboxEvent{}, false
	}
	return task, true
}

func (h *Handler) createChannelAmbientPromptTaskRowTx(ctx context.Context, tx pgx.Tx, ch ChannelResponse, agent db.Agent, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID, prompt string) (db.ChatSession, db.AgentInboxEvent, bool) {
	if h.TaskService == nil {
		slog.Warn("channel ambient observation: task service missing", "channel", ch.ID, "agent", uuidToString(agent.ID))
		return db.ChatSession{}, db.AgentInboxEvent{}, false
	}
	txQueries := h.Queries.WithTx(tx)
	session, err := h.ensureChannelAgentSessionWithDB(ctx, txQueries, tx, ch, agent.ID, initiatorUserID)
	if err != nil {
		slog.Warn("channel ambient observation: ensure chat session failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
		return db.ChatSession{}, db.AgentInboxEvent{}, false
	}
	promptMsg, err := h.createChannelAgentPromptMessageWithDB(ctx, tx, session.ID, prompt, trigger)
	if err != nil {
		slog.Warn("channel ambient observation: create chat message failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
		return db.ChatSession{}, db.AgentInboxEvent{}, false
	}
	task, err := h.TaskService.CreateAmbientChatTaskRow(ctx, txQueries, session, initiatorUserID)
	if err != nil {
		slog.Warn("channel ambient observation: enqueue chat task failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
		return db.ChatSession{}, db.AgentInboxEvent{}, false
	}
	if _, err := tx.Exec(ctx, `UPDATE chat_message SET task_id = $1 WHERE id = $2`, task.ID, promptMsg.ID); err != nil {
		slog.Warn("channel ambient observation: tag prompt with task failed", "channel", ch.ID, "agent", uuidToString(agent.ID), "task", uuidToString(task.ID), "error", err)
		return db.ChatSession{}, db.AgentInboxEvent{}, false
	}
	return session, task, true
}

func (h *Handler) lockChannelAmbientGate(ctx context.Context, tx pgx.Tx, ch ChannelResponse, agent db.Agent) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`, ch.ID, uuidToString(agent.ID))
	return err
}

func deterministicChannelAmbientRelevanceSkip(content string) (bool, string) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return true, channelAmbientGateReasonNonTextNoise
	}
	rs := []rune(trimmed)
	if len(rs) > 8 {
		return false, ""
	}
	hasSemanticText := false
	for _, r := range rs {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			hasSemanticText = true
			break
		}
	}
	if !hasSemanticText {
		return true, channelAmbientGateReasonNonTextNoise
	}
	return false, ""
}

func (h *Handler) channelAmbientGateStats(ctx context.Context, channelID, agentID, runtimeID pgtype.UUID, window time.Duration) (channelAmbientGateStats, error) {
	return h.channelAmbientGateStatsWithDB(ctx, h.DB, channelID, agentID, runtimeID, window)
}

func (h *Handler) channelAmbientGateStatsWithDB(ctx context.Context, exec db.DBTX, channelID, agentID, runtimeID pgtype.UUID, window time.Duration) (channelAmbientGateStats, error) {
	var stats channelAmbientGateStats
	err := exec.QueryRow(ctx, `
		SELECT
			COALESCE((
				SELECT count(*)
				FROM agent_inbox_event atq
				JOIN channel_agent_session cas ON cas.chat_session_id = atq.chat_session_id
				WHERE cas.channel_id = $1
				  AND cas.agent_id = $2
				  AND atq.priority = 1
				  AND COALESCE(atq.force_fresh_session, false) = true
				  AND atq.status IN ('pending', 'draining', 'failed')
			), 0) AS active_for_agent,
			COALESCE((
				SELECT count(*)
				FROM agent_inbox_event atq
				JOIN channel_agent_session cas ON cas.chat_session_id = atq.chat_session_id
				WHERE cas.channel_id = $1
				  AND cas.agent_id = $2
				  AND atq.priority = 1
				  AND COALESCE(atq.force_fresh_session, false) = true
				  AND atq.created_at >= now() - make_interval(secs => $4::double precision)
			), 0) AS recent_for_agent,
			COALESCE((
				SELECT count(*)
				FROM agent_inbox_event atq
				JOIN channel_agent_session cas ON cas.chat_session_id = atq.chat_session_id
				WHERE cas.channel_id = $1
				  AND atq.priority = 1
				  AND COALESCE(atq.force_fresh_session, false) = true
				  AND atq.created_at >= now() - make_interval(secs => $4::double precision)
			), 0) AS recent_for_channel,
			COALESCE((
				SELECT count(*)
				FROM agent_inbox_event atq
				WHERE atq.runtime_id = $3
				  AND atq.priority = 1
				  AND COALESCE(atq.force_fresh_session, false) = true
				  AND atq.created_at >= now() - make_interval(secs => $4::double precision)
			), 0) AS recent_for_runtime`,
		channelID, agentID, runtimeID, window.Seconds()).Scan(
		&stats.activeForAgent,
		&stats.recentForAgent,
		&stats.recentForChannel,
		&stats.recentForRuntime,
	)
	return stats, err
}

func (h *Handler) recordChannelAmbientGateDecision(action, reason string, ch ChannelResponse, agent db.Agent, trigger ChannelMessageResponse) {
	if h.Metrics != nil {
		h.Metrics.RecordChannelAmbientGateDecision(action, reason)
	}
	if action == channelAmbientGateActionEnqueued {
		return
	}
	slog.Debug("channel ambient gate decision",
		"action", action,
		"reason", reason,
		"channel", ch.ID,
		"agent", uuidToString(agent.ID),
		"message", trigger.ID,
	)

	// Ambient gate hold events are deferred — recording them from this
	// function causes deadlocks in concurrent gate tests because the
	// INSERT contends with the advisory lock held by sibling goroutines.
	// A safe approach requires collecting decisions during the gate pass
	// and batch-inserting after all transactions commit.
}
