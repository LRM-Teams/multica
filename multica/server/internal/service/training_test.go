// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/arealrl"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	testTrainingProjectID   = "11111111-1111-1111-1111-111111111111"
	testTrainAgentID        = "22222222-2222-2222-2222-222222222222"
	testTrainingTaskID      = "33333333-3333-3333-3333-333333333333"
	testOtherAgentID        = "44444444-4444-4444-4444-444444444444"
	testTrainingWorkspaceID = "55555555-5555-5555-5555-555555555555"
	testProxyURL            = "http://db_bridge_stub:9100/v1"
)

type fakeDispatchLookup struct {
	dispatch db.TrainingDispatch
	err      error
	calls    int
}

func (f *fakeDispatchLookup) GetTrainingDispatchByProject(_ context.Context, _ pgtype.UUID) (db.TrainingDispatch, error) {
	f.calls++
	return f.dispatch, f.err
}

type fakeTaskStore struct {
	task   db.AgentInboxEvent
	getErr error
	merged []db.MergeTaskArealProxyContextParams
}

func (f *fakeTaskStore) GetAgentTask(_ context.Context, _ pgtype.UUID) (db.AgentInboxEvent, error) {
	return f.task, f.getErr
}

func (f *fakeTaskStore) MergeTaskArealProxyContext(_ context.Context, arg db.MergeTaskArealProxyContextParams) error {
	f.merged = append(f.merged, arg)
	return nil
}

type fakeRLClient struct {
	creds          arealrl.SessionCreds
	err            error
	calls          int
	lastTask       string
	lastEnv        string
	setRewardCalls []struct {
		proxyKey string
		reward   float64
	}
	callOrder []string // "StartSession", "SetReward"
}

func (f *fakeRLClient) StartSession(_ context.Context, taskID, envID string) (arealrl.SessionCreds, error) {
	f.calls++
	f.lastTask = taskID
	f.lastEnv = envID
	f.callOrder = append(f.callOrder, "StartSession")
	return f.creds, f.err
}

func (f *fakeRLClient) SetReward(_ context.Context, proxyKey string, reward float64) error {
	f.setRewardCalls = append(f.setRewardCalls, struct {
		proxyKey string
		reward   float64
	}{proxyKey, reward})
	f.callOrder = append(f.callOrder, "SetReward")
	return f.err
}

func trainingDispatchRow(trainAgentID string) db.TrainingDispatch {
	return db.TrainingDispatch{
		ProjectID:     util.MustParseUUID(testTrainingProjectID),
		WorkspaceID:   util.MustParseUUID(testTrainingWorkspaceID),
		TrainAgentID:  util.MustParseUUID(trainAgentID),
		DefaultReward: 1.0,
	}
}

func newTrainingDeps(lookup *fakeDispatchLookup, store *fakeTaskStore, rl *fakeRLClient) *TrainingSessionDeps {
	return &TrainingSessionDeps{
		Lookup:   lookup,
		Store:    store,
		RL:       rl,
		Closer:   rl,
		ProxyURL: testProxyURL,
	}
}

// (a) training project + agent == train_agent_id + no existing areal_proxy ->
// StartSession called once with the task id; context.areal_proxy persisted with
// proxy_key/base_url/session_id.
func TestMaybeOpenTrainingSession_TrainedTarget_OpensAndPersists(t *testing.T) {
	lookup := &fakeDispatchLookup{dispatch: trainingDispatchRow(testTrainAgentID)}
	store := &fakeTaskStore{}
	rl := &fakeRLClient{creds: arealrl.SessionCreds{SessionID: "sess-abc", ProxyKey: "pk-xyz"}}

	err := maybeOpenTrainingSession(
		context.Background(),
		newTrainingDeps(lookup, store, rl),
		testTrainingTaskID, testTrainAgentID, testTrainingProjectID, "",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rl.calls != 1 {
		t.Fatalf("StartSession calls = %d, want 1", rl.calls)
	}
	if rl.lastTask != testTrainingTaskID {
		t.Fatalf("StartSession task id = %q, want %q", rl.lastTask, testTrainingTaskID)
	}
	if len(store.merged) != 1 {
		t.Fatalf("MergeTaskArealProxyContext calls = %d, want 1", len(store.merged))
	}
	var cfg arealProxyConfig
	if err := json.Unmarshal(store.merged[0].ArealProxy, &cfg); err != nil {
		t.Fatalf("unmarshal persisted areal_proxy: %v", err)
	}
	if cfg.Provider != "openai" || cfg.Model != "areal-default" {
		t.Fatalf("provider/model = %q/%q, want openai/areal-default", cfg.Provider, cfg.Model)
	}
	if cfg.APIKey != "pk-xyz" {
		t.Fatalf("api_key = %q, want pk-xyz", cfg.APIKey)
	}
	if cfg.BaseURL != testProxyURL {
		t.Fatalf("base_url = %q, want %q", cfg.BaseURL, testProxyURL)
	}
	if cfg.SessionID != "sess-abc" {
		t.Fatalf("session_id = %q, want sess-abc", cfg.SessionID)
	}
}

// (b) non-training project -> no StartSession, context untouched.
func TestMaybeOpenTrainingSession_NonTrainingProject_NoOp(t *testing.T) {
	lookup := &fakeDispatchLookup{err: pgx.ErrNoRows}
	store := &fakeTaskStore{}
	rl := &fakeRLClient{}

	err := maybeOpenTrainingSession(
		context.Background(),
		newTrainingDeps(lookup, store, rl),
		testTrainingTaskID, testTrainAgentID, testTrainingProjectID, "",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rl.calls != 0 {
		t.Fatalf("StartSession calls = %d, want 0", rl.calls)
	}
	if len(store.merged) != 0 {
		t.Fatalf("context should be untouched, got %d merges", len(store.merged))
	}
}

// (c) agent != train_agent_id -> no StartSession.
func TestMaybeOpenTrainingSession_NonTargetAgent_NoOp(t *testing.T) {
	lookup := &fakeDispatchLookup{dispatch: trainingDispatchRow(testTrainAgentID)}
	store := &fakeTaskStore{}
	rl := &fakeRLClient{}

	err := maybeOpenTrainingSession(
		context.Background(),
		newTrainingDeps(lookup, store, rl),
		testTrainingTaskID, testOtherAgentID, testTrainingProjectID, "",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rl.calls != 0 {
		t.Fatalf("StartSession calls = %d, want 0", rl.calls)
	}
	if len(store.merged) != 0 {
		t.Fatalf("context should be untouched, got %d merges", len(store.merged))
	}
}

// (d) task already has areal_proxy -> skipped (idempotent).
func TestMaybeOpenTrainingSession_AlreadyOpen_Idempotent(t *testing.T) {
	lookup := &fakeDispatchLookup{dispatch: trainingDispatchRow(testTrainAgentID)}
	store := &fakeTaskStore{
		task: db.AgentInboxEvent{
			Context: []byte(`{"areal_proxy":{"provider":"areal","session_id":"sess-old"}}`),
		},
	}
	rl := &fakeRLClient{}

	err := maybeOpenTrainingSession(
		context.Background(),
		newTrainingDeps(lookup, store, rl),
		testTrainingTaskID, testTrainAgentID, testTrainingProjectID, "",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rl.calls != 0 {
		t.Fatalf("StartSession calls = %d, want 0 (idempotent)", rl.calls)
	}
	if len(store.merged) != 0 {
		t.Fatalf("idempotent skip should not re-persist, got %d merges", len(store.merged))
	}
}

// (e) D10: a trained-target session open records the {session_id -> agent_run_id,
// issue_id} mapping via the interaction DAG exactly once. agent_run_id is task.ID
// (D8); a re-open that finds an already-proxied session does not re-record.
func TestMaybeOpenTrainingSession_RecordsSessionAgentRun(t *testing.T) {
	lookup := &fakeDispatchLookup{dispatch: trainingDispatchRow(testTrainAgentID)}
	store := &fakeTaskStore{
		task: db.AgentInboxEvent{
			IssueID: util.MustParseUUID(testTrainingProjectID),
		},
	}
	rl := &fakeRLClient{creds: arealrl.SessionCreds{SessionID: "sess-d10", ProxyKey: "pk-d10"}}
	dagStore := newFakeInteractionDAGStore()
	dag := NewInteractionDAGService(dagStore, &fakeArealSegmentClient{}, true)

	deps := newTrainingDeps(lookup, store, rl)
	deps.DAG = dag

	if err := maybeOpenTrainingSession(
		context.Background(),
		deps,
		testTrainingTaskID, testTrainAgentID, testTrainingProjectID, "",
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dagStore.mu.Lock()
	gotCount := len(dagStore.sessionRuns)
	run, ok := dagStore.sessionRuns["sess-d10"]
	dagStore.mu.Unlock()
	if gotCount != 1 {
		t.Fatalf("session_runs = %d, want 1 after open", gotCount)
	}
	if !ok {
		t.Fatalf("no session_run recorded for sess-d10")
	}
	if run.AgentRunID != testTrainingTaskID {
		t.Fatalf("agent_run_id = %q, want %q (D8: agent_run_id = task.ID)", run.AgentRunID, testTrainingTaskID)
	}
	if !run.IssueID.Valid || run.IssueID.String != testTrainingProjectID {
		t.Fatalf("issue_id = %+v, want %q", run.IssueID, testTrainingProjectID)
	}

	// Idempotent: a re-open that finds an already-proxied session must not
	// re-record. The fake store does not mirror the persisted areal_proxy back
	// into task.Context, so simulate the post-open state explicitly.
	store.task.Context = []byte(`{"areal_proxy":{"provider":"areal","session_id":"sess-d10"}}`)
	if err := maybeOpenTrainingSession(
		context.Background(),
		deps,
		testTrainingTaskID, testTrainAgentID, testTrainingProjectID, "",
	); err != nil {
		t.Fatalf("unexpected error on re-open: %v", err)
	}
	dagStore.mu.Lock()
	gotCount = len(dagStore.sessionRuns)
	dagStore.mu.Unlock()
	if gotCount != 1 {
		t.Fatalf("session_runs = %d after re-open, want 1 (idempotent)", gotCount)
	}
}

// nil deps (training not configured) -> no-op, no error.
func TestMaybeOpenTrainingSession_NilDeps_NoOp(t *testing.T) {
	if err := maybeOpenTrainingSession(
		context.Background(), nil,
		testTrainingTaskID, testTrainAgentID, testTrainingProjectID, "",
	); err != nil {
		t.Fatalf("nil deps should be a no-op, got %v", err)
	}
}

// training target but RL bridge unconfigured -> loud error (do not run un-proxied).
func TestMaybeOpenTrainingSession_TargetButMissingBridge_LoudError(t *testing.T) {
	lookup := &fakeDispatchLookup{dispatch: trainingDispatchRow(testTrainAgentID)}
	store := &fakeTaskStore{}
	deps := &TrainingSessionDeps{Lookup: lookup, Store: store, RL: nil, ProxyURL: ""}

	err := maybeOpenTrainingSession(
		context.Background(), deps,
		testTrainingTaskID, testTrainAgentID, testTrainingProjectID, "",
	)
	if err == nil {
		t.Fatalf("expected loud error when RL bridge missing for a training target")
	}
}

// env_id is passed through to StartSession when provided.
func TestMaybeOpenTrainingSession_PassesEnvID(t *testing.T) {
	lookup := &fakeDispatchLookup{dispatch: trainingDispatchRow(testTrainAgentID)}
	store := &fakeTaskStore{}
	rl := &fakeRLClient{creds: arealrl.SessionCreds{SessionID: "sess-abc", ProxyKey: "pk-xyz"}}
	testEnvID := "env-12345"

	err := maybeOpenTrainingSession(
		context.Background(),
		newTrainingDeps(lookup, store, rl),
		testTrainingTaskID, testTrainAgentID, testTrainingProjectID, testEnvID,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rl.calls != 1 {
		t.Fatalf("StartSession calls = %d, want 1", rl.calls)
	}
	if rl.lastTask != testTrainingTaskID {
		t.Fatalf("StartSession task id = %q, want %q", rl.lastTask, testTrainingTaskID)
	}
	if rl.lastEnv != testEnvID {
		t.Fatalf("StartSession env id = %q, want %q", rl.lastEnv, testEnvID)
	}
}

// empty env_id is passed through when no env_id is available (e.g., non-env-dispatch paths).
func TestMaybeOpenTrainingSession_EmptyEnvID_WhenUnavailable(t *testing.T) {
	lookup := &fakeDispatchLookup{dispatch: trainingDispatchRow(testTrainAgentID)}
	store := &fakeTaskStore{}
	rl := &fakeRLClient{creds: arealrl.SessionCreds{SessionID: "sess-abc", ProxyKey: "pk-xyz"}}

	err := maybeOpenTrainingSession(
		context.Background(),
		newTrainingDeps(lookup, store, rl),
		testTrainingTaskID, testTrainAgentID, testTrainingProjectID, "",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rl.calls != 1 {
		t.Fatalf("StartSession calls = %d, want 1", rl.calls)
	}
	if rl.lastEnv != "" {
		t.Fatalf("StartSession env id = %q, want empty string", rl.lastEnv)
	}
}

// TestMaybeCloseTrainingSession_CompletedTask_CallsSetReward:
// task with context.areal_proxy carrying session_id + api_key reaches
// 'completed' -> SetReward(proxy_key, default_reward) is the whole close
// protocol. AReaL v2 has no end_session, so SetReward must be the only RL call.
func TestMaybeCloseTrainingSession_CompletedTask_CallsSetReward(t *testing.T) {
	lookup := &fakeDispatchLookup{dispatch: trainingDispatchRow(testTrainAgentID)}
	rl := &fakeRLClient{}
	deps := newTrainingDeps(lookup, nil, rl)

	task := db.AgentInboxEvent{
		ID:      util.MustParseUUID(testTrainingTaskID),
		Status:  "completed",
		Context: []byte(`{"areal_proxy":{"provider":"areal","model":"areal-default","api_key":"pk-xyz","base_url":"http://test","session_id":"sess-abc"}}`),
	}
	projectID := util.MustParseUUID(testTrainingProjectID)

	maybeCloseTrainingSession(context.Background(), deps, task, projectID)

	if len(rl.setRewardCalls) != 1 {
		t.Fatalf("SetReward calls = %d, want 1", len(rl.setRewardCalls))
	}
	if rl.setRewardCalls[0].proxyKey != "pk-xyz" {
		t.Fatalf("SetReward proxyKey = %q, want pk-xyz", rl.setRewardCalls[0].proxyKey)
	}
	if rl.setRewardCalls[0].reward != 1.0 {
		t.Fatalf("SetReward reward = %f, want 1.0", rl.setRewardCalls[0].reward)
	}

	if len(rl.callOrder) != 1 || rl.callOrder[0] != "SetReward" {
		t.Fatalf("call order = %v, want exactly [SetReward]", rl.callOrder)
	}
}

// TestMaybeCloseTrainingSession_FailedTask_AlsoCloses:
// task with areal_proxy reaches 'failed' -> SetReward called.
func TestMaybeCloseTrainingSession_FailedTask_AlsoCloses(t *testing.T) {
	lookup := &fakeDispatchLookup{dispatch: trainingDispatchRow(testTrainAgentID)}
	rl := &fakeRLClient{}
	deps := newTrainingDeps(lookup, nil, rl)

	task := db.AgentInboxEvent{
		ID:      util.MustParseUUID(testTrainingTaskID),
		Status:  "failed",
		Context: []byte(`{"areal_proxy":{"api_key":"pk-xyz","session_id":"sess-abc"}}`),
	}
	projectID := util.MustParseUUID(testTrainingProjectID)

	maybeCloseTrainingSession(context.Background(), deps, task, projectID)

	if len(rl.setRewardCalls) != 1 {
		t.Fatalf("SetReward not called for failed task: got %d calls", len(rl.setRewardCalls))
	}
}

// TestMaybeCloseTrainingSession_CancelledTask_AlsoCloses:
// task with areal_proxy reaches 'cancelled' -> SetReward called.
func TestMaybeCloseTrainingSession_CancelledTask_AlsoCloses(t *testing.T) {
	lookup := &fakeDispatchLookup{dispatch: trainingDispatchRow(testTrainAgentID)}
	rl := &fakeRLClient{}
	deps := newTrainingDeps(lookup, nil, rl)

	task := db.AgentInboxEvent{
		ID:      util.MustParseUUID(testTrainingTaskID),
		Status:  "cancelled",
		Context: []byte(`{"areal_proxy":{"api_key":"pk-xyz","session_id":"sess-abc"}}`),
	}
	projectID := util.MustParseUUID(testTrainingProjectID)

	maybeCloseTrainingSession(context.Background(), deps, task, projectID)

	if len(rl.setRewardCalls) != 1 {
		t.Fatalf("SetReward not called for cancelled task: got %d calls", len(rl.setRewardCalls))
	}
}

// TestMaybeCloseTrainingSession_NoArealProxy_NoOp:
// task without areal_proxy in context -> no RL calls.
func TestMaybeCloseTrainingSession_NoArealProxy_NoOp(t *testing.T) {
	lookup := &fakeDispatchLookup{dispatch: trainingDispatchRow(testTrainAgentID)}
	rl := &fakeRLClient{}
	deps := newTrainingDeps(lookup, nil, rl)

	task := db.AgentInboxEvent{
		ID:      util.MustParseUUID(testTrainingTaskID),
		Status:  "completed",
		Context: []byte(`{"other_key":"value"}`),
	}
	projectID := util.MustParseUUID(testTrainingProjectID)

	maybeCloseTrainingSession(context.Background(), deps, task, projectID)

	if len(rl.callOrder) != 0 {
		t.Fatalf("no-op expected, got RL calls %v", rl.callOrder)
	}
}

// TestMaybeCloseTrainingSession_RLClientError_LoggedNotFatal:
// SetReward returns error -> error is logged, the close hook still returns
// normally (the task is already terminal; a bridge failure must not escalate).
func TestMaybeCloseTrainingSession_RLClientError_LoggedNotFatal(t *testing.T) {
	lookup := &fakeDispatchLookup{dispatch: trainingDispatchRow(testTrainAgentID)}
	rl := &fakeRLClient{err: fmt.Errorf("rl bridge error")}
	deps := newTrainingDeps(lookup, nil, rl)

	task := db.AgentInboxEvent{
		ID:      util.MustParseUUID(testTrainingTaskID),
		Status:  "completed",
		Context: []byte(`{"areal_proxy":{"api_key":"pk-xyz","session_id":"sess-abc"}}`),
	}
	projectID := util.MustParseUUID(testTrainingProjectID)

	maybeCloseTrainingSession(context.Background(), deps, task, projectID)

	if len(rl.setRewardCalls) != 1 {
		t.Fatalf("SetReward should have been called once despite error")
	}
}

// TestMaybeCloseTrainingSession_NoTrainingDeps_NoOp:
// s.Training == nil -> no-op.
func TestMaybeCloseTrainingSession_NoTrainingDeps_NoOp(t *testing.T) {
	task := db.AgentInboxEvent{
		ID:      util.MustParseUUID(testTrainingTaskID),
		Status:  "completed",
		Context: []byte(`{"areal_proxy":{"api_key":"pk-xyz","session_id":"sess-abc"}}`),
	}
	projectID := util.MustParseUUID(testTrainingProjectID)

	maybeCloseTrainingSession(context.Background(), nil, task, projectID)
	// No error, that's all we need to check
}

// TestMaybeCloseTrainingSession_NoTrainingDispatch_FallbackReward:
// project has no training dispatch row -> use trainingDefaultReward fallback.
func TestMaybeCloseTrainingSession_NoTrainingDispatch_FallbackReward(t *testing.T) {
	lookup := &fakeDispatchLookup{err: pgx.ErrNoRows}
	rl := &fakeRLClient{}
	deps := newTrainingDeps(lookup, nil, rl)

	task := db.AgentInboxEvent{
		ID:      util.MustParseUUID(testTrainingTaskID),
		Status:  "completed",
		Context: []byte(`{"areal_proxy":{"api_key":"pk-xyz","session_id":"sess-abc"}}`),
	}
	projectID := util.MustParseUUID(testTrainingProjectID)

	maybeCloseTrainingSession(context.Background(), deps, task, projectID)

	if len(rl.setRewardCalls) != 1 {
		t.Fatalf("SetReward calls = %d, want 1", len(rl.setRewardCalls))
	}
	if rl.setRewardCalls[0].reward != trainingDefaultReward {
		t.Fatalf("SetReward reward = %f, want fallback %f", rl.setRewardCalls[0].reward, trainingDefaultReward)
	}
}

// --- checkpoint trigger tests ---

type fakeCheckpointTrigger struct {
	calls []fakeCheckpointTriggerCall
	err   error
}

type fakeCheckpointTriggerCall struct {
	task      db.AgentInboxEvent
	projectID pgtype.UUID
}

func (f *fakeCheckpointTrigger) TriggerCheckpoint(_ context.Context, task db.AgentInboxEvent, projectID pgtype.UUID) error {
	f.calls = append(f.calls, fakeCheckpointTriggerCall{task, projectID})
	return f.err
}

func TestTrainingCheckpointTriggerCreatesForTrainedStructuralEvent(t *testing.T) {
	trigger := &fakeCheckpointTrigger{}
	deps := &TrainingSessionDeps{CheckpointTrigger: trigger}
	task := db.AgentInboxEvent{ID: util.MustParseUUID(testTrainingTaskID)}
	projectID := util.MustParseUUID(testTrainingProjectID)

	maybeTriggerCheckpoint(context.Background(), deps, task, projectID)

	if len(trigger.calls) != 1 {
		t.Fatalf("want 1 trigger call, got %d", len(trigger.calls))
	}
	if util.UUIDToString(trigger.calls[0].projectID) != testTrainingProjectID {
		t.Fatalf("projectID mismatch: got %s", util.UUIDToString(trigger.calls[0].projectID))
	}
}

func TestTrainingCheckpointTriggerSkipsNonTrainingProject(t *testing.T) {
	// When no trigger is configured (non-training deployment or feature off),
	// maybeTriggerCheckpoint is a no-op — no panic, no call.
	trigger := &fakeCheckpointTrigger{}
	deps := &TrainingSessionDeps{CheckpointTrigger: nil}
	task := db.AgentInboxEvent{ID: util.MustParseUUID(testTrainingTaskID)}

	maybeTriggerCheckpoint(context.Background(), deps, task, util.MustParseUUID(testTrainingProjectID))

	if len(trigger.calls) != 0 {
		t.Fatalf("want 0 trigger calls when no trigger configured, got %d", len(trigger.calls))
	}
}

func TestTrainingCheckpointTriggerSkipsSweeperAutopilotAndSandboxLifecycleEvents(t *testing.T) {
	trigger := &fakeCheckpointTrigger{}
	deps := &TrainingSessionDeps{CheckpointTrigger: trigger}
	projectID := util.MustParseUUID(testTrainingProjectID)
	baseTask := db.AgentInboxEvent{ID: util.MustParseUUID(testTrainingTaskID)}

	t.Run("autopilot", func(t *testing.T) {
		trigger.calls = nil
		task := baseTask
		task.AutopilotRunID = util.MustParseUUID(testTrainingProjectID)
		maybeTriggerCheckpoint(context.Background(), deps, task, projectID)
		if len(trigger.calls) != 0 {
			t.Fatalf("autopilot task must not trigger checkpoint, got %d calls", len(trigger.calls))
		}
	})

	t.Run("sweeper_queued_expired", func(t *testing.T) {
		trigger.calls = nil
		task := baseTask
		task.FailureReason = pgtype.Text{String: "queued_expired", Valid: true}
		maybeTriggerCheckpoint(context.Background(), deps, task, projectID)
		if len(trigger.calls) != 0 {
			t.Fatalf("sweeper-failed task must not trigger checkpoint, got %d calls", len(trigger.calls))
		}
	})

	t.Run("sweeper_runtime_offline", func(t *testing.T) {
		trigger.calls = nil
		task := baseTask
		task.FailureReason = pgtype.Text{String: "runtime_offline", Valid: true}
		maybeTriggerCheckpoint(context.Background(), deps, task, projectID)
		if len(trigger.calls) != 0 {
			t.Fatalf("runtime-offline task must not trigger checkpoint, got %d calls", len(trigger.calls))
		}
	})

	t.Run("sandbox_lifecycle", func(t *testing.T) {
		trigger.calls = nil
		task := baseTask
		task.Context = []byte(`{"sandbox_lifecycle": {"job_type": "stop"}}`)
		maybeTriggerCheckpoint(context.Background(), deps, task, projectID)
		if len(trigger.calls) != 0 {
			t.Fatalf("sandbox lifecycle task must not trigger checkpoint, got %d calls", len(trigger.calls))
		}
	})
}

// AC-4: linkExistingTrainingSession links a real derived-agent task to a
// training session that env-dispatch provisioning already opened (before
// sandbox creation), WITHOUT calling StartSession (reuse), and is idempotent.
func TestLinkExistingTrainingSession_ReusesPersistsNoStartSession(t *testing.T) {
	ctx := context.Background()
	taskUUID := util.MustParseUUID(testTrainingTaskID)
	store := &fakeTaskStore{task: db.AgentInboxEvent{ID: taskUUID}}
	rl := &fakeRLClient{creds: arealrl.SessionCreds{SessionID: "fresh", ProxyKey: "fresh-key"}}
	deps := newTrainingDeps(&fakeDispatchLookup{}, store, rl)

	const sessionID, sessionKey = "sess-123", "proxy-key-456"
	if err := linkExistingTrainingSession(ctx, deps, testTrainingTaskID, testTrainAgentID, testTrainingProjectID, "env-1", sessionID, sessionKey); err != nil {
		t.Fatalf("link: %v", err)
	}
	// AC-4 retry identity: did NOT open a new session.
	if rl.calls != 0 {
		t.Errorf("StartSession called %d times, want 0 (reuse)", rl.calls)
	}
	// Persisted areal_proxy on the real task from the binding key + areal-default.
	if len(store.merged) != 1 {
		t.Fatalf("merged %d times, want 1", len(store.merged))
	}
	var cfg arealProxyConfig
	if err := json.Unmarshal(store.merged[0].ArealProxy, &cfg); err != nil {
		t.Fatalf("unmarshal areal_proxy: %v", err)
	}
	if cfg.APIKey != sessionKey || cfg.Model != arealProxyModel || cfg.SessionID != sessionID || cfg.BaseURL != testProxyURL {
		t.Errorf("unexpected areal_proxy: %+v", cfg)
	}
	// Idempotent: a task already carrying areal_proxy is a no-op.
	store.task.Context = []byte(`{"areal_proxy":{"session_id":"sess-123"}}`)
	store.merged = nil
	if err := linkExistingTrainingSession(ctx, deps, testTrainingTaskID, testTrainAgentID, testTrainingProjectID, "env-1", sessionID, sessionKey); err != nil {
		t.Fatalf("link idempotent: %v", err)
	}
	if len(store.merged) != 0 {
		t.Errorf("idempotent: merged %d times, want 0", len(store.merged))
	}
}

// AC-4: OpenEnvDispatchTrainingSession opens a session with session_ref=bindingID
// (not task_id) before sandbox creation; IsTrainingTarget detects the training
// source via the project's training dispatch.
func TestIsTrainingTarget_And_OpenEnvDispatchTrainingSession(t *testing.T) {
	ctx := context.Background()
	rl := &fakeRLClient{creds: arealrl.SessionCreds{SessionID: "open-sess", ProxyKey: "open-key"}}
	deps := &TrainingSessionDeps{Lookup: &fakeDispatchLookup{dispatch: trainingDispatchRow(testTrainAgentID)}, RL: rl, ProxyURL: testProxyURL}
	ts := &TaskService{Training: deps}

	if !ts.IsTrainingTarget(ctx, testTrainingProjectID, testTrainAgentID) {
		t.Errorf("IsTrainingTarget=false, want true for train agent")
	}
	if ts.IsTrainingTarget(ctx, testTrainingProjectID, "00000000-0000-0000-0000-000000000000") {
		t.Errorf("IsTrainingTarget=true for non-train agent")
	}

	const bindingID, envID = "binding-abc", "env-1"
	creds, err := ts.OpenEnvDispatchTrainingSession(ctx, envID, bindingID)
	if err != nil {
		t.Fatalf("OpenEnvDispatchTrainingSession: %v", err)
	}
	if creds.SessionID != "open-sess" || creds.ProxyKey != "open-key" {
		t.Errorf("creds = %+v", creds)
	}
	// session_ref is the binding ID (AC-4), NOT a task id.
	if rl.lastTask != bindingID {
		t.Errorf("StartSession session_ref = %q, want binding id %q", rl.lastTask, bindingID)
	}
	if rl.lastEnv != envID {
		t.Errorf("StartSession env = %q, want %q", rl.lastEnv, envID)
	}
	if ts.TrainingProxyURL() != testProxyURL {
		t.Errorf("TrainingProxyURL = %q, want %q", ts.TrainingProxyURL(), testProxyURL)
	}
}
