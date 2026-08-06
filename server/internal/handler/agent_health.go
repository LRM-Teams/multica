package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/taskfailure"
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
	// "thinking" is not emitted yet — it needs a task-phase signal that
	// doesn't exist as an agent-visible fact today. Leaving it unemitted is
	// intentional: the family rule is status stays unknown rather than
	// invented. Emitted today from real facts:
	//   "stopped"  — agent_runtime.offline_reason (task ①)
	//   "starting" — agent_runtime.starting_since / cold-start probe (#1802)
	//   "crashed"  — agent.crashed_since from idle resident death (#1803)
	//   "blocked"  — agent.provider_blocked_until (quota lock, #64/#77)
	agentDisplayStatusIdle         = "idle"
	agentDisplayStatusWorking      = "working"
	agentDisplayStatusStarting     = "starting"
	agentDisplayStatusCrashed      = "crashed"
	agentDisplayStatusBlocked      = "blocked"
	agentDisplayStatusDisconnected = "disconnected"
	agentDisplayStatusOffline      = "offline"
	agentDisplayStatusStopped      = "stopped"

	// agentRuntimeStartingTTL bounds how long a fresh MarkAgentRuntimesStarting
	// call keeps a runtime showing "starting" if the daemon never follows up
	// with a completing register call (crash between the two, lost request,
	// etc.) — register unconditionally clears starting_since on success, so
	// this TTL only matters for the failure case. ~3x the ~20s cold-start
	// version-probe loop it's meant to cover; expiring early just means a
	// slow-starting machine stops showing "starting" a little sooner, not
	// that it shows something wrong (falls through to the existing
	// connectivity-based tiers, i.e. today's behavior).
	agentRuntimeStartingTTL = 60 * time.Second
)

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
}

func (h *Handler) listAgentHealthEvents(ctx context.Context, agent db.Agent, runtimeID pgtype.UUID, limit int) ([]AgentHealthEvent, error) {
	return []AgentHealthEvent{}, nil
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
	runtimeConnectivityOnline  = service.RuntimeConnectivityOnline
	runtimeConnectivityStale   = service.RuntimeConnectivityStale
	runtimeConnectivityDead    = service.RuntimeConnectivityDead
	runtimeConnectivityStopped = service.RuntimeConnectivityStopped
)

func runtimeConnectivity(rt db.AgentRuntime, now time.Time) runtimeConnectivityTier {
	return service.RuntimeConnectivity(rt, now)
}

// agentRuntimeDisplayStatus derives an honest status for the agent
// list/detail surface (task #42③). It does not pass through the raw
// agent_runtime.status column: that column can read "online" for up to
// ~180s after the daemon actually went silent (sweeper lag).
//
// crashedSince is the per-agent idle-resident crash fact (agent.crashed_since).
// It is checked only while connectivity is still Online: a whole-machine
// drop is "offline"/"disconnected", not "crashed" — we no longer have a
// live daemon asserting the provider-death fact. Mid-turn process_failure
// never sets this column (different fact; do not invent).
//
// providerBlockDetail / providerBlockedUntil are the sticky provider-quota
// lock (tasks #64/#77). detail non-empty locks; until NULL means unknown end
// (still locked). Checked while connectivity is Online so a still-reachable
// daemon cannot paint "idle"/"working" during lockout.
func agentRuntimeDisplayStatus(agentStatus string, rt db.AgentRuntime, crashedSince pgtype.Timestamptz, providerBlockDetail string, providerBlockedUntil pgtype.Timestamptz, now time.Time) string {
	// Checked before connectivity: a machine coming back from a crash sets
	// starting_since before it has refreshed last_seen_at, so connectivity
	// would otherwise still read Dead/Stale from before the crash — exactly
	// the window "starting" exists to describe. TTL-gated here (not by a
	// write-side clear) so a daemon that never completes register after
	// this call still falls through safely once the window passes, instead
	// of staying stuck showing "starting" forever.
	if rt.StartingSince.Valid && now.Sub(rt.StartingSince.Time) < agentRuntimeStartingTTL {
		return agentDisplayStatusStarting
	}
	switch runtimeConnectivity(rt, now) {
	case runtimeConnectivityStopped:
		return agentDisplayStatusStopped
	case runtimeConnectivityDead:
		return agentDisplayStatusOffline
	case runtimeConnectivityStale:
		return agentDisplayStatusDisconnected
	}
	if crashedSince.Valid {
		return agentDisplayStatusCrashed
	}
	if taskfailure.ProviderLockActive(providerBlockDetail, providerBlockedUntil.Time, providerBlockedUntil.Valid, now) {
		return agentDisplayStatusBlocked
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
	case runtimeConnectivityStopped:
		// Task ① (agent intentional-stop signal): a confirmed offline_reason
		// bypasses the Stale/Dead time ramp at the connectivity-tier level,
		// so it must be handled here too or it would silently fall through
		// to the agentHealthStateOnline default above — mislabeling a
		// deliberately-stopped runtime as online. This deliberately reuses
		// the existing 5-word Activity Health vocabulary (agentHealthStateOffline)
		// rather than adding a new state; only the reason_code reflects the
		// specific, real fact instead of a guessed one.
		state = agentHealthStateOffline
		reason = rt.OfflineReason.String
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
