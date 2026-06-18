package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
)

// Channel ↔ project binding lives in its own file so it doesn't collide with
// the actively-developed channel.go. The binding is read at task claim time via
// COALESCE(channel.project_id, chat_session.project_id) (see ClaimTaskByRuntime),
// so setting it here makes every agent in the channel share the project's
// directory without touching individual chat sessions.

// GetChannelProject returns the channel's bound project id (empty when unbound).
func (h *Handler) GetChannelProject(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	var pid pgtype.UUID
	_ = h.DB.QueryRow(r.Context(), `SELECT project_id FROM channel WHERE id = $1`, channelID).Scan(&pid)
	out := ""
	if pid.Valid {
		out = uuidToString(pid)
	}
	writeJSON(w, http.StatusOK, map[string]string{"project_id": out})
}

type setChannelProjectRequest struct {
	// ProjectID: a uuid binds/switches; null or "" clears.
	ProjectID json.RawMessage `json:"project_id"`
}

// SetChannelProject binds, switches, or clears the channel's project.
func (h *Handler) SetChannelProject(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	var req setChannelProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var projectID pgtype.UUID // zero/invalid → clears the binding (NULL)
	if len(req.ProjectID) > 0 && string(req.ProjectID) != "null" {
		var raw string
		if err := json.Unmarshal(req.ProjectID, &raw); err != nil {
			writeError(w, http.StatusBadRequest, "invalid project_id")
			return
		}
		if raw = strings.TrimSpace(raw); raw != "" {
			pid, perr := util.ParseUUID(raw)
			if perr != nil {
				writeError(w, http.StatusBadRequest, "invalid project_id")
				return
			}
			proj, gerr := h.Queries.GetProject(r.Context(), pid)
			if gerr != nil || uuidToString(proj.WorkspaceID) != workspaceID {
				writeError(w, http.StatusBadRequest, "project not found")
				return
			}
			projectID = pid
		}
	}
	if _, err := h.DB.Exec(r.Context(),
		`UPDATE channel SET project_id = $2 WHERE id = $1`,
		channelID, projectID,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update channel project")
		return
	}
	out := ""
	if projectID.Valid {
		out = uuidToString(projectID)
	}
	writeJSON(w, http.StatusOK, map[string]string{"project_id": out})
}
