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
var errComputerConnectionUnauthorized = errors.New("Computer Workspace connection unauthorized")

// authorizeComputerConnectionRequest proves that this request may act for one
// explicit (Computer, Workspace) connection. Daemon credentials must match
// both immutable subjects; PAT/JWT compatibility must belong to the persisted
// Computer owner. Membership alone is insufficient because it would let any
// member claim an arbitrary daemon_id and fence another user's resident.
func (h *Handler) authorizeComputerConnectionRequest(ctx context.Context, r *http.Request, daemonID, workspaceID string) error {
	daemonID = strings.TrimSpace(daemonID)
	workspaceID = strings.TrimSpace(workspaceID)
	var exists bool
	if tokenWorkspace := middleware.DaemonWorkspaceIDFromContext(r.Context()); tokenWorkspace != "" {
		if tokenWorkspace != workspaceID || !strings.EqualFold(middleware.DaemonIDFromContext(r.Context()), daemonID) {
			return errComputerConnectionUnauthorized
		}
		if err := h.DB.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM computer_workspace_bindings
   WHERE daemon_id=$1 AND workspace_id=$2 AND active=TRUE
)`, daemonID, workspaceID).Scan(&exists); err != nil {
			return err
		}
	} else {
		userID := strings.TrimSpace(requestUserID(r))
		if userID == "" {
			return errComputerConnectionUnauthorized
		}
		if err := h.DB.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM computer_workspace_bindings
   WHERE daemon_id=$1 AND workspace_id=$2 AND user_id=$3 AND active=TRUE
)`, daemonID, workspaceID, userID).Scan(&exists); err != nil {
			return err
		}
	}
	if !exists {
		return errComputerConnectionUnauthorized
	}
	return nil
}

// authorizeComputerOwnerRequest proves machine-wide ownership for operations
// such as full-set successor attestation. A daemon token proves one active
// connection; the database's immutable daemon_id owner fence then covers its
// sibling connections. PAT/JWT requests must match that owner directly.
func (h *Handler) authorizeComputerOwnerRequest(ctx context.Context, r *http.Request, daemonID string) error {
	daemonID = strings.TrimSpace(daemonID)
	if tokenWorkspace := middleware.DaemonWorkspaceIDFromContext(r.Context()); tokenWorkspace != "" {
		return h.authorizeComputerConnectionRequest(ctx, r, daemonID, tokenWorkspace)
	}
	userID := strings.TrimSpace(requestUserID(r))
	if userID == "" {
		return errComputerConnectionUnauthorized
	}
	var exists bool
	if err := h.DB.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM computer_identity_owner WHERE daemon_id=$1 AND user_id=$2
)`, daemonID, userID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return errComputerConnectionUnauthorized
	}
	return nil
}

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
	if middleware.DaemonWorkspaceIDFromContext(r.Context()) == "" {
		if _, ok := h.requireWorkspaceMember(w, r, req.WorkspaceID, "workspace not found"); !ok {
			return
		}
	}
	if err := h.authorizeComputerConnectionRequest(r.Context(), r, req.DaemonID, req.WorkspaceID); err != nil {
		if errors.Is(err, errComputerConnectionUnauthorized) {
			writeError(w, http.StatusForbidden, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, "failed to authorize Computer connection")
		}
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
