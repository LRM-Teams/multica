package handler

import (
	"context"
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
func (h *Handler) DeleteEnvDispatchChannel(w http.ResponseWriter, r *http.Request) {
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
	projectID, envID, err := h.resolveChannelProject(r.Context(), workspaceID, channelID)
	if err != nil {
		// Already absent: idempotent success.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := h.deleteEnvDispatchChannelRollout(r.Context(), workspaceID, channelID, projectID, envID); err != nil {
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
func (h *Handler) deleteEnvDispatchChannelRollout(ctx context.Context, workspaceID, channelID, projectID, envID string) error {
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
	for _, b := range bindings {
		if b.Status != "ready" || b.SandboxInstanceID == nil {
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
		if lifecycle != nil {
			if derr := lifecycle.Delete(ctx, service.SandboxInstanceRef{WorkspaceID: workspaceID, InstanceID: *b.SandboxInstanceID}, ""); derr != nil {
				slog.Warn("env-dispatch channel cleanup: delete sandbox", "instance_id", *b.SandboxInstanceID, "error", derr)
			}
		}
		if b.RuntimeID != nil {
			_ = adapter.DeleteAgentRuntime(ctx, workspaceID, *b.RuntimeID)
		}
		// Nothing to tear down on the AReaL side: v2 has no end_session, and a
		// session is reclaimed there by export_trajectories(remove_session) or
		// the stale-session reaper once this rollout stops driving it.
	}
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
