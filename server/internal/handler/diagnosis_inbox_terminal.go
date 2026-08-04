// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"context"
	"errors"
	"log/slog"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// diagnosis_inbox_terminal.go maps daemon-reported agent-inbox task outcomes
// for sandboxed diagnosis runs back onto the diagnosis state store (spec 005,
// T022). The orchestrator is fire-and-forget; without this mapping a run would
// reach a terminal state only when the agent itself calls complete-diagnosis.
//
// Wiring (least-invasive variant): the two hook methods below are called from
// the existing terminal paths in agent_inbox.go — one line at the tail of
// CompleteAgentInboxEvent, one in each branch of finishFailedAgentInboxEvent.
// Both hooks self-guard on the run identity (stamped task-context run ID, then
// the per-run agent name) so non-diagnosis tasks cost at most one cheap
// agent-name lookup and no state-store traffic.

// diagnosisRunIDForInboxEvent attributes an agent-inbox task to its diagnosis
// run: the stamped run ID in the task-context JSON wins; the per-run agent
// name "diagnosis-<runID>" is the fallback for tasks enqueued before stamping.
// Returns "" for non-diagnosis tasks.
func (h *Handler) diagnosisRunIDForInboxEvent(ctx context.Context, event db.AgentInboxEvent) string {
	if runID := service.DiagnosisRunIDFromTaskContext(event.Context); runID != "" {
		return runID
	}
	if h.DB == nil {
		return ""
	}
	var name string
	err := h.DB.QueryRow(ctx,
		`SELECT name FROM agent WHERE id = $1 AND name LIKE $2`,
		event.AgentID, service.DiagnosisAgentNamePrefix+"%").Scan(&name)
	if err != nil {
		return ""
	}
	return service.DiagnosisRunIDFromAgentName(name)
}

// mapDiagnosisInboxCompletion translates a completed daemon task into the
// run's terminal transition: when the agent already closed the run via
// complete-diagnosis (status terminal, sandbox already reclaimed) it is a
// no-op; otherwise it attempts the coverage-gated CompleteRun CAS. A CAS
// failure means the agent stopped short of full coverage, so the run fails as
// agent-incomplete. Every terminal transition triggered here reclaims the
// run's sandbox through the same path as the complete-diagnosis handler.
func (h *Handler) mapDiagnosisInboxCompletion(ctx context.Context, event db.AgentInboxEvent) {
	runID := h.diagnosisRunIDForInboxEvent(ctx, event)
	if runID == "" {
		return
	}
	state := service.NewDiagnosisStateStore(h.Queries)
	run, err := state.GetRun(ctx, runID)
	if err != nil {
		if !errors.Is(err, service.ErrDiagnosisRunNotFound) {
			slog.Warn("diagnosis inbox mapping: load run failed", "run_id", runID, "error", err)
		}
		return
	}
	if run.Status == service.DiagnosisRunCompleted || run.Status == service.DiagnosisRunFailed {
		// The agent already terminated the run via complete-diagnosis (which
		// also reclaimed the sandbox); the daemon task completion is the echo.
		return
	}
	if err := state.CompleteRun(ctx, runID, run.TopologyHash); err != nil {
		slog.Warn("diagnosis inbox mapping: task completed with incomplete coverage",
			"run_id", runID, "error", err)
		if failErr := state.FailRun(ctx, runID, errors.New("agent: task completed with incomplete coverage")); failErr != nil {
			slog.Warn("diagnosis inbox mapping: fail run transition failed", "run_id", runID, "error", failErr)
		}
		h.reclaimDiagnosisRunSandbox(ctx, run)
		return
	}
	slog.Info("diagnosis inbox mapping: run completed from daemon task outcome", "run_id", runID)
	h.reclaimDiagnosisRunSandbox(ctx, run)
}

// mapDiagnosisInboxFailure translates a failed daemon task into a classified
// run failure (timeout | connectivity | agent — see
// service.ClassifyDiagnosisTaskFailure) and reclaims the run's sandbox. A run
// the agent already terminated stays untouched.
func (h *Handler) mapDiagnosisInboxFailure(ctx context.Context, event db.AgentInboxEvent, errText, failureReason, reasonCode string) {
	runID := h.diagnosisRunIDForInboxEvent(ctx, event)
	if runID == "" {
		return
	}
	state := service.NewDiagnosisStateStore(h.Queries)
	run, err := state.GetRun(ctx, runID)
	if err != nil {
		if !errors.Is(err, service.ErrDiagnosisRunNotFound) {
			slog.Warn("diagnosis inbox mapping: load run failed", "run_id", runID, "error", err)
		}
		return
	}
	if run.Status == service.DiagnosisRunCompleted || run.Status == service.DiagnosisRunFailed {
		return
	}
	cause := service.ClassifyDiagnosisTaskFailure(errText, failureReason, reasonCode)
	if err := state.FailRun(ctx, runID, cause); err != nil {
		slog.Warn("diagnosis inbox mapping: fail run transition failed", "run_id", runID, "error", err)
		return
	}
	slog.Info("diagnosis inbox mapping: run failed from daemon task outcome",
		"run_id", runID, "cause", cause.Error())
	h.reclaimDiagnosisRunSandbox(ctx, run)
}
