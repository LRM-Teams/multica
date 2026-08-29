package handler

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ReportAgentProviderCrashed records that a daemon found this agent's idle
// resident provider process dead (ResidentRuntimeCrashEvent / task #42② /
// Parker Raft status ②). Best-effort from the daemon's point of view; the
// daemon continues local recovery regardless of this call's outcome.
//
// Only the daemon's proactive idle-liveness path may call this — mid-turn
// process_failure is a different fact and must not be funneled here as a
// blanket "crashed" (Parker's rule: status stays unknown rather than invented).
func (h *Handler) ReportAgentProviderCrashed(w http.ResponseWriter, r *http.Request) {
	agentUUID, runtime, ok := h.requireDaemonAgentOnRuntime(w, r)
	if !ok {
		return
	}
	var request struct {
		CredentialID string `json:"credential_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	credentialID, ok := parseUUIDOrBadRequest(w, request.CredentialID, "credential_id")
	if !ok {
		return
	}
	ownerID, err := h.resolveRuntimeOwner(r, runtime)
	if err != nil {
		writeError(w, http.StatusConflict, "agent runtime has no owning user")
		return
	}
	rows, err := h.Queries.MarkAgentCrashed(r.Context(), db.MarkAgentCrashedParams{
		AgentID: agentUUID, CredentialID: credentialID,
		WorkspaceID: runtime.WorkspaceID, OwnerID: ownerID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mark agent crashed")
		return
	}
	if rows == 0 {
		writeError(w, http.StatusConflict, "Agent launch credential is not current")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ClearAgentProviderCrashed clears a prior ReportAgentProviderCrashed once the
// daemon has a live resident provider again (successful recreate) or a
// human-driven lifecycle restart succeeded.
func (h *Handler) ClearAgentProviderCrashed(w http.ResponseWriter, r *http.Request) {
	agentUUID, _, ok := h.requireDaemonAgentOnRuntime(w, r)
	if !ok {
		return
	}
	if err := h.Queries.ClearAgentCrashed(r.Context(), agentUUID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clear agent crashed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// requireDaemonAgentOnRuntime loads the runtime + agent, checks daemon
// workspace access, and verifies the agent is bound to that runtime.
func (h *Handler) requireDaemonAgentOnRuntime(w http.ResponseWriter, r *http.Request) (pgtype.UUID, db.AgentRuntime, bool) {
	runtimeID := chi.URLParam(r, "runtimeId")
	runtimeUUID, ok := parseUUIDOrBadRequest(w, runtimeID, "runtime_id")
	if !ok {
		return pgtype.UUID{}, db.AgentRuntime{}, false
	}
	agentID := chi.URLParam(r, "agentId")
	agentUUID, ok := parseUUIDOrBadRequest(w, agentID, "agent_id")
	if !ok {
		return pgtype.UUID{}, db.AgentRuntime{}, false
	}
	rt, err := h.Queries.GetAgentRuntime(r.Context(), runtimeUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "runtime not found")
		return pgtype.UUID{}, db.AgentRuntime{}, false
	}
	if !h.requireDaemonWorkspaceAccess(w, r, uuidToString(rt.WorkspaceID)) {
		return pgtype.UUID{}, db.AgentRuntime{}, false
	}
	if middleware.DaemonAuthPathFromContext(r.Context()) != middleware.DaemonAuthPathDaemonToken ||
		!rt.DaemonID.Valid || rt.DaemonID.String != middleware.DaemonIDFromContext(r.Context()) {
		writeError(w, http.StatusForbidden, "daemon token is not bound to this runtime")
		return pgtype.UUID{}, db.AgentRuntime{}, false
	}
	agent, err := h.Queries.GetAgent(r.Context(), agentUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "agent not found")
		return pgtype.UUID{}, db.AgentRuntime{}, false
	}
	if !agent.RuntimeID.Valid || uuidToString(agent.RuntimeID) != uuidToString(runtimeUUID) {
		writeError(w, http.StatusNotFound, "agent not found on runtime")
		return pgtype.UUID{}, db.AgentRuntime{}, false
	}
	return agentUUID, rt, true
}
