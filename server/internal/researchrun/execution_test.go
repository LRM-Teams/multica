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
		"dispatch-discovered": {ID: "inbox-discovered", Status: "running"},
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
	if !reflect.DeepEqual(dispatcher.inspected, []string{"dispatch-discovered", "dispatch-stale", "dispatch-fresh"}) {
		t.Fatalf("inspect keys=%v", dispatcher.inspected)
	}
	if !reflect.DeepEqual(dispatcher.cancelledIDs, []string{"inbox-attached", "inbox-discovered"}) || dispatcher.cancelReason != "run_paused" {
		t.Fatalf("cancel ids=%v reason=%q", dispatcher.cancelledIDs, dispatcher.cancelReason)
	}
	if store.completedSession != "session-1" || !reflect.DeepEqual(store.completedAttemptIDs, []string{"attached", "discovered", "stale"}) {
		t.Fatalf("completed session=%q attempts=%v", store.completedSession, store.completedAttemptIDs)
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
	attempt := Attempt{ID: "attempt-1", DispatchKey: "dispatch-1", AssignedAgentID: "lead-1"}
	store := &executionTestStore{
		createdAttempt: attempt,
		taskSnapshot:   RunSnapshot{Contract: ResearchContract{Language: "zh-CN", Audience: "decision maker", Freshness: "current"}},
	}
	dispatcher := &executionTestDispatcher{dispatchResult: DispatchResult{InboxTaskID: "inbox-1"}}
	module := executionModule{store: store, dispatcher: dispatcher, clock: executionFixedClock{now: now}, prompts: taskPromptModule{}}
	run := Run{
		SessionID: "session-1", WorkspaceID: "workspace-1", Goal: "评估供应商",
		GoalVersion: 1, PlanVersion: 1, OrchestratorVersion: OrchestratorVersionV1,
		Config: RunConfig{MaxParallelTasks: 1},
	}
	task := Task{
		ID: "task-1", Kind: TaskKindPlan, Objective: "制定可验证的调研计划", ExpectedResult: "plan",
		Status: TaskStatusReady, GoalVersion: 1, PlanVersion: 1, Priority: 1,
	}
	members := []FleetMember{{AgentID: "lead-1", Role: "lead", Status: "active", IsLead: true}}

	dispatched, err := module.DispatchReady(context.Background(), run, []Task{task}, nil, members)
	if err != nil {
		t.Fatal(err)
	}
	if dispatched != 1 {
		t.Fatalf("dispatched=%d", dispatched)
	}
	if store.createdSession != "session-1" || store.createdTaskID != "task-1" || store.createdAgentID != "lead-1" {
		t.Fatalf("created session=%q task=%q agent=%q", store.createdSession, store.createdTaskID, store.createdAgentID)
	}
	if len(dispatcher.dispatchRequests) != 1 {
		t.Fatalf("dispatch requests=%d", len(dispatcher.dispatchRequests))
	}
	request := dispatcher.dispatchRequests[0]
	if request.AttemptID != "attempt-1" || request.Key != "dispatch-1" || request.AgentID != "lead-1" || !strings.Contains(request.Prompt, "制定可验证的调研计划") {
		t.Fatalf("dispatch request=%+v", request)
	}
	if store.attachedAttemptID != "attempt-1" || store.attachedInboxTaskID != "inbox-1" {
		t.Fatalf("attached attempt=%q inbox=%q", store.attachedAttemptID, store.attachedInboxTaskID)
	}
}

func TestExecutionModuleDispatchReadyCancelsRuntimeTaskWhenIdentityAttachFails(t *testing.T) {
	attachErr := errors.New("attempt changed before inbox identity attach")
	store := &executionTestStore{
		createdAttempt: Attempt{ID: "attempt-1", DispatchKey: "dispatch-1", AssignedAgentID: "lead-1"},
		taskSnapshot:   RunSnapshot{Contract: ResearchContract{Language: "zh-CN"}},
		attachErr:      attachErr,
	}
	dispatcher := &executionTestDispatcher{dispatchResult: DispatchResult{InboxTaskID: "inbox-orphan"}}
	module := executionModule{store: store, dispatcher: dispatcher, clock: executionFixedClock{now: time.Now().UTC()}, prompts: taskPromptModule{}}
	run := Run{
		SessionID: "session-1", WorkspaceID: "workspace-1", Goal: "评估供应商",
		GoalVersion: 1, PlanVersion: 1, OrchestratorVersion: OrchestratorVersionV1,
		Config: RunConfig{MaxParallelTasks: 1},
	}
	task := Task{ID: "task-1", Kind: TaskKindPlan, Objective: "制定计划", Status: TaskStatusReady, GoalVersion: 1, PlanVersion: 1}
	members := []FleetMember{{AgentID: "lead-1", Role: "lead", Status: "active", IsLead: true}}

	dispatched, err := module.DispatchReady(context.Background(), run, []Task{task}, nil, members)
	if dispatched != 0 || !errors.Is(err, attachErr) {
		t.Fatalf("dispatched=%d error=%v", dispatched, err)
	}
	if !reflect.DeepEqual(dispatcher.cancelledIDs, []string{"inbox-orphan"}) || dispatcher.cancelReason != "research_attempt_no_longer_dispatchable" {
		t.Fatalf("cancel ids=%v reason=%q", dispatcher.cancelledIDs, dispatcher.cancelReason)
	}
}

type executionFixedClock struct{ now time.Time }

func (clock executionFixedClock) Now() time.Time { return clock.now }

type executionTestStore struct {
	attempts            []Attempt
	pending             []PendingCancellation
	reconciledSession   string
	reconciledStates    map[string]InboxTaskState
	activatedSession    string
	completedSession    string
	completedAttemptIDs []string
	createdAttempt      Attempt
	createdSession      string
	createdTaskID       string
	createdAgentID      string
	taskSnapshot        RunSnapshot
	failedAttempt       AttemptFailure
	attachedAttemptID   string
	attachedInboxTaskID string
	attachErr           error
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

func (store *executionTestStore) MarkCancellationsCompleted(_ context.Context, sessionID string, attemptIDs []string) error {
	store.completedSession = sessionID
	store.completedAttemptIDs = append([]string(nil), attemptIDs...)
	return nil
}

func (store *executionTestStore) CreateAttempt(_ context.Context, sessionID, taskID, agentID string) (Attempt, RunEvent, error) {
	store.createdSession = sessionID
	store.createdTaskID = taskID
	store.createdAgentID = agentID
	return store.createdAttempt, RunEvent{}, nil
}

func (store *executionTestStore) TaskContext(context.Context, string, string) (RunSnapshot, error) {
	return store.taskSnapshot, nil
}

func (store *executionTestStore) FailAttempt(_ context.Context, failure AttemptFailure) (RunEvent, error) {
	store.failedAttempt = failure
	return RunEvent{}, nil
}

func (store *executionTestStore) AttachInboxTask(_ context.Context, attemptID, inboxTaskID string) (Attempt, RunEvent, error) {
	store.attachedAttemptID = attemptID
	store.attachedInboxTaskID = inboxTaskID
	return Attempt{ID: attemptID, InboxTaskID: inboxTaskID}, RunEvent{}, store.attachErr
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
