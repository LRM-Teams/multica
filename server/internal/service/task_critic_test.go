// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"errors"
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
	existing      db.AgentTaskQueue
	findErr       error
	createErr     error
	createdTasks  []db.AgentTaskQueue
	createdParams []db.CreateCriticTaskParams
}

func (f *fakeCriticCreator) FindCriticTaskForTrained(_ context.Context, _ string) (db.AgentTaskQueue, error) {
	if f.findErr != nil {
		return db.AgentTaskQueue{}, f.findErr
	}
	if f.existing.ID.Valid {
		return f.existing, nil
	}
	return db.AgentTaskQueue{}, pgx.ErrNoRows
}

func (f *fakeCriticCreator) CreateCriticTask(_ context.Context, arg db.CreateCriticTaskParams) (db.AgentTaskQueue, error) {
	f.createdParams = append(f.createdParams, arg)
	task := db.AgentTaskQueue{
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
func trainedTaskForCriticTest() db.AgentTaskQueue {
	return db.AgentTaskQueue{
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
	assert.Empty(t, rl.endSessionCalls)
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
	assert.Empty(t, rl.endSessionCalls)
}

// (3) Existing critic task already linked → idempotent skip, no duplicate spawn.
func TestMaybeSpawnCriticTask_Idempotent(t *testing.T) {
	creator := &fakeCriticCreator{
		existing: db.AgentTaskQueue{ID: util.MustParseUUID("66666666-6666-6666-6666-666666666666")},
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
	assert.Empty(t, rl.endSessionCalls)
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
	require.Len(t, rl.endSessionCalls, 1)
	// default reward used
	assert.Equal(t, 1.0, rl.setRewardCalls[0].reward)
}
