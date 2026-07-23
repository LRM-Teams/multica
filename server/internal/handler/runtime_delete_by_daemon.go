package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type deleteRuntimesByDaemonRequest struct {
	DaemonID string `json:"daemon_id"`
}

type archiveAgentsAndDeleteRuntimesByDaemonRequest struct {
	DaemonID               string   `json:"daemon_id"`
	ExpectedActiveAgentIDs []string `json:"expected_active_agent_ids"`
}

// DeleteAgentRuntimesByDaemon is the Computer-level strict delete: tear down
// every runtime sharing a daemon_id in one transaction. Refuses with 409 +
// aggregated active_agents when any non-archived agent is still bound (same
// code as the single-runtime DELETE) so the front-end can pivot to the
// cascade dialog. Empty-bind / Offline machines succeed and return the
// deleted runtime ids — this is the LRM-438 bulk contract Frank asked for
// ("一键删除").
func (h *Handler) DeleteAgentRuntimesByDaemon(w http.ResponseWriter, r *http.Request) {
	var req deleteRuntimesByDaemonRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	daemonID := strings.TrimSpace(req.DaemonID)
	if daemonID == "" {
		writeError(w, http.StatusBadRequest, "daemon_id is required")
		return
	}

	workspaceID := h.resolveWorkspaceID(r)
	member, ok := h.requireWorkspaceMember(w, r, workspaceID, "workspace not found")
	if !ok {
		return
	}
	userID := uuidToString(member.UserID)

	runtimes, err := h.listAgentRuntimesByDaemonID(r.Context(), parseUUID(workspaceID), daemonID)
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

	var blockingAgents []db.Agent
	for _, rt := range runtimes {
		agents, err := h.Queries.ListActiveAgentsByRuntime(r.Context(), rt.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to check runtime dependencies")
			return
		}
		blockingAgents = append(blockingAgents, agents...)
	}
	if len(blockingAgents) > 0 {
		writeJSON(w, http.StatusConflict, runtimeHasActiveAgentsResponse(blockingAgents))
		return
	}

	for _, rt := range runtimes {
		activeSquadCount, err := h.Queries.CountActiveSquadsWithArchivedLeadersByRuntime(r.Context(), rt.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to check runtime squad dependencies")
			return
		}
		if activeSquadCount > 0 {
			writeError(w, http.StatusConflict, "cannot delete runtime: it has active squads led by archived agents. Archive those squads or assign them a new leader first.")
			return
		}
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
		if err := teardownRuntimeLightTx(r.Context(), qtx, tx, rt); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		deletedIDs = append(deletedIDs, uuidToString(rt.ID))
	}

	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete runtimes")
		return
	}

	slog.Info("runtimes deleted by daemon",
		"daemon_id", daemonID,
		"deleted_by", userID,
		"count", len(deletedIDs),
	)

	h.publish(protocol.EventDaemonRegister, workspaceID, "member", userID, map[string]any{
		"action": "delete",
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"status":              "ok",
		"deleted_runtime_ids": deletedIDs,
	})
}

// ArchiveAgentsAndDeleteRuntimesByDaemon is the Computer-level cascade: archive
// every active agent across all runtimes for a daemon_id, cancel their tasks,
// then delete every runtime row — one transaction. expected_active_agent_ids
// is the machine-wide snapshot the dialog confirmed (same race guard as the
// single-runtime cascade).
func (h *Handler) ArchiveAgentsAndDeleteRuntimesByDaemon(w http.ResponseWriter, r *http.Request) {
	var req archiveAgentsAndDeleteRuntimesByDaemonRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	daemonID := strings.TrimSpace(req.DaemonID)
	if daemonID == "" {
		writeError(w, http.StatusBadRequest, "daemon_id is required")
		return
	}
	expected, ok := parseExpectedActiveAgentIDs(req.ExpectedActiveAgentIDs)
	if !ok {
		writeError(w, http.StatusBadRequest, "expected_active_agent_ids must be a list of valid UUIDs")
		return
	}

	workspaceID := h.resolveWorkspaceID(r)
	member, ok := h.requireWorkspaceMember(w, r, workspaceID, "workspace not found")
	if !ok {
		return
	}
	userID := uuidToString(member.UserID)

	runtimes, err := h.listAgentRuntimesByDaemonID(r.Context(), parseUUID(workspaceID), daemonID)
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

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	var currentActive []db.Agent
	runtimeIDs := make([]pgtype.UUID, 0, len(runtimes))
	for _, rt := range runtimes {
		if _, err := qtx.LockAgentRuntime(r.Context(), rt.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to lock runtime")
			return
		}
		agents, err := qtx.ListActiveAgentsByRuntimeForUpdate(r.Context(), rt.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list active agents")
			return
		}
		currentActive = append(currentActive, agents...)
		runtimeIDs = append(runtimeIDs, rt.ID)
	}

	if !activeAgentSetMatches(currentActive, expected) {
		body := runtimeHasActiveAgentsResponse(currentActive)
		body["code"] = "runtime_delete_plan_changed"
		body["error"] = "the active agent set changed; please review and confirm again."
		writeJSON(w, http.StatusConflict, body)
		return
	}

	currentActiveIDs := make([]pgtype.UUID, len(currentActive))
	for i, a := range currentActive {
		currentActiveIDs[i] = a.ID
	}

	var archivedAgents []db.Agent
	if len(currentActiveIDs) > 0 {
		archivedAgents, err = qtx.ArchiveAgentsByIDs(r.Context(), db.ArchiveAgentsByIDsParams{
			ArchivedBy: member.UserID,
			AgentIds:   currentActiveIDs,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to archive agents")
			return
		}
	}

	archivedIDs := make([]pgtype.UUID, len(archivedAgents))
	for i, a := range archivedAgents {
		archivedIDs[i] = a.ID
	}
	cancelledTasks, err := qtx.CancelAgentTasksByRuntimeOrAgent(r.Context(), db.CancelAgentTasksByRuntimeOrAgentParams{
		RuntimeIds: runtimeIDs,
		AgentIds:   archivedIDs,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel tasks")
		return
	}

	deletedIDs := make([]string, 0, len(runtimes))
	for _, rt := range runtimes {
		allArchivedIDs, err := qtx.ListArchivedAgentIDsByRuntime(r.Context(), rt.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to enumerate archived agents")
			return
		}
		if len(allArchivedIDs) > 0 {
			if err := qtx.PauseAutopilotsByAgentAssignees(r.Context(), allArchivedIDs); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to pause autopilots")
				return
			}
		}
		if err := qtx.DeleteSquadsByArchivedAgentsOnRuntime(r.Context(), rt.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to clean up squads referencing archived agents")
			return
		}
		if err := qtx.DeleteArchivedAgentsByRuntime(r.Context(), rt.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to clean up archived agents")
			return
		}
		if _, err := tx.Exec(r.Context(), `
			UPDATE memory_curation_run
			   SET status = 'failed', error = 'runtime deleted', finished_at = now()
			 WHERE runtime_id = $1 AND status IN ('queued', 'waiting_runtime', 'running')
		`, rt.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to clean up memory curation runs")
			return
		}
		if err := qtx.DeleteAgentRuntime(r.Context(), rt.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to delete runtime")
			return
		}
		deletedIDs = append(deletedIDs, uuidToString(rt.ID))
	}

	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit transaction")
		return
	}

	if h.TaskService != nil && len(cancelledTasks) > 0 {
		h.TaskService.BroadcastCancelledTasks(r.Context(), cancelledTasks)
	}
	for _, a := range archivedAgents {
		h.publish(protocol.EventAgentArchived, workspaceID, "member", userID, map[string]any{
			"agent": agentToResponse(a),
		})
	}
	h.publish(protocol.EventDaemonRegister, workspaceID, "member", userID, map[string]any{
		"action": "delete",
	})

	slog.Info("runtimes deleted via daemon cascade",
		"daemon_id", daemonID,
		"deleted_by", userID,
		"runtimes", len(deletedIDs),
		"agents_archived", len(archivedAgents),
		"tasks_cancelled", len(cancelledTasks),
	)

	writeJSON(w, http.StatusOK, map[string]any{
		"status":              "ok",
		"deleted_runtime_ids": deletedIDs,
		"agents_archived":     len(archivedAgents),
		"tasks_cancelled":     len(cancelledTasks),
	})
}

func (h *Handler) listAgentRuntimesByDaemonID(ctx context.Context, workspaceID pgtype.UUID, daemonID string) ([]db.AgentRuntime, error) {
	rows, err := h.DB.Query(ctx, `
		SELECT id, workspace_id, daemon_id, name, runtime_mode, provider, status,
		       device_info, metadata, last_seen_at, created_at, updated_at,
		       owner_id, legacy_daemon_id, visibility
		FROM agent_runtime
		WHERE workspace_id = $1
		  AND LOWER(daemon_id) = LOWER($2)
		ORDER BY provider ASC, created_at ASC`, workspaceID, daemonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []db.AgentRuntime
	for rows.Next() {
		var rt db.AgentRuntime
		if err := rows.Scan(
			&rt.ID, &rt.WorkspaceID, &rt.DaemonID, &rt.Name, &rt.RuntimeMode,
			&rt.Provider, &rt.Status, &rt.DeviceInfo, &rt.Metadata, &rt.LastSeenAt,
			&rt.CreatedAt, &rt.UpdatedAt, &rt.OwnerID, &rt.LegacyDaemonID, &rt.Visibility,
		); err != nil {
			return nil, err
		}
		out = append(out, rt)
	}
	return out, rows.Err()
}

// teardownRuntimeLightTx performs the post-guard teardown for a single runtime
// whose active-agent and archived-leader-squad checks already passed. Shared by
// single-runtime DELETE and Computer-level delete-by-daemon.
func teardownRuntimeLightTx(ctx context.Context, qtx *db.Queries, tx pgx.Tx, rt db.AgentRuntime) error {
	archivedAgentIDs, err := qtx.ListArchivedAgentIDsByRuntime(ctx, rt.ID)
	if err != nil {
		return errString("failed to enumerate archived agents")
	}
	if len(archivedAgentIDs) > 0 {
		if err := qtx.PauseAutopilotsByAgentAssignees(ctx, archivedAgentIDs); err != nil {
			return errString("failed to pause autopilots")
		}
	}
	if err := qtx.DeleteSquadsByArchivedAgentsOnRuntime(ctx, rt.ID); err != nil {
		return errString("failed to clean up squads referencing archived agents")
	}
	if err := qtx.DeleteArchivedAgentsByRuntime(ctx, rt.ID); err != nil {
		return errString("failed to clean up archived agents")
	}
	if _, err := tx.Exec(ctx, `
		UPDATE memory_curation_run
		   SET status = 'failed', error = 'runtime deleted', finished_at = now()
		 WHERE runtime_id = $1 AND status IN ('queued', 'waiting_runtime', 'running')
	`, rt.ID); err != nil {
		return errString("failed to clean up memory curation runs")
	}
	if err := qtx.DeleteAgentRuntime(ctx, rt.ID); err != nil {
		return errString("failed to delete runtime")
	}
	return nil
}

type errString string

func (e errString) Error() string { return string(e) }
