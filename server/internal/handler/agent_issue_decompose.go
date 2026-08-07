package handler

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/workgraph"
)

// DecomposeAgentIssue creates a bounded platform-level subagent plan. Each
// child is an ordinary Issue with its own task/session lifecycle; no Goal or
// executable Work Graph is created.
func (h *Handler) DecomposeAgentIssue(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	if h.WorkGraph == nil {
		writeError(w, http.StatusServiceUnavailable, "issue decomposition unavailable")
		return
	}
	var in workgraph.DecomposeInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	in.WorkspaceID = p.WorkspaceID
	in.ActorAgentID = p.AgentID
	in.ParentIssueID = chi.URLParam(r, "id")
	if key := r.Header.Get("Idempotency-Key"); key != "" {
		in.IdempotencyKey = key
	}
	result, err := h.WorkGraph.DecomposeIssue(r.Context(), in)
	if err != nil {
		writeWorkGraphError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}
