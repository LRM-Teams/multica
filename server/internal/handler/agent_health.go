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
	"github.com/multica-ai/multica/server/internal/service"
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
	// agentHealthReconnectAfter/agentHealthStaleThreshold are local aliases
	// for the shared thresholds in service.RuntimeConnectivity (task #53
	// consolidated what used to be an independent handler-package copy) —
	// kept as local names so agent_lifecycle.go/squad.go's existing inline
	// freshness checks don't need to change in the same pass.
	agentHealthReconnectAfter         = service.AgentHealthReconnectAfter
	agentHealthRecoveredSummaryWindow = 5 * time.Minute
	agentHealthStaleThreshold         = service.AgentHealthStaleThreshold
	defaultAgentHealthEventLimit      = 50

	// agentDisplayStatus* is the honest, read-time status vocabulary for the
	// agent list/detail surface (task #42③). It deliberately does not reuse
	// the agentHealthState* names: this is a coarser, workload-aware view
	// meant for the primary status badge, not the Activity Health tab.
	// "starting"/"thinking"/"crashed"/"stopped" are not emitted yet — they
	// need signals (lifecycle-start marker, task phase, provider-crash
	// event) that don't exist as agent-visible facts today. Leaving them
	// unemitted is intentional: the family rule is status stays unknown
	// rather than invented.
	agentDisplayStatusIdle         = "idle"
	agentDisplayStatusWorking      = "working"
	agentDisplayStatusDisconnected = "disconnected"
	agentDisplayStatusOffline      = "offline"
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
			target_kind, target_id, reason_code, message, details, visibility
		)
		SELECT
			a.workspace_id, a.id, a.runtime_id, 'transport', $3, 'info',
			'agent', a.id, $4, $5, $6::jsonb, $7
		FROM agent a
		WHERE a.workspace_id = $1
		  AND a.runtime_id = $2
		  AND a.archived_at IS NULL
	`, workspaceID, runtimeID, eventType, reasonCode, message, string(payload), activityVisibilityFor(activityKindTransport, eventType, "info", reasonCode))
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

	// Task #908: online/health presence (can I tell if it's around before I
	// use it) is unconditional for every workspace member. The raw
	// health_events diagnostic log stays admin|owner-gated below.
	workspaceID := uuidToString(agent.WorkspaceID)
	actorType, actorID := h.resolveActor(r, requestUserID(r), workspaceID)
	hasInternalsAccess := h.canAccessAgentInternals(r.Context(), agent, actorType, actorID, workspaceID)

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
	// LRM-548: presence chrome follows the bound runtime's heartbeat, not a
	// claim/capacity "runnable" predicate. A channel/workspace agent on the
	// owner's private runtime (e.g. after switching to Grok) still shows
	// Online when last_seen is fresh — Runtime Config already does.

	events, err := h.listAgentHealthEvents(r.Context(), agent, rt.ID, defaultAgentHealthEventLimit)
	if err != nil {
		slog.Warn("agent health: list events failed", "agent_id", agentID, "runtime_id", uuidToString(rt.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load agent health")
		return
	}

	now := time.Now()
	summary := agentHealthSummary(agent, rt, events, now)
	if operation, err := getActiveAgentLifecycleOperation(r.Context(), h.DB, agent.ID); err != nil {
		slog.Warn("agent health: load lifecycle overlay failed", "agent_id", agentID, "error", err)
	} else if operation != nil {
		summary.State = "restarting"
		summary.ReasonCode = "agent_lifecycle_" + string(operation.ActionKind)
		if operation.StartedAt != nil {
			summary.StateSince = operation.StartedAt
		} else {
			summary.StateSince = &operation.CreatedAt
		}
	}
	events = prependCurrentAgentHealthEvent(agent, rt, summary, events)
	if !hasInternalsAccess {
		events = []AgentHealthEvent{}
	}
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

// runtimeConnectivityTier is a read-time judgment of whether a runtime's
// heartbeat is currently trustworthy. It is never written back to the
// database — agent_runtime.status stays the sweeper's job (~150s+sweep
// interval to flip offline) because dispatch/admission logic depends on
// that column being stable and cheap to read. This tier exists purely to
// stop display surfaces from repeating what the raw column says once it's
// known to be stale. Shared by agentHealthSummary (Activity Health tab, 5
// state vocab) and agentRuntimeDisplayStatus (agent list/detail badge, task
// #42③, 4-of-8-word vocab so far).
// runtimeConnectivityTier and runtimeConnectivity are local aliases for
// service.RuntimeConnectivity (task #53 consolidated what used to be an
// independent duplicate implementation here — service.AgentReadiness needed
// the same tiering and couldn't import this package, so the canonical
// version now lives in service and this package delegates to it).
type runtimeConnectivityTier = service.RuntimeConnectivityTier

const (
	runtimeConnectivityOnline = service.RuntimeConnectivityOnline
	runtimeConnectivityStale  = service.RuntimeConnectivityStale
	runtimeConnectivityDead   = service.RuntimeConnectivityDead
)

func runtimeConnectivity(rt db.AgentRuntime, now time.Time) runtimeConnectivityTier {
	return service.RuntimeConnectivity(rt, now)
}

// agentRuntimeDisplayStatus derives an honest status for the agent
// list/detail surface (task #42③). It does not pass through the raw
// agent_runtime.status column: that column can read "online" for up to
// ~180s after the daemon actually went silent (sweeper lag), and
// indefinitely if the daemon's transport stays up while the provider
// subprocess it spawned has died — that second case has no persisted signal
// anywhere yet (tracked separately, needs a daemon-side crash-detection
// hook; see #42①②).
func agentRuntimeDisplayStatus(agentStatus string, rt db.AgentRuntime, now time.Time) string {
	switch runtimeConnectivity(rt, now) {
	case runtimeConnectivityDead:
		return agentDisplayStatusOffline
	case runtimeConnectivityStale:
		return agentDisplayStatusDisconnected
	}
	if agentStatus == "working" {
		return agentDisplayStatusWorking
	}
	return agentDisplayStatusIdle
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

	// Freshness gate: even if the DB row still says "online", a stale
	// heartbeat means the runtime is effectively unreachable. This closes
	// the gap between a daemon going silent and the sweeper marking the
	// row offline (~150s + sweep interval). (#284)
	switch runtimeConnectivity(rt, now) {
	case runtimeConnectivityStale:
		state = agentHealthStateSuspectedDisconnect
		reason = "heartbeat_stale"
	case runtimeConnectivityDead:
		state = agentHealthStateReconnecting
		reason = "probe_timeout"
	case runtimeConnectivityOnline:
	}

	if state == agentHealthStateOnline && len(events) > 0 && events[0].Type == agentHealthEventTransportRecover {
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
