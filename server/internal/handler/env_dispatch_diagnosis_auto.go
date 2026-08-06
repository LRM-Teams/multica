// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// diagnosisSinkWriter lets the terminal hook reuse the same idempotent
// sandbox launch path as the explicit endpoint without fabricating an
// internal HTTP round trip.
type diagnosisSinkWriter struct {
	header http.Header
	status int
}

func (w *diagnosisSinkWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *diagnosisSinkWriter) WriteHeader(status int) { w.status = status }
func (w *diagnosisSinkWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return len(body), nil
}

func (h *Handler) automaticDiagnosisTaskProject(ctx context.Context, task db.AgentInboxEvent) (string, error) {
	var projectID pgtype.UUID
	switch {
	case task.ChatSessionID.Valid:
		if err := h.DB.QueryRow(ctx, `SELECT project_id FROM chat_session WHERE id = $1`, task.ChatSessionID).Scan(&projectID); err != nil {
			return "", err
		}
	case task.IssueID.Valid:
		if err := h.DB.QueryRow(ctx, `SELECT project_id FROM issue WHERE id = $1`, task.IssueID).Scan(&projectID); err != nil {
			return "", err
		}
	default:
		return "", nil
	}
	if !projectID.Valid {
		return "", nil
	}
	return util.UUIDToString(projectID), nil
}

// envDispatchBusinessTasksTerminal is the all-business-task barrier. The
// internal diagnosis task is excluded by its stamped run context, and failed
// inbox deliveries remain non-terminal because they can still be retried.
func (h *Handler) envDispatchBusinessTasksTerminal(ctx context.Context, projectID string) (bool, error) {
	var active bool
	err := h.DB.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM agent_inbox_event e
  LEFT JOIN chat_session cs ON cs.id = e.chat_session_id
  LEFT JOIN issue i ON i.id = e.issue_id
  WHERE (cs.project_id = $1 OR i.project_id = $1)
    AND COALESCE(e.context->>'diagnosis_run_id', '') = ''
    AND e.status NOT IN ('acked', 'suppressed')
)`, projectID).Scan(&active)
	return !active, err
}

// maybeStartAutomaticSharedDiagnosis is called after the just-terminal task's
// DAG segment has been closed. It starts only non-training shared dispatches,
// only after every business task is terminal, and delegates run uniqueness to
// the diagnosis run CAS/partial unique index.
func (h *Handler) maybeStartAutomaticSharedDiagnosis(ctx context.Context, task db.AgentInboxEvent) {
	if h == nil || h.DB == nil || h.Queries == nil || service.DiagnosisRunIDFromTaskContext(task.Context) != "" {
		return
	}
	ctx = context.WithoutCancel(ctx)
	projectID, err := h.automaticDiagnosisTaskProject(ctx, task)
	if err != nil || projectID == "" {
		return
	}

	var workspaceID, rootTaskID string
	var trainingMode bool
	if err := h.DB.QueryRow(ctx, `
SELECT workspace_id::text, root_task_id::text, training_mode
FROM env_dispatch_run
WHERE project_id = $1 AND root_task_id IS NOT NULL`, projectID).Scan(&workspaceID, &rootTaskID, &trainingMode); err != nil || trainingMode {
		return
	}
	shared, bindingErr := h.resolveSharedDiagnosisBinding(ctx, workspaceID, projectID)
	if bindingErr != nil {
		// Continue through the common launch path: it creates the run and
		// persists the provisioning_binding failure for latest-run polling.
		slog.Warn("automatic diagnosis: shared binding invalid", "project_id", projectID, "error", bindingErr)
	}
	if shared == nil && bindingErr == nil {
		return
	}
	terminal, err := h.envDispatchBusinessTasksTerminal(ctx, projectID)
	if err != nil || !terminal {
		return
	}
	readiness, err := service.NewEnvDispatchService(newEnvDispatchDepsAdapter(h), envDispatchConcurrency()).GetDagReadiness(ctx, projectID, workspaceID)
	if err != nil || readiness != service.DagReadinessTerminal {
		return
	}
	dag, err := service.NewInteractionDAGService(h.Queries, nil, true).AssembleAssembledDag(ctx, projectID)
	if err != nil || !denseCover(dag) || len(dag.Segments) == 0 {
		return
	}
	segmentIDs, err := diagnosisTopologicalSegmentIDs(dag)
	if err != nil {
		slog.Warn("automatic diagnosis: invalid DAG", "project_id", projectID, "error", err)
		return
	}
	actorUserID := util.UUIDToString(task.InitiatorUserID)
	if actorUserID == "" {
		slog.Warn("automatic diagnosis: terminal task has no initiator", "project_id", projectID)
		return
	}

	req := (&http.Request{}).WithContext(ctx)
	sink := &diagnosisSinkWriter{}
	h.diagnoseEnvDispatchProjectSandbox(sink, req, projectID, workspaceID, actorUserID, rootTaskID, segmentIDs, service.LoadTrainingConfig())
	if sink.status >= http.StatusBadRequest {
		slog.Warn("automatic diagnosis: launch failed", "project_id", projectID, "status", sink.status)
		return
	}
	slog.Info("automatic diagnosis: launch accepted", "project_id", projectID, "shared_sandbox", shared != nil)
}
