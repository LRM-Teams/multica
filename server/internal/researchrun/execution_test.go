package researchrun

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestExecutionModuleSyncAttemptsUsesCanonicalRuntimeKeys(t *testing.T) {
	store := &executionTestStore{attempts: []Attempt{
		{ID: "attached", Status: AttemptStatusDispatching, InboxTaskID: "inbox-1", DispatchKey: "dispatch-attached"},
		{ID: "unattached", Status: AttemptStatusRunning, DispatchKey: "dispatch-2"},
		{ID: "terminal", Status: AttemptStatusSucceeded, InboxTaskID: "inbox-terminal", DispatchKey: "dispatch-terminal"},
	}}
	dispatcher := &executionTestDispatcher{states: map[string]InboxTaskState{
		"inbox-1":    {ID: "inbox-1", Status: "running"},
		"dispatch-2": {ID: "inbox-2", Status: "running"},
	}}
	module := executionModule{store: store, dispatcher: dispatcher, prompts: taskPromptModule{}}

	if err := module.SyncAttempts(context.Background(), "session-1"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(dispatcher.inspected, []string{"inbox-1", "dispatch-2"}) {
		t.Fatalf("inspect keys=%v", dispatcher.inspected)
	}
	if store.reconciledSession != "session-1" || !reflect.DeepEqual(store.reconciledStates, dispatcher.states) {
		t.Fatalf("reconciled session=%q states=%v", store.reconciledSession, store.reconciledStates)
	}
	if store.activatedSession != "session-1" {
		t.Fatalf("activated session=%q", store.activatedSession)
	}
}

func TestExecutionModuleSyncAttemptsFailsBeforeStateMutationWhenInspectFails(t *testing.T) {
	inspectErr := errors.New("runtime unavailable")
	store := &executionTestStore{attempts: []Attempt{{ID: "attempt-1", Status: AttemptStatusRunning, InboxTaskID: "inbox-1"}}}
	module := executionModule{
		store: store, dispatcher: &executionTestDispatcher{inspectErr: inspectErr}, prompts: taskPromptModule{},
	}

	err := module.SyncAttempts(context.Background(), "session-1")
	if !errors.Is(err, inspectErr) || !strings.Contains(err.Error(), "inspect research attempts") {
		t.Fatalf("error=%v", err)
	}
	if store.reconciledSession != "" || store.activatedSession != "" {
		t.Fatalf("state mutated after inspect failure: reconciled=%q activated=%q", store.reconciledSession, store.activatedSession)
	}
}

func TestExecutionModuleSyncAttemptsActivatesDependenciesWithoutActiveRuntimeWork(t *testing.T) {
	store := &executionTestStore{}
	dispatcher := &executionTestDispatcher{}
	module := executionModule{store: store, dispatcher: dispatcher, prompts: taskPromptModule{}}

	if err := module.SyncAttempts(context.Background(), "session-1"); err != nil {
		t.Fatal(err)
	}
	if dispatcher.inspectCalls != 0 || store.reconciledSession != "session-1" || store.activatedSession != "session-1" {
		t.Fatalf("inspect=%d reconciled=%q activated=%q", dispatcher.inspectCalls, store.reconciledSession, store.activatedSession)
	}
}

func TestExecutionModuleCancelPendingAttemptsResolvesAttachedDiscoveredAndStaleAttempts(t *testing.T) {
	now := time.Date(2026, time.August, 5, 10, 0, 0, 0, time.UTC)
	store := &executionTestStore{pending: []PendingCancellation{
		{AttemptID: "attached", InboxTaskID: "inbox-attached", DispatchKey: "dispatch-attached", DispatchedAt: now.Add(-time.Minute)},
		{AttemptID: "discovered", DispatchKey: "dispatch-discovered", DispatchedAt: now.Add(-time.Minute)},
		{AttemptID: "stale", DispatchKey: "dispatch-stale", DispatchedAt: now.Add(-11 * time.Minute)},
		{AttemptID: "fresh", DispatchKey: "dispatch-fresh", DispatchedAt: now.Add(-time.Minute)},
	}}
	dispatcher := &executionTestDispatcher{states: map[string]InboxTaskState{
		"inbox-attached":      {ID: "inbox-attached", Status: "running", HasActiveLease: true},
		"dispatch-discovered": {ID: "inbox-discovered", Status: "running", HasActiveLease: true},
	}}
	module := executionModule{store: store, dispatcher: dispatcher, clock: executionFixedClock{now: now}}
	run := Run{SessionID: "session-1", Config: RunConfig{StaleAfterSeconds: 600}}

	pending, err := module.CancelPendingAttempts(context.Background(), run, "run_paused")
	if err != nil {
		t.Fatal(err)
	}
	if !pending {
		t.Fatal("fresh unattached attempt must keep cancellation pending")
	}
	if !reflect.DeepEqual(dispatcher.inspected, []string{"inbox-attached", "dispatch-discovered", "dispatch-stale", "dispatch-fresh"}) {
		t.Fatalf("inspect keys=%v", dispatcher.inspected)
	}
	if !reflect.DeepEqual(dispatcher.cancelledIDs, []string{"inbox-attached", "inbox-discovered"}) || dispatcher.cancelReason != "run_paused" {
		t.Fatalf("cancel ids=%v reason=%q", dispatcher.cancelledIDs, dispatcher.cancelReason)
	}
	wantRequests := []CancellationRequest{
		{AttemptID: "attached", InboxTaskID: "inbox-attached"},
		{AttemptID: "discovered", InboxTaskID: "inbox-discovered"},
	}
	if store.requestedSession != "session-1" || !reflect.DeepEqual(store.requestedRequests, wantRequests) {
		t.Fatalf("requested session=%q requests=%v", store.requestedSession, store.requestedRequests)
	}
	if store.completedSession != "session-1" || !reflect.DeepEqual(store.completedAttemptIDs, []string{"stale"}) {
		t.Fatalf("completed session=%q attempts=%v", store.completedSession, store.completedAttemptIDs)
	}
}

func TestExecutionModuleCancelPendingAttemptsWaitsForRuntimeLeaseLoss(t *testing.T) {
	now := time.Date(2026, time.August, 5, 10, 0, 0, 0, time.UTC)
	requestedAt := now.Add(-time.Minute)
	store := &executionTestStore{pending: []PendingCancellation{{
		AttemptID: "attempt-1", InboxTaskID: "inbox-1", Status: AttemptStatusCancelling,
		DispatchedAt: now.Add(-time.Hour), CancellationRequestedAt: &requestedAt,
	}}}
	dispatcher := &executionTestDispatcher{states: map[string]InboxTaskState{
		"inbox-1": {ID: "inbox-1", Status: "cancelled", HasActiveLease: true},
	}}
	module := executionModule{store: store, dispatcher: dispatcher, clock: executionFixedClock{now: now}}
	run := Run{SessionID: "session-1", Config: RunConfig{StaleAfterSeconds: 600}}

	pending, err := module.CancelPendingAttempts(context.Background(), run, "task_timeout")
	if err != nil || !pending {
		t.Fatalf("pending=%v error=%v", pending, err)
	}
	if len(dispatcher.cancelledIDs) != 0 || len(store.completedAttemptIDs) != 0 {
		t.Fatalf("active runtime was acknowledged: cancelled=%v completed=%v", dispatcher.cancelledIDs, store.completedAttemptIDs)
	}

	dispatcher.states["inbox-1"] = InboxTaskState{ID: "inbox-1", Status: "cancelled", HasActiveLease: false}
	pending, err = module.CancelPendingAttempts(context.Background(), run, "task_timeout")
	if err != nil || pending {
		t.Fatalf("pending=%v error=%v", pending, err)
	}
	if !reflect.DeepEqual(store.completedAttemptIDs, []string{"attempt-1"}) {
		t.Fatalf("completed=%v", store.completedAttemptIDs)
	}
}

func TestExecutionModuleCancelPendingAttemptsDoesNotAcknowledgeFailedRuntimeCancellation(t *testing.T) {
	cancelErr := errors.New("runtime cancellation unavailable")
	store := &executionTestStore{pending: []PendingCancellation{{AttemptID: "attempt-1", InboxTaskID: "inbox-1"}}}
	dispatcher := &executionTestDispatcher{cancelErr: cancelErr}
	module := executionModule{store: store, dispatcher: dispatcher, clock: executionFixedClock{now: time.Now().UTC()}}

	pending, err := module.CancelPendingAttempts(context.Background(), Run{SessionID: "session-1"}, "run_cancelled")
	if !pending || !errors.Is(err, cancelErr) || !strings.Contains(err.Error(), "cancel research inbox tasks") {
		t.Fatalf("pending=%v error=%v", pending, err)
	}
	if store.completedSession != "" || len(store.completedAttemptIDs) != 0 {
		t.Fatalf("cancellation acknowledged after runtime failure: session=%q attempts=%v", store.completedSession, store.completedAttemptIDs)
	}
}

func TestExecutionModuleDispatchReadyCreatesRuntimeTaskAndAttachesIdentity(t *testing.T) {
	now := time.Date(2026, time.August, 5, 10, 0, 0, 0, time.UTC)
	run := Run{
		SessionID: "session-1", WorkspaceID: "workspace-1", Goal: "评估供应商", StateVersion: 7,
		GoalVersion: 1, PlanVersion: 1, OrchestratorVersion: OrchestratorVersionV1,
		Config: RunConfig{MaxParallelTasks: 1},
	}
	task := Task{
		ID: "task-1", Kind: TaskKindPlan, Objective: "制定可验证的调研计划", ExpectedResult: "plan",
		Status: TaskStatusReady, GoalVersion: 1, PlanVersion: 1, Priority: 1,
	}
	store := &executionTestStore{
		taskSnapshot: RunSnapshot{Run: run, Tasks: []Task{task}, Contract: ResearchContract{Language: "zh-CN", Audience: "decision maker", Freshness: "current"}},
	}
	dispatcher := &executionTestDispatcher{dispatchResult: DispatchResult{InboxTaskID: "inbox-1"}}
	module := executionModule{store: store, dispatcher: dispatcher, clock: executionFixedClock{now: now}, prompts: taskPromptModule{}}
	members := []FleetMember{{AgentID: "lead-1", Role: "lead", Status: "active", IsLead: true}}

	dispatched, err := module.DispatchReady(context.Background(), run, []Task{task}, nil, members)
	if err != nil {
		t.Fatal(err)
	}
	if dispatched != 1 {
		t.Fatalf("dispatched=%d", dispatched)
	}
	if store.createdInput.SessionID != "session-1" || store.createdInput.TaskID != "task-1" || store.createdInput.AgentID != "lead-1" || store.createdInput.ExpectedStateVersion != 7 {
		t.Fatalf("created input=%+v", store.createdInput)
	}
	if len(dispatcher.dispatchRequests) != 1 {
		t.Fatalf("dispatch requests=%d", len(dispatcher.dispatchRequests))
	}
	request := dispatcher.dispatchRequests[0]
	if request.AttemptID == "" || request.Key == "" || request.AgentID != "lead-1" || request.RequestHash == "" || !strings.Contains(request.Prompt, "制定可验证的调研计划") {
		t.Fatalf("dispatch request=%+v", request)
	}
	if store.acknowledgedAttemptID != request.AttemptID || store.acknowledgedInboxTaskID != "inbox-1" {
		t.Fatalf("acknowledged attempt=%q inbox=%q", store.acknowledgedAttemptID, store.acknowledgedInboxTaskID)
	}
}

func TestExecutionModuleDispatchReadyLeavesExternalTaskForReplayWhenAcknowledgementFails(t *testing.T) {
	acknowledgeErr := errors.New("database unavailable during acknowledgement")
	run := Run{SessionID: "session-1", WorkspaceID: "workspace-1", Goal: "评估供应商", StateVersion: 3,
		GoalVersion: 1, PlanVersion: 1, OrchestratorVersion: OrchestratorVersionV1, Config: RunConfig{MaxParallelTasks: 1}}
	task := Task{ID: "task-1", Kind: TaskKindPlan, Objective: "制定计划", Status: TaskStatusReady, GoalVersion: 1, PlanVersion: 1}
	store := &executionTestStore{
		taskSnapshot:   RunSnapshot{Run: run, Tasks: []Task{task}, Contract: ResearchContract{Language: "zh-CN"}},
		acknowledgeErr: acknowledgeErr,
	}
	dispatcher := &executionTestDispatcher{dispatchResult: DispatchResult{InboxTaskID: "inbox-orphan"}}
	module := executionModule{store: store, dispatcher: dispatcher, clock: executionFixedClock{now: time.Now().UTC()}, prompts: taskPromptModule{}}
	members := []FleetMember{{AgentID: "lead-1", Role: "lead", Status: "active", IsLead: true}}

	dispatched, err := module.DispatchReady(context.Background(), run, []Task{task}, nil, members)
	if dispatched != 1 || !errors.Is(err, acknowledgeErr) {
		t.Fatalf("dispatched=%d error=%v", dispatched, err)
	}
	if len(dispatcher.cancelledIDs) != 0 {
		t.Fatalf("recoverable external task was cancelled: %v", dispatcher.cancelledIDs)
	}
}

func TestExecutionModuleDeliverPendingReplaysFrozenRequestAfterCrash(t *testing.T) {
	request := DispatchRequest{Run: Run{SessionID: "session-1"}, Task: Task{ID: "task-1"}, AttemptID: "attempt-1", AgentID: "agent-1", Prompt: "frozen prompt", Key: "dispatch-1"}
	request.RequestHash, _ = HashDispatchRequest(request)
	store := &executionTestStore{claimed: []DispatchIntent{{ID: "intent-1", AttemptID: "attempt-1", SessionID: "session-1", Request: request, DeliveryAttempts: 2}}}
	dispatcher := &executionTestDispatcher{dispatchResult: DispatchResult{InboxTaskID: "inbox-1"}}
	module := executionModule{store: store, dispatcher: dispatcher, clock: executionFixedClock{now: time.Now().UTC()}}

	delivered, err := module.DeliverPending(context.Background(), "session-1", 1)
	if err != nil || delivered != 1 {
		t.Fatalf("delivered=%d error=%v", delivered, err)
	}
	if len(dispatcher.dispatchRequests) != 1 || !reflect.DeepEqual(dispatcher.dispatchRequests[0], request) {
		t.Fatalf("replayed request=%+v", dispatcher.dispatchRequests)
	}
	if store.acknowledgedAttemptID != "attempt-1" || store.acknowledgedInboxTaskID != "inbox-1" {
		t.Fatalf("acknowledged attempt=%q inbox=%q", store.acknowledgedAttemptID, store.acknowledgedInboxTaskID)
	}
}

func TestExecutionModuleDeliverPendingReschedulesRetryableFailureWithoutConsumingAttempt(t *testing.T) {
	now := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	request := DispatchRequest{Run: Run{SessionID: "session-1"}, Task: Task{ID: "task-1"}, AttemptID: "attempt-1", AgentID: "agent-1", Prompt: "frozen prompt", Key: "dispatch-1"}
	request.RequestHash, _ = HashDispatchRequest(request)
	store := &executionTestStore{claimed: []DispatchIntent{{ID: "intent-1", AttemptID: "attempt-1", SessionID: "session-1", Request: request, DeliveryAttempts: 3}}}
	dispatchErr := errors.New("provider temporarily unavailable")
	module := executionModule{store: store, dispatcher: &executionTestDispatcher{dispatchErr: dispatchErr}, clock: executionFixedClock{now: now}}

	delivered, err := module.DeliverPending(context.Background(), "session-1", 1)
	if err != nil || delivered != 0 {
		t.Fatalf("delivered=%d error=%v", delivered, err)
	}
	if store.rescheduledIntentID != "intent-1" || store.failedIntentID != "" {
		t.Fatalf("rescheduled=%q failed=%q", store.rescheduledIntentID, store.failedIntentID)
	}
}

type executionFixedClock struct{ now time.Time }

func (clock executionFixedClock) Now() time.Time { return clock.now }

type executionTestStore struct {
	attempts                []Attempt
	pending                 []PendingCancellation
	reconciledSession       string
	reconciledStates        map[string]InboxTaskState
	activatedSession        string
	completedSession        string
	completedAttemptIDs     []string
	requestedSession        string
	requestedRequests       []CancellationRequest
	createdInput            CreateDispatchIntentInput
	claimed                 []DispatchIntent
	rescheduledIntentID     string
	failedIntentID          string
	taskSnapshot            RunSnapshot
	acknowledgedAttemptID   string
	acknowledgedInboxTaskID string
	rejectAcknowledgement   bool
	acknowledgeErr          error
}

func (store *executionTestStore) ListAttempts(context.Context, string) ([]Attempt, error) {
	return append([]Attempt(nil), store.attempts...), nil
}

func (store *executionTestStore) ReconcileAttempts(_ context.Context, sessionID string, states map[string]InboxTaskState) ([]RunEvent, error) {
	store.reconciledSession = sessionID
	store.reconciledStates = states
	return nil, nil
}

func (store *executionTestStore) ActivateReadyTasks(_ context.Context, sessionID string) (int, error) {
	store.activatedSession = sessionID
	return 0, nil
}

func (store *executionTestStore) ListPendingCancellations(context.Context, string) ([]PendingCancellation, error) {
	return append([]PendingCancellation(nil), store.pending...), nil
}

func (store *executionTestStore) MarkCancellationsRequested(_ context.Context, sessionID string, requests []CancellationRequest) error {
	store.requestedSession = sessionID
	store.requestedRequests = append([]CancellationRequest(nil), requests...)
	return nil
}

func (store *executionTestStore) CompleteCancellations(_ context.Context, sessionID string, attemptIDs []string) ([]RunEvent, error) {
	store.completedSession = sessionID
	store.completedAttemptIDs = append([]string(nil), attemptIDs...)
	return nil, nil
}

func (store *executionTestStore) CreateDispatchIntent(_ context.Context, in CreateDispatchIntentInput) (Attempt, RunEvent, error) {
	store.createdInput = in
	intent := DispatchIntent{ID: "intent-" + in.AttemptID, AttemptID: in.AttemptID, SessionID: in.SessionID, Request: in.Request, DeliveryAttempts: 1}
	store.claimed = append(store.claimed, intent)
	return Attempt{ID: in.AttemptID, SessionID: in.SessionID, TaskID: in.TaskID, AssignedAgentID: in.AgentID, DispatchKey: in.Request.Key}, RunEvent{}, nil
}

func (store *executionTestStore) TaskContext(context.Context, string, string) (RunSnapshot, error) {
	return store.taskSnapshot, nil
}

func (store *executionTestStore) ClaimDispatchIntents(_ context.Context, _ string, _ string, _ time.Duration, limit int) ([]DispatchIntent, error) {
	if limit > len(store.claimed) {
		limit = len(store.claimed)
	}
	out := append([]DispatchIntent(nil), store.claimed[:limit]...)
	store.claimed = store.claimed[limit:]
	return out, nil
}

func (store *executionTestStore) RescheduleDispatchIntent(_ context.Context, intentID, _ string, _ string, _ time.Time) (bool, error) {
	store.rescheduledIntentID = intentID
	return true, nil
}

func (store *executionTestStore) FailDispatchIntent(_ context.Context, intentID, _ string, _ AttemptFailure) (bool, RunEvent, error) {
	store.failedIntentID = intentID
	return true, RunEvent{}, nil
}

func (store *executionTestStore) AcknowledgeDispatchIntent(_ context.Context, _ string, _ string, inboxTaskID string) (bool, Attempt, RunEvent, error) {
	if len(store.createdInput.AttemptID) > 0 {
		store.acknowledgedAttemptID = store.createdInput.AttemptID
	} else {
		store.acknowledgedAttemptID = "attempt-1"
	}
	store.acknowledgedInboxTaskID = inboxTaskID
	accepted := !store.rejectAcknowledgement
	return accepted, Attempt{ID: store.acknowledgedAttemptID, InboxTaskID: inboxTaskID}, RunEvent{}, store.acknowledgeErr
}

type executionTestDispatcher struct {
	states           map[string]InboxTaskState
	inspectErr       error
	inspectCalls     int
	inspected        []string
	dispatchResult   DispatchResult
	dispatchErr      error
	dispatchRequests []DispatchRequest
	cancelledIDs     []string
	cancelReason     string
	cancelErr        error
}

func (dispatcher *executionTestDispatcher) Inspect(_ context.Context, keys []string) (map[string]InboxTaskState, error) {
	dispatcher.inspectCalls++
	dispatcher.inspected = append([]string(nil), keys...)
	return dispatcher.states, dispatcher.inspectErr
}

func (dispatcher *executionTestDispatcher) Dispatch(_ context.Context, request DispatchRequest) (DispatchResult, error) {
	dispatcher.dispatchRequests = append(dispatcher.dispatchRequests, request)
	return dispatcher.dispatchResult, dispatcher.dispatchErr
}

func (dispatcher *executionTestDispatcher) Cancel(_ context.Context, inboxTaskIDs []string, reason string) error {
	dispatcher.cancelledIDs = append([]string(nil), inboxTaskIDs...)
	dispatcher.cancelReason = reason
	return dispatcher.cancelErr
}
