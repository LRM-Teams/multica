package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// resolveChannelProject resolves the project (and its env) bound to an
// EnvDispatch group channel. It verifies workspace access and that the channel
// has a project_id (EnvDispatch channels always do; ordinary channels do not).
// A missing channel or a channel with no bound project returns a "not found"
// error the caller maps to 404 - this is what makes channel cleanup idempotent.
func (h *Handler) resolveChannelProject(ctx context.Context, workspaceID, channelID string) (projectID, envID string, err error) {
	var pid pgtype.Text
	if err := h.DB.QueryRow(ctx,
		`SELECT project_id::text FROM channel WHERE id = $1 AND workspace_id = $2`,
		channelID, workspaceID,
	).Scan(&pid); err != nil {
		return "", "", fmt.Errorf("not found: %w", err)
	}
	if !pid.Valid {
		return "", "", fmt.Errorf("not found: channel has no bound project")
	}
	projectID = pid.String
	var eid pgtype.Text
	if err := h.DB.QueryRow(ctx,
		`SELECT env_id::text FROM project WHERE id = $1 AND workspace_id = $2`,
		projectID, workspaceID,
	).Scan(&eid); err != nil {
		return projectID, "", fmt.Errorf("not found: %w", err)
	}
	if !eid.Valid {
		return projectID, "", fmt.Errorf("not found: project has no bound env")
	}
	return projectID, eid.String, nil
}

// GetEnvDispatchChannelDag handles GET /api/v1/env-dispatch/channels/{channelID}/dag.
// It resolves the bound project internally and reuses the project-scoped DAG
// helper; no internal HTTP request is made.
func (h *Handler) GetEnvDispatchChannelDag(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace ID required")
		return
	}
	channelID := chi.URLParam(r, "channelID")
	if _, ok := parseUUIDOrBadRequest(w, channelID, "channelID"); !ok {
		return
	}
	projectID, _, err := h.resolveChannelProject(r.Context(), workspaceID, channelID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
		return
	}
	h.getEnvDispatchDagForProject(w, r, projectID)
}

// ListChannelEnvCheckpoints handles GET /api/v1/channels/{channelID}/env-checkpoints.
// It resolves the bound project internally and reuses the project-scoped
// checkpoint list helper.
func (h *Handler) ListChannelEnvCheckpoints(w http.ResponseWriter, r *http.Request) {
	if !envCheckpointsEnabled() {
		writeError(w, http.StatusNotFound, "env checkpoints are not enabled")
		return
	}
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace ID required")
		return
	}
	channelID := chi.URLParam(r, "channelID")
	if _, ok := parseUUIDOrBadRequest(w, channelID, "channelID"); !ok {
		return
	}
	projectID, _, err := h.resolveChannelProject(r.Context(), workspaceID, channelID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
		return
	}
	h.listEnvCheckpointsForProject(w, r, projectID)
}

// DeleteEnvDispatchChannel handles DELETE /api/v1/env-dispatch/channels/{channelID}.
// It resolves the bound project+env and performs concurrency-safe rollout
// cleanup. Idempotent: a missing channel returns 204.
// errSharedDiagnosisInProgress prevents channel cleanup from racing the
// diagnosis task that borrows the team sandbox.
var errSharedDiagnosisInProgress = errors.New("diagnosis_in_progress")

func (h *Handler) DeleteEnvDispatchChannel(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace ID required")
		return
	}
	channelID := chi.URLParam(r, "channelID")
	if _, ok := parseUUIDOrBadRequest(w, channelID, "channelID"); !ok {
		return
	}
	projectID, envID, err := h.resolveChannelProject(r.Context(), workspaceID, channelID)
	if err != nil {
		// Already absent: idempotent success.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := h.deleteEnvDispatchChannelRollout(r.Context(), workspaceID, actorUserID, channelID, projectID, envID); err != nil {
		if errors.Is(err, errSharedDiagnosisInProgress) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "diagnosis_in_progress"})
			return
		}
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteEnvDispatchChannelRollout performs the concurrency-safe cleanup of an
// EnvDispatch channel rollout. It marks the env's bindings deleting (which
// prevents new provisioning claims), waits for any in-flight provisioning to
// reach a terminal state, reclaims ready sandboxes/runtimes, then removes the
// channel, project, bindings, and env in foreign-key-safe order.
func (h *Handler) deleteEnvDispatchChannelRollout(ctx context.Context, workspaceID, actorUserID, channelID, projectID, envID string) error {
	var active bool
	if err := h.DB.QueryRow(ctx, `
SELECT EXISTS(
  SELECT 1
  FROM interaction_dag_diagnosis_run
  WHERE project_id = $1
    AND sandbox_mode = 'shared'
    AND status IN ('provisioning', 'running', 'compacting')
)`, projectID).Scan(&active); err != nil {
		return fmt.Errorf("lookup shared diagnosis status: %w", err)
	}
	if active {
		return errSharedDiagnosisInProgress
	}
	store := envDispatchChannelStore{}
	if err := store.markDeleting(ctx, h.DB, envID); err != nil {
		return fmt.Errorf("mark bindings deleting: %w", err)
	}
	if err := h.waitForEnvDispatchProvisioning(ctx, envID); err != nil {
		return fmt.Errorf("wait for provisioning: %w", err)
	}
	bindings, err := store.listBindings(ctx, h.DB, envID)
	if err != nil {
		return fmt.Errorf("list bindings: %w", err)
	}
	lifecycle := newEnvSandboxLifecycleService(h)
	adapter := &envDispatchDepsAdapter{h: h}
	// Shared_sandbox rollouts (US3/T022, research D4): N bindings of one env
	// can point at the SAME shared sandbox_instance and agent_runtime, so the
	// reclaim below dedupes — each distinct sandbox/runtime is torn down
	// exactly once. Derived agents stay per-binding: every one is archived
	// FIRST (across all bindings), because they all hold agent.runtime_id
	// (ON DELETE RESTRICT) on the shared runtime and the runtime delete fails
	// until every archived agent is hard-deleted. Previously the interleaved
	// per-binding order only succeeded by retrying the runtime delete on the
	// last binding after N-1 FK violations.
	var sandboxIDs, runtimeIDs []string
	seenSandbox, seenRuntime := map[string]bool{}, map[string]bool{}
	for _, b := range bindings {
		// markDeleting above moved every ready binding to "deleting", so
		// gating on "ready" alone skipped the whole reclaim below and left the
		// sandbox, runtime, and derived agent behind as orphans no cleanup
		// path could reach afterwards (the bindings are deleted at the end of
		// this function, erasing the only pointer to them). Both states are
		// reclaimable: "deleting" is the mark this cleanup just wrote, and a
		// binding that finished provisioning during
		// waitForEnvDispatchProvisioning is still "ready".
		if (b.Status != "ready" && b.Status != "deleting") || b.SandboxInstanceID == nil {
			continue
		}
		// AC-6 owned-resource cascade (spec: cancel tasks -> archive derived
		// -> delete sandbox -> retire runtime -> close session -> revoke
		// credentials). Already-absent resources are success (idempotent).
		if b.DerivedAgentID != nil && *b.DerivedAgentID != "" {
			derivedUUID := parseUUID(*b.DerivedAgentID)
			if h.TaskService != nil {
				if _, cerr := h.TaskService.CancelTasksForAgent(ctx, derivedUUID); cerr != nil {
					slog.Warn("env-dispatch channel cleanup: cancel derived tasks", "derived_agent_id", *b.DerivedAgentID, "error", cerr)
				}
			}
			// archived_by is nullable; NULL records a system/cleanup archive.
			if _, aerr := h.Queries.ArchiveAgent(ctx, db.ArchiveAgentParams{ID: derivedUUID, ArchivedBy: pgtype.UUID{}}); aerr != nil {
				slog.Warn("env-dispatch channel cleanup: archive derived agent", "derived_agent_id", *b.DerivedAgentID, "error", aerr)
			}
		}
		if !seenSandbox[*b.SandboxInstanceID] {
			seenSandbox[*b.SandboxInstanceID] = true
			sandboxIDs = append(sandboxIDs, *b.SandboxInstanceID)
		}
		if b.RuntimeID != nil && !seenRuntime[*b.RuntimeID] {
			seenRuntime[*b.RuntimeID] = true
			runtimeIDs = append(runtimeIDs, *b.RuntimeID)
		}
	}
	if lifecycle != nil {
		for _, instanceID := range sandboxIDs {
			// The actor must be a real user: Delete enqueues a sandboxd
			// "delete" job whose initiator_user_id is NOT NULL, and it only
			// falls back to a force-delete when the node is unavailable - an
			// empty actor fails to parse and leaves the cube sandbox running.
			if derr := lifecycle.Delete(ctx, service.SandboxInstanceRef{WorkspaceID: workspaceID, InstanceID: instanceID}, actorUserID); derr != nil {
				slog.Warn("env-dispatch channel cleanup: delete sandbox", "instance_id", instanceID, "error", derr)
			}
		}
	}
	for _, runtimeID := range runtimeIDs {
		runtimeUUID := parseUUID(runtimeID)
		// ArchiveAgent above only sets archived_at; the derived agent still
		// holds agent.runtime_id (ON DELETE RESTRICT), so the runtime
		// delete fails until the archived agents are hard-deleted. Same
		// sequence as the runtime cascade endpoint (see runtime.go). Left
		// undone, the runtime row outlives the rollout and the in-sandbox
		// daemon keeps re-registering itself against it.
		archived, aerr := h.Queries.ListArchivedAgentIDsByRuntime(ctx, runtimeUUID)
		if aerr != nil {
			slog.Warn("env-dispatch channel cleanup: list archived agents", "runtime_id", runtimeID, "error", aerr)
		} else if len(archived) > 0 {
			if terr := teardownArchivedAgentDependents(ctx, h.Queries, archived); terr != nil {
				slog.Warn("env-dispatch channel cleanup: teardown archived agent dependents", "runtime_id", runtimeID, "error", terr)
			}
			if derr := h.Queries.DeleteArchivedAgentsByRuntime(ctx, runtimeUUID); derr != nil {
				slog.Warn("env-dispatch channel cleanup: delete archived agents", "runtime_id", runtimeID, "error", derr)
			}
		}
		// Logged, not discarded: a swallowed error here is what kept this
		// leak invisible.
		if rerr := adapter.DeleteAgentRuntime(ctx, workspaceID, runtimeID); rerr != nil {
			slog.Warn("env-dispatch channel cleanup: delete runtime", "runtime_id", runtimeID, "error", rerr)
		}
	}
	// Nothing to tear down on the AReaL side: v2 has no end_session, and a
	// session is reclaimed there by export_trajectories(remove_session) or
	// the stale-session reaper once this rollout stops driving it.
	// Foreign-key-safe order: channel (cascades messages/members/sessions and
	// the bindings' channel_id FK) -> project (cascades DAG/chat/issues) ->
	// environment_agent_sandbox rows (env_id FK backstop) -> environment.
	if _, err := h.DB.Exec(ctx, `DELETE FROM channel WHERE id = $1 AND workspace_id = $2`, channelID, workspaceID); err != nil {
		return fmt.Errorf("delete channel: %w", err)
	}
	if err := adapter.DeleteProject(ctx, projectID, workspaceID); err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	if _, err := h.DB.Exec(ctx, `DELETE FROM environment_agent_sandbox WHERE env_id = $1`, envID); err != nil {
		return fmt.Errorf("delete env-agent bindings: %w", err)
	}
	_ = adapter.DeleteEnv(ctx, envID, workspaceID) // best-effort; may be referenced elsewhere
	return nil
}

// waitForEnvDispatchProvisioning blocks until no binding for the env is in the
// "provisioning" state, so cleanup reclaims the fully-created resources rather
// than racing an in-flight provisioner. Bounded so a stuck provisioner does not
// block cleanup indefinitely; the deleting mark already prevents new claims.
func (h *Handler) waitForEnvDispatchProvisioning(ctx context.Context, envID string) error {
	store := envDispatchChannelStore{}
	for i := 0; i < 100; i++ {
		bindings, err := store.listBindings(ctx, h.DB, envID)
		if err != nil {
			return err
		}
		inFlight := false
		for _, b := range bindings {
			if b.Status == "provisioning" {
				inFlight = true
				break
			}
		}
		if !inFlight {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return nil
}
