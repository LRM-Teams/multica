package researchrun

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestFailureModuleRetryableDispatchFailureDoesNotMutateRun(t *testing.T) {
	dispatchErr := errors.New("provider temporarily unavailable")
	store := &failureTestStore{}
	module := failureModule{store: store, cancellations: &failureTestCancellation{}, projection: &failureTestProjection{}}

	err := module.HandleDispatchFailure(context.Background(), "session-1", dispatchErr)
	if !errors.Is(err, dispatchErr) {
		t.Fatalf("error=%v", err)
	}
	if len(store.calls) != 0 {
		t.Fatalf("retryable dispatch mutated store: %v", store.calls)
	}
}

func TestFailureModuleNonRetryableDispatchFailureFailsCancelsAndProjects(t *testing.T) {
	dispatchErr := NonRetryableDispatchError(errors.New("invalid runtime contract"))
	store := &failureTestStore{failedRun: Run{SessionID: "session-1", Status: RunStatusFailed}}
	cancellation := &failureTestCancellation{}
	projection := &failureTestProjection{}
	module := failureModule{store: store, cancellations: cancellation, projection: projection}

	err := module.HandleDispatchFailure(context.Background(), "session-1", dispatchErr)
	if !errors.Is(err, dispatchErr) {
		t.Fatalf("error=%v", err)
	}
	if store.failedSession != "session-1" || store.failedReason != "non-retryable research task dispatch failed: invalid runtime contract" {
		t.Fatalf("failed session=%q reason=%q", store.failedSession, store.failedReason)
	}
	if cancellation.reason != "research_dispatch_failed" || cancellation.run.Status != RunStatusFailed {
		t.Fatalf("cancellation run=%+v reason=%q", cancellation.run, cancellation.reason)
	}
	if !reflect.DeepEqual(store.calls, []string{"mark_failed"}) || projection.sessionID != "session-1" {
		t.Fatalf("calls=%v projected=%q", store.calls, projection.sessionID)
	}
}

func TestFailureModuleCancellationFailureDefersProjection(t *testing.T) {
	cause := errors.New("terminal remediation failed")
	cancelErr := errors.New("runtime cancellation failed")
	store := &failureTestStore{failedRun: Run{SessionID: "session-1", Status: RunStatusFailed}}
	projection := &failureTestProjection{}
	module := failureModule{
		store: store, cancellations: &failureTestCancellation{err: cancelErr}, projection: projection,
	}

	err := module.FailRun(context.Background(), "session-1", "terminal remediation", "research_remediation_failed", cause)
	if !errors.Is(err, cause) || !errors.Is(err, cancelErr) {
		t.Fatalf("error=%v", err)
	}
	if projection.calls != 0 {
		t.Fatalf("projected before cancellation acknowledgement: calls=%d", projection.calls)
	}
}

func TestFailureModuleBudgetExhaustionRecordsDecisionBeforePassingGate(t *testing.T) {
	store := &failureTestStore{gate: GateResult{Passed: true}}
	projection := &failureTestProjection{}
	module := failureModule{store: store, cancellations: &failureTestCancellation{}, projection: projection}

	if err := module.HandleBudgetExhaustion(context.Background(), Run{SessionID: "session-1"}, "wall_time", "limit reached"); err != nil {
		t.Fatal(err)
	}
	wantCalls := []string{"record_budget", "evaluate_gate", "await_confirmation"}
	if !reflect.DeepEqual(store.calls, wantCalls) || store.budgetKind != "wall_time" || store.budgetDetails != "limit reached" {
		t.Fatalf("calls=%v kind=%q details=%q", store.calls, store.budgetKind, store.budgetDetails)
	}
	if projection.sessionID != "session-1" || store.failedSession != "" {
		t.Fatalf("projected=%q failed=%q", projection.sessionID, store.failedSession)
	}
}

func TestFailureModuleBudgetExhaustionFailsUndeliverableRun(t *testing.T) {
	store := &failureTestStore{
		gate:      GateResult{Findings: []GateFinding{{Code: "required_questions_unanswered"}}},
		failedRun: Run{SessionID: "session-1", Status: RunStatusFailed},
	}
	cancellation := &failureTestCancellation{}
	projection := &failureTestProjection{}
	module := failureModule{store: store, cancellations: cancellation, projection: projection}

	if err := module.HandleBudgetExhaustion(context.Background(), Run{SessionID: "session-1"}, "tasks", "task limit reached"); err != nil {
		t.Fatal(err)
	}
	wantCalls := []string{"record_budget", "evaluate_gate", "mark_failed"}
	if !reflect.DeepEqual(store.calls, wantCalls) {
		t.Fatalf("calls=%v", store.calls)
	}
	if store.failedReason != "research budget exhausted before delivery gates passed: task limit reached" {
		t.Fatalf("failure reason=%q", store.failedReason)
	}
	if cancellation.reason != "research_budget_exhausted" || projection.sessionID != "session-1" {
		t.Fatalf("cancel reason=%q projected=%q", cancellation.reason, projection.sessionID)
	}
}

type failureTestStore struct {
	calls         []string
	failedRun     Run
	failedSession string
	failedReason  string
	budgetKind    string
	budgetDetails string
	gate          GateResult
	markErr       error
	budgetErr     error
	gateErr       error
	awaitErr      error
}

func (store *failureTestStore) MarkFailed(_ context.Context, sessionID, reason string) (Run, RunEvent, []string, error) {
	store.calls = append(store.calls, "mark_failed")
	store.failedSession = sessionID
	store.failedReason = reason
	return store.failedRun, RunEvent{}, nil, store.markErr
}

func (store *failureTestStore) RecordBudgetExhausted(_ context.Context, _ string, kind, details string) (RunEvent, error) {
	store.calls = append(store.calls, "record_budget")
	store.budgetKind = kind
	store.budgetDetails = details
	return RunEvent{}, store.budgetErr
}

func (store *failureTestStore) EvaluateGate(context.Context, string) (GateResult, error) {
	store.calls = append(store.calls, "evaluate_gate")
	return store.gate, store.gateErr
}

func (store *failureTestStore) SetAwaitingConfirmation(context.Context, string, GateResult) (Run, RunEvent, error) {
	store.calls = append(store.calls, "await_confirmation")
	return Run{Status: RunStatusAwaitingUserConfirm}, RunEvent{}, store.awaitErr
}

type failureTestCancellation struct {
	run    Run
	reason string
	err    error
}

func (cancellation *failureTestCancellation) CancelPendingAttempts(_ context.Context, run Run, reason string) (bool, error) {
	cancellation.run = run
	cancellation.reason = reason
	return false, cancellation.err
}

type failureTestProjection struct {
	sessionID string
	calls     int
	err       error
}

func (projection *failureTestProjection) ProjectPending(_ context.Context, sessionID string) error {
	projection.calls++
	projection.sessionID = sessionID
	return projection.err
}
