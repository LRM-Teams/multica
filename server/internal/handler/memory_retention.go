package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
)

type memoryRetentionCapsResponse struct {
	TrajectoryHotDays int `json:"trajectory_hot_days"`
	ArchiveDays       int `json:"archive_days"`
	TraceHotDays      int `json:"trace_hot_days"`
	// DiagnosticThinkingDays is a hard platform ceiling (spec §12.2):
	// diagnostic provider thinking never outlives it.
	DiagnosticThinkingDays int `json:"diagnostic_thinking_days"`
}

type memoryRetentionResponse struct {
	Policy service.MemoryRetentionPolicy `json:"policy"`
	Caps   memoryRetentionCapsResponse   `json:"caps"`
}

type memoryRetentionUpdateRequest struct {
	TrajectoryHotDays int `json:"trajectory_hot_days"`
	ArchiveDays       int `json:"archive_days"`
	TraceHotDays      int `json:"trace_hot_days"`
	// DiagnosticThinkingDays must be sent explicitly (1..30): an update
	// that omits it is rejected rather than silently binding the ceiling.
	DiagnosticThinkingDays int   `json:"diagnostic_thinking_days"`
	ExpectedVersion        int64 `json:"expected_version"`
}

// GetMemoryRetention serves GET /api/workspaces/{id}/memory/retention
// (owner/admin only): the workspace's active versioned policy plus the
// platform caps it must stay within.
func (h *Handler) GetMemoryRetention(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.retainWorkspaceScope(w, r)
	if !ok {
		return
	}
	if h.MemoryRetention == nil {
		writeError(w, http.StatusServiceUnavailable, "memory retention is not configured")
		return
	}
	policy, err := h.MemoryRetention.CurrentPolicy(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load memory retention policy")
		return
	}
	writeJSON(w, http.StatusOK, memoryRetentionResponse{
		Policy: policy,
		Caps: memoryRetentionCapsResponse{
			TrajectoryHotDays:      service.MemoryRetentionTrajectoryHotCapDays,
			ArchiveDays:            service.MemoryRetentionArchiveCapDays,
			TraceHotDays:           service.MemoryRetentionTraceHotCapDays,
			DiagnosticThinkingDays: service.MemoryRetentionThinkingCapDays,
		},
	})
}

// UpdateMemoryRetention serves PUT /api/workspaces/{id}/memory/retention
// (owner/admin only): a CAS version append. Values above the platform caps
// are 422; a stale expected version is 409.
func (h *Handler) UpdateMemoryRetention(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.retainWorkspaceScope(w, r)
	if !ok {
		return
	}
	if h.MemoryRetention == nil {
		writeError(w, http.StatusServiceUnavailable, "memory retention is not configured")
		return
	}
	var req memoryRetentionUpdateRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	actor := "user:" + userID
	policy, err := h.MemoryRetention.UpdatePolicy(r.Context(), workspaceID, service.MemoryRetentionUpdate{
		TrajectoryHotDays:      req.TrajectoryHotDays,
		ArchiveDays:            req.ArchiveDays,
		TraceHotDays:           req.TraceHotDays,
		DiagnosticThinkingDays: req.DiagnosticThinkingDays,
		ExpectedVersion:        req.ExpectedVersion,
	}, actor)
	switch {
	case err == nil:
	case errors.Is(err, service.ErrMemoryRetentionVersion):
		writeError(w, http.StatusConflict, "retention policy version conflict")
		return
	case errors.Is(err, service.ErrMemoryRetentionCap), errors.Is(err, service.ErrMemoryRetentionDaysGlobal):
		writeError(w, http.StatusUnprocessableEntity, "retention policy exceeds platform caps")
		return
	default:
		writeError(w, http.StatusInternalServerError, "failed to update memory retention policy")
		return
	}
	writeJSON(w, http.StatusOK, memoryRetentionResponse{
		Policy: policy,
		Caps: memoryRetentionCapsResponse{
			TrajectoryHotDays:      service.MemoryRetentionTrajectoryHotCapDays,
			ArchiveDays:            service.MemoryRetentionArchiveCapDays,
			TraceHotDays:           service.MemoryRetentionTraceHotCapDays,
			DiagnosticThinkingDays: service.MemoryRetentionThinkingCapDays,
		},
	})
}

// retainWorkspaceScope resolves the workspace, requires an authenticated
// owner/admin principal, and answers false after writing the error.
func (h *Handler) retainWorkspaceScope(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	workspaceID := workspaceIDFromURL(r, "id")
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return pgtype.UUID{}, false
	}
	if !h.isWorkspaceOwnerOrAdmin(r.Context(), workspaceID, member.UserID) {
		writeError(w, http.StatusForbidden, "workspace owner or admin required")
		return pgtype.UUID{}, false
	}
	return parseUUID(workspaceID), true
}
