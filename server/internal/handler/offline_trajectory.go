package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
)

// OfflineTrajectoriesResolveRequest is the body of
// POST /api/v1/env-dispatch/runs/{run_id}/offline-trajectories:resolve.
type OfflineTrajectoriesResolveRequest struct {
	SnapshotID string   `json:"snapshot_id"`
	CallIDs    []string `json:"call_ids"`
}

// ResolveOfflineTrajectories streams one NDJSON result per deduplicated
// requested call ID. Request-level authorization and snapshot binding failures
// return HTTP errors; membership/mode/eligibility/normalization are per-call.
func (h *Handler) ResolveOfflineTrajectories(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace ID required")
		return
	}
	runID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "runID"), "runID")
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspaceID")
	if !ok {
		return
	}

	var req OfflineTrajectoriesResolveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "decode offline trajectory resolve: "+err.Error())
		return
	}
	if err := validateOfflineTrajectoriesResolveRequest(req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	resolver := service.NewOfflineTrajectoryService(h.Queries)
	lines, err := resolver.Resolve(r.Context(), service.OfflineResolveRequest{
		RunID:       runID,
		WorkspaceID: wsUUID,
		SnapshotID:  req.SnapshotID,
		CallIDs:     req.CallIDs,
	})
	if err != nil {
		writeOfflineResolveError(w, err)
		return
	}
	if err := writeOfflineTrajectoryNDJSON(w, lines); err != nil {
		// Headers may already be committed; best-effort log via write is not
		// possible. The stream ends without a trailing HTTP error body.
		return
	}
}

func validateOfflineTrajectoriesResolveRequest(req OfflineTrajectoriesResolveRequest) error {
	if req.SnapshotID == "" {
		return errors.New("snapshot_id is required")
	}
	if req.CallIDs == nil {
		return errors.New("call_ids is required")
	}
	return nil
}

func writeOfflineResolveError(w http.ResponseWriter, err error) {
	var resolveErr *service.OfflineResolveError
	if errors.As(err, &resolveErr) {
		status := resolveErr.Status
		if status == 0 {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]any{"error": resolveErr.Code, "message": resolveErr.Message})
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
		return
	}
	writeError(w, http.StatusServiceUnavailable, "resolve offline trajectories: "+err.Error())
}

// writeOfflineTrajectoryNDJSON emits application/x-ndjson with one JSON object
// per line. It never includes raw provider payloads or credentials.
func writeOfflineTrajectoryNDJSON(w http.ResponseWriter, lines []service.OfflineResolveLine) error {
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	encoder := json.NewEncoder(w)
	for _, line := range lines {
		if err := encoder.Encode(sanitizeOfflineResolveLine(line)); err != nil {
			return err
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}
	return nil
}

// sanitizeOfflineResolveLine is a defensive copy that keeps only the public
// exporter contract fields.
func sanitizeOfflineResolveLine(line service.OfflineResolveLine) service.OfflineResolveLine {
	out := service.OfflineResolveLine{
		CallID: line.CallID,
		Status: line.Status,
		Reason: line.Reason,
	}
	if len(line.Details) > 0 {
		out.Details = line.Details
	}
	if line.Trajectory != nil {
		copied := *line.Trajectory
		out.Trajectory = &copied
	}
	return out
}

// offlineResolveAuthorizedRun checks workspace ownership for a loaded run.
// Extracted for unit tests that avoid a live Queries dependency.
func offlineResolveAuthorizedRun(runWorkspaceID, requestWorkspaceID pgtype.UUID) bool {
	return runWorkspaceID.Valid && requestWorkspaceID.Valid && runWorkspaceID == requestWorkspaceID
}

// offlineResolveSnapshotMatches reports whether the request binds the frozen
// snapshot identity exactly.
func offlineResolveSnapshotMatches(frozenSnapshotID, requestSnapshotID string) bool {
	return frozenSnapshotID != "" && frozenSnapshotID == requestSnapshotID
}
