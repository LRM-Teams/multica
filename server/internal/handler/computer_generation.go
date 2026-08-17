package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/util"
)

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

type computerHeartbeatRequest struct {
	DaemonID    string `json:"daemon_id"`
	WorkspaceID string `json:"workspace_id"`
	Generation  int64  `json:"generation"`
}

// ComputerHeartbeat is a compatibility writer for older residents that still
// publish HTTP liveness. Current DaemonCore liveness is the Workspace Runner
// socket (connect = alive), matching Raft 1.0.16 /daemon/connect.
//
// TODO(computer-liveness): Remove after v0.4.24-alpha.55 is no
// longer a supported direct self-upgrade source.
func (h *Handler) ComputerHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req computerHeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.DaemonID = strings.TrimSpace(req.DaemonID)
	req.WorkspaceID = strings.TrimSpace(req.WorkspaceID)
	if req.DaemonID == "" || req.WorkspaceID == "" {
		writeError(w, http.StatusBadRequest, "daemon_id and workspace_id are required")
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
	if err := h.recordComputerConnected(r.Context(), req.DaemonID, req.WorkspaceID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record Computer connection")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// recordComputerConnected writes last_seen for older HTTP-heartbeat
// residents. Current Computers do not use this as liveness.
//
// TODO(computer-liveness): Remove after v0.4.24-alpha.55 is no
// longer a supported direct self-upgrade source.
func (h *Handler) recordComputerConnected(ctx context.Context, daemonID, workspaceID string) error {
	workspaceUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return err
	}
	_, err = h.DB.Exec(ctx, `
INSERT INTO daemon_heartbeat (workspace_id, daemon_id, last_seen_at, updated_at)
VALUES ($1, $2, now(), now())
ON CONFLICT (workspace_id, daemon_id) DO UPDATE
SET last_seen_at=now(), updated_at=now()`, workspaceUUID, daemonID)
	return err
}
