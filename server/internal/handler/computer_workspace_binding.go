package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/pkg/db"
)

// workspaceBindingRequest is the POST body for establishing a Binding.
type workspaceBindingRequest struct {
	WorkspaceID string `json:"workspace_id"`
}

// bindingService wires the machine-wide Binding contract to the
// computer_workspace_bindings table (migration 307) via the pgx adapter.
func (h *Handler) bindingService() *computer.BindingService {
	return &computer.BindingService{Store: db.NewBindingStore(h.DB)}
}

// CreateComputerWorkspaceBinding establishes or repairs the Binding between
// the named Computer (daemon_id) and one immutable Workspace, scoped to the
// signed-in user. Idempotent: a repeat for the same (Computer, Workspace) is a
// repair, never a duplicate (#2489, #2490).
func (h *Handler) CreateComputerWorkspaceBinding(w http.ResponseWriter, r *http.Request) {
	daemonID := strings.TrimSpace(r.PathValue("daemonId"))
	var req workspaceBindingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	if daemonID == "" || req.WorkspaceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "daemonId and workspace_id are required"})
		return
	}
	workspaceUUID, ok := parseUUIDOrBadRequest(w, req.WorkspaceID, "workspace_id")
	if !ok {
		return
	}
	req.WorkspaceID = uuidToString(workspaceUUID)
	if _, ok := h.requireWorkspaceMember(w, r, req.WorkspaceID, "workspace not found"); !ok {
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to begin Workspace connection creation"})
		return
	}
	defer tx.Rollback(r.Context())
	txHandler := *h
	txHandler.Queries = h.Queries.WithTx(tx)
	txHandler.DB = tx
	credential, err := txHandler.prepareDaemonRegisterToken(r.Context(), workspaceUUID, daemonID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to issue Workspace connection credential"})
		return
	}

	res, err := (&computer.BindingService{Store: db.NewBindingStore(tx)}).Create(
		computer.BindingRequest{ActorUserID: requestUserID(r), TargetComputerID: daemonID, TargetWorkspaceID: req.WorkspaceID},
		computer.WorkspaceBinding{ComputerID: daemonID, WorkspaceID: req.WorkspaceID, Credential: credential.raw},
	)
	if err != nil {
		status := http.StatusInternalServerError
		message := err.Error()
		if errors.Is(err, computer.ErrBindingUnauthorized) {
			status = http.StatusForbidden
			message = "Workspace connection is not authorized for this Computer"
		}
		writeJSON(w, status, map[string]any{"error": message})
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to commit Workspace connection creation"})
		return
	}
	h.cacheDaemonRegisterToken(r.Context(), credential, workspaceUUID, daemonID)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                    true,
		"kind":                  res.Kind,
		"workspace_id":          res.Binding.WorkspaceID,
		"credential":            credential.raw,
		"credential_expires_at": credential.expiresAt.UTC().Format(time.RFC3339Nano),
	})
}

// RevokeComputerWorkspaceBinding revokes exactly one (Computer, Workspace)
// Binding, preserving every sibling Binding and all local Agent data (#2493).
func (h *Handler) RevokeComputerWorkspaceBinding(w http.ResponseWriter, r *http.Request) {
	daemonID := strings.TrimSpace(r.PathValue("daemonId"))
	workspaceID := strings.TrimSpace(r.PathValue("workspaceId"))
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	workspaceID = uuidToString(workspaceUUID)
	member, ok := h.requireWorkspaceMember(w, r, workspaceID, "workspace not found")
	if !ok {
		return
	}
	revokedTokenHashes, err := h.bindingService().Revoke(
		computer.BindingRequest{
			ActorUserID:             requestUserID(r),
			ActorCanManageWorkspace: roleAllowed(member.Role, "owner", "admin"),
			TargetComputerID:        daemonID,
			TargetWorkspaceID:       workspaceID,
		},
		workspaceID,
	)
	if err != nil {
		message := err.Error()
		if errors.Is(err, computer.ErrBindingUnauthorized) {
			message = "Workspace connection is not authorized for this Computer"
		}
		writeJSON(w, http.StatusForbidden, map[string]any{"error": message})
		return
	}
	for _, tokenHash := range revokedTokenHashes {
		h.DaemonTokenCache.Invalidate(r.Context(), tokenHash)
	}
	// Binding removal is workspace-local: make only matching runtimes
	// unavailable. Sibling Workspace runtimes and local Agent Roots are untouched.
	_, _ = h.DB.Exec(r.Context(), `
UPDATE agent_runtime
   SET status = 'offline', updated_at = now()
 WHERE workspace_id = $1 AND LOWER(daemon_id) = LOWER($2) AND status <> 'offline'`, workspaceUUID, daemonID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "workspace_id": workspaceID, "kept_local_data": true})
}
