// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// diagnosisSandboxRuntimeReadinessTimeout bounds how long provisioning waits
// for the in-sandbox daemon to register its online Pi runtime (mirrors
// envDispatchRuntimeReadinessTimeout).
const diagnosisSandboxRuntimeReadinessTimeout = 2 * time.Minute

// newDiagnosisSandboxOrchestrator builds the production orchestrator, or nil
// when the handler lacks the sandbox wiring (test fixtures without Queries).
func (h *Handler) newDiagnosisSandboxOrchestrator() *service.DiagnosisSandboxOrchestrator {
	lifecycle := newEnvSandboxLifecycleService(h)
	if lifecycle == nil || h.DB == nil {
		return nil
	}
	orchestrator, err := service.NewDiagnosisSandboxOrchestrator(service.DiagnosisSandboxOrchestratorConfig{
		State:     service.NewDiagnosisStateStore(h.Queries),
		Sandboxes: lifecycle,
		Resolver:  diagnosisSandboxRuntimeResolver{h: h},
		Pusher:    diagnosisExtensionPusher{h: h},
		Enqueuer:  diagnosisWorkEnqueuer{h: h},
		Reclaimer: diagnosisSandboxReclaimer{lifecycle: lifecycle},
	})
	if err != nil {
		return nil
	}
	return orchestrator
}

// ── Runtime resolver ──

// diagnosisSandboxRuntimeResolver resolves the sandbox's online pi runtime.
// Fresh provisioning matches by (workspace, daemon nonce, sandbox_instance_id)
// via the shared discovery poller; reattach matches by sandbox_instance_id
// alone because the daemon nonce is not persisted on the run row.
type diagnosisSandboxRuntimeResolver struct{ h *Handler }

func (a diagnosisSandboxRuntimeResolver) WaitOnline(ctx context.Context, workspaceID, daemonID, sandboxInstanceID string) (service.RuntimeRef, error) {
	if daemonID != "" {
		return service.WaitForOnlineSandboxRuntime(ctx, &envSandboxLifecycleDepsAdapter{h: a.h}, workspaceID, daemonID, sandboxInstanceID, diagnosisSandboxRuntimeReadinessTimeout)
	}
	return waitOnlineDiagnosisRuntimeBySandbox(ctx, a.h.DB, workspaceID, sandboxInstanceID, diagnosisSandboxRuntimeReadinessTimeout)
}

// waitOnlineDiagnosisRuntimeBySandbox polls for the online pi runtime bound to
// a sandbox instance with the same fail-closed semantics as
// service.WaitForOnlineSandboxRuntime (100ms interval, sanitized timeout).
func waitOnlineDiagnosisRuntimeBySandbox(ctx context.Context, exec db.DBTX, workspaceID, sandboxInstanceID string, timeout time.Duration) (service.RuntimeRef, error) {
	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		rt, err := findOnlineDiagnosisRuntimeBySandbox(deadlineCtx, exec, workspaceID, sandboxInstanceID)
		if err == nil {
			return rt, nil
		}
		if !errors.Is(err, service.ErrSandboxRuntimeNotOnline) {
			return service.RuntimeRef{}, fmt.Errorf("resolve sandbox runtime: %w", err)
		}
		select {
		case <-deadlineCtx.Done():
			return service.RuntimeRef{}, fmt.Errorf("runtime readiness timeout")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// findOnlineDiagnosisRuntimeBySandbox resolves the online pi runtime for a
// sandbox instance without the daemon nonce (reattach path). Raw SQL mirrors
// findOnlineSandboxRuntime; returns service.ErrSandboxRuntimeNotOnline while
// no matching runtime has registered.
func findOnlineDiagnosisRuntimeBySandbox(ctx context.Context, exec db.DBTX, workspaceID, sandboxInstanceID string) (service.RuntimeRef, error) {
	const q = `
SELECT id::text, workspace_id::text, daemon_id::text, provider, status,
       metadata->>'sandbox_instance_id' AS sandbox_instance_id
FROM agent_runtime
WHERE workspace_id = $1
  AND provider = 'pi'
  AND status = 'online'
  AND metadata->>'sandbox_instance_id' = $2
LIMIT 1`
	var rt service.RuntimeRef
	err := exec.QueryRow(ctx, q, workspaceID, sandboxInstanceID).Scan(
		&rt.ID, &rt.WorkspaceID, &rt.DaemonID, &rt.Provider, &rt.Status, &rt.SandboxInstanceID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return service.RuntimeRef{}, service.ErrSandboxRuntimeNotOnline
		}
		return service.RuntimeRef{}, fmt.Errorf("find online sandbox runtime: %w", err)
	}
	return rt, nil
}

// ── Extension pusher ──

// diagnosisExtensionPusher delivers the generated extension source into the
// sandbox via the daemonws workdir write RPC. The RPC is edit-only (it cannot
// create files), so the diagnosis sandbox image must bake a placeholder at the
// push target; a Missing response maps to service.ErrDiagnosisExtensionMissing
// and the orchestrator falls back to the image-baked extension.
type diagnosisExtensionPusher struct{ h *Handler }

func (p diagnosisExtensionPusher) PushExtension(ctx context.Context, runtimeID, relPath, filePath, content string) error {
	if p.h.DaemonHub == nil {
		return fmt.Errorf("daemon hub unavailable")
	}
	rpcCtx, cancel := context.WithTimeout(ctx, agentFileRPCTimeout)
	defer cancel()
	resp, err := p.h.DaemonHub.RequestWriteFile(rpcCtx, protocol.WriteWorkdirFileRequestPayload{
		RequestID: uuid.NewString(),
		RuntimeID: runtimeID,
		RelPath:   relPath,
		FilePath:  filePath,
		Content:   content,
	})
	if err != nil {
		return err
	}
	switch {
	case resp.Missing:
		return service.ErrDiagnosisExtensionMissing
	case resp.Error != "":
		return fmt.Errorf("write extension file: %s", resp.Error)
	case resp.Conflict, resp.TooLarge, resp.Binary:
		return fmt.Errorf("write extension file rejected")
	}
	return nil
}

// ── Work enqueuer ──

// diagnosisWorkEnqueuer delivers the diagnosis bootstrap prompt through a
// direct agent-inbox enqueue against the sandbox runtime: a per-run diagnosis
// agent row carries the per-run credentials in custom_env (the established
// secret channel the daemon injects into the pi process at launch), one
// project chat session holds the single user-role prompt message, and one
// chat task routes the work to the discovered runtime. This replaces the
// server-mode process-wide os.Setenv — env is scoped to the per-run agent
// and injected by the daemon at task execution, never process-wide.
type diagnosisWorkEnqueuer struct{ h *Handler }

func (e diagnosisWorkEnqueuer) EnqueueDiagnosisWork(ctx context.Context, work service.DiagnosisWorkItem) error {
	agentID, err := e.ensureDiagnosisAgent(ctx, work)
	if err != nil {
		return err
	}
	session, err := e.h.Queries.CreateChatSessionForProject(ctx, db.CreateChatSessionForProjectParams{
		WorkspaceID: parseUUID(work.WorkspaceID),
		ProjectID:   parseUUID(work.ProjectID),
		AgentID:     agentID,
		CreatorID:   parseUUID(work.ActorUserID),
		Title:       "diagnosis-" + work.RunID,
	})
	if err != nil {
		return fmt.Errorf("create diagnosis chat session: %w", err)
	}
	var taskContext []byte
	// Stamp the run ID into the task context so the terminal mapping (T022)
	// can attribute the daemon-reported task outcome to this run without a
	// database lookup; the per-run agent name is the fallback identifier.
	contextJSON, err := json.Marshal(map[string]string{service.DiagnosisRunIDContextKey: work.RunID})
	if err != nil {
		return fmt.Errorf("stamp diagnosis run id: %w", err)
	}
	if work.Model != "" {
		taskContext, err = service.WithTaskExecutionConfig(contextJSON, work.Model, "")
		if err != nil {
			return fmt.Errorf("snapshot diagnosis task execution config: %w", err)
		}
	} else {
		taskContext = contextJSON
	}
	task, err := e.h.Queries.CreateChatTask(ctx, db.CreateChatTaskParams{
		AgentID:         agentID,
		RuntimeID:       parseUUID(work.RuntimeID),
		Priority:        envDispatchTaskPriority,
		ChatSessionID:   session.ID,
		InitiatorUserID: parseUUID(work.ActorUserID),
		Context:         taskContext,
	})
	if err != nil {
		return fmt.Errorf("create diagnosis task: %w", err)
	}
	if _, err := e.h.Queries.CreateChatMessage(ctx, db.CreateChatMessageParams{
		ChatSessionID: session.ID,
		Role:          "user",
		Content:       work.BootstrapPrompt,
		TaskID:        task.ID,
	}); err != nil {
		return fmt.Errorf("create diagnosis prompt message: %w", err)
	}
	if e.h.DaemonHub != nil {
		e.h.DaemonHub.NotifyTaskAvailable(work.RuntimeID, util.UUIDToString(task.ID))
	}
	return nil
}

// ensureDiagnosisAgent upserts the per-run diagnosis agent (named after the
// run). A fresh provision (work.Env set) refreshes custom_env, the runtime
// binding, and instructions; a reattach (work.Env nil) reuses the stored row
// so the original capability token — which only ever existed in custom_env —
// stays valid for the resumed session.
func (e diagnosisWorkEnqueuer) ensureDiagnosisAgent(ctx context.Context, work service.DiagnosisWorkItem) (pgtype.UUID, error) {
	name := service.DiagnosisAgentNamePrefix + work.RunID
	var agentID pgtype.UUID
	lookupErr := e.h.DB.QueryRow(ctx,
		`SELECT id FROM agent WHERE workspace_id = $1 AND name = $2 AND archived_at IS NULL`,
		work.WorkspaceID, name).Scan(&agentID)
	if lookupErr != nil && !errors.Is(lookupErr, pgx.ErrNoRows) {
		return pgtype.UUID{}, fmt.Errorf("lookup diagnosis agent: %w", lookupErr)
	}
	if work.Env == nil {
		if errors.Is(lookupErr, pgx.ErrNoRows) {
			return pgtype.UUID{}, fmt.Errorf("diagnosis agent for run %s is missing; cannot reattach without the per-run credentials", work.RunID)
		}
		return agentID, nil
	}
	customEnv, err := json.Marshal(work.Env)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("encode diagnosis agent env: %w", err)
	}
	if lookupErr == nil {
		if _, err := e.h.DB.Exec(ctx, `
UPDATE agent
SET custom_env = $2, runtime_id = $3, instructions = $4, updated_at = now()
WHERE id = $1`, agentID, customEnv, parseUUID(work.RuntimeID), work.SystemPrompt); err != nil {
			return pgtype.UUID{}, fmt.Errorf("refresh diagnosis agent: %w", err)
		}
		return agentID, nil
	}
	agent, err := e.h.Queries.CreateAgent(ctx, db.CreateAgentParams{
		WorkspaceID:   parseUUID(work.WorkspaceID),
		Name:          name,
		DisplayName:   "Diagnosis " + work.RunID,
		Description:   "Per-run sandboxed diagnosis agent (spec 005); reclaimed with its sandbox.",
		RuntimeMode:   "local",
		RuntimeConfig: []byte("{}"),
		RuntimeID:     parseUUID(work.RuntimeID),
		OwnerID:       parseUUID(work.ActorUserID),
		Instructions:  work.SystemPrompt,
		CustomEnv:     customEnv,
		Model:         pgtype.Text{String: work.Model, Valid: work.Model != ""},
	})
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("create diagnosis agent: %w", err)
	}
	return agent.ID, nil
}

// ── Reclaimer ──

// diagnosisSandboxReclaimer deletes a run's sandbox through the env-sandbox
// lifecycle (sandboxd delete job, with the force-delete fallback when no
// sandboxd node is available) — the same reclaim pattern as the
// ephemeral-sandbox manager. An already-gone instance is success.
type diagnosisSandboxReclaimer struct {
	lifecycle *service.EnvSandboxLifecycleService
}

func (r diagnosisSandboxReclaimer) ReclaimDiagnosisSandbox(ctx context.Context, workspaceID, sandboxInstanceID string) error {
	ref, err := r.lifecycle.GetSandboxInstanceRef(ctx, workspaceID, sandboxInstanceID)
	if err != nil {
		return nil
	}
	return r.lifecycle.Delete(ctx, ref, ref.CreatorUserID)
}

// reclaimDiagnosisRunSandbox enqueues the sandbox delete for a terminal
// sandbox-mode run. It resolves the workspace from the run's project (the run
// row does not carry it) and never blocks the caller on reclaim failures —
// they are logged for the leak audit instead.
func (h *Handler) reclaimDiagnosisRunSandbox(ctx context.Context, run service.DiagnosisRunCheckpoint) {
	if run.SandboxMode == service.DiagnosisSandboxModeShared {
		// The env-dispatch channel cleanup owns the team sandbox and runs only
		// after this shared diagnosis reaches a terminal state.
		return
	}
	if run.ExecutionMode != service.DiagnosisExecutionModeSandbox || run.SandboxInstanceID == "" {
		return
	}
	lifecycle := newEnvSandboxLifecycleService(h)
	if lifecycle == nil {
		return
	}
	project, err := h.Queries.GetProject(ctx, parseUUID(run.ProjectID))
	if err != nil {
		slog.Warn("diagnosis sandbox reclaim: resolve project workspace failed",
			"run_id", run.RunID, "error", err)
		return
	}
	reclaimer := diagnosisSandboxReclaimer{lifecycle: lifecycle}
	if err := reclaimer.ReclaimDiagnosisSandbox(context.WithoutCancel(ctx), util.UUIDToString(project.WorkspaceID), run.SandboxInstanceID); err != nil {
		slog.Warn("diagnosis sandbox reclaim: delete failed",
			"run_id", run.RunID, "sandbox_instance_id", run.SandboxInstanceID, "error", err)
		return
	}
	slog.Info("diagnosis sandbox reclaim: reclaim requested",
		"run_id", run.RunID, "sandbox_instance_id", run.SandboxInstanceID)
}
