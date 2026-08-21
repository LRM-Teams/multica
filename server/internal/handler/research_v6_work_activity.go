package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/researchrun"
)

// GetResearchV6WorkActivity exposes the presentation-safe execution identity
// and Attempt time window needed to attach Runner Activity to a Work S node.
func (h *Handler) GetResearchV6WorkActivity(w http.ResponseWriter, r *http.Request) {
	service, available := h.ResearchRun.(researchrun.V6WorkActivityReader)
	if !available {
		writeRonaldoV6Error(w, http.StatusServiceUnavailable, "research.v6.capability_unavailable", "research V6 work activity unavailable", true)
		return
	}
	runID, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(chi.URLParam(r, "runId")), "runId")
	if !ok {
		return
	}
	workItemID, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(chi.URLParam(r, "workItemId")), "workItemId")
	if !ok {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	response, err := service.ProjectionV6WorkActivity(
		r.Context(),
		workspaceID,
		uuidToString(runID),
		uuidToString(workItemID),
	)
	if errors.Is(err, pgx.ErrNoRows) {
		writeRonaldoV6Error(w, http.StatusNotFound, "research.v6.not_found", "research V6 work item not found", false)
		return
	}
	if err != nil {
		writeResearchV6Error(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}
