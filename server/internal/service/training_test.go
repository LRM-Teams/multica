// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/arealrl"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	testTrainingProjectID = "11111111-1111-1111-1111-111111111111"
	testTrainAgentID      = "22222222-2222-2222-2222-222222222222"
	testTrainingTaskID    = "33333333-3333-3333-3333-333333333333"
	testOtherAgentID      = "44444444-4444-4444-4444-444444444444"
	testProxyURL          = "http://db_bridge_stub:9100/v1"
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
	task   db.AgentTaskQueue
	getErr error
	merged []db.MergeTaskArealProxyContextParams
}

func (f *fakeTaskStore) GetAgentTask(_ context.Context, _ pgtype.UUID) (db.AgentTaskQueue, error) {
	return f.task, f.getErr
}

func (f *fakeTaskStore) MergeTaskArealProxyContext(_ context.Context, arg db.MergeTaskArealProxyContextParams) error {
	f.merged = append(f.merged, arg)
	return nil
}

type fakeRLClient struct {
	creds    arealrl.SessionCreds
	err      error
	calls    int
	lastTask string
}

func (f *fakeRLClient) StartSession(_ context.Context, taskID string) (arealrl.SessionCreds, error) {
	f.calls++
	f.lastTask = taskID
	return f.creds, f.err
}

func trainingDispatchRow(trainAgentID string) db.TrainingDispatch {
	return db.TrainingDispatch{
		ProjectID:     util.MustParseUUID(testTrainingProjectID),
		TrainAgentID:  util.MustParseUUID(trainAgentID),
		DefaultReward: 1.0,
	}
}

func newTrainingDeps(lookup *fakeDispatchLookup, store *fakeTaskStore, rl *fakeRLClient) *TrainingSessionDeps {
	return &TrainingSessionDeps{
		Lookup:   lookup,
		Store:    store,
		RL:       rl,
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
		testTrainingTaskID, testTrainAgentID, testTrainingProjectID,
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
	if cfg.Provider != "areal" || cfg.Model != "areal-default" {
		t.Fatalf("provider/model = %q/%q, want areal/areal-default", cfg.Provider, cfg.Model)
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
		testTrainingTaskID, testTrainAgentID, testTrainingProjectID,
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
		testTrainingTaskID, testOtherAgentID, testTrainingProjectID,
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
		task: db.AgentTaskQueue{
			Context: []byte(`{"areal_proxy":{"provider":"areal","session_id":"sess-old"}}`),
		},
	}
	rl := &fakeRLClient{}

	err := maybeOpenTrainingSession(
		context.Background(),
		newTrainingDeps(lookup, store, rl),
		testTrainingTaskID, testTrainAgentID, testTrainingProjectID,
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

// nil deps (training not configured) -> no-op, no error.
func TestMaybeOpenTrainingSession_NilDeps_NoOp(t *testing.T) {
	if err := maybeOpenTrainingSession(
		context.Background(), nil,
		testTrainingTaskID, testTrainAgentID, testTrainingProjectID,
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
		testTrainingTaskID, testTrainAgentID, testTrainingProjectID,
	)
	if err == nil {
		t.Fatalf("expected loud error when RL bridge missing for a training target")
	}
}
