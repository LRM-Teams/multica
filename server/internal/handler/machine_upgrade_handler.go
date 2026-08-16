package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type createMachineUpgradeRequest struct {
	TargetVersion string `json:"target_version"`
	RequestID     string `json:"request_id"`
}

type acceptMachineUpgradeRequest struct {
	GenerationID   string `json:"generation_id"`
	CLIVersion     string `json:"cli_version"`
	ResolvedTarget string `json:"resolved_target"`
}

type machineUpgradeProgressRequest struct {
	Phase        MachineUpgradePhase `json:"phase"`
	Generation   string              `json:"generation_id,omitempty"`
	ErrorCode    string              `json:"error_code,omitempty"`
	ErrorMessage string              `json:"error_message,omitempty"`
}

type commitComputerMachineUpgradeTakeoverRequest struct {
	ComputerID                    string   `json:"daemon_id"` // Legacy installed-client spelling.
	GenerationID                  string   `json:"generation_id"`
	CLIVersion                    string   `json:"cli_version"`
	PredecessorComputerGeneration int64    `json:"predecessor_computer_generation"`
	CandidateComputerGeneration   int64    `json:"candidate_computer_generation"`
	WorkspaceIDs                  []string `json:"workspace_ids"`
}

// CommitComputerMachineUpgradeTakeover is a compatibility receipt for older
// Computers that still POST after local PID+version proof. Cloud liveness is
// the current connect socket, not a computer_generation CAS. Raft Computer
// completes replacement locally.
func (h *Handler) CommitComputerMachineUpgradeTakeover(w http.ResponseWriter, r *http.Request) {
	if h.MachineUpgradeStore == nil {
		writeError(w, http.StatusServiceUnavailable, "machine upgrade store is not configured")
		return
	}
	var req commitComputerMachineUpgradeTakeoverRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.ComputerID = strings.TrimSpace(req.ComputerID)
	if req.ComputerID == "" {
		writeError(w, http.StatusBadRequest, "daemon_id is required")
		return
	}
	if credentialComputerID := middleware.DaemonIDFromContext(r.Context()); credentialComputerID != "" && !strings.EqualFold(credentialComputerID, req.ComputerID) {
		writeError(w, http.StatusForbidden, "Computer credential scope mismatch")
		return
	}
	if err := h.authorizeComputerOwnerRequest(r.Context(), r, req.ComputerID); err != nil {
		if errors.Is(err, errComputerConnectionUnauthorized) {
			writeError(w, http.StatusForbidden, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, "failed to authorize Computer owner")
		}
		return
	}
	op, err := h.MachineUpgradeStore.Get(r.Context(), req.ComputerID, chi.URLParam(r, "upgradeId"))
	if err != nil {
		h.writeMachineUpgradeDaemonError(w, err)
		return
	}
	if op == nil {
		writeError(w, http.StatusNotFound, "machine upgrade not found")
		return
	}
	writeJSON(w, http.StatusOK, op)
}

// AttestComputerMachineUpgrade is a retired successor HTTP proof.
// TODO(computer-upgrade-attest): Remove after installed Computers no longer
// POST /computer/machine-upgrades/{id}/attest. Completion is the current
// Binding socket reporting the resolved target.
func (h *Handler) AttestComputerMachineUpgrade(w http.ResponseWriter, r *http.Request) {
	writeCodedError(w, http.StatusGone, "machine_upgrade_attest_retired", "Computer Machine Upgrade completes on the current Binding socket")
}

// AcceptMachineUpgrade is daemon-authenticated. The daemon may accept an
// action only through a capable sibling runtime; the server snapshots every
// sibling identity before accepting so a later partial re-registration cannot
// make the machine look converged.
func (h *Handler) AcceptMachineUpgrade(w http.ResponseWriter, r *http.Request) {
	if h.MachineUpgradeStore == nil {
		writeError(w, http.StatusServiceUnavailable, "machine upgrade store is not configured")
		return
	}
	runtimeID := strings.TrimSpace(chi.URLParam(r, "runtimeId"))
	rt, ok := h.requireDaemonRuntimeAccess(w, r, runtimeID)
	if !ok {
		return
	}
	if !agentRuntimeHasCapability(rt, protocol.DaemonCapabilityMachineUpgrade) {
		writeCodedError(w, http.StatusConflict, "unsupported_runtime_capability", "runtime does not support machine upgrades")
		return
	}
	var req acceptMachineUpgradeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	op, err := h.MachineUpgradeStore.Accept(r.Context(), runtimeDaemonKey(rt), chi.URLParam(r, "upgradeId"), req.GenerationID, req.CLIVersion, req.ResolvedTarget)
	if err != nil {
		h.writeMachineUpgradeDaemonError(w, err)
		return
	}
	h.publishComputerUpgradeProjection(r, runtimeDaemonKey(rt))
	writeJSON(w, http.StatusOK, op)
}

// GetDaemonMachineUpgrade returns the canonical server receipt to one of the
// operation's daemon runtimes. Successors use it only to compare-and-clear an
// exact local recovery marker after terminal server proof.
func (h *Handler) GetDaemonMachineUpgrade(w http.ResponseWriter, r *http.Request) {
	if h.MachineUpgradeStore == nil {
		writeError(w, http.StatusServiceUnavailable, "machine upgrade store is not configured")
		return
	}
	runtimeID := strings.TrimSpace(chi.URLParam(r, "runtimeId"))
	rt, ok := h.requireDaemonRuntimeAccess(w, r, runtimeID)
	if !ok {
		return
	}
	op, err := h.MachineUpgradeStore.Get(r.Context(), runtimeDaemonKey(rt), chi.URLParam(r, "upgradeId"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load machine upgrade")
		return
	}
	if op == nil {
		writeError(w, http.StatusNotFound, "machine upgrade not found")
		return
	}
	writeJSON(w, http.StatusOK, op)
}

// ReportMachineUpgradeProgress projects durable daemon execution phases.
// Completion is deliberately absent: only full successor registration can
// complete a machine upgrade.
func (h *Handler) ReportMachineUpgradeProgress(w http.ResponseWriter, r *http.Request) {
	if h.MachineUpgradeStore == nil {
		writeError(w, http.StatusServiceUnavailable, "machine upgrade store is not configured")
		return
	}
	runtimeID := strings.TrimSpace(chi.URLParam(r, "runtimeId"))
	rt, ok := h.requireDaemonRuntimeAccess(w, r, runtimeID)
	if !ok {
		return
	}
	var req machineUpgradeProgressRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var op *MachineUpgrade
	var err error
	if req.Phase == MachineUpgradeRollbackPending {
		op, err = h.MachineUpgradeStore.BeginRollback(r.Context(), runtimeDaemonKey(rt), chi.URLParam(r, "upgradeId"), req.Generation, req.ErrorCode, req.ErrorMessage)
	} else {
		op, err = h.MachineUpgradeStore.Progress(r.Context(), runtimeDaemonKey(rt), chi.URLParam(r, "upgradeId"), req.Phase, req.ErrorCode, req.ErrorMessage)
	}
	if err != nil {
		h.writeMachineUpgradeDaemonError(w, err)
		return
	}
	if op == nil {
		writeCodedError(w, http.StatusConflict, "machine_upgrade_phase_conflict", "machine upgrade phase cannot advance from its current state")
		return
	}
	h.publishComputerUpgradeProjection(r, runtimeDaemonKey(rt))
	writeJSON(w, http.StatusOK, op)
}

// CreateMachineUpgrade is the canonical daemon-identity endpoint. The
// workspace header selects an authorization context; the operation itself is
// nevertheless keyed only by daemon_id, so sibling runtime/provider surfaces
// cannot acquire independent lineages.
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

func (h *Handler) GetMachineUpgrade(w http.ResponseWriter, r *http.Request) {
	daemonID := strings.TrimSpace(chi.URLParam(r, "daemonId"))
	_, _, ok := h.requireMachineUpgradeViewer(w, r, daemonID)
	if !ok {
		return
	}
	op, err := h.MachineUpgradeStore.Get(r.Context(), daemonID, chi.URLParam(r, "upgradeId"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load machine upgrade: "+err.Error())
		return
	}
	if op == nil {
		writeError(w, http.StatusNotFound, "machine upgrade not found")
		return
	}
	writeJSON(w, http.StatusOK, op)
}

func (h *Handler) CancelMachineUpgrade(w http.ResponseWriter, r *http.Request) {
	daemonID := strings.TrimSpace(chi.URLParam(r, "daemonId"))
	rt, _, ok := h.requireMachineUpgradeManager(w, r, daemonID)
	if !ok {
		return
	}
	op, err := h.MachineUpgradeStore.Cancel(r.Context(), daemonID, chi.URLParam(r, "upgradeId"))
	if err != nil {
		switch {
		case errors.Is(err, errMachineUpgradeNotFound):
			writeError(w, http.StatusNotFound, "machine upgrade not found")
		case errors.Is(err, errMachineUpgradeAlreadyAccepted):
			writeCodedError(w, http.StatusConflict, "machine_upgrade_already_accepted", err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "failed to cancel machine upgrade: "+err.Error())
		}
		return
	}
	h.publishComputerUpgradeProjection(r, runtimeDaemonKey(rt))
	writeJSON(w, http.StatusOK, op)
}

func (h *Handler) createMachineUpgrade(
	r *http.Request,
	rt db.AgentRuntime,
	_ db.Member,
	req createMachineUpgradeRequest,
	_ bool,
) (*MachineUpgrade, bool, error) {
	requestID, err := h.dispatchMachineUpgrade(r, rt, req)
	if err != nil {
		return nil, false, err
	}
	target := strings.TrimSpace(req.TargetVersion)
	if target == "" {
		target = "latest"
	}
	return &MachineUpgrade{ID: requestID, RequestID: requestID, RequestedTarget: target, Phase: MachineUpgradeStarting}, true, nil
}

// dispatchMachineUpgrade is the Raft 1.0.16 click path: authorize, then send
// computer:upgrade on the current Binding socket. It does not create a cloud
// machine_upgrade row.
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
	payload := protocol.ComputerUpgradePayload{RequestID: requestID, OperationID: requestID, TargetVersion: target}
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

type machineUpgradeInputError struct {
	code    string
	message string
}

func (e *machineUpgradeInputError) Error() string { return e.message }

func (h *Handler) writeMachineUpgradeError(w http.ResponseWriter, err error) {
	var input *machineUpgradeInputError
	var conflict *machineUpgradeConflictError
	switch {
	case errors.As(err, &input):
		writeCodedError(w, http.StatusConflict, input.code, input.message)
	case errors.As(err, &conflict):
		body := map[string]any{"code": "upgrade_already_in_progress", "error": conflict.Error()}
		if active := conflict.Active(); active != nil {
			body["operation"] = active
		}
		writeJSON(w, http.StatusConflict, body)
	default:
		writeError(w, http.StatusInternalServerError, "failed to create machine upgrade: "+err.Error())
	}
}

func (h *Handler) writeMachineUpgradeDaemonError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errMachineUpgradeNotFound):
		writeError(w, http.StatusNotFound, "machine upgrade not found")
	case errors.Is(err, errMachineUpgradeAcceptanceConflict), errors.Is(err, errMachineUpgradeAttestationRejected):
		writeCodedError(w, http.StatusConflict, "machine_upgrade_attestation_rejected", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "machine upgrade operation failed: "+err.Error())
	}
}

func (h *Handler) machineUpgradeRuntimeIDs(ctx context.Context, rt db.AgentRuntime) ([]string, error) {
	computerID := runtimeDaemonKey(rt)
	if computerID == "" {
		return nil, errMachineUpgradeAttestationRejected
	}
	rows, err := h.DB.Query(ctx, machineUpgradeComputerRuntimeSelect, computerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id pgtype.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, uuidToString(id))
	}
	return normalizedMachineRuntimeIDs(ids), rows.Err()
}

// completeMachineUpgradeOnCurrentSocket finishes the Computer's unique
// non-terminal Machine Upgrade when this authenticated Binding socket
// reports the resolved target. The successor does not send an operation
// ID or generation.
func (h *Handler) completeMachineUpgradeOnCurrentSocket(_ context.Context, _ daemonws.ClientIdentity) {
	// TODO(computer-upgrade-queued): delete leftover machine_upgrade
	// completion. New upgrades finish on computer:upgrade:done.
}

// attestMachineUpgradeRegistration runs after a register transaction has
// committed. A registration is proof only for an accepted generation and its
// captured sibling set; unrelated, stale, and partial registrations are
// deliberately ignored rather than completing an operation.
func (h *Handler) attestMachineUpgradeRegistration(r *http.Request, rt db.AgentRuntime, cliVersion, generation string) {
	if h == nil || h.MachineUpgradeStore == nil || strings.TrimSpace(generation) == "" {
		return
	}
	op, err := h.MachineUpgradeStore.LatestForDaemon(r.Context(), runtimeDaemonKey(rt))
	if err != nil || op == nil || (op.Phase != MachineUpgradeHandoff && op.Phase != MachineUpgradeConverging) {
		return
	}
	runtimeIDs, err := h.machineUpgradeRuntimeIDs(r.Context(), rt)
	if err != nil {
		slog.Warn("machine upgrade registration runtime set failed", "error", err, "runtime_id", uuidToString(rt.ID))
		return
	}
	updated, err := h.MachineUpgradeStore.Attest(r.Context(), runtimeDaemonKey(rt), op.ID, generation, uuidToString(rt.ID), cliVersion, runtimeIDs)
	if err != nil {
		if !errors.Is(err, errMachineUpgradeAttestationRejected) {
			slog.Warn("machine upgrade registration attestation failed", "error", err, "runtime_id", uuidToString(rt.ID), "upgrade_id", op.ID)
		}
		return
	}
	if updated != nil {
		h.publishComputerUpgradeProjection(r, runtimeDaemonKey(rt))
	}
}

func (h *Handler) attestMachineUpgradeRollbackRegistration(r *http.Request, rt db.AgentRuntime, cliVersion, generation string) {
	if h == nil || h.MachineUpgradeStore == nil || strings.TrimSpace(generation) == "" {
		return
	}
	op, err := h.MachineUpgradeStore.LatestForDaemon(r.Context(), runtimeDaemonKey(rt))
	if err != nil || op == nil || op.Phase != MachineUpgradeRollbackPending {
		return
	}
	runtimeIDs, err := h.machineUpgradeRuntimeIDs(r.Context(), rt)
	if err != nil {
		return
	}
	updated, err := h.MachineUpgradeStore.AttestRollback(r.Context(), runtimeDaemonKey(rt), op.ID, generation, uuidToString(rt.ID), cliVersion, runtimeIDs)
	if err != nil && !errors.Is(err, errMachineUpgradeAttestationRejected) {
		slog.Warn("machine upgrade rollback registration attestation failed", "error", err, "runtime_id", uuidToString(rt.ID), "upgrade_id", op.ID)
	}
	if updated != nil {
		h.publishComputerUpgradeProjection(r, runtimeDaemonKey(rt))
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

// publishComputerUpgradeProjection invalidates the Computer projection in
// every active Workspace binding. Upgrade lifecycle does not belong to a
// Runtime, and the Workspace that initiated the mutation has no special role.
func (h *Handler) publishComputerUpgradeSocketEvent(identity daemonws.ClientIdentity, eventType string, payload map[string]any) {
	if h == nil || strings.TrimSpace(identity.WorkspaceID) == "" {
		return
	}
	h.publish(eventType, identity.WorkspaceID, "system", "", payload)
}

func (h *Handler) publishComputerUpgradeProjection(r *http.Request, computerID string) {
	computerID = strings.TrimSpace(computerID)
	if computerID == "" {
		return
	}
	rows, err := h.DB.Query(r.Context(), `
		SELECT workspace_id
		FROM computer_workspace_bindings
		WHERE daemon_id = $1 AND active = TRUE AND revoked_at IS NULL
		ORDER BY workspace_id`, computerID)
	if err != nil {
		slog.Warn("machine upgrade projection scope failed", "error", err, "computer_id", computerID)
		return
	}
	workspaceIDs := make([]pgtype.UUID, 0)
	for rows.Next() {
		var workspaceID pgtype.UUID
		if err := rows.Scan(&workspaceID); err != nil {
			rows.Close()
			slog.Warn("machine upgrade projection scan failed", "error", err, "computer_id", computerID)
			return
		}
		workspaceIDs = append(workspaceIDs, workspaceID)
	}
	rowsErr := rows.Err()
	rows.Close()
	if rowsErr != nil {
		slog.Warn("machine upgrade projection scope failed", "error", rowsErr, "computer_id", computerID)
		return
	}
	for _, workspaceID := range workspaceIDs {
		h.publish(protocol.EventComputerUpdated, uuidToString(workspaceID), "system", "", map[string]any{
			"computer_id": computerID,
		})
	}
}

func runtimeUpdateFromMachineUpgrade(op *MachineUpgrade, runtimeID string) *UpdateRequest {
	if op == nil {
		return nil
	}
	status := UpdateQueued
	switch op.Phase {
	case MachineUpgradeStarting, MachineUpgradeStaging, MachineUpgradeVerifying, MachineUpgradeHandoff, MachineUpgradeConverging, MachineUpgradeRollbackPending:
		status = UpdateRunning
	case MachineUpgradeCompleted:
		status = UpdateCompleted
	case MachineUpgradeFailed, MachineUpgradeRolledBack, MachineUpgradeCancelled:
		status = UpdateFailed
	case MachineUpgradeTimeout:
		status = UpdateTimeout
	}
	target := op.RequestedTarget
	if op.ResolvedTarget != nil && strings.TrimSpace(*op.ResolvedTarget) != "" {
		target = *op.ResolvedTarget
	}
	errMsg := ""
	if op.ErrorMessage != nil {
		errMsg = *op.ErrorMessage
	}
	if op.Phase == MachineUpgradeCancelled && errMsg == "" {
		errMsg = "machine upgrade cancelled"
	}
	return &UpdateRequest{
		ID:            op.ID,
		RuntimeID:     runtimeID,
		Status:        status,
		TargetVersion: target,
		Error:         errMsg,
		CreatedAt:     op.CreatedAt,
		UpdatedAt:     op.UpdatedAt,
	}
}
