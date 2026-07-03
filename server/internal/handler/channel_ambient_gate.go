package handler

import (
	"context"
	"log/slog"
	"strings"
	"time"
	"unicode"

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
	cfg := h.channelAmbientGateConfig()
	if cfg.mode == channelAmbientGateModeOff {
		h.recordChannelAmbientGateDecision(channelAmbientGateActionEnqueued, channelAmbientGateReasonGateOff, ch, agent, trigger)
		return true
	}
	if skip, reason := deterministicChannelAmbientRelevanceSkip(trigger.Content); skip {
		h.recordChannelAmbientGateDecision(channelAmbientGateActionRelevanceSkipped, reason, ch, agent, trigger)
		return false
	}
	stats, err := h.channelAmbientGateStats(ctx, parseUUID(ch.ID), agent.ID, agent.RuntimeID, cfg.window)
	if err != nil {
		slog.Warn("channel ambient gate: stats query failed; allowing ambient dispatch", "channel", ch.ID, "agent", uuidToString(agent.ID), "error", err)
		h.recordChannelAmbientGateDecision(channelAmbientGateActionEnqueued, channelAmbientGateReasonGateError, ch, agent, trigger)
		return true
	}
	if stats.activeForAgent > 0 {
		h.recordChannelAmbientGateDecision(channelAmbientGateActionCoalesced, channelAmbientGateReasonAgentActive, ch, agent, trigger)
		return false
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
	var stats channelAmbientGateStats
	err := h.DB.QueryRow(ctx, `
		SELECT
			COALESCE((
				SELECT count(*)
				FROM agent_task_queue atq
				JOIN channel_agent_session cas ON cas.chat_session_id = atq.chat_session_id
				WHERE cas.channel_id = $1
				  AND cas.agent_id = $2
				  AND atq.priority = 1
				  AND COALESCE(atq.force_fresh_session, false) = true
				  AND atq.status IN ('queued', 'dispatched', 'running', 'waiting_local_directory')
			), 0) AS active_for_agent,
			COALESCE((
				SELECT count(*)
				FROM agent_task_queue atq
				JOIN channel_agent_session cas ON cas.chat_session_id = atq.chat_session_id
				WHERE cas.channel_id = $1
				  AND cas.agent_id = $2
				  AND atq.priority = 1
				  AND COALESCE(atq.force_fresh_session, false) = true
				  AND atq.created_at >= now() - make_interval(secs => $4::double precision)
			), 0) AS recent_for_agent,
			COALESCE((
				SELECT count(*)
				FROM agent_task_queue atq
				JOIN channel_agent_session cas ON cas.chat_session_id = atq.chat_session_id
				WHERE cas.channel_id = $1
				  AND atq.priority = 1
				  AND COALESCE(atq.force_fresh_session, false) = true
				  AND atq.created_at >= now() - make_interval(secs => $4::double precision)
			), 0) AS recent_for_channel,
			COALESCE((
				SELECT count(*)
				FROM agent_task_queue atq
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
}
