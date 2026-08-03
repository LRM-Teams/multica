package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (h *Handler) hasDurableResearchRun(ctx context.Context, workspaceID, sessionID pgtype.UUID) (bool, error) {
	var initialized bool
	err := h.DB.QueryRow(ctx, `
		SELECT run_initialized_at IS NOT NULL
		FROM research_session
		WHERE id = $1 AND workspace_id = $2
	`, sessionID, workspaceID).Scan(&initialized)
	return initialized, err
}

// rejectLegacyResearchMutation prevents legacy agent endpoints from mutating
// authoritative state after the durable Research Run engine takes ownership.
func (h *Handler) rejectLegacyResearchMutation(w http.ResponseWriter, r *http.Request, workspaceID, sessionID pgtype.UUID) bool {
	initialized, err := h.hasDurableResearchRun(r.Context(), workspaceID, sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "research session not found")
		return true
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to inspect research run ownership")
		return true
	}
	if !initialized {
		return false
	}
	writeError(w, http.StatusConflict, "durable research runs accept authoritative changes only through the assigned task-result endpoint")
	return true
}
