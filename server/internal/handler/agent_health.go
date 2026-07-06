package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	agentHealthEventServerPing       = "server_ping_received"
	agentHealthEventLivenessProbe    = "daemon_liveness_probe_sent"
	agentHealthEventProbeTimeout     = "probe_timeout_reconnect"
	agentHealthEventTransportRecover = "transport_reconnected"

	agentHealthStateOnline              = "online"
	agentHealthStateSuspectedDisconnect = "suspected_disconnect"
	agentHealthStateReconnecting        = "reconnecting"
	agentHealthStateRecovered           = "recovered"
	agentHealthStateOffline             = "offline"
	agentHealthReconnectAfter           = 5 * time.Minute
	agentHealthRecoveredSummaryWindow   = 5 * time.Minute
	defaultAgentHealthEventLimit        = 50
)

var agentHealthEventTypes = []string{
	agentHealthEventServerPing,
	agentHealthEventLivenessProbe,
	agentHealthEventProbeTimeout,
	agentHealthEventTransportRecover,
}

type AgentHealthResponse struct {
	Summary AgentHealthSummary `json:"health_summary"`
	Events  []AgentHealthEvent `json:"health_events"`
}

type AgentHealthSummary struct {
	AgentID     string  `json:"agent_id"`
	RuntimeID   *string `json:"runtime_id"`
	State       string  `json:"state"`
	ReasonCode  string  `json:"reason_code"`
	StateSince  *string `json:"state_since"`
	LastSeenAt  *string `json:"last_seen_at"`
	LastEventAt *string `json:"last_event_at"`
}

type AgentHealthEvent struct {
	ID         string         `json:"id"`
	AgentID    string         `json:"agent_id"`
	RuntimeID  *string        `json:"runtime_id"`
	Type       string         `json:"type"`
	StateAfter string         `json:"state_after"`
	ReasonCode string         `json:"reason_code"`
	Message    string         `json:"message"`
	OccurredAt string         `json:"occurred_at"`
	Details    map[string]any `json:"details,omitempty"`
	Synthetic  bool           `json:"synthetic,omitempty"`
}

type RuntimeHealthEventExecutor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

// RecordRuntimeHealthEventForRuntimeAgents writes a lifecycle event for each
// active agent bound to a runtime. Runtimes are the liveness source, but the
// Activity Health surface is agent-scoped, so persisted rows stay agent-scoped
// and can reuse the existing agent_activity_event visibility model.
func RecordRuntimeHealthEventForRuntimeAgents(ctx context.Context, exec RuntimeHealthEventExecutor, workspaceID, runtimeID pgtype.UUID, eventType, stateAfter, reasonCode, message string, details map[string]any) error {
	if exec == nil {
		return nil
	}
	if details == nil {
		details = map[string]any{}
	}
	details["state_after"] = stateAfter
	payload, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("marshal runtime health event details: %w", err)
	}
	_, err = exec.Exec(ctx, `
		INSERT INTO agent_activity_event (
			workspace_id, agent_id, runtime_id, event_kind, event_type, severity,
			target_kind, target_id, reason_code, message, details
		)
		SELECT
			a.workspace_id, a.id, a.runtime_id, 'lifecycle', $3, 'info',
			'agent', a.id, $4, $5, $6::jsonb
		FROM agent a
		WHERE a.workspace_id = $1
		  AND a.runtime_id = $2
		  AND a.archived_at IS NULL
	`, workspaceID, runtimeID, eventType, reasonCode, message, string(payload))
	if err != nil {
		return fmt.Errorf("insert runtime health event: %w", err)
	}
	return nil
}

func (h *Handler) GetAgentHealth(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	agent, ok := h.loadAgentForUser(w, r, agentID)
	if !ok {
		return
	}

	workspaceID := uuidToString(agent.WorkspaceID)
	actorType, actorID := h.resolveActor(r, requestUserID(r), workspaceID)
	if !h.canAccessPrivateAgent(r.Context(), agent, actorType, actorID, workspaceID) {
		writeError(w, http.StatusForbidden, "you do not have access to this agent")
		return
	}

	rt, err := h.Queries.GetAgentRuntimeForWorkspace(r.Context(), db.GetAgentRuntimeForWorkspaceParams{
		ID:          agent.RuntimeID,
		WorkspaceID: agent.WorkspaceID,
	})
	if err != nil {
		if isNotFound(err) {
			resp := AgentHealthResponse{
				Summary: agentHealthMissingRuntimeSummary(agent),
				Events:  []AgentHealthEvent{},
			}
			writeJSON(w, http.StatusOK, resp)
			return
		}
		slog.Warn("agent health: load runtime failed", "agent_id", agentID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load agent health")
		return
	}

	events, err := h.listAgentHealthEvents(r.Context(), agent, rt.ID, defaultAgentHealthEventLimit)
	if err != nil {
		slog.Warn("agent health: list events failed", "agent_id", agentID, "runtime_id", uuidToString(rt.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load agent health")
		return
	}

	now := time.Now()
	summary := agentHealthSummary(agent, rt, events, now)
	events = prependCurrentAgentHealthEvent(agent, rt, summary, events)
	writeJSON(w, http.StatusOK, AgentHealthResponse{
		Summary: summary,
		Events:  events,
	})
}

func (h *Handler) recordRuntimeHealthEventForActiveAgents(ctx context.Context, rt db.AgentRuntime, eventType, stateAfter, reasonCode, message string, details map[string]any) {
	if err := RecordRuntimeHealthEventForRuntimeAgents(ctx, h.DB, rt.WorkspaceID, rt.ID, eventType, stateAfter, reasonCode, message, details); err != nil {
		slog.Warn("record runtime health event failed",
			"runtime_id", uuidToString(rt.ID),
			"workspace_id", uuidToString(rt.WorkspaceID),
			"event_type", eventType,
			"error", err)
	}
}

func (h *Handler) listAgentHealthEvents(ctx context.Context, agent db.Agent, runtimeID pgtype.UUID, limit int) ([]AgentHealthEvent, error) {
	if h.DB == nil {
		return []AgentHealthEvent{}, nil
	}
	if limit <= 0 || limit > defaultAgentHealthEventLimit {
		limit = defaultAgentHealthEventLimit
	}
	rows, err := h.DB.Query(ctx, `
		SELECT id, runtime_id, event_type, reason_code, message, details, created_at
		FROM agent_activity_event
		WHERE workspace_id = $1
		  AND agent_id = $2
		  AND runtime_id = $3
		  AND event_type = ANY($4::text[])
		ORDER BY created_at DESC, id DESC
		LIMIT $5
	`, agent.WorkspaceID, agent.ID, runtimeID, agentHealthEventTypes, int32(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]AgentHealthEvent, 0, limit)
	for rows.Next() {
		var (
			id         pgtype.UUID
			rowRT      pgtype.UUID
			eventType  string
			reasonCode string
			message    string
			detailsRaw []byte
			createdAt  pgtype.Timestamptz
		)
		if err := rows.Scan(&id, &rowRT, &eventType, &reasonCode, &message, &detailsRaw, &createdAt); err != nil {
			return nil, err
		}
		details := map[string]any{}
		if len(detailsRaw) > 0 {
			_ = json.Unmarshal(detailsRaw, &details)
		}
		state := agentHealthEventState(eventType)
		if fromDetails, ok := details["state_after"].(string); ok && fromDetails != "" {
			state = fromDetails
		}
		events = append(events, AgentHealthEvent{
			ID:         uuidToString(id),
			AgentID:    uuidToString(agent.ID),
			RuntimeID:  uuidToPtr(rowRT),
			Type:       eventType,
			StateAfter: state,
			ReasonCode: reasonCode,
			Message:    message,
			OccurredAt: timestampToString(createdAt),
			Details:    details,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func agentHealthMissingRuntimeSummary(agent db.Agent) AgentHealthSummary {
	return AgentHealthSummary{
		AgentID:    uuidToString(agent.ID),
		RuntimeID:  nil,
		State:      agentHealthStateOffline,
		ReasonCode: "runtime_missing",
	}
}

func agentHealthSummary(agent db.Agent, rt db.AgentRuntime, events []AgentHealthEvent, now time.Time) AgentHealthSummary {
	runtimeID := uuidToString(rt.ID)
	lastSeenAt := timestampToPtr(rt.LastSeenAt)
	stateSince := timestampToPtr(rt.UpdatedAt)
	lastEventAt := lastSeenAt
	if len(events) > 0 {
		lastEventAt = &events[0].OccurredAt
	}

	state := agentHealthStateOnline
	reason := "heartbeat_received"
	if rt.Status == "offline" {
		state = agentHealthStateSuspectedDisconnect
		reason = "heartbeat_stale"
		if rt.UpdatedAt.Valid && now.Sub(rt.UpdatedAt.Time) >= agentHealthReconnectAfter {
			state = agentHealthStateReconnecting
			reason = "probe_timeout"
		}
	} else if len(events) > 0 && events[0].Type == agentHealthEventTransportRecover {
		if events[0].OccurredAt != "" {
			if recoveredAt, err := time.Parse(time.RFC3339, events[0].OccurredAt); err == nil && now.Sub(recoveredAt) < agentHealthRecoveredSummaryWindow {
				state = agentHealthStateRecovered
				reason = "transport_reconnected"
			}
		}
	}

	return AgentHealthSummary{
		AgentID:     uuidToString(agent.ID),
		RuntimeID:   &runtimeID,
		State:       state,
		ReasonCode:  reason,
		StateSince:  stateSince,
		LastSeenAt:  lastSeenAt,
		LastEventAt: lastEventAt,
	}
}

func prependCurrentAgentHealthEvent(agent db.Agent, rt db.AgentRuntime, summary AgentHealthSummary, events []AgentHealthEvent) []AgentHealthEvent {
	eventType, message := agentHealthCurrentEvent(summary.State)
	if eventType == "" {
		return events
	}
	if len(events) > 0 && events[0].Type == eventType && events[0].StateAfter == summary.State {
		return events
	}
	occurredAt := ""
	if summary.StateSince != nil {
		occurredAt = *summary.StateSince
	}
	if summary.LastSeenAt != nil && (summary.State == agentHealthStateOnline || summary.State == agentHealthStateRecovered) {
		occurredAt = *summary.LastSeenAt
	}
	runtimeID := uuidToString(rt.ID)
	synthetic := AgentHealthEvent{
		ID:         fmt.Sprintf("synthetic:%s:%s:%s:%s", uuidToString(agent.ID), runtimeID, eventType, occurredAt),
		AgentID:    uuidToString(agent.ID),
		RuntimeID:  &runtimeID,
		Type:       eventType,
		StateAfter: summary.State,
		ReasonCode: summary.ReasonCode,
		Message:    message,
		OccurredAt: occurredAt,
		Details: map[string]any{
			"state_after": summary.State,
			"source":      "agent_runtime",
		},
		Synthetic: true,
	}
	out := make([]AgentHealthEvent, 0, len(events)+1)
	out = append(out, synthetic)
	out = append(out, events...)
	return out
}

func agentHealthCurrentEvent(state string) (string, string) {
	switch state {
	case agentHealthStateOnline:
		return agentHealthEventServerPing, "runtime heartbeat received"
	case agentHealthStateSuspectedDisconnect:
		return agentHealthEventLivenessProbe, "runtime heartbeat stale; liveness probe sent"
	case agentHealthStateReconnecting:
		return agentHealthEventProbeTimeout, "runtime liveness probe timed out; waiting for reconnect"
	case agentHealthStateRecovered:
		return agentHealthEventTransportRecover, "runtime transport reconnected"
	default:
		return "", ""
	}
}

func agentHealthEventState(eventType string) string {
	switch eventType {
	case agentHealthEventServerPing:
		return agentHealthStateOnline
	case agentHealthEventLivenessProbe:
		return agentHealthStateSuspectedDisconnect
	case agentHealthEventProbeTimeout:
		return agentHealthStateReconnecting
	case agentHealthEventTransportRecover:
		return agentHealthStateRecovered
	default:
		return agentHealthStateOffline
	}
}
