package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
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
	if launch == nil || launch.status != protocol.AgentStatusActive {
		return AgentPresenceOffline
	}
	source := h.currentRunnerPresenceSource()
	if source == nil || !source.IsCurrentWorkspaceRunner(launch.daemonID, workspaceID, launch.daemonInstanceID) {
		return AgentPresenceOffline
	}
	return AgentPresenceOnline
}

func (h *Handler) loadRunnerLaunchPresence(ctx context.Context, workspaceID, agentID pgtype.UUID) (*runnerLaunchPresence, error) {
	var launch runnerLaunchPresence
	err := h.DB.QueryRow(ctx, `
		SELECT daemon_id, daemon_instance_id, status
		FROM agent_activity_launch
		WHERE workspace_id = $1 AND agent_id = $2`, workspaceID, agentID).
		Scan(&launch.daemonID, &launch.daemonInstanceID, &launch.status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load Runner launch Presence: %w", err)
	}
	return &launch, nil
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
		SELECT a.id,
		       COALESCE(l.daemon_id, ''),
		       COALESCE(l.daemon_instance_id, ''),
		       COALESCE(l.status, '')
		FROM agent a
		LEFT JOIN agent_activity_launch l
		  ON l.workspace_id = a.workspace_id AND l.agent_id = a.id
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
		var launch runnerLaunchPresence
		if err := rows.Scan(&agentID, &launch.daemonID, &launch.daemonInstanceID, &launch.status); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list Agent Presence")
			return
		}
		items = append(items, AgentPresenceItem{
			AgentID:  util.UUIDToString(agentID),
			Presence: h.projectRunnerLaunchPresence(workspaceIDText, &launch),
		})
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list Agent Presence")
		return
	}
	writeJSON(w, http.StatusOK, AgentPresenceResponse{Items: items})
}
