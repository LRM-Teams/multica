// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const testCriticAgentID = "55555555-5555-5555-5555-555555555555"

// fakeCriticCreator is the test fake for the criticTaskCreator interface. It
// records every CreateCriticTask call so tests can assert against the params
// and resulting task shape. FindCriticTaskForTrained returns either the
// pre-configured existing row (idempotency simulation) or pgx.ErrNoRows.
type fakeCriticCreator struct {
	existing      db.AgentInboxEvent
	findErr       error
	createErr     error
	createdTasks  []db.AgentInboxEvent
	createdParams []db.CreateCriticTaskParams
}

func (f *fakeCriticCreator) FindCriticTaskForTrained(_ context.Context, _ string) (db.AgentInboxEvent, error) {
	if f.findErr != nil {
		return db.AgentInboxEvent{}, f.findErr
	}
	if f.existing.ID.Valid {
		return f.existing, nil
	}
	return db.AgentInboxEvent{}, pgx.ErrNoRows
}

func (f *fakeCriticCreator) CreateCriticTask(_ context.Context, arg db.CreateCriticTaskParams) (db.AgentInboxEvent, error) {
	f.createdParams = append(f.createdParams, arg)
	task := db.AgentInboxEvent{
		ID:      util.MustParseUUID("66666666-6666-6666-6666-666666666666"),
		AgentID: arg.AgentID,
		IssueID: arg.IssueID,
		Status:  "queued",
		Context: arg.Context,
	}
	f.createdTasks = append(f.createdTasks, task)
	return task, f.createErr
}

// trainingDispatchRowWithCritic extends trainingDispatchRow to also set
// CriticAgentID. Pass "" for no critic (leaves CriticAgentID invalid).
func trainingDispatchRowWithCritic(trainAgentID, criticAgentID string) db.TrainingDispatch {
	td := trainingDispatchRow(trainAgentID)
	if criticAgentID != "" {
		td.CriticAgentID = util.MustParseUUID(criticAgentID)
	}
	return td
}

// trainedTaskForCriticTest builds a representative trained task row carrying
// an areal_proxy context and a TaskCompletedPayload result. Tests mutate it
// locally when they need a different shape (e.g., no proxy, no result).
func trainedTaskForCriticTest() db.AgentInboxEvent {
	return db.AgentInboxEvent{
		ID:      util.MustParseUUID(testTrainingTaskID),
		AgentID: util.MustParseUUID(testTrainAgentID),
		Context: []byte(`{"areal_proxy":{"session_id":"s1","api_key":"pk1","provider":"areal","model":"areal-default","base_url":"http://stub:9100/v1"}}`),
		Result:  []byte(`{"output":"trained agent's final output text"}`),
	}
}

// (1) Critic configured + no existing critic → spawn a critic task with
// context.critic_of linkage; parent_task_id NOT set; D's close NOT called.
func TestMaybeSpawnCriticTask_CreatesCriticTask(t *testing.T) {
	creator := &fakeCriticCreator{}
	rl := &fakeRLClient{}
	deps := &TrainingSessionDeps{
		Lookup:  &fakeDispatchLookup{dispatch: trainingDispatchRowWithCritic(testTrainAgentID, testCriticAgentID)},
		Creator: creator,
		Closer:  rl,
	}
	trainedTask := trainedTaskForCriticTest()
	td := trainingDispatchRowWithCritic(testTrainAgentID, testCriticAgentID)
	projectID := util.MustParseUUID(testTrainingProjectID)

	err := maybeSpawnCriticTask(context.Background(), deps, trainedTask, td, projectID)
	require.NoError(t, err)
	require.Len(t, creator.createdTasks, 1)

	critic := creator.createdTasks[0]
	assert.Equal(t, util.MustParseUUID(testCriticAgentID), critic.AgentID)
	// parent_task_id NOT set — critic is a peer, not a subtask
	assert.False(t, critic.ParentTaskID.Valid)

	// context.critic_of populated with trained task / session / project linkage
	var cof struct {
		TrainedTaskID string `json:"trained_task_id"`
		ProxyKey      string `json:"proxy_key"`
		SessionID     string `json:"session_id"`
		ProjectID     string `json:"project_id"`
	}
	var ctxPayload struct {
		CriticOf json.RawMessage `json:"critic_of"`
	}
	require.NoError(t, json.Unmarshal(critic.Context, &ctxPayload))
	require.NotEmpty(t, ctxPayload.CriticOf)
	require.NoError(t, json.Unmarshal(ctxPayload.CriticOf, &cof))
	assert.Equal(t, testTrainingTaskID, cof.TrainedTaskID)
	assert.Equal(t, "pk1", cof.ProxyKey)
	assert.Equal(t, "s1", cof.SessionID)
	assert.Equal(t, testTrainingProjectID, cof.ProjectID)

	// D's close NOT called — session remains open for the critic run
	assert.Empty(t, rl.setRewardCalls)
}

// (2) No critic configured → no spawn, no close (caller's D-close fallback fires).
func TestMaybeSpawnCriticTask_NoCritic_NoSpawn(t *testing.T) {
	creator := &fakeCriticCreator{}
	rl := &fakeRLClient{}
	deps := &TrainingSessionDeps{
		Lookup:  &fakeDispatchLookup{dispatch: trainingDispatchRowWithCritic(testTrainAgentID, "")},
		Creator: creator,
		Closer:  rl,
	}
	trainedTask := trainedTaskForCriticTest()
	td := trainingDispatchRowWithCritic(testTrainAgentID, "") // no critic
	projectID := util.MustParseUUID(testTrainingProjectID)

	err := maybeSpawnCriticTask(context.Background(), deps, trainedTask, td, projectID)
	require.NoError(t, err)
	assert.Empty(t, creator.createdTasks)
	// close NOT called here — the routing layer's no-critic branch owns the D-close fallback
	assert.Empty(t, rl.setRewardCalls)
}

// (3) Existing critic task already linked → idempotent skip, no duplicate spawn.
func TestMaybeSpawnCriticTask_Idempotent(t *testing.T) {
	creator := &fakeCriticCreator{
		existing: db.AgentInboxEvent{ID: util.MustParseUUID("66666666-6666-6666-6666-666666666666")},
	}
	rl := &fakeRLClient{}
	deps := &TrainingSessionDeps{
		Lookup:  &fakeDispatchLookup{dispatch: trainingDispatchRowWithCritic(testTrainAgentID, testCriticAgentID)},
		Creator: creator,
		Closer:  rl,
	}
	trainedTask := trainedTaskForCriticTest()
	td := trainingDispatchRowWithCritic(testTrainAgentID, testCriticAgentID)
	projectID := util.MustParseUUID(testTrainingProjectID)

	err := maybeSpawnCriticTask(context.Background(), deps, trainedTask, td, projectID)
	require.NoError(t, err)
	assert.Empty(t, creator.createdTasks) // no duplicate spawn
	assert.Empty(t, rl.setRewardCalls)    // close NOT called
}

// (4) CreateCriticTask fails → error swallowed, D's close fires with default reward.
func TestMaybeSpawnCriticTask_SpawnFails_FallsBackToD(t *testing.T) {
	creator := &fakeCriticCreator{createErr: errors.New("db down")}
	rl := &fakeRLClient{}
	deps := &TrainingSessionDeps{
		Lookup:        &fakeDispatchLookup{dispatch: trainingDispatchRowWithCritic(testTrainAgentID, testCriticAgentID)},
		Creator:       creator,
		Closer:        rl,
		DefaultReward: 1.0,
	}
	trainedTask := trainedTaskForCriticTest()
	td := trainingDispatchRowWithCritic(testTrainAgentID, testCriticAgentID)
	projectID := util.MustParseUUID(testTrainingProjectID)

	err := maybeSpawnCriticTask(context.Background(), deps, trainedTask, td, projectID)
	require.NoError(t, err) // error swallowed, fallback fired
	require.Len(t, rl.setRewardCalls, 1)
	// default reward used
	assert.Equal(t, 1.0, rl.setRewardCalls[0].reward)
}

// criticTaskForCloseTest builds a critic task row carrying context.critic_of
// and a TaskCompletedPayload result whose Output ends in {"reward": <float>}.
// Tests mutate it locally when they need a different shape (e.g., no critic_of,
// unparseable output, out-of-range reward).
func criticTaskForCloseTest(reward float64) db.AgentInboxEvent {
	return db.AgentInboxEvent{
		ID:      util.MustParseUUID("66666666-6666-6666-6666-666666666666"),
		AgentID: util.MustParseUUID(testCriticAgentID),
		Context: []byte(`{"critic_of":{"trained_task_id":"` + testTrainingTaskID + `","proxy_key":"pk1","session_id":"s1","project_id":"` + testTrainingProjectID + `"},"trained_output":"trained agent's final output text"}`),
		Result:  []byte(`{"output":"Some critique text...\n{\"reward\": ` + fmtFloat(reward) + `}"}`),
	}
}

// fmtFloat formats a float without trailing zeros (0.85 → "0.85", 1.5 → "1.5").
// Used only by test fixtures to embed expected rewards in JSON literals.
func fmtFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// (T8-1) Critic task with valid context.critic_of + Output ending in
// {"reward": 0.85} → SetReward("pk1", 0.85), and nothing else.
func TestMaybeCloseTrainingSessionFromCritic_ParsesRewardAndCloses(t *testing.T) {
	rl := &fakeRLClient{}
	deps := &TrainingSessionDeps{
		Closer:        rl,
		DefaultReward: 1.0,
	}
	criticTask := criticTaskForCloseTest(0.85)

	closed := maybeCloseTrainingSessionFromCritic(context.Background(), deps, criticTask)
	require.True(t, closed)
	require.Len(t, rl.setRewardCalls, 1)
	assert.Equal(t, "pk1", rl.setRewardCalls[0].proxyKey)
	assert.InDelta(t, 0.85, rl.setRewardCalls[0].reward, 0.001)
	assert.Equal(t, []string{"SetReward"}, rl.callOrder)
}

// (T8-2) Critic output without a parseable reward line → default reward used,
// session still closed.
func TestMaybeCloseTrainingSessionFromCritic_Unparseable_FallbackDefault(t *testing.T) {
	rl := &fakeRLClient{}
	deps := &TrainingSessionDeps{
		Closer:        rl,
		DefaultReward: 1.0,
	}
	criticTask := db.AgentInboxEvent{
		Context: []byte(`{"critic_of":{"proxy_key":"pk1","session_id":"s1","project_id":"` + testTrainingProjectID + `"}}`),
		Result:  []byte(`{"output":"I couldn't decide on a score"}`),
	}

	closed := maybeCloseTrainingSessionFromCritic(context.Background(), deps, criticTask)
	require.True(t, closed)
	require.Len(t, rl.setRewardCalls, 1)
	assert.InDelta(t, 1.0, rl.setRewardCalls[0].reward, 0.001) // default
}

// (T8-3) Critic reward 1.5 (out of [0.0, 1.0]) → default reward used.
func TestMaybeCloseTrainingSessionFromCritic_OutOfRange_FallbackDefault(t *testing.T) {
	rl := &fakeRLClient{}
	deps := &TrainingSessionDeps{
		Closer:        rl,
		DefaultReward: 1.0,
	}
	criticTask := criticTaskForCloseTest(1.5)

	closed := maybeCloseTrainingSessionFromCritic(context.Background(), deps, criticTask)
	require.True(t, closed)
	require.Len(t, rl.setRewardCalls, 1)
	assert.InDelta(t, 1.0, rl.setRewardCalls[0].reward, 0.001) // default fallback
}

// (T8-4) Non-critic task (no context.critic_of) → no-op, returns false so the
// routing layer proceeds with trained-terminal logic.
func TestMaybeCloseTrainingSessionFromCritic_NonCriticTask_NoOp(t *testing.T) {
	rl := &fakeRLClient{}
	deps := &TrainingSessionDeps{
		Closer:        rl,
		DefaultReward: 1.0,
	}
	regularTask := db.AgentInboxEvent{
		Context: []byte(`{}`), // no critic_of
	}

	closed := maybeCloseTrainingSessionFromCritic(context.Background(), deps, regularTask)
	assert.False(t, closed)
	assert.Empty(t, rl.setRewardCalls)
}

// (T8-5) Nil deps (training not configured) → no-op, returns false.
func TestMaybeCloseTrainingSessionFromCritic_NilDeps_NoOp(t *testing.T) {
	regularTask := db.AgentInboxEvent{
		Context: []byte(`{"critic_of":{"proxy_key":"pk1","session_id":"s1","project_id":"` + testTrainingProjectID + `"}}`),
	}
	closed := maybeCloseTrainingSessionFromCritic(context.Background(), nil, regularTask)
	assert.False(t, closed)
}

// (T8-6) Malformed context.critic_of (not valid JSON) → treat as non-critic,
// return false so routing proceeds.
func TestMaybeCloseTrainingSessionFromCritic_MalformedCriticOf_NoOp(t *testing.T) {
	rl := &fakeRLClient{}
	deps := &TrainingSessionDeps{
		Closer:        rl,
		DefaultReward: 1.0,
	}
	criticTask := db.AgentInboxEvent{
		Context: []byte(`{"critic_of":not-valid-json}`),
	}

	closed := maybeCloseTrainingSessionFromCritic(context.Background(), deps, criticTask)
	assert.False(t, closed)
	assert.Empty(t, rl.setRewardCalls)
}
