package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/computer"
	storedb "github.com/multica-ai/multica/server/pkg/db"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// DeleteComputer permanently removes a Computer from one Workspace.
// It deletes that Workspace's runtime projection and revokes the matching
// Computer Binding in one transaction. Sibling Workspace Bindings and local
// files are outside this operation. Local computers receive a durable
// registration tombstone before their runtime rows disappear, so a
// still-running daemon cannot recreate the machine through its next heartbeat.
//
// Active agents are a hard precondition: the endpoint returns their current
// list and makes no change. Once the computer is empty, Binding revocation,
// tombstone registration, token revocation, stale-work cancellation, and
// provider-runtime deletion happen atomically. A Binding-only Computer with no
// runtime rows follows the same operation and disappears without a ghost row.
//
// Route: DELETE /api/computers/{daemonId}
func (h *Handler) DeleteComputer(w http.ResponseWriter, r *http.Request) {
	daemonID := strings.TrimSpace(chi.URLParam(r, "daemonId"))
	if daemonID == "" {
		writeError(w, http.StatusBadRequest, "daemon_id is required")
		return
	}
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

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete runtimes")
		return
	}
	defer tx.Rollback(context.Background())
	if err := lockDaemonRegistration(r.Context(), tx, workspaceID, daemonID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to lock computer registration")
		return
	}
	qtx := h.Queries.WithTx(tx)

	runtimes, err := qtx.ListAgentRuntimesByDaemonID(r.Context(), db.ListAgentRuntimesByDaemonIDParams{
		WorkspaceID: parseUUID(workspaceID),
		DaemonID:    daemonID,
		RuntimeMode: "",
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list runtimes for daemon")
		return
	}
	bindingService := &computer.BindingService{Store: storedb.NewBindingStore(tx)}
	bindings, err := bindingService.All(daemonID, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read Computer connection")
		return
	}
	hasWorkspaceBinding := false
	for _, binding := range bindings {
		if binding.WorkspaceID == workspaceID && binding.Active {
			hasWorkspaceBinding = true
			break
		}
	}
	if len(runtimes) == 0 && !hasWorkspaceBinding {
		writeError(w, http.StatusNotFound, "computer not found")
		return
	}

	for _, rt := range runtimes {
		if !canDeleteRuntime(member, rt) {
			writeError(w, http.StatusForbidden, "you can only delete your own runtimes")
			return
		}
	}

	runtimeIDs := sortedRuntimeIDs(runtimes)

	// Lock provider runtimes in deterministic UUID order. Besides serializing
	// concurrent removals, the parent-row lock blocks a concurrent agent bind:
	// agent.runtime_id FK validation needs a conflicting FOR KEY SHARE lock.
	for _, runtimeID := range runtimeIDs {
		if _, err := qtx.LockAgentRuntime(r.Context(), runtimeID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to lock runtime")
			return
		}
	}

	var currentActive []db.Agent
	if len(runtimeIDs) > 0 {
		currentActive, err = qtx.ListActiveAgentsByRuntimesForUpdate(r.Context(), runtimeIDs)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to enumerate active agents")
			return
		}
	}
	if len(currentActive) > 0 {
		body := runtimeHasActiveAgentsResponse(currentActive)
		body["code"] = "computer_has_active_agents"
		body["error"] = "remove every agent from this computer before deleting it."
		body["daemon_id"] = daemonID
		writeJSON(w, http.StatusConflict, body)
		return
	}

	bindingTokenHashes := make([]string, 0)
	if hasWorkspaceBinding {
		bindingTokenHashes, err = bindingService.Revoke(
			computer.BindingRequest{
				ActorUserID:             requestUserID(r),
				ActorCanManageWorkspace: roleAllowed(member.Role, "owner", "admin"),
				TargetComputerID:        daemonID,
				TargetWorkspaceID:       workspaceID,
			},
			workspaceID,
		)
		if err != nil {
			if errors.Is(err, computer.ErrBindingUnauthorized) {
				writeError(w, http.StatusForbidden, "Computer is not owned by the current user")
			} else {
				writeError(w, http.StatusInternalServerError, "failed to revoke Computer connection")
			}
			return
		}
	}

	normalizedDaemonID := strings.ToLower(daemonID)
	shouldTombstone := len(runtimes) == 0 && hasWorkspaceBinding
	for _, rt := range runtimes {
		if rt.RuntimeMode == "local" {
			shouldTombstone = true
			break
		}
	}
	if shouldTombstone {
		if _, err := qtx.UpsertDaemonRegistrationTombstone(r.Context(), db.UpsertDaemonRegistrationTombstoneParams{
			WorkspaceID: parseUUID(workspaceID),
			DaemonID:    normalizedDaemonID,
			RemovedBy:   member.UserID,
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to revoke computer registration")
			return
		}
	}

	revokedTokenHashes, err := qtx.DeleteDaemonTokensByWorkspaceAndDaemons(r.Context(), db.DeleteDaemonTokensByWorkspaceAndDaemonsParams{
		WorkspaceID: parseUUID(workspaceID),
		DaemonIds:   []string{daemonID},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to revoke computer credentials")
		return
	}
	revokedTokenHashes = append(revokedTokenHashes, bindingTokenHashes...)

	archivedAgentIDs := make([]pgtype.UUID, 0)
	for _, runtimeID := range runtimeIDs {
		ids, err := qtx.ListArchivedAgentIDsByRuntime(r.Context(), runtimeID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to enumerate archived agents")
			return
		}
		archivedAgentIDs = append(archivedAgentIDs, ids...)
	}
	cancelledTasks, err := qtx.CancelAgentTasksByRuntimeOrAgent(r.Context(), db.CancelAgentTasksByRuntimeOrAgentParams{
		RuntimeIds: runtimeIDs,
		AgentIds:   archivedAgentIDs,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel active work")
		return
	}
	if err := qtx.DeleteDaemonUpdateStatus(r.Context(), db.DeleteDaemonUpdateStatusParams{
		WorkspaceID: parseUUID(workspaceID),
		DaemonID:    daemonID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete computer update status")
		return
	}

	deletedIDs := make([]string, 0, len(runtimes))
	for _, runtimeID := range runtimeIDs {
		if err := teardownRuntimeWithoutActiveAgents(r.Context(), qtx, tx, runtimeID); err != nil {
			slog.Error("computer bulk delete teardown failed",
				"daemon_id", daemonID,
				"runtime_id", uuidToString(runtimeID),
				"error", err,
			)
			writeError(w, http.StatusInternalServerError, "failed to delete runtimes")
			return
		}
		deletedIDs = append(deletedIDs, uuidToString(runtimeID))
	}

	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete runtimes")
		return
	}

	for _, hash := range revokedTokenHashes {
		h.DaemonTokenCache.Invalidate(r.Context(), hash)
	}
	if h.TaskService != nil && len(cancelledTasks) > 0 {
		h.TaskService.BroadcastCancelledTasks(r.Context(), cancelledTasks)
	}
	for _, agentID := range archivedAgentIDs {
		h.publish(protocol.EventAgentDeleted, workspaceID, "member", userID, map[string]any{
			"agent_id": uuidToString(agentID),
		})
	}
	if h.LivenessStore.Available() {
		for _, runtimeID := range runtimeIDs {
			h.LivenessStore.Forget(r.Context(), uuidToString(runtimeID))
		}
	}

	slog.Info("computer runtimes deleted",
		"daemon_id", daemonID,
		"deleted_count", len(deletedIDs),
		"deleted_by", userID,
		"tasks_cancelled", len(cancelledTasks),
		"daemon_tokens_revoked", len(revokedTokenHashes),
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
		"tasks_cancelled":     len(cancelledTasks),
	})
}

func sortedRuntimeIDs(runtimes []db.AgentRuntime) []pgtype.UUID {
	runtimeIDs := make([]pgtype.UUID, len(runtimes))
	for i, rt := range runtimes {
		runtimeIDs[i] = rt.ID
	}
	sort.Slice(runtimeIDs, func(i, j int) bool {
		return uuidToString(runtimeIDs[i]) < uuidToString(runtimeIDs[j])
	})
	return runtimeIDs
}

// teardownRuntimeWithoutActiveAgents runs the shared delete path for a
// runtime that has already been verified to have zero active agents. Caller
// owns the transaction and any row locks.
func teardownRuntimeWithoutActiveAgents(ctx context.Context, qtx *db.Queries, tx pgx.Tx, runtimeID pgtype.UUID) error {
	archivedAgentIDs, err := qtx.ListArchivedAgentIDsByRuntime(ctx, runtimeID)
	if err != nil {
		return err
	}
	if len(archivedAgentIDs) > 0 {
		if err := teardownArchivedAgentDependents(ctx, qtx, archivedAgentIDs); err != nil {
			return err
		}
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

// teardownArchivedAgentDependents resolves the non-cascading ownership edges
// that intentionally survive ordinary agent lifecycle changes but cannot
// survive permanent computer deletion. Caller owns the transaction and has
// already enumerated the exact archived-agent set.
func teardownArchivedAgentDependents(ctx context.Context, qtx *db.Queries, agentIDs []pgtype.UUID) error {
	if len(agentIDs) == 0 {
		return nil
	}
	if err := qtx.CancelRunningAgentExecutionsByAgentIDs(ctx, db.CancelRunningAgentExecutionsByAgentIDsParams{
		FailureReason: pgtype.Text{String: "agent permanently deleted", Valid: true},
		AgentIds:      agentIDs,
	}); err != nil {
		return err
	}
	if err := qtx.DeleteMixedRLRunsByAgentIDs(ctx, agentIDs); err != nil {
		return err
	}
	if err := qtx.DeleteMixedRLDeliveryObligationsBySourceAgentIDs(ctx, agentIDs); err != nil {
		return err
	}
	if err := qtx.DetachDerivedAgentsFromSources(ctx, agentIDs); err != nil {
		return err
	}
	if err := qtx.DeleteLegacySquadsByLeaderIDs(ctx, agentIDs); err != nil {
		return err
	}
	return qtx.DeleteVoiceCallSessionsByAgentIDs(ctx, agentIDs)
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
