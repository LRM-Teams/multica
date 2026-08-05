// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// maybeStartSharedDispatchDiagnosis starts the internal, non-roster diagnosis
// task after a business env-dispatch task becomes terminal. It deliberately
// performs no work for ordinary, training, non-shared, or diagnosis tasks.
// A previously bound shared run is never enqueued again; the state store
// resumes an unbound run after a process interruption.
func (h *Handler) maybeStartSharedDispatchDiagnosis(ctx context.Context, event db.AgentInboxEvent) {
	if h.DB == nil || h.Queries == nil || !event.ChatSessionID.Valid || !event.InitiatorUserID.Valid {
		return
	}
	if h.diagnosisRunIDForInboxEvent(ctx, event) != "" {
		return
	}

	var projectID string
	if err := h.DB.QueryRow(ctx,
		`SELECT project_id::text FROM chat_session WHERE id = $1 AND project_id IS NOT NULL`, event.ChatSessionID,
	).Scan(&projectID); err != nil {
		return
	}
	var rootTaskID string
	var trainingMode bool
	if err := h.DB.QueryRow(ctx, `
SELECT root_task_id::text, training_mode
FROM env_dispatch_run
WHERE project_id = $1 AND workspace_id = $2 AND root_task_id IS NOT NULL`, projectID, event.WorkspaceID,
	).Scan(&rootTaskID, &trainingMode); err != nil || trainingMode {
		return
	}
	workspaceID := util.UUIDToString(event.WorkspaceID)
	binding, err := h.resolveSharedDiagnosisBinding(ctx, workspaceID, projectID)
	if err != nil {
		slog.Warn("shared diagnosis terminal barrier: invalid binding", "project_id", projectID, "error", err)
		return
	}
	if binding == nil {
		return
	}

	// Only business tasks participate. The internal diagnosis task always has
	// the stamped context key and is excluded even if a future trigger races it.
	var businessInFlight bool
	err = h.DB.QueryRow(ctx, `
SELECT EXISTS(
  SELECT 1
  FROM agent_inbox_event e
  JOIN chat_session s ON s.id = e.chat_session_id
  WHERE s.project_id = $1
    AND e.context->>'diagnosis_run_id' IS NULL
    AND e.status NOT IN ('acked', 'failed', 'cancelled', 'suppressed')
)`, projectID).Scan(&businessInFlight)
	if err != nil || businessInFlight {
		return
	}

	dag, err := service.NewInteractionDAGService(h.Queries, nil, true).AssembleAssembledDag(ctx, projectID)
	if err != nil || !denseCover(dag) || len(dag.Segments) == 0 {
		slog.Warn("shared diagnosis terminal barrier: DAG is not ready", "project_id", projectID, "error", err)
		return
	}
	segmentIDs, err := diagnosisTopologicalSegmentIDs(dag)
	if err != nil {
		slog.Warn("shared diagnosis terminal barrier: invalid DAG", "project_id", projectID, "error", err)
		return
	}
	state := service.NewDiagnosisStateStore(h.Queries)
	run, _, completed, err := service.CreateOrResumeDiagnosisRun(ctx, state, projectID, rootTaskID, segmentIDs)
	if err != nil || completed {
		if err != nil {
			slog.Warn("shared diagnosis terminal barrier: create/resume run", "project_id", projectID, "error", err)
		}
		return
	}
	// Any persisted sandbox binding means an earlier trigger has already begun
	// provisioning. Re-enqueueing would violate the one internal task invariant.
	if run.SandboxMode == service.DiagnosisSandboxModeShared || run.SandboxInstanceID != "" {
		return
	}

	cfg := service.LoadTrainingConfig()
	runner, err := service.NewDiagnosisAgentRunner(service.DiagnosisAgentConfig{
		Provider: "pi", ExecutablePath: cfg.DiagnosisAgentPath, Model: cfg.DiagnosisAgentModel,
		Timeout: cfg.DiagnosisAgentTimeout, ScoreMax: cfg.DiagnosisAgentScoreMax,
		DAGStore: h.Queries, MessageStore: h.Queries,
	})
	if err != nil {
		_ = state.FailRun(ctx, run.RunID, fmt.Errorf("provisioning: initialize diagnosis runner: %w", err))
		return
	}
	bootstrap, err := runner.PrepareSandboxBootstrap(ctx, state, diagnosisDAGWriterAdapter{store: h.Queries}, projectID, run, segmentIDs)
	if err != nil {
		_ = state.FailRun(ctx, run.RunID, fmt.Errorf("provisioning: freeze segment targets: %w", err))
		return
	}
	orchestrator := h.newDiagnosisSandboxOrchestrator()
	if orchestrator == nil {
		_ = state.FailRun(ctx, run.RunID, errors.New("provisioning: diagnosis sandbox unavailable"))
		return
	}
	req := service.DiagnosisProvisionRequest{
		RunID:           run.RunID,
		WorkspaceID:     workspaceID,
		ProjectID:       projectID,
		ActorUserID:     util.UUIDToString(event.InitiatorUserID),
		BootstrapPrompt: bootstrap.BootstrapPrompt,
		SystemPrompt:    bootstrap.SystemPrompt,
		Model:           cfg.DiagnosisAgentModel,
		SharedSandbox:   binding,
	}
	if err := orchestrator.ProvisionRun(ctx, req); err != nil {
		slog.Warn("shared diagnosis terminal barrier: provisioning failed", "run_id", run.RunID, "error", err)
	}
}
