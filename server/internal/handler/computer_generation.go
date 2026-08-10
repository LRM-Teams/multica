package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/middleware"
)

var errStaleComputerGeneration = errors.New("stale Computer generation")

func (h *Handler) claimComputerGeneration(ctx context.Context, daemonID string, generation int64) error {
	if h == nil || h.DB == nil || strings.TrimSpace(daemonID) == "" || generation < 1 {
		return errStaleComputerGeneration
	}
	var accepted int64
	err := h.DB.QueryRow(ctx, `
INSERT INTO computer_generation (daemon_id, generation, updated_at)
VALUES ($1, $2, now())
ON CONFLICT (daemon_id) DO UPDATE
SET generation = EXCLUDED.generation, updated_at = now()
WHERE computer_generation.generation <= EXCLUDED.generation
RETURNING generation`, strings.TrimSpace(daemonID), generation).Scan(&accepted)
	if errors.Is(err, pgx.ErrNoRows) || accepted != generation {
		return errStaleComputerGeneration
	}
	return err
}

func (h *Handler) requireCurrentComputerGeneration(w http.ResponseWriter, r *http.Request, daemonID string) bool {
	if h == nil || h.DB == nil || strings.TrimSpace(daemonID) == "" {
		return true
	}
	got, parseErr := strconv.ParseInt(strings.TrimSpace(r.Header.Get("X-Computer-Generation")), 10, 64)
	err := h.checkCurrentComputerGeneration(r.Context(), daemonID, got, parseErr == nil)
	if err == nil {
		return true // compatibility until the first generation-aware resident
	}
	if !errors.Is(err, errStaleComputerGeneration) {
		writeError(w, http.StatusInternalServerError, "failed to verify Computer generation")
		return false
	}
	writeCodedError(w, http.StatusConflict, "stale_computer_generation", err.Error())
	return false
}

func (h *Handler) checkCurrentComputerGeneration(ctx context.Context, daemonID string, got int64, provided bool) error {
	var current int64
	err := h.DB.QueryRow(ctx, `SELECT generation FROM computer_generation WHERE daemon_id=$1`, strings.TrimSpace(daemonID)).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !provided || got != current {
		return fmt.Errorf("%w: Computer generation %d is fenced by generation %d", errStaleComputerGeneration, got, current)
	}
	return nil
}

type computerHeartbeatRequest struct {
	DaemonID    string `json:"daemon_id"`
	WorkspaceID string `json:"workspace_id"`
	Generation  int64  `json:"generation"`
}

// ComputerHeartbeat is independent of Agent runtimes, so a zero-Agent
// Workspace still has truthful connectivity. Claiming the monotonic generation
// fences every older resident before this heartbeat is published.
func (h *Handler) ComputerHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req computerHeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.DaemonID = strings.TrimSpace(req.DaemonID)
	req.WorkspaceID = strings.TrimSpace(req.WorkspaceID)
	if req.DaemonID == "" || req.WorkspaceID == "" || req.Generation < 1 {
		writeError(w, http.StatusBadRequest, "daemon_id, workspace_id, and generation are required")
		return
	}
	workspaceUUID, ok := parseUUIDOrBadRequest(w, req.WorkspaceID, "workspace_id")
	if !ok {
		return
	}
	req.WorkspaceID = uuidToString(workspaceUUID)
	if tokenWorkspace := middleware.DaemonWorkspaceIDFromContext(r.Context()); tokenWorkspace != "" {
		if tokenWorkspace != req.WorkspaceID || !strings.EqualFold(middleware.DaemonIDFromContext(r.Context()), req.DaemonID) {
			writeError(w, http.StatusForbidden, "Computer credential scope mismatch")
			return
		}
	} else if _, ok := h.requireWorkspaceMember(w, r, req.WorkspaceID, "workspace not found"); !ok {
		return
	}
	if err := h.claimComputerGeneration(r.Context(), req.DaemonID, req.Generation); err != nil {
		writeCodedError(w, http.StatusConflict, "stale_computer_generation", err.Error())
		return
	}
	_, err := h.DB.Exec(r.Context(), `
INSERT INTO daemon_heartbeat (workspace_id, daemon_id, computer_generation, last_seen_at, updated_at)
VALUES ($1, $2, $3, now(), now())
ON CONFLICT (workspace_id, daemon_id) DO UPDATE
SET computer_generation=EXCLUDED.computer_generation, last_seen_at=now(), updated_at=now()
WHERE daemon_heartbeat.computer_generation <= EXCLUDED.computer_generation`, workspaceUUID, req.DaemonID, req.Generation)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record Computer heartbeat")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "generation": req.Generation})
}
