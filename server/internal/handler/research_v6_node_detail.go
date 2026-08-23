package handler

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/researchrun"
)

func (h *Handler) GetResearchV6ProjectionNodeDetail(w http.ResponseWriter, r *http.Request) {
	service, ok := h.ResearchRun.(researchrun.V6ProjectionReader)
	if !ok {
		writeRonaldoV6Error(w, http.StatusServiceUnavailable, "research.v6.capability_unavailable", "research V6 projection unavailable", true)
		return
	}
	runID, valid := parseUUIDOrBadRequest(w, strings.TrimSpace(chi.URLParam(r, "runId")), "runId")
	if !valid {
		return
	}
	nodeID, err := url.PathUnescape(strings.TrimSpace(chi.URLParam(r, "nodeId")))
	if err != nil || !researchrun.IsValidV6ProjectionNodeID(nodeID) {
		writeRonaldoV6Error(w, http.StatusBadRequest, "research.v6.invalid_contract", "nodeId must match the frozen V6 projection key contract", false)
		return
	}
	snapshotID, valid := parseUUIDOrBadRequest(w, strings.TrimSpace(r.URL.Query().Get("snapshot_id")), "snapshot_id")
	if !valid {
		return
	}
	detail, err := service.ProjectionV6NodeDetail(
		r.Context(),
		h.resolveWorkspaceID(r),
		uuidToString(runID),
		uuidToString(snapshotID),
		nodeID,
		strings.TrimSpace(r.URL.Query().Get("view")),
	)
	if errors.Is(err, pgx.ErrNoRows) {
		writeRonaldoV6Error(w, http.StatusNotFound, "research.v6.not_found", "research V6 projection node not found", false)
		return
	}
	if errors.Is(err, researchrun.ErrInvalidContract) {
		writeRonaldoV6Error(w, http.StatusBadRequest, "research.v6.invalid_contract", "view must be brief, full, or history", false)
		return
	}
	if err != nil {
		writeResearchV6Error(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}
