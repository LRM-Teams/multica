package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/daemonws"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type createMachineUpgradeRequest struct {
	TargetVersion string `json:"target_version"`
	RequestID     string `json:"request_id"`
}

type machineUpgradeInputError struct {
	code    string
	message string
}

func (e *machineUpgradeInputError) Error() string { return e.message }

// CreateMachineUpgrade authorizes a Computer owner, then sends computer:upgrade
// on one current Binding socket. It does not create a cloud receipt.
func (h *Handler) CreateMachineUpgrade(w http.ResponseWriter, r *http.Request) {
	daemonID := strings.TrimSpace(chi.URLParam(r, "daemonId"))
	rt, _, ok := h.requireMachineUpgradeManager(w, r, daemonID)
	if !ok {
		return
	}
	var req createMachineUpgradeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.RequestID) == "" {
		writeError(w, http.StatusBadRequest, "request_id is required")
		return
	}
	requestID, err := h.dispatchMachineUpgrade(r, rt, req)
	if err != nil {
		h.writeMachineUpgradeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"request_id": requestID})
}

// dispatchMachineUpgrade is the Raft 1.0.16 click path: authorize, then send
// computer:upgrade on the current Binding socket.
func (h *Handler) dispatchMachineUpgrade(r *http.Request, rt db.AgentRuntime, req createMachineUpgradeRequest) (string, error) {
	daemonID := runtimeDaemonKey(rt)
	if daemonID == "" {
		return "", &machineUpgradeInputError{code: "machine_upgrade_identity_missing", message: "runtime has no daemon identity"}
	}
	if pinnedVersion, pinned := runtimePinnedVersion(rt); pinned {
		return "", &machineUpgradeInputError{code: "runtime_pinned", message: "this computer is pinned to version " + pinnedVersion}
	}
	if launchedBy(runtimeMetadata(rt)) == "desktop" {
		return "", &machineUpgradeInputError{code: "desktop_managed", message: "this computer is managed by the Desktop updater"}
	}
	target := strings.TrimSpace(req.TargetVersion)
	if target == "" {
		target = "latest"
	}
	requestID := strings.TrimSpace(req.RequestID)
	if requestID == "" {
		return "", &machineUpgradeInputError{code: "request_id_required", message: "request_id is required"}
	}
	if !h.dispatchComputerUpgradeToRunners(r.Context(), daemonID, requestID, target) {
		return "", &machineUpgradeInputError{code: "no_current_socket", message: "Computer upgrade needs the current Binding socket"}
	}
	return requestID, nil
}

// dispatchComputerUpgradeToRunners is the Raft 1.0.16 connect-socket path:
// command goes to one current DaemonCore socket. Upgrade is machine-wide; the
// child forwards it to Computer Host, and Host drains every Binding locally.
func (h *Handler) dispatchComputerUpgradeToRunners(ctx context.Context, computerID, requestID, target string) bool {
	if h == nil || h.DaemonHub == nil || strings.TrimSpace(computerID) == "" || strings.TrimSpace(requestID) == "" {
		return false
	}
	rows, err := h.DB.Query(ctx, `
		SELECT workspace_id
		FROM computer_workspace_bindings
		WHERE daemon_id = $1 AND active = TRUE AND revoked_at IS NULL
		ORDER BY workspace_id`, computerID)
	if err != nil {
		return false
	}
	defer rows.Close()
	payload := protocol.ComputerUpgradePayload{RequestID: requestID, TargetVersion: target}
	for rows.Next() {
		var workspaceID pgtype.UUID
		if err := rows.Scan(&workspaceID); err != nil {
			return false
		}
		if h.DaemonHub.NotifyWorkspaceRunner(computerID, uuidToString(workspaceID), protocol.EventComputerUpgrade, payload) {
			return true
		}
	}
	return false
}

func (h *Handler) writeMachineUpgradeError(w http.ResponseWriter, err error) {
	var input *machineUpgradeInputError
	switch {
	case errors.As(err, &input):
		writeCodedError(w, http.StatusConflict, input.code, input.message)
	default:
		writeError(w, http.StatusInternalServerError, "failed to dispatch computer upgrade: "+err.Error())
	}
}

// requireMachineUpgradeViewer resolves a Computer through the current
// Workspace. Every Workspace member may observe the one machine-wide upgrade;
// visibility does not grant lifecycle mutation authority.
func (h *Handler) requireMachineUpgradeViewer(w http.ResponseWriter, r *http.Request, daemonID string) (db.AgentRuntime, db.Member, bool) {
	if daemonID == "" {
		writeError(w, http.StatusBadRequest, "daemon_id is required")
		return db.AgentRuntime{}, db.Member{}, false
	}
	workspaceID := h.resolveWorkspaceID(r)
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return db.AgentRuntime{}, db.Member{}, false
	}
	var runtimeID pgtype.UUID
	err := h.DB.QueryRow(r.Context(), `
		SELECT id FROM agent_runtime
		WHERE workspace_id = $1 AND daemon_id = $2
		ORDER BY created_at ASC, id ASC
		LIMIT 1`, workspaceUUID, daemonID).Scan(&runtimeID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "daemon not found")
		return db.AgentRuntime{}, db.Member{}, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load daemon")
		return db.AgentRuntime{}, db.Member{}, false
	}
	rt, err := h.Queries.GetAgentRuntime(r.Context(), runtimeID)
	if err != nil {
		writeError(w, http.StatusNotFound, "daemon not found")
		return db.AgentRuntime{}, db.Member{}, false
	}
	member, ok := h.requireWorkspaceMember(w, r, uuidToString(rt.WorkspaceID), "daemon not found")
	if !ok {
		return db.AgentRuntime{}, db.Member{}, false
	}
	return rt, member, true
}

// requireMachineUpgradeManager adds the Computer-owner mutation fence to the
// Workspace visibility check.
func (h *Handler) requireMachineUpgradeManager(w http.ResponseWriter, r *http.Request, daemonID string) (db.AgentRuntime, db.Member, bool) {
	rt, member, ok := h.requireMachineUpgradeViewer(w, r, daemonID)
	if !ok {
		return db.AgentRuntime{}, db.Member{}, false
	}
	if !canManageMachineUpgrade(member, rt) {
		writeError(w, http.StatusForbidden, "only the Computer owner can manage this upgrade")
		return db.AgentRuntime{}, db.Member{}, false
	}
	return rt, member, true
}

func (h *Handler) publishComputerUpgradeSocketEvent(identity daemonws.ClientIdentity, eventType string, payload map[string]any) {
	if h == nil || strings.TrimSpace(identity.WorkspaceID) == "" {
		return
	}
	h.publish(eventType, identity.WorkspaceID, "system", "", payload)
}
