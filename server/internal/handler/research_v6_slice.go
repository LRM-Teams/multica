package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/researchrun"
)

func (h *Handler) GetResearchV6ProjectionSlice(w http.ResponseWriter, r *http.Request) {
	service, ok := h.ResearchRun.(researchrun.V6ProjectionReader)
	if !ok {
		writeRonaldoV6Error(w, http.StatusServiceUnavailable, "research.v6.capability_unavailable", "research V6 projection unavailable", true)
		return
	}
	depth, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("depth")))
	if err != nil || depth != 1 {
		writeRonaldoV6Error(w, http.StatusBadRequest, "research.v6.invalid_contract", "depth must be 1", false)
		return
	}
	runID, valid := parseUUIDOrBadRequest(w, strings.TrimSpace(chi.URLParam(r, "runId")), "runId")
	if !valid {
		return
	}
	page, err := service.ProjectionV6Slice(r.Context(), researchrun.V6ProjectionSliceRequest{
		WorkspaceID: h.resolveWorkspaceID(r),
		RunID:       uuidToString(runID),
		SnapshotID:  strings.TrimSpace(r.URL.Query().Get("snapshot_id")),
		RootNodeID:  strings.TrimSpace(r.URL.Query().Get("root")),
		Cursor:      strings.TrimSpace(r.URL.Query().Get("cursor")),
		Depth:       depth,
		Limit:       1000,
	})
	if errors.Is(err, researchrun.ErrProjectionResyncRequired) {
		writeRonaldoV6Error(w, http.StatusConflict, "research.v6.projection_resync_required", "projection snapshot expired; resync required", true)
		return
	}
	if errors.Is(err, researchrun.ErrInvalidContract) {
		writeRonaldoV6Error(w, http.StatusBadRequest, "research.v6.invalid_contract", "invalid projection slice request", false)
		return
	}
	if err != nil {
		writeResearchV6Error(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}
