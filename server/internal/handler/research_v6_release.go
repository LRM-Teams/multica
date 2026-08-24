package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/researchrun"
)

type researchV6ReleaseControl struct {
	WorkspaceID       string    `json:"workspace_id"`
	CreateEnabled     bool      `json:"create_enabled"`
	MaintenanceReason string    `json:"maintenance_reason"`
	PausedRunCount    int       `json:"paused_run_count"`
	UpdatedAt         time.Time `json:"updated_at"`
}

func (h *Handler) researchV6CreateAllowed(ctx context.Context, workspaceID string) bool {
	if !researchV6UserCreateEnabled(h.cfg) {
		return false
	}
	control, err := h.loadV6ReleaseControl(ctx, workspaceID)
	if err != nil {
		return researchV6UserCreateEnabled(h.cfg)
	}
	return control.CreateEnabled
}

func (h *Handler) loadV6ReleaseControl(ctx context.Context, workspaceID string) (researchV6ReleaseControl, error) {
	control := researchV6ReleaseControl{WorkspaceID: workspaceID, CreateEnabled: true}
	if h.DB == nil {
		return control, nil
	}
	err := h.DB.QueryRow(ctx, `
		SELECT workspace_id::text, create_enabled, maintenance_reason, paused_run_count, updated_at
		FROM research_v6_release_control WHERE workspace_id = $1::uuid
	`, workspaceID).Scan(&control.WorkspaceID, &control.CreateEnabled, &control.MaintenanceReason, &control.PausedRunCount, &control.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return researchV6ReleaseControl{WorkspaceID: workspaceID, CreateEnabled: true}, nil
	}
	return control, err
}

func (h *Handler) GetResearchV6Release(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id"); !ok {
		return
	}
	control, err := h.loadV6ReleaseControl(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load V6 release control")
		return
	}
	writeJSON(w, http.StatusOK, control)
}

type patchResearchV6ReleaseRequest struct {
	CreateEnabled     *bool  `json:"create_enabled"`
	MaintenanceReason string `json:"maintenance_reason"`
}

func (h *Handler) PatchResearchV6Release(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id"); !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if !h.isWorkspaceOwnerOrAdmin(r.Context(), workspaceID, parseUUID(userID)) {
		writeError(w, http.StatusForbidden, "only workspace owners or admins can change V6 release control")
		return
	}
	var req patchResearchV6ReleaseRequest
	if !decodeResearchJSON(w, r, &req) {
		return
	}
	if req.CreateEnabled == nil {
		writeError(w, http.StatusBadRequest, "create_enabled is required")
		return
	}
	reason := strings.TrimSpace(req.MaintenanceReason)
	if !*req.CreateEnabled && reason == "" {
		writeError(w, http.StatusBadRequest, "maintenance_reason is required when disabling V6 create")
		return
	}
	if len(reason) > 1024 {
		writeError(w, http.StatusBadRequest, "maintenance_reason is too long")
		return
	}
	paused := 0
	if !*req.CreateEnabled {
		err := h.DB.QueryRow(r.Context(), `
			WITH paused AS (
			  UPDATE research_session
			  SET status = 'paused', updated_at = now()
			  WHERE workspace_id = $1::uuid
			    AND orchestrator_version = $2
			    AND status IN ('drafting','running','awaiting_user_confirm','awaiting_director')
			  RETURNING 1
			)
			SELECT count(*)::int FROM paused
		`, workspaceID, researchrun.OrchestratorVersionV6).Scan(&paused)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to pause existing V6 runs")
			return
		}
	}
	_, err := h.DB.Exec(r.Context(), `
		INSERT INTO research_v6_release_control (workspace_id, create_enabled, maintenance_reason, paused_run_count, updated_by, updated_at)
		VALUES ($1::uuid, $2, $3, $4, $5::uuid, now())
		ON CONFLICT (workspace_id) DO UPDATE SET
		  create_enabled = EXCLUDED.create_enabled,
		  maintenance_reason = EXCLUDED.maintenance_reason,
		  paused_run_count = EXCLUDED.paused_run_count,
		  updated_by = EXCLUDED.updated_by,
		  updated_at = now()
	`, workspaceID, *req.CreateEnabled, reason, paused, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to persist V6 release control")
		return
	}
	control, err := h.loadV6ReleaseControl(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load V6 release control")
		return
	}
	writeJSON(w, http.StatusOK, control)
}
