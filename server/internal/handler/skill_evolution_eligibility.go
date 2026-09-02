package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	db "github.com/multica-ai/multica/server/pkg/db/generated"

	"github.com/multica-ai/multica/server/internal/skillevolution"
)

// Skill trajectory eligibility admin API (plan Phase 3 wrap-up,
// migration 496): owner/admin reads and the revoke-only mutation. The
// revoke reason is an audit floor — the one fact the eligibility ledger
// cannot reconstruct later — so it is mandatory, and the actor is the
// authenticated principal, never a request-supplied string.

type skillEligibilityResponse struct {
	RunID             string    `json:"run_id"`
	EvolutionEligible bool      `json:"evolution_eligible"`
	RunKind           string    `json:"run_kind"`
	TaskType          string    `json:"task_type"`
	FixedAt           time.Time `json:"fixed_at"`
	FixedByActor      string    `json:"fixed_by_actor"`
	RevokedByActor    string    `json:"revoked_by_actor,omitempty"`
	RevokedAt         time.Time `json:"revoked_at,omitempty"`
	RevokedReason     string    `json:"revoked_reason,omitempty"`
}

type skillEligibilityRevokeRequest struct {
	Reason string `json:"reason"`
}

func skillEligibilityFromRecord(record skillevolution.TrajectoryEligibility) skillEligibilityResponse {
	return skillEligibilityResponse{
		RunID: record.RunID, EvolutionEligible: record.EvolutionEligible,
		RunKind: record.RunKind, TaskType: record.TaskType,
		FixedAt: record.FixedAt, FixedByActor: record.FixedByActor,
		RevokedByActor: record.RevokedByActor, RevokedAt: record.RevokedAt,
		RevokedReason: record.RevokedReason,
	}
}

// skillEligibilityScope resolves the workspace, requires an
// owner/admin principal, and returns the acting member for the audit
// actor.
func (h *Handler) skillEligibilityScope(w http.ResponseWriter, r *http.Request) (db.Member, bool) {
	workspaceID := workspaceIDFromURL(r, "id")
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return db.Member{}, false
	}
	if !h.isWorkspaceOwnerOrAdmin(r.Context(), workspaceID, member.UserID) {
		writeError(w, http.StatusForbidden, "workspace owner or admin required")
		return db.Member{}, false
	}
	return member, true
}

// GetSkillTrajectoryEligibility serves
// GET /api/workspaces/{id}/skill-evolution/eligibility/{runId}
// (owner/admin only): the run's pinned eligibility with its revocation
// state, if any.
func (h *Handler) GetSkillTrajectoryEligibility(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.skillEligibilityScope(w, r); !ok {
		return
	}
	if h.SkillEvolutionLedger == nil {
		writeError(w, http.StatusServiceUnavailable, "skill evolution ledger is not configured")
		return
	}
	record, err := h.SkillEvolutionLedger.GetEligibility(r.Context(), workspaceIDFromURL(r, "id"), chi.URLParam(r, "runId"))
	if err != nil {
		if errors.Is(err, skillevolution.ErrLedgerNotFound) {
			writeError(w, http.StatusNotFound, "no eligibility pin for that run")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load trajectory eligibility")
		return
	}
	writeJSON(w, http.StatusOK, skillEligibilityFromRecord(record))
}

// RevokeSkillTrajectoryEligibility serves
// POST /api/workspaces/{id}/skill-evolution/eligibility/{runId}/revoke
// (owner/admin only): withdraws a run's evolution eligibility with a
// mandatory audit reason. Revocation is revoke-only and idempotent: a
// replay against an already-revoked run returns the recorded revocation,
// never a second one.
func (h *Handler) RevokeSkillTrajectoryEligibility(w http.ResponseWriter, r *http.Request) {
	member, ok := h.skillEligibilityScope(w, r)
	if !ok {
		return
	}
	if h.SkillEvolutionLedger == nil {
		writeError(w, http.StatusServiceUnavailable, "skill evolution ledger is not configured")
		return
	}
	var req skillEligibilityRevokeRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		writeError(w, http.StatusUnprocessableEntity, "reason is required")
		return
	}
	if len(reason) > 2048 {
		writeError(w, http.StatusUnprocessableEntity, "reason must be at most 2048 characters")
		return
	}
	workspaceID := workspaceIDFromURL(r, "id")
	runID := chi.URLParam(r, "runId")
	actor := "user:" + member.UserID.String()
	if err := h.SkillEvolutionLedger.RevokeEligibility(r.Context(), workspaceID, runID, actor, reason, time.Now().UTC()); err != nil {
		switch {
		case errors.Is(err, skillevolution.ErrLedgerNotFound):
			writeError(w, http.StatusNotFound, "no eligibility pin for that run")
		case errors.Is(err, skillevolution.ErrLedgerConflict):
			// Idempotent replay: the recorded revocation is the answer.
			record, getErr := h.SkillEvolutionLedger.GetEligibility(r.Context(), workspaceID, runID)
			if getErr == nil && !record.RevokedAt.IsZero() {
				writeJSON(w, http.StatusOK, skillEligibilityFromRecord(record))
				return
			}
			writeError(w, http.StatusConflict, "eligibility could not be revoked")
		case errors.Is(err, skillevolution.ErrInvalidContract):
			writeError(w, http.StatusUnprocessableEntity, "invalid revocation")
		default:
			writeError(w, http.StatusInternalServerError, "failed to revoke trajectory eligibility")
		}
		return
	}
	record, err := h.SkillEvolutionLedger.GetEligibility(r.Context(), workspaceID, runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "revoked, but the resulting eligibility could not be loaded")
		return
	}
	writeJSON(w, http.StatusOK, skillEligibilityFromRecord(record))
}
