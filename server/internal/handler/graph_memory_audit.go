package handler

import (
	"net/http"
)

// GetGraphMemoryAudit serves GET /api/workspaces/{id}/graph-memory/audit
// (spec §10): 24h recall volume/hit-rate/rounds, judge write-back coverage,
// and regression-set size across the workspace's physical graphs.
func (h *Handler) GetGraphMemoryAudit(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	sum, err := h.GraphMemoryAudit.Summary(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load graph memory audit")
		return
	}
	writeJSON(w, http.StatusOK, sum)
}
