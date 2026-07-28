package handler

import (
	"net/http"
)

// GetAgentWorkspace — GET /api/agent/workspace
// Returns the workspace bound to the AgentPrincipal (token stamp).
func (h *Handler) GetAgentWorkspace(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	wsUUID, ok := p.WorkspaceUUID()
	if !ok {
		writeError(w, http.StatusForbidden, "access denied")
		return
	}
	ws, err := h.Queries.GetWorkspace(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	writeJSON(w, http.StatusOK, workspaceToResponse(ws))
}

// GetAgentWorkspaceByID — GET /api/agent/workspaces/{id}
// Only the principal's bound workspace is visible.
func (h *Handler) GetAgentWorkspaceByID(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	id := workspaceIDFromURL(r, "id")
	if id != "" && id != p.WorkspaceID {
		writeError(w, http.StatusForbidden, "access denied")
		return
	}
	h.GetAgentWorkspace(w, r)
}
