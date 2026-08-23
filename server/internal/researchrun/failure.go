package researchrun

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// failureStore is the canonical persistence surface required by Failure
// Module. Gate interpretation, task dispatch, result acceptance, and event
// projection remain owned by their respective modules.
type failureStore interface {
	MarkFailed(context.Context, string, string) (Run, RunEvent, []string, error)
	RecordBudgetExhausted(context.Context, string, string, string) (RunEvent, error)
	EvaluateGate(context.Context, string, string) (GateResult, error)
	SetAwaitingConfirmation(context.Context, string, GateResult) (Run, RunEvent, error)
}

type pendingAttemptCanceller interface {
	CancelPendingAttempts(context.Context, Run, string) (bool, error)
}

type pendingEventProjector interface {
	ProjectPending(context.Context, string) error
}

type failureModule struct {
	store         failureStore
	cancellations pendingAttemptCanceller
	projection    pendingEventProjector
}

// FailRun applies the single durable failure sequence: transition the Run,
// request cancellation for every active Attempt, then project committed events.
// Cancellation failure stops projection so a later reconcile can retry the
// unacknowledged cancellation before treating the terminal Run as quiescent.
func (module failureModule) FailRun(ctx context.Context, sessionID, reason, cancellationReason string, cause error) error {
	failed, _, _, failErr := module.store.MarkFailed(ctx, sessionID, reason)
	_, cancelErr := module.cancellations.CancelPendingAttempts(ctx, failed, cancellationReason)
	if cancelErr != nil {
		return errors.Join(cause, failErr, cancelErr)
	}
	return errors.Join(cause, failErr, module.projection.ProjectPending(ctx, sessionID))
}

// HandleDispatchFailure terminates only deterministic dispatch defects. Unknown
// or explicitly retryable Adapter failures stay retryable and do not mutate the
// Run lifecycle.
func (module failureModule) HandleDispatchFailure(ctx context.Context, sessionID string, dispatchErr error) error {
	switch {
	case errors.Is(dispatchErr, ErrCapabilityUnavailable):
		return module.FailRun(ctx, sessionID, dispatchErr.Error(), "research_capability_unavailable", dispatchErr)
	case !dispatchErrorRetryable(dispatchErr):
		reason := "non-retryable research task dispatch failed: " + dispatchErr.Error()
		return module.FailRun(ctx, sessionID, reason, "research_dispatch_failed", dispatchErr)
	default:
		return dispatchErr
	}
}

// HandleBudgetExhaustion records the exhausted budget before evaluating the
// delivery gate. A deliverable Run waits for confirmation; an incomplete Run
// becomes failed and cancels active work.
func (module failureModule) HandleBudgetExhaustion(ctx context.Context, run Run, budgetKind, details string) error {
	if _, err := module.store.RecordBudgetExhausted(ctx, run.SessionID, budgetKind, details); err != nil {
		return err
	}
	gate, err := module.store.EvaluateGate(ctx, run.SessionID, run.WorkspaceID)
	if err != nil {
		return err
	}
	if gate.Passed {
		if _, _, err = module.store.SetAwaitingConfirmation(ctx, run.SessionID, gate); err != nil {
			return err
		}
		return module.projection.ProjectPending(ctx, run.SessionID)
	}
	reason := "research budget exhausted before delivery gates passed: " + details
	return module.FailRun(ctx, run.SessionID, reason, "research_budget_exhausted", nil)
}

// terminalRemediationFailure prevents a permanently failed initial plan or an
// identical failed control task from being recreated indefinitely.
func terminalRemediationFailure(run Run, tasks []Task, kind TaskKind, objective string) (string, bool) {
	planningSucceeded := false
	var terminalInitialPlan *Task
	var terminalSameControl *Task
	for i := range tasks {
		task := &tasks[i]
		if task.GoalVersion != run.GoalVersion || task.PlanVersion != run.PlanVersion {
			continue
		}
		if (task.Kind == TaskKindPlan || task.Kind == TaskKindReplan) && task.Status == TaskStatusSucceeded {
			planningSucceeded = true
		}
		terminal := task.Status == TaskStatusBlocked || task.Status == TaskStatusFailed
		if terminal && task.Kind == TaskKindPlan {
			terminalInitialPlan = task
		}
		if terminal && task.Kind == kind && task.Objective == objective && strings.HasPrefix(task.ClientKey, "control:") {
			terminalSameControl = task
		}
	}
	if kind == TaskKindReplan && !planningSucceeded && terminalInitialPlan != nil {
		return terminalResearchTaskReason("initial research plan", *terminalInitialPlan), true
	}
	if terminalSameControl != nil {
		return terminalResearchTaskReason("research remediation", *terminalSameControl), true
	}
	return "", false
}

func terminalResearchTaskReason(label string, task Task) string {
	reason := strings.TrimSpace(task.TerminalReason)
	if reason == "" {
		reason = string(task.Status)
	}
	return fmt.Sprintf("%s task %s exhausted its attempts: %s", label, task.ID, reason)
}

func (e *Engine) failureModule() failureModule {
	return failureModule{
		store:         e.store,
		cancellations: e.executionModule(),
		projection:    projectionModule{store: e.store, output: e.projector, clock: e.clock},
	}
}
