package handler

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// DeleteRuntimesByDaemon is the Computer / host one-click delete endpoint
// (LRM-438). Scope is workspace + daemon_id (case-insensitive), optionally
// narrowed by runtime_mode so the FE machine key (`local:<daemon>` /
// `cloud:<daemon>`) maps 1:1.
//
// Semantics are all-or-nothing: if any runtime on the machine is online,
// has active agents, has active tasks, or has blocking archived-leader
// squads, the whole request refuses with a structured 4xx and nothing is
// deleted. That matches the product gate — the REMOTE list only loses the
// machine when every runtime under it is gone — and LRM-238 (refuse with an
// explicit reason; never silent-disable).
//
// Route: DELETE /api/runtimes/by-daemon/{daemonId}?runtime_mode=
func (h *Handler) DeleteRuntimesByDaemon(w http.ResponseWriter, r *http.Request) {
	daemonID := strings.TrimSpace(chi.URLParam(r, "daemonId"))
	if daemonID == "" {
		writeError(w, http.StatusBadRequest, "daemon_id is required")
		return
	}
	runtimeMode := strings.TrimSpace(r.URL.Query().Get("runtime_mode"))

	workspaceID := h.resolveWorkspaceID(r)
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace required")
		return
	}
	member, ok := h.requireWorkspaceMember(w, r, workspaceID, "runtime not found")
	if !ok {
		return
	}
	userID := uuidToString(member.UserID)

	runtimes, err := h.Queries.ListAgentRuntimesByDaemonID(r.Context(), db.ListAgentRuntimesByDaemonIDParams{
		WorkspaceID: parseUUID(workspaceID),
		DaemonID:    daemonID,
		RuntimeMode: runtimeMode,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list runtimes for daemon")
		return
	}
	if len(runtimes) == 0 {
		writeError(w, http.StatusNotFound, "no runtimes found for daemon")
		return
	}

	for _, rt := range runtimes {
		if !canEditRuntime(member, rt) {
			writeError(w, http.StatusForbidden, "you can only delete your own runtimes")
			return
		}
	}

	if blocked := collectOnlineRuntimes(runtimes); len(blocked) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":           "cannot delete computer: one or more runtimes are still online. Wait until the machine is offline, then try again.",
			"code":            "computer_has_online_runtimes",
			"daemon_id":       daemonID,
			"online_runtimes": runtimeSummaries(blocked),
		})
		return
	}

	var allActiveAgents []db.Agent
	for _, rt := range runtimes {
		activeAgents, err := h.Queries.ListActiveAgentsByRuntime(r.Context(), rt.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to check runtime dependencies")
			return
		}
		allActiveAgents = append(allActiveAgents, activeAgents...)
	}
	if len(allActiveAgents) > 0 {
		body := runtimeHasActiveAgentsResponse(allActiveAgents)
		body["code"] = "computer_has_active_agents"
		body["error"] = "cannot delete computer: it has active agents bound to one or more runtimes. Archive or reassign the agents first."
		body["daemon_id"] = daemonID
		writeJSON(w, http.StatusConflict, body)
		return
	}

	for _, rt := range runtimes {
		activeSquadCount, err := h.Queries.CountActiveSquadsWithArchivedLeadersByRuntime(r.Context(), rt.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to check runtime squad dependencies")
			return
		}
		if activeSquadCount > 0 {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":     "cannot delete computer: it has active squads led by archived agents. Archive those squads or assign them a new leader first.",
				"code":      "computer_has_active_squads",
				"daemon_id": daemonID,
			})
			return
		}
	}

	runtimeIDs := make([]pgtype.UUID, len(runtimes))
	for i, rt := range runtimes {
		runtimeIDs[i] = rt.ID
	}
	activeTaskCount, err := h.countActiveRuntimeWork(r.Context(), runtimeIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check runtime task dependencies")
		return
	}
	if activeTaskCount > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":             "cannot delete computer: one or more runtimes still have active tasks. Wait for them to finish or cancel them first.",
			"code":              "computer_has_active_tasks",
			"daemon_id":         daemonID,
			"active_task_count": activeTaskCount,
		})
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete runtimes")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	deletedIDs := make([]string, 0, len(runtimes))
	for _, rt := range runtimes {
		if _, err := qtx.LockAgentRuntime(r.Context(), rt.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to lock runtime")
			return
		}
		// Re-check active agents under the lock so a concurrent bind cannot
		// sneak past the pre-flight snapshot (same race the single-runtime
		// cascade path closes with FOR UPDATE).
		activeAgents, err := qtx.ListActiveAgentsByRuntimeForUpdate(r.Context(), rt.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to re-check runtime dependencies")
			return
		}
		if len(activeAgents) > 0 {
			body := runtimeHasActiveAgentsResponse(activeAgents)
			body["code"] = "computer_has_active_agents"
			body["error"] = "cannot delete computer: it has active agents bound to one or more runtimes. Archive or reassign the agents first."
			body["daemon_id"] = daemonID
			writeJSON(w, http.StatusConflict, body)
			return
		}
		if err := teardownRuntimeWithoutActiveAgents(r.Context(), qtx, tx, rt.ID); err != nil {
			slog.Error("computer bulk delete teardown failed",
				"daemon_id", daemonID,
				"runtime_id", uuidToString(rt.ID),
				"error", err,
			)
			writeError(w, http.StatusInternalServerError, "failed to delete runtimes")
			return
		}
		deletedIDs = append(deletedIDs, uuidToString(rt.ID))
	}

	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete runtimes")
		return
	}

	slog.Info("computer runtimes deleted",
		"daemon_id", daemonID,
		"deleted_count", len(deletedIDs),
		"deleted_by", userID,
	)

	h.publish(protocol.EventDaemonRegister, workspaceID, "member", userID, map[string]any{
		"action":    "delete",
		"daemon_id": daemonID,
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"status":              "ok",
		"daemon_id":           daemonID,
		"deleted_count":       len(deletedIDs),
		"deleted_runtime_ids": deletedIDs,
	})
}

func collectOnlineRuntimes(runtimes []db.AgentRuntime) []db.AgentRuntime {
	var online []db.AgentRuntime
	for _, rt := range runtimes {
		if rt.Status == "online" {
			online = append(online, rt)
		}
	}
	return online
}

func runtimeSummaries(runtimes []db.AgentRuntime) []map[string]string {
	out := make([]map[string]string, len(runtimes))
	for i, rt := range runtimes {
		out[i] = map[string]string{
			"id":           uuidToString(rt.ID),
			"name":         rt.Name,
			"provider":     rt.Provider,
			"status":       rt.Status,
			"runtime_mode": rt.RuntimeMode,
		}
	}
	return out
}

// teardownRuntimeWithoutActiveAgents runs the shared delete path for a
// runtime that has already been verified to have zero active agents and
// zero blocking archived-leader squads. Caller owns the transaction and
// any row locks.
func teardownRuntimeWithoutActiveAgents(ctx context.Context, qtx *db.Queries, tx pgx.Tx, runtimeID pgtype.UUID) error {
	archivedAgentIDs, err := qtx.ListArchivedAgentIDsByRuntime(ctx, runtimeID)
	if err != nil {
		return err
	}
	if len(archivedAgentIDs) > 0 {
		if err := qtx.PauseAutopilotsByAgentAssignees(ctx, archivedAgentIDs); err != nil {
			return err
		}
	}

	if err := qtx.DeleteSquadsByArchivedAgentsOnRuntime(ctx, runtimeID); err != nil {
		return err
	}
	if err := qtx.DeleteArchivedAgentsByRuntime(ctx, runtimeID); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE memory_curation_run
		   SET status = 'failed', error = 'runtime deleted', finished_at = now()
		 WHERE runtime_id = $1 AND status IN ('queued', 'waiting_runtime', 'running')
	`, runtimeID); err != nil {
		return err
	}

	// agent_inbox_event.runtime_id snapshots the runtime chosen when a chat turn
	// was enqueued. Terminal rows are history, not live work, so detach them
	// before deleting the runtime; retryable/claimed rows are refused by callers.
	if _, err := tx.Exec(ctx, `
		UPDATE agent_inbox_event
		   SET runtime_id = NULL, updated_at = now()
		 WHERE runtime_id = $1
		   AND status NOT IN ('pending', 'draining', 'failed')
	`, runtimeID); err != nil {
		return err
	}

	return qtx.DeleteAgentRuntime(ctx, runtimeID)
}

func countActiveInboxEventsByRuntimeIDs(ctx context.Context, exec dbExecutor, runtimeIDs []pgtype.UUID) (int64, error) {
	var count int64
	err := exec.QueryRow(ctx, `
		SELECT count(*)::bigint
		  FROM agent_inbox_event
		 WHERE runtime_id = ANY($1::uuid[])
		   AND status IN ('pending', 'draining', 'failed')
	`, runtimeIDs).Scan(&count)
	return count, err
}

func (h *Handler) countActiveRuntimeWork(ctx context.Context, runtimeIDs []pgtype.UUID) (int64, error) {
	queuedTasks, err := h.Queries.CountActiveTasksByRuntimeIDs(ctx, runtimeIDs)
	if err != nil {
		return 0, err
	}

	inboxEvents, err := countActiveInboxEventsByRuntimeIDs(ctx, h.DB, runtimeIDs)
	if err != nil {
		return 0, err
	}

	return queuedTasks + inboxEvents, nil
}
