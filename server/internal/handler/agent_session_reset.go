package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

const agentLifecycleSessionResetStep = "session_reset"

type resetAgentRuntimeSessionRequest struct {
	OperationID string `json:"operation_id"`
}

// ResetAgentRuntimeSession clears provider resume identity while preserving
// chat history, work directories, and every file in the Agent Workspace.
// The lifecycle operation row is the authorization and idempotency boundary.
func (h *Handler) ResetAgentRuntimeSession(w http.ResponseWriter, r *http.Request) {
	runtimeID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "runtimeId"), "runtime_id")
	if !ok {
		return
	}
	agentID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "agentId"), "agent_id")
	if !ok {
		return
	}
	var req resetAgentRuntimeSessionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	operationID, ok := parseUUIDOrBadRequest(w, req.OperationID, "operation_id")
	if !ok {
		return
	}
	runtime, err := h.Queries.GetAgentRuntime(r.Context(), runtimeID)
	if err != nil {
		writeError(w, http.StatusNotFound, "runtime not found")
		return
	}
	if !h.requireDaemonWorkspaceAccess(w, r, uuidToString(runtime.WorkspaceID)) {
		return
	}
	if h.TxStarter == nil {
		writeError(w, http.StatusServiceUnavailable, "agent session reset is unavailable")
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reset agent session")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()

	var actionKind, status, step string
	err = tx.QueryRow(r.Context(), `
		SELECT action_kind, status, step
		FROM agent_lifecycle_operation
		WHERE id = $1 AND agent_id = $2 AND runtime_id = $3
		FOR UPDATE
	`, operationID, agentID, runtimeID).Scan(&actionKind, &status, &step)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "agent lifecycle operation not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reset agent session")
		return
	}
	if actionKind != string(agentLifecycleResetSessionRestart) && actionKind != string(agentLifecycleFullResetRestart) {
		writeError(w, http.StatusConflict, "lifecycle operation does not reset the session")
		return
	}
	if status != agentLifecycleRunning && status != agentLifecycleScheduled {
		writeError(w, http.StatusConflict, "agent lifecycle operation is not active")
		return
	}
	if step == agentLifecycleSessionResetStep {
		writeJSON(w, http.StatusOK, map[string]string{"status": "already_reset"})
		return
	}

	// Clear both canonical and legacy resume sources through the same state
	// seam used by the server lifecycle orchestrator.
	if err := clearAgentRuntimeSessionState(r.Context(), tx, req.OperationID, chi.URLParam(r, "agentId"), chi.URLParam(r, "runtimeId")); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reset agent session")
		return
	}
	if _, err := tx.Exec(r.Context(), `
		UPDATE agent_lifecycle_operation SET step = $2 WHERE id = $1
	`, operationID, agentLifecycleSessionResetStep); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reset agent session")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reset agent session")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
}
