package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
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
	agentUUID, ok := h.requireDaemonAgentOnRuntime(w, r)
	if !ok {
		return
	}
	if err := h.Queries.MarkAgentCrashed(r.Context(), agentUUID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mark agent crashed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ClearAgentProviderCrashed clears a prior ReportAgentProviderCrashed once the
// daemon has a live resident provider again (successful recreate) or a
// human-driven lifecycle restart succeeded.
func (h *Handler) ClearAgentProviderCrashed(w http.ResponseWriter, r *http.Request) {
	agentUUID, ok := h.requireDaemonAgentOnRuntime(w, r)
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
func (h *Handler) requireDaemonAgentOnRuntime(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	runtimeID := chi.URLParam(r, "runtimeId")
	runtimeUUID, ok := parseUUIDOrBadRequest(w, runtimeID, "runtime_id")
	if !ok {
		return pgtype.UUID{}, false
	}
	agentID := chi.URLParam(r, "agentId")
	agentUUID, ok := parseUUIDOrBadRequest(w, agentID, "agent_id")
	if !ok {
		return pgtype.UUID{}, false
	}
	rt, err := h.Queries.GetAgentRuntime(r.Context(), runtimeUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "runtime not found")
		return pgtype.UUID{}, false
	}
	if !h.requireDaemonWorkspaceAccess(w, r, uuidToString(rt.WorkspaceID)) {
		return pgtype.UUID{}, false
	}
	agent, err := h.Queries.GetAgent(r.Context(), agentUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "agent not found")
		return pgtype.UUID{}, false
	}
	if !agent.RuntimeID.Valid || uuidToString(agent.RuntimeID) != uuidToString(runtimeUUID) {
		writeError(w, http.StatusNotFound, "agent not found on runtime")
		return pgtype.UUID{}, false
	}
	return agentUUID, true
}
