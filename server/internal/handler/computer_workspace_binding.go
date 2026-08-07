package handler

import (
	"encoding/json"
	"net/http"

	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/pkg/db"
)

// workspaceBindingRequest is the POST body for establishing a Binding.
type workspaceBindingRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Credential  string `json:"credential"`
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
	daemonID := r.PathValue("daemonId")
	var req workspaceBindingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	if daemonID == "" || req.WorkspaceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "daemonId and workspace_id are required"})
		return
	}

	res, err := h.bindingService().Create(
		computer.BindingRequest{ActorUserID: requestUserID(r), TargetComputerID: daemonID, TargetWorkspaceID: req.WorkspaceID},
		computer.WorkspaceBinding{ComputerID: daemonID, WorkspaceID: req.WorkspaceID, Credential: req.Credential},
	)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"kind":        res.Kind,
		"workspace_id": res.Binding.WorkspaceID,
	})
}

// RevokeComputerWorkspaceBinding revokes exactly one (Computer, Workspace)
// Binding, preserving every sibling Binding and all local Agent data (#2493).
func (h *Handler) RevokeComputerWorkspaceBinding(w http.ResponseWriter, r *http.Request) {
	daemonID := r.PathValue("daemonId")
	workspaceID := r.PathValue("workspaceId")
	if err := h.bindingService().Revoke(
		computer.BindingRequest{ActorUserID: requestUserID(r), TargetComputerID: daemonID, TargetWorkspaceID: workspaceID},
		workspaceID,
	); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "workspace_id": workspaceID, "kept_local_data": true})
}
