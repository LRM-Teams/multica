package researchrun

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDeliveryGateModuleAdvanceTransitionsPassingRunToConfirmation(t *testing.T) {
	store := &gateTestStore{gate: GateResult{Passed: true}}
	projection := &failureTestProjection{}
	module := deliveryGateModule{store: store, failures: &gateTestFailure{}, projection: projection}

	outcome, err := module.Advance(context.Background(), Run{SessionID: "session-1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.RemediationCreated || outcome.NextReconcileAfter != time.Hour {
		t.Fatalf("outcome=%+v", outcome)
	}
	if !reflect.DeepEqual(store.calls, []string{"evaluate_gate", "await_confirmation"}) || projection.sessionID != "session-1" {
		t.Fatalf("calls=%v projected=%q", store.calls, projection.sessionID)
	}
}

func TestDeliveryGateModuleAdvanceCreatesSmallestQuestionBoundRemediation(t *testing.T) {
	store := &gateTestStore{
		gate: GateResult{Findings: []GateFinding{{
			Code: "required_questions_unanswered", Metadata: map[string]any{"question_id": "question-1"},
		}}},
		controlTask: Task{ID: "task-1", Status: TaskStatusPending},
	}
	module := deliveryGateModule{store: store, failures: &gateTestFailure{}, projection: &failureTestProjection{}}
	run := Run{SessionID: "session-1", GoalVersion: 1, PlanVersion: 1}

	outcome, err := module.Advance(context.Background(), run, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.RemediationCreated || outcome.NextReconcileAfter != 0 {
		t.Fatalf("outcome=%+v", outcome)
	}
	control := store.controlInput
	if control.SessionID != "session-1" || control.Kind != TaskKindDiscover || control.Capability != "scout" || control.QuestionID != "question-1" {
		t.Fatalf("control=%+v", control)
	}
	if len(control.Findings) != 1 || !strings.Contains(control.Objective, "required_questions_unanswered") {
		t.Fatalf("control findings=%v objective=%q", control.Findings, control.Objective)
	}
}

func TestDeliveryGateModuleAdvanceRetriesWhenBoundTargetChangedConcurrently(t *testing.T) {
	store := &gateTestStore{
		gate:      GateResult{Findings: []GateFinding{{Code: "required_questions_unanswered"}}},
		createErr: ErrControlTargetChanged,
	}
	projection := &failureTestProjection{}
	module := deliveryGateModule{store: store, failures: &gateTestFailure{}, projection: projection}

	outcome, err := module.Advance(context.Background(), Run{SessionID: "session-1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.RemediationCreated || outcome.NextReconcileAfter != time.Second || projection.sessionID != "session-1" {
		t.Fatalf("outcome=%+v projected=%q", outcome, projection.sessionID)
	}
}

func TestDeliveryGateModuleAdvanceRoutesTaskBudgetToFailureModule(t *testing.T) {
	store := &gateTestStore{
		gate:      GateResult{Findings: []GateFinding{{Code: "report_missing"}}},
		createErr: ErrBudgetExhausted,
	}
	failures := &gateTestFailure{}
	module := deliveryGateModule{store: store, failures: failures, projection: &failureTestProjection{}}
	run := Run{SessionID: "session-1"}

	if _, err := module.Advance(context.Background(), run, nil); err != nil {
		t.Fatal(err)
	}
	if failures.budgetRun.SessionID != "session-1" || failures.budgetKind != "tasks" || !strings.Contains(failures.budgetDetails, ErrBudgetExhausted.Error()) {
		t.Fatalf("budget run=%+v kind=%q details=%q", failures.budgetRun, failures.budgetKind, failures.budgetDetails)
	}
}

func TestDeliveryGateModuleAdvanceStopsFailedInitialPlanLoop(t *testing.T) {
	store := &gateTestStore{gate: GateResult{Findings: []GateFinding{{Code: "plan_incomplete"}}}}
	failures := &gateTestFailure{}
	module := deliveryGateModule{store: store, failures: failures, projection: &failureTestProjection{}}
	run := Run{SessionID: "session-1", GoalVersion: 1, PlanVersion: 1}
	tasks := []Task{{
		ID: "plan-1", Kind: TaskKindPlan, Status: TaskStatusFailed,
		GoalVersion: 1, PlanVersion: 1, TerminalReason: "dispatch_failed",
	}}

	_, err := module.Advance(context.Background(), run, tasks)
	if err == nil || !strings.Contains(err.Error(), "plan-1") {
		t.Fatalf("error=%v", err)
	}
	if failures.sessionID != "session-1" || failures.cancelReason != "research_remediation_failed" || !strings.Contains(failures.reason, "dispatch_failed") {
		t.Fatalf("failure session=%q reason=%q cancel=%q", failures.sessionID, failures.reason, failures.cancelReason)
	}
	if !reflect.DeepEqual(store.calls, []string{"evaluate_gate"}) {
		t.Fatalf("terminal plan created another task: calls=%v", store.calls)
	}
}

func TestDeliveryGateModuleConfirmRequiresFreshPassingGate(t *testing.T) {
	t.Run("reject stale confirmation", func(t *testing.T) {
		store := &gateTestStore{gate: GateResult{Findings: []GateFinding{{Code: "report_stale_after_evidence"}}}}
		projection := &failureTestProjection{}
		module := deliveryGateModule{store: store, failures: &gateTestFailure{}, projection: projection}

		_, err := module.Confirm(context.Background(), "session-1", "workspace-1", "user-1")
		if !errors.Is(err, ErrInvalidTransition) || !strings.Contains(err.Error(), "report_stale_after_evidence") {
			t.Fatalf("error=%v", err)
		}
		if store.completedSession != "" || projection.calls != 0 {
			t.Fatalf("completed=%q projection calls=%d", store.completedSession, projection.calls)
		}
	})

	t.Run("complete fresh delivery", func(t *testing.T) {
		store := &gateTestStore{gate: GateResult{Passed: true}, completedRun: Run{SessionID: "session-1", Status: RunStatusCompleted}}
		projection := &failureTestProjection{}
		module := deliveryGateModule{store: store, failures: &gateTestFailure{}, projection: projection}

		run, err := module.Confirm(context.Background(), "session-1", "workspace-1", "user-1")
		if err != nil {
			t.Fatal(err)
		}
		if run.Status != RunStatusCompleted || store.completedSession != "session-1" || store.completedWorkspace != "workspace-1" || store.completedUser != "user-1" {
			t.Fatalf("run=%+v complete=(%q,%q,%q)", run, store.completedSession, store.completedWorkspace, store.completedUser)
		}
		if projection.calls != 0 {
			t.Fatalf("confirm projected without a reconcile lease: calls=%d", projection.calls)
		}
	})
}

type gateTestStore struct {
	calls              []string
	gate               GateResult
	gateErr            error
	awaitErr           error
	controlTask        Task
	controlInput       ControlTaskInput
	createErr          error
	completedRun       Run
	completedSession   string
	completedWorkspace string
	completedUser      string
	completeErr        error
}

func (store *gateTestStore) EvaluateGate(context.Context, string) (GateResult, error) {
	store.calls = append(store.calls, "evaluate_gate")
	return store.gate, store.gateErr
}

func (store *gateTestStore) SetAwaitingConfirmation(context.Context, string, GateResult) (Run, RunEvent, error) {
	store.calls = append(store.calls, "await_confirmation")
	return Run{Status: RunStatusAwaitingUserConfirm}, RunEvent{}, store.awaitErr
}

func (store *gateTestStore) CreateControlTask(_ context.Context, input ControlTaskInput) (Task, RunEvent, error) {
	store.calls = append(store.calls, "create_control_task")
	store.controlInput = input
	return store.controlTask, RunEvent{}, store.createErr
}

func (store *gateTestStore) Complete(_ context.Context, sessionID, workspaceID, userID string) (Run, RunEvent, error) {
	store.calls = append(store.calls, "complete")
	store.completedSession = sessionID
	store.completedWorkspace = workspaceID
	store.completedUser = userID
	return store.completedRun, RunEvent{}, store.completeErr
}

type gateTestFailure struct {
	sessionID     string
	reason        string
	cancelReason  string
	cause         error
	err           error
	budgetRun     Run
	budgetKind    string
	budgetDetails string
}

func (failure *gateTestFailure) FailRun(_ context.Context, sessionID, reason, cancellationReason string, cause error) error {
	failure.sessionID = sessionID
	failure.reason = reason
	failure.cancelReason = cancellationReason
	failure.cause = cause
	if failure.err != nil {
		return failure.err
	}
	return cause
}

func (failure *gateTestFailure) HandleBudgetExhaustion(_ context.Context, run Run, kind, details string) error {
	failure.budgetRun = run
	failure.budgetKind = kind
	failure.budgetDetails = details
	return failure.err
}
