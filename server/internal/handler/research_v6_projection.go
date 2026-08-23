package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/researchrun"
)

func (h *Handler) GetResearchV6ProjectionSnapshot(w http.ResponseWriter, r *http.Request) {
	service, ok := h.ResearchRun.(researchrun.V6ProjectionReader)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "research run engine is unavailable")
		return
	}
	limit := 1000
	runID, valid := parseUUIDOrBadRequest(w, strings.TrimSpace(chi.URLParam(r, "runId")), "runId")
	if !valid {
		return
	}
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil {
			writeError(w, http.StatusBadRequest, "snapshot limit must be an integer")
			return
		}
		limit = parsed
	}
	snapshot, err := service.ProjectionV6Snapshot(r.Context(), researchrun.V6ProjectionPageRequest{
		WorkspaceID: h.resolveWorkspaceID(r),
		RunID:       uuidToString(runID),
		Cursor:      strings.TrimSpace(r.URL.Query().Get("cursor")),
		Limit:       limit,
	})
	if errors.Is(err, researchrun.ErrProjectionResyncRequired) {
		writeError(w, http.StatusConflict, "projection snapshot expired; resync required")
		return
	}
	if errors.Is(err, researchrun.ErrInvalidContract) {
		writeError(w, http.StatusBadRequest, "invalid projection cursor or limit")
		return
	}
	if err != nil {
		writeResearchV6Error(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (h *Handler) GetResearchV6ProjectionDeltas(w http.ResponseWriter, r *http.Request) {
	service, ok := h.ResearchRun.(researchrun.V6ProjectionReader)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "research V6 projection unavailable")
		return
	}
	if strings.TrimSpace(r.URL.Query().Get("cursor")) != "" {
		writeError(w, http.StatusBadRequest, "projection delta cursor is not valid for this bounded page")
		return
	}
	runID, valid := parseUUIDOrBadRequest(w, strings.TrimSpace(chi.URLParam(r, "runId")), "runId")
	if !valid {
		return
	}
	after, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("after")), 10, 64)
	if err != nil || after < 0 {
		writeError(w, http.StatusBadRequest, "after must be a non-negative integer")
		return
	}
	page, err := service.ProjectionV6Deltas(r.Context(), researchrun.V6ProjectionDeltaRequest{
		WorkspaceID: h.resolveWorkspaceID(r),
		RunID:       uuidToString(runID),
		After:       after,
	})
	if err != nil {
		writeResearchV6Error(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) PostResearchV6ProjectionResume(w http.ResponseWriter, r *http.Request) {
	var request struct {
		SnapshotID            string `json:"snapshot_id,omitempty"`
		LastConfirmedSequence int64  `json:"last_confirmed_sequence"`
		ProjectionHash        string `json:"projection_hash"`
	}
	if !decodeResearchJSON(w, r, &request) {
		return
	}
	if request.LastConfirmedSequence < 0 || request.SnapshotID == "" || request.ProjectionHash == "" {
		writeError(w, http.StatusBadRequest, "snapshot_id, projection_hash and a non-negative last_confirmed_sequence are required")
		return
	}
	service, ok := h.ResearchRun.(researchrun.V6ProjectionReader)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "research V6 projection unavailable")
		return
	}
	runID, valid := parseUUIDOrBadRequest(w, strings.TrimSpace(chi.URLParam(r, "runId")), "runId")
	if !valid {
		return
	}
	page, err := service.ProjectionV6Deltas(r.Context(), researchrun.V6ProjectionDeltaRequest{
		WorkspaceID:    h.resolveWorkspaceID(r),
		RunID:          uuidToString(runID),
		SnapshotID:     request.SnapshotID,
		ProjectionHash: request.ProjectionHash,
		After:          request.LastConfirmedSequence,
	})
	if err != nil {
		writeResearchV6Error(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func writeResearchV6Error(w http.ResponseWriter, err error) {
	if errors.Is(err, researchrun.ErrRunNotFound) || errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "research V6 run not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "failed to load research V6 projection")
}
