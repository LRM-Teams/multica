package handler

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	AgentPresenceOnline  = "online"
	AgentPresenceOffline = "offline"
)

// RunnerPresenceSource exposes only the live ownership question Presence
// needs. The daemon Hub implements it; tests may substitute the transport
// boundary without replacing database or projection behavior.
type RunnerPresenceSource interface {
	IsCurrentWorkspaceRunner(daemonID, workspaceID, daemonInstanceID string) bool
}

type AgentPresenceItem struct {
	AgentID  string `json:"agent_id"`
	Presence string `json:"presence"`
}

type AgentPresenceResponse struct {
	Items []AgentPresenceItem `json:"items"`
}

type AgentPresenceRealtimePayload struct {
	AgentID  string `json:"agent_id"`
	Presence string `json:"presence"`
}

type runnerLaunchPresence struct {
	daemonID         string
	daemonInstanceID string
	status           string
}

func (h *Handler) currentRunnerPresenceSource() RunnerPresenceSource {
	if h == nil {
		return nil
	}
	if h.RunnerPresenceSource != nil {
		return h.RunnerPresenceSource
	}
	return h.DaemonHub
}

func (h *Handler) runnerPresenceLocked(fn func() error) error {
	if h != nil && h.RunnerPresenceMu != nil {
		h.RunnerPresenceMu.Lock()
		defer h.RunnerPresenceMu.Unlock()
	}
	return fn()
}

func (h *Handler) projectRunnerLaunchPresence(workspaceID string, launch *runnerLaunchPresence) string {
	// Online is process admission: only agent:status active on the current
	// Runner. A start ACK (accepted + any queue_state) is a transport receipt.
	if launch == nil || launch.status != protocol.AgentStatusActive {
		return AgentPresenceOffline
	}
	source := h.currentRunnerPresenceSource()
	if source == nil || !source.IsCurrentWorkspaceRunner(launch.daemonID, workspaceID, launch.daemonInstanceID) {
		return AgentPresenceOffline
	}
	return AgentPresenceOnline
}

func (h *Handler) loadRunnerLaunchPresence(_ context.Context, workspaceID, agentID string) (*runnerLaunchPresence, error) {
	obs, ok := h.observations().get(workspaceID, agentID)
	if !ok {
		return nil, nil
	}
	return &runnerLaunchPresence{daemonID: obs.daemonID, daemonInstanceID: obs.daemonInstanceID, status: obs.status}, nil
}

func (h *Handler) publishAgentPresence(workspaceID, agentID, presence string) {
	if h == nil || h.Bus == nil {
		return
	}
	h.publish(protocol.EventAgentPresence, workspaceID, "system", "", AgentPresenceRealtimePayload{
		AgentID: agentID, Presence: presence,
	})
}

func (h *Handler) publishAgentPresenceChange(workspaceID, agentID, before, after string) {
	if before == after {
		return
	}
	h.publishAgentPresence(workspaceID, agentID, after)
}

// GetAgentPresence returns one binary Presence row for every non-archived
// Agent in the request Workspace. Runtime health, Tasks, Activity, and
// diagnostics are deliberately absent from this projection.
func (h *Handler) GetAgentPresence(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	workspaceIDText := ctxWorkspaceID(r.Context())
	if workspaceIDText == "" {
		workspaceIDText = h.resolveWorkspaceID(r)
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, workspaceIDText, "workspace id")
	if !ok {
		return
	}
	actorType, _ := h.resolveActor(r, requestUserID(r), workspaceIDText)
	if actorType == "agent" {
		writeError(w, http.StatusForbidden, "agent principals may not read Agent Presence")
		return
	}
	if _, ok := h.workspaceMember(w, r, workspaceIDText); !ok {
		return
	}

	rows, err := h.DB.Query(r.Context(), `
		SELECT a.id
		FROM agent a
		WHERE a.workspace_id = $1 AND a.archived_at IS NULL
		ORDER BY a.created_at ASC, a.id ASC`, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list Agent Presence")
		return
	}
	defer rows.Close()

	items := make([]AgentPresenceItem, 0)
	for rows.Next() {
		var agentID pgtype.UUID
		if err := rows.Scan(&agentID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list Agent Presence")
			return
		}
		obs, _ := h.observations().get(workspaceIDText, util.UUIDToString(agentID))
		launch := &runnerLaunchPresence{daemonID: obs.daemonID, daemonInstanceID: obs.daemonInstanceID, status: obs.status}
		items = append(items, AgentPresenceItem{
			AgentID:  util.UUIDToString(agentID),
			Presence: h.projectRunnerLaunchPresence(workspaceIDText, launch),
		})
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list Agent Presence")
		return
	}
	writeJSON(w, http.StatusOK, AgentPresenceResponse{Items: items})
}
