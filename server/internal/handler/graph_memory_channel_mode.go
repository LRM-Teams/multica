package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/multica-ai/multica/server/internal/service"
)

type graphMemoryChannelModeResponse struct {
	WorkspaceID   string `json:"workspace_id"`
	ChannelID     string `json:"channel_id"`
	Override      string `json:"override"`
	EffectiveMode string `json:"effective_mode"`
	Status        string `json:"status"`
	BlockedReason string `json:"blocked_reason,omitempty"`
	AgentID       string `json:"agent_id,omitempty"`
	RuntimeID     string `json:"runtime_id,omitempty"`
}

type updateGraphMemoryChannelModeRequest struct {
	Override string `json:"override"`
}

func validGraphMemoryChannelOverride(value string) bool {
	return value == "inherit" || value == "inject" || value == "agent"
}

func (h *Handler) GetGraphMemoryChannelMode(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	var response graphMemoryChannelModeResponse
	var memoryType, workspaceMode string
	err := h.DB.QueryRow(r.Context(), `
		SELECT c.workspace_id::text,c.id::text,c.graph_memory_mode_override,p.memory_type,p.graph_memory_mode,
		       COALESCE(a.status,'inactive'),COALESCE(a.blocked_reason,''),COALESCE(a.agent_id::text,''),COALESCE(a.runtime_id::text,'')
		FROM channel c JOIN graph_memory_profile p ON p.workspace_id=c.workspace_id
		LEFT JOIN graph_memory_channel_agent a ON a.channel_id=c.id
		WHERE c.id=$1 AND c.workspace_id=$2`, channelID, parseUUID(workspaceID)).Scan(
		&response.WorkspaceID, &response.ChannelID, &response.Override, &memoryType, &workspaceMode,
		&response.Status, &response.BlockedReason, &response.AgentID, &response.RuntimeID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "channel or graph memory profile not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load channel graph memory mode")
		return
	}
	response.EffectiveMode = service.EffectiveGraphMemoryMode(memoryType, workspaceMode, response.Override)
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) UpdateGraphMemoryChannelMode(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin"); !ok {
		return
	}
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	var req updateGraphMemoryChannelModeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Override = strings.ToLower(strings.TrimSpace(req.Override))
	if !validGraphMemoryChannelOverride(req.Override) {
		writeError(w, http.StatusBadRequest, "override must be 'inherit', 'inject', or 'agent'")
		return
	}
	tag, err := h.DB.Exec(r.Context(), `UPDATE channel SET graph_memory_mode_override=$3,updated_at=now() WHERE id=$1 AND workspace_id=$2 AND kind='group'`, channelID, parseUUID(workspaceID), req.Override)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update channel graph memory mode")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "group channel not found")
		return
	}
	if h.GraphMemoryAgentControl == nil {
		writeError(w, http.StatusServiceUnavailable, "graph memory agent control plane is unavailable")
		return
	}
	status, err := h.GraphMemoryAgentControl.ReconcileChannel(r.Context(), workspaceID, uuidToString(channelID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reconcile channel graph memory mode")
		return
	}
	writeJSON(w, http.StatusOK, graphMemoryChannelModeResponse{
		WorkspaceID: workspaceID, ChannelID: status.ChannelID, Override: req.Override,
		EffectiveMode: status.EffectiveMode, Status: status.Status, BlockedReason: status.BlockedReason,
		AgentID: status.AgentID, RuntimeID: status.RuntimeID,
	})
}

func (h *Handler) ResetGraphMemoryChannelAgent(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin"); !ok {
		return
	}
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if h.GraphMemoryAgentControl == nil {
		writeError(w, http.StatusServiceUnavailable, "graph memory agent control plane is unavailable")
		return
	}
	if err := h.GraphMemoryAgentControl.ResetState(r.Context(), workspaceID, uuidToString(channelID)); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
}

func (h *Handler) reconcileGraphMemoryWorkspaceChannels(ctx context.Context, workspaceID string) {
	if h.GraphMemoryAgentControl == nil {
		return
	}
	rows, err := h.DB.Query(ctx, `SELECT id::text FROM channel WHERE workspace_id=$1::uuid AND kind='group' AND system_key IS NULL`, workspaceID)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var channelID string
		if rows.Scan(&channelID) == nil {
			_, _ = h.GraphMemoryAgentControl.ReconcileChannel(ctx, workspaceID, channelID)
		}
	}
}
