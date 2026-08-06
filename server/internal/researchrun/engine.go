package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Engine struct {
	store      *PostgresStore
	dispatcher Dispatcher
	projector  Projector
	clock      Clock
}

func NewEngine(store *PostgresStore, dispatcher Dispatcher, projector Projector) ResearchRun {
	return newEngine(store, dispatcher, projector)
}

func newEngine(store *PostgresStore, dispatcher Dispatcher, projector Projector) *Engine {
	return &Engine{store: store, dispatcher: dispatcher, projector: projector, clock: systemClock{}}
}

func (e *Engine) Create(ctx context.Context, in StartInput) (Run, error) {
	if e == nil || e.store == nil || e.dispatcher == nil {
		return Run{}, errors.New("research run engine is unavailable")
	}
	run, _, err := e.store.CreateRun(ctx, in, DefaultRunConfig(in.DepthTier))
	if err != nil {
		return Run{}, err
	}
	if err = e.ReconcileSession(ctx, run.SessionID); err != nil {
		return run, err
	}
	return e.store.GetRun(ctx, run.SessionID, in.WorkspaceID)
}

func (e *Engine) Start(ctx context.Context, in StartInput) (Run, error) {
	if e == nil || e.store == nil || e.dispatcher == nil {
		return Run{}, errors.New("research run engine is unavailable")
	}
	run, _, err := e.store.InitializeRun(ctx, in, DefaultRunConfig(in.DepthTier))
	if err != nil {
		return Run{}, err
	}
	if err = e.ReconcileSession(ctx, in.SessionID); err != nil {
		return run, err
	}
	return e.store.GetRun(ctx, in.SessionID, in.WorkspaceID)
}

func (e *Engine) SubmitResult(ctx context.Context, sessionID, workspaceID, taskID, attemptID, agentID, inboxTaskID string, raw json.RawMessage) (AcceptResultOutcome, error) {
	outcome, err := (resultAcceptanceModule{store: e.store}).Accept(ctx, resultSubmission{
		SessionID: sessionID, WorkspaceID: workspaceID, TaskID: taskID,
		AttemptID: attemptID, AgentID: agentID, InboxTaskID: inboxTaskID, Raw: raw,
	})
	if err != nil {
		return AcceptResultOutcome{}, err
	}
	if err = e.ReconcileSession(ctx, sessionID); err != nil {
		return outcome, fmt.Errorf("result accepted but run advancement failed: %w", err)
	}
	return outcome, nil
}

func (e *Engine) ReconcileDue(ctx context.Context, limit int) (int, error) {
	ids, err := e.store.ListDueRunIDs(ctx, limit)
	if err != nil {
		return 0, err
	}
	processed := 0
	errs := []error{}
	for _, sessionID := range ids {
		if err = e.ReconcileSession(ctx, sessionID); err != nil {
			errs = append(errs, fmt.Errorf("research run %s: %w", sessionID, err))
			continue
		}
		processed++
	}
	return processed, errors.Join(errs...)
}

func (e *Engine) ReconcileSession(ctx context.Context, sessionID string) (retErr error) {
	if e == nil || e.store == nil || e.dispatcher == nil {
		return errors.New("research run engine is unavailable")
	}
	token := uuid.NewString()
	run, claimed, err := e.store.ClaimRun(ctx, sessionID, token, 45*time.Second)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}
	next := e.clock.Now().Add(15 * time.Second)
	defer func() {
		if err := e.store.ReleaseRun(context.WithoutCancel(ctx), sessionID, token, next); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("release research run lease: %w", err))
		}
	}()
	pendingCancellations, cancelErr := e.cancelPendingAttempts(ctx, run, "research_run_"+string(run.Status))
	if cancelErr != nil {
		next = e.clock.Now().Add(10 * time.Second)
		return cancelErr
	}
	if pendingCancellations {
		next = e.clock.Now().Add(10 * time.Second)
		return e.projectPending(ctx, sessionID)
	}
	if run.Status != RunStatusRunning {
		next = e.clock.Now().Add(time.Hour)
		return e.projectPending(ctx, sessionID)
	}
	if run.InitializedAt != nil && run.Config.MaxRunSeconds > 0 && e.clock.Now().After(run.InitializedAt.Add(time.Duration(run.Config.MaxRunSeconds)*time.Second)) {
		return e.failureModule().HandleBudgetExhaustion(ctx, run, "wall_time", fmt.Sprintf("run exceeded %d seconds", run.Config.MaxRunSeconds))
	}

	if err = e.executionModule().SyncAttempts(ctx, sessionID); err != nil {
		return err
	}

	tasks, err := e.store.ListTasks(ctx, sessionID)
	if err != nil {
		return err
	}
	attempts, err := e.store.ListAttempts(ctx, sessionID)
	if err != nil {
		return err
	}
	members, err := e.store.ListFleetMembers(ctx, sessionID, run.WorkspaceID)
	if err != nil {
		return err
	}
	dispatched, err := e.dispatchReady(ctx, run, tasks, attempts, members)
	if err != nil {
		return e.failureModule().HandleDispatchFailure(ctx, sessionID, err)
	}
	if dispatched > 0 || hasActiveCurrentWork(run, tasks) {
		next = e.clock.Now().Add(10 * time.Second)
		return e.projectPending(ctx, sessionID)
	}

	gateOutcome, err := e.gateModule().Advance(ctx, run, tasks)
	if err != nil {
		return err
	}
	if !gateOutcome.RemediationCreated {
		if gateOutcome.NextReconcileAfter > 0 {
			next = e.clock.Now().Add(gateOutcome.NextReconcileAfter)
		}
		return nil
	}
	if err = e.executionModule().ActivateReadyTasks(ctx, sessionID); err != nil {
		return err
	}
	tasks, err = e.store.ListTasks(ctx, sessionID)
	if err != nil {
		return err
	}
	attempts, err = e.store.ListAttempts(ctx, sessionID)
	if err != nil {
		return err
	}
	if _, err = e.dispatchReady(ctx, run, tasks, attempts, members); err != nil {
		return e.failureModule().HandleDispatchFailure(ctx, sessionID, err)
	}
	next = e.clock.Now().Add(10 * time.Second)
	return e.projectPending(ctx, sessionID)
}

func hasActiveCurrentWork(run Run, tasks []Task) bool {
	for _, task := range tasks {
		if task.GoalVersion != run.GoalVersion || task.PlanVersion != run.PlanVersion {
			continue
		}
		switch task.Status {
		case TaskStatusPending, TaskStatusReady, TaskStatusDispatching, TaskStatusRunning:
			return true
		}
	}
	return false
}

func (e *Engine) Pause(ctx context.Context, sessionID, workspaceID, userID string) (Run, error) {
	run, _, _, err := e.store.Pause(ctx, sessionID, workspaceID, userID)
	if err != nil {
		return Run{}, err
	}
	if _, err = e.cancelPendingAttempts(ctx, run, "research_run_paused"); err != nil {
		return run, err
	}
	return run, e.projectPending(ctx, sessionID)
}

func (e *Engine) Resume(ctx context.Context, sessionID, workspaceID, userID string) (Run, error) {
	run, _, err := e.store.Resume(ctx, sessionID, workspaceID, userID)
	if err != nil {
		return Run{}, err
	}
	return run, e.ReconcileSession(ctx, sessionID)
}

func (e *Engine) Cancel(ctx context.Context, sessionID, workspaceID, userID, reason string) (Run, error) {
	run, _, _, err := e.store.Cancel(ctx, sessionID, workspaceID, userID, reason)
	if err != nil {
		return Run{}, err
	}
	if _, err = e.cancelPendingAttempts(ctx, run, "research_run_cancelled"); err != nil {
		return run, err
	}
	return run, e.projectPending(ctx, sessionID)
}

func (e *Engine) Archive(ctx context.Context, sessionID, workspaceID, userID, reason string) (Run, error) {
	run, _, _, err := e.store.Archive(ctx, sessionID, workspaceID, userID, reason)
	if err != nil {
		return Run{}, err
	}
	if _, err = e.cancelPendingAttempts(ctx, run, "research_run_archived"); err != nil {
		return run, err
	}
	return run, e.projectPending(ctx, sessionID)
}

func (e *Engine) Confirm(ctx context.Context, sessionID, workspaceID, userID string) (Run, error) {
	return e.gateModule().Confirm(ctx, sessionID, workspaceID, userID)
}

func (e *Engine) Snapshot(ctx context.Context, sessionID, workspaceID string) (RunSnapshot, error) {
	run, err := e.store.GetRun(ctx, sessionID, workspaceID)
	if err != nil {
		return RunSnapshot{}, err
	}
	contract, err := e.store.GetCurrentContract(ctx, sessionID, workspaceID)
	if err != nil {
		return RunSnapshot{}, err
	}
	method, err := e.store.GetCurrentMethod(ctx, sessionID, workspaceID)
	if err != nil {
		return RunSnapshot{}, err
	}
	questions, err := e.store.ListQuestions(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	tasks, err := e.store.ListTasks(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	attempts, err := e.store.ListAttempts(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	sources, err := e.store.ListSourceSnapshots(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	observations, err := e.store.ListObservations(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	claims, err := e.store.ListClaims(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	gate, err := e.gateModule().Evaluate(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	return RunSnapshot{
		Run: run, Contract: contract, Method: method, Questions: questions, Tasks: tasks, Attempts: attempts,
		Sources: sources, Observations: observations, Claims: claims, Gate: gate,
	}, nil
}

// ListFleetMembers returns the session-bound research fleet roster used for
// presence/dispatch (LRM-1377 follow-up).
func (e *Engine) ListFleetMembers(ctx context.Context, sessionID, workspaceID string) ([]FleetMember, error) {
	return e.store.ListFleetMembers(ctx, sessionID, workspaceID)
}

func (e *Engine) Steer(ctx context.Context, in SteerInput) (Run, error) {
	run, _, _, err := e.store.Steer(ctx, in)
	if err != nil {
		return Run{}, err
	}
	if _, err = e.cancelPendingAttempts(ctx, run, "research_goal_steered"); err != nil {
		return run, err
	}
	if _, _, err = e.store.CreateControlTask(ctx, ControlTaskInput{
		SessionID: in.SessionID, Kind: TaskKindReplan,
		Objective:  "Create a new evidence-oriented plan for the revised user goal. Treat earlier-version artifacts as audit history only.",
		Capability: "lead", Priority: 1, Rationale: "The user changed the durable research goal.",
	}); err != nil {
		return run, err
	}
	return run, e.ReconcileSession(ctx, in.SessionID)
}

// NodeCommand applies continue|fork|retry|reassign from a canvas node, then
// reconciles so ready tasks can dispatch (LRM-1413 / LRM-1408).
func (e *Engine) NodeCommand(ctx context.Context, in NodeCommandInput) (NodeCommandOutcome, error) {
	outcome, err := e.store.NodeCommand(ctx, in)
	if err != nil {
		return NodeCommandOutcome{}, err
	}
	if !outcome.Replayed {
		if recErr := e.ReconcileSession(ctx, in.SessionID); recErr != nil {
			// Command already committed; surface reconcile failure without rolling back.
			return outcome, recErr
		}
	}
	if outcome.Task != nil {
		if latest, getErr := e.store.GetTask(ctx, outcome.Task.ID, in.SessionID); getErr == nil {
			outcome.Task = &latest
			if aid := strings.TrimSpace(latest.AssignedAgentID); aid != "" {
				outcome.Assigned = &aid
			}
			outcome.Queued = latest.Status == TaskStatusReady || latest.Status == TaskStatusPending ||
				latest.Status == TaskStatusDispatching || latest.Status == TaskStatusRunning
		}
	}
	return outcome, nil
}
