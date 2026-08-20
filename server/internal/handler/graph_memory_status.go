package handler

import (
	"net/http"
)

// GetGraphMemoryStatus serves GET /api/workspaces/{id}/graph-memory/status
// (spec §10): staging depth, versions/current pointer, last consolidation,
// and 24h recall hit rate per physical graph.
func (h *Handler) GetGraphMemoryStatus(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	st, err := h.GraphMemoryStatus.Status(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load graph memory status")
		return
	}
	writeJSON(w, http.StatusOK, st)
}
