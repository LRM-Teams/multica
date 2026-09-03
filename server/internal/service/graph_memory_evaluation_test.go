// SPDX-License-Identifier: Apache-2.0

package service

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/util"
)

// Evaluation protocol plane tests (Handoff 7 §6/§10): fail-closed gate,
// run/episode lifecycle with strict closure, live-channel uniqueness,
// binding immutability, append-only usage ledger, official-score state
// machine, and the arm enforcement seams (recall Begin, gateway operations).
//
// Arm enforcement tests run BEFORE any benchmark execution may, per the
// handoff: persistence_off must refuse recall and every gateway operation
// from durable server state alone.

func evaluationTestEnv(t *testing.T, workspaceIDs ...string) {
	t.Helper()
	t.Setenv("MULTICA_GRAPH_MEMORY_EVALUATION_ENABLED", "1")
	allow := ""
	for _, id := range workspaceIDs {
		if allow != "" {
			allow += ","
		}
		allow += id
	}
	t.Setenv("MULTICA_GRAPH_MEMORY_EVALUATION_WORKSPACES", allow)
}

func fullClosureChecklist() map[string]GraphMemoryClosureState {
	complete := GraphMemoryClosureState{State: "complete"}
	return map[string]GraphMemoryClosureState{
		GraphMemoryClosureSessionGenerationReset: complete,
		GraphMemoryClosurePrimaryReplyCommitted:  complete,
		GraphMemoryClosureDaemonProjection:       complete,
		GraphMemoryClosureNoActiveClaim:          complete,
		GraphMemoryClosureCheckpointSettled:      complete,
		GraphMemoryClosureJobsSettled:            complete,
		GraphMemoryClosureStateTiedToGeneration:  complete,
	}
}

// The plane fails closed in every direction: no env gate → disabled; gate on
// but workspace not allowlisted → disabled; allowlist matching is
// case-insensitive and whitespace tolerant.
func TestGraphMemoryEvaluationGateFailsClosed(t *testing.T) {
	pool := dualShapeTestPool(t)
	f := newDualShapeFixture(t, pool)
	svc := NewGraphMemoryEvaluationService(pool)
	ctx := context.Background()

	input := GraphMemoryEvaluationRunInput{WorkspaceID: f.wsID, RunID: "gate-run-" + uuid.NewString()[:8], CreatedByActor: "harness"}
	require.ErrorIs(t, svc.CreateRun(ctx, input), ErrGraphMemoryEvaluationDisabled)

	t.Setenv("MULTICA_GRAPH_MEMORY_EVALUATION_ENABLED", "1")
	t.Setenv("MULTICA_GRAPH_MEMORY_EVALUATION_WORKSPACES", "")
	require.ErrorIs(t, svc.CreateRun(ctx, input), ErrGraphMemoryEvaluationDisabled)
	require.False(t, GraphMemoryEvaluationGated(f.wsID))

	t.Setenv("MULTICA_GRAPH_MEMORY_EVALUATION_WORKSPACES", " "+util.UUIDToString(util.MustParseUUID(f.wsID))+" ")
	require.NoError(t, svc.CreateRun(ctx, input))
	require.True(t, GraphMemoryEvaluationGated(f.wsID))
	// Duplicate run ids collide loudly, never overwrite.
	require.ErrorIs(t, svc.CreateRun(ctx, input), ErrGraphMemoryEvaluationState)
}

// Full lifecycle: episode creation binds arm+session immutably, one live
// episode per channel, strict seven-condition closure gates settle, usage
// kind is database-enforced, and views read back the truth.
func TestGraphMemoryEvaluationEpisodeLifecycleAndClosure(t *testing.T) {
	pool := dualShapeTestPool(t)
	f := newDualShapeFixture(t, pool)
	evaluationTestEnv(t, f.wsID)
	svc := NewGraphMemoryEvaluationService(pool)
	ctx := context.Background()

	runID := "lifecycle-run-" + uuid.NewString()[:8]
	require.NoError(t, svc.CreateRun(ctx, GraphMemoryEvaluationRunInput{
		WorkspaceID: f.wsID, RunID: runID, Label: "lifecycle", CreatedByActor: "harness",
	}))

	episodeID := "ep-off"
	require.NoError(t, svc.CreateEpisode(ctx, GraphMemoryEvaluationEpisodeInput{
		WorkspaceID: f.wsID, RunID: runID, EpisodeID: episodeID,
		ChannelID: f.channelID, PrimaryAgentID: f.inboxAgent,
		Arm: GraphMemoryEvaluationArmPersistenceOff, SessionGeneration: "sgen-1",
	}))
	require.ErrorIs(t, svc.CreateEpisode(ctx, GraphMemoryEvaluationEpisodeInput{
		WorkspaceID: f.wsID, RunID: runID, EpisodeID: "ep-second",
		ChannelID: f.channelID, PrimaryAgentID: f.inboxAgent,
		Arm: GraphMemoryEvaluationArmGraphOn, SessionGeneration: "sgen-2",
	}), ErrGraphMemoryEvaluationPolicyLock)
	// Unknown arms never bind.
	require.ErrorIs(t, svc.CreateEpisode(ctx, GraphMemoryEvaluationEpisodeInput{
		WorkspaceID: f.wsID, RunID: runID, EpisodeID: "ep-bad-arm",
		ChannelID: f.channelID, PrimaryAgentID: f.inboxAgent,
		Arm: "prompt_only", SessionGeneration: "sgen-3",
	}), ErrGraphMemoryEvaluationState)

	inputMessage := uuid.NewString()
	require.NoError(t, svc.StartEpisode(ctx, f.wsID, runID, episodeID, inputMessage))

	// Closure refuses while any required condition is not complete.
	partial := fullClosureChecklist()
	partial[GraphMemoryClosureCheckpointSettled] = GraphMemoryClosureState{State: "skipped"}
	delete(partial, GraphMemoryClosureJobsSettled)
	require.ErrorIs(t, svc.SettleEpisode(ctx, f.wsID, runID, episodeID, uuid.NewString(), partial), ErrGraphMemoryEvaluationClosure)

	require.NoError(t, svc.RecordUsage(ctx, GraphMemoryUsageEventInput{
		WorkspaceID: f.wsID, RunID: runID, EpisodeID: episodeID,
		Kind: "provider_tokens", Payload: map[string]any{"input": 120, "output": 80},
	}))
	require.Error(t, svc.RecordUsage(ctx, GraphMemoryUsageEventInput{
		WorkspaceID: f.wsID, RunID: runID, EpisodeID: episodeID, Kind: "not_a_kind",
	})) // kind vocabulary is database-enforced
	require.ErrorIs(t, svc.RecordUsage(ctx, GraphMemoryUsageEventInput{
		WorkspaceID: f.wsID, RunID: runID, EpisodeID: "missing-episode",
		Kind: "provider_tokens",
	}), ErrGraphMemoryEvaluationNotFound)

	outputMessage := uuid.NewString()
	require.NoError(t, svc.SettleEpisode(ctx, f.wsID, runID, episodeID, outputMessage, fullClosureChecklist()))
	// Settled episodes are terminal for the lifecycle path.
	require.ErrorIs(t, svc.SettleEpisode(ctx, f.wsID, runID, episodeID, outputMessage, fullClosureChecklist()), ErrGraphMemoryEvaluationNotFound)
	require.ErrorIs(t, svc.FailEpisode(ctx, f.wsID, runID, episodeID, "late"), ErrGraphMemoryEvaluationNotFound)

	// A settled episode frees the channel for the next one.
	require.NoError(t, svc.CreateEpisode(ctx, GraphMemoryEvaluationEpisodeInput{
		WorkspaceID: f.wsID, RunID: runID, EpisodeID: "ep-next",
		ChannelID: f.channelID, PrimaryAgentID: f.inboxAgent,
		Arm: GraphMemoryEvaluationArmGraphOn, SessionGeneration: "sgen-4",
	}))

	run, episodes, err := svc.GetRun(ctx, f.wsID, runID)
	require.NoError(t, err)
	assert.Equal(t, "running", run.Status)
	require.Len(t, episodes, 2)
	assert.Equal(t, GraphMemoryEvaluationArmPersistenceOff, episodes[0].Arm)
	assert.Equal(t, "persistence_off", episodes[0].MemoryPolicy)
	assert.Equal(t, "settled", episodes[0].Status)
	assert.Equal(t, "sgen-1", episodes[0].SessionGeneration)
	assert.Equal(t, outputMessage, episodes[0].OutputMessageID)
	assert.Equal(t, "unscored", episodes[0].OfficialScoreState)

	runs, err := svc.ListRuns(ctx, f.wsID, 10)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(runs), 1)
	assert.Equal(t, runID, runs[0].RunID)

	require.NoError(t, svc.CompleteRun(ctx, f.wsID, runID, "completed"))
	require.ErrorIs(t, svc.CreateEpisode(ctx, GraphMemoryEvaluationEpisodeInput{
		WorkspaceID: f.wsID, RunID: runID, EpisodeID: "ep-after-complete",
		ChannelID: f.channelID, PrimaryAgentID: f.inboxAgent,
		Arm: GraphMemoryEvaluationArmGraphOn, SessionGeneration: "sgen-5",
	}), ErrGraphMemoryEvaluationNotFound)
}

// The database, not the store, holds binding immutability and the
// append-only usage ledger.
func TestGraphMemoryEvaluationBindingImmutableAndUsageAppendOnly(t *testing.T) {
	pool := dualShapeTestPool(t)
	f := newDualShapeFixture(t, pool)
	evaluationTestEnv(t, f.wsID)
	svc := NewGraphMemoryEvaluationService(pool)
	ctx := context.Background()

	runID := "immutable-run-" + uuid.NewString()[:8]
	require.NoError(t, svc.CreateRun(ctx, GraphMemoryEvaluationRunInput{WorkspaceID: f.wsID, RunID: runID, CreatedByActor: "harness"}))
	require.NoError(t, svc.CreateEpisode(ctx, GraphMemoryEvaluationEpisodeInput{
		WorkspaceID: f.wsID, RunID: runID, EpisodeID: "ep",
		ChannelID: f.channelID, PrimaryAgentID: f.inboxAgent,
		Arm: GraphMemoryEvaluationArmGraphOn, SessionGeneration: "sgen",
	}))
	require.NoError(t, svc.RecordUsage(ctx, GraphMemoryUsageEventInput{
		WorkspaceID: f.wsID, RunID: runID, EpisodeID: "ep", Kind: "gateway_operation",
	}))

	_, err := pool.Exec(ctx, `UPDATE graph_memory_evaluation_episode SET arm='persistence_off' WHERE workspace_id=$1::uuid AND run_id=$2`, f.wsID, runID)
	require.ErrorContains(t, err, "immutable")
	_, err = pool.Exec(ctx, `UPDATE graph_memory_evaluation_usage_event SET payload='{"x":1}'::jsonb WHERE workspace_id=$1::uuid AND run_id=$2`, f.wsID, runID)
	require.ErrorContains(t, err, "append-only")
	_, err = pool.Exec(ctx, `DELETE FROM graph_memory_evaluation_usage_event WHERE workspace_id=$1::uuid AND run_id=$2`, f.wsID, runID)
	require.ErrorContains(t, err, "append-only")
}

// Official scoring is a state machine with an absorbing 'unsupported' state:
// only settled episodes score, 'scored' requires a payload, and explicit
// 'unsupported' can never be scored afterwards.
func TestGraphMemoryEvaluationOfficialScoreStateMachine(t *testing.T) {
	pool := dualShapeTestPool(t)
	f := newDualShapeFixture(t, pool)
	evaluationTestEnv(t, f.wsID)
	svc := NewGraphMemoryEvaluationService(pool)
	ctx := context.Background()

	newEpisode := func(id string) string {
		t.Helper()
		runID := "score-run-" + uuid.NewString()[:8]
		require.NoError(t, svc.CreateRun(ctx, GraphMemoryEvaluationRunInput{WorkspaceID: f.wsID, RunID: runID, CreatedByActor: "harness"}))
		require.NoError(t, svc.CreateEpisode(ctx, GraphMemoryEvaluationEpisodeInput{
			WorkspaceID: f.wsID, RunID: runID, EpisodeID: id,
			ChannelID: f.channelID, PrimaryAgentID: f.inboxAgent,
			Arm: GraphMemoryEvaluationArmGraphOn, SessionGeneration: "sgen-" + id,
		}))
		require.NoError(t, svc.StartEpisode(ctx, f.wsID, runID, id, uuid.NewString()))
		require.NoError(t, svc.SettleEpisode(ctx, f.wsID, runID, id, uuid.NewString(), fullClosureChecklist()))
		return runID
	}

	// Scoring an unsettled episode is impossible.
	require.ErrorIs(t, svc.MarkOfficialScore(ctx, f.wsID, "no-such-run", "ep", "scored", map[string]any{"s": 1}, "sha256:ab"), ErrGraphMemoryEvaluationEvidence)

	scoredRun := newEpisode("ep-scored")
	require.NoError(t, svc.MarkOfficialScore(ctx, f.wsID, scoredRun, "ep-scored", "ready", nil, "sha256:"+repeat("a", 64)))
	require.NoError(t, svc.MarkOfficialScore(ctx, f.wsID, scoredRun, "ep-scored", "scored", map[string]any{"score": 0.75}, "sha256:"+repeat("b", 64)))
	require.ErrorIs(t, svc.MarkOfficialScore(ctx, f.wsID, scoredRun, "ep-scored", "scored", nil, "sha256:"+repeat("c", 64)), ErrGraphMemoryEvaluationEvidence)
	_, episodes, err := svc.GetRun(ctx, f.wsID, scoredRun)
	require.NoError(t, err)
	require.Len(t, episodes, 1)
	assert.Equal(t, "scored", episodes[0].OfficialScoreState)
	assert.JSONEq(t, `{"score":0.75}`, string(episodes[0].OfficialScore))

	unsupportedRun := newEpisode("ep-unsupported")
	require.NoError(t, svc.MarkOfficialScore(ctx, f.wsID, unsupportedRun, "ep-unsupported", "unsupported", nil, ""))
	require.ErrorIs(t, svc.MarkOfficialScore(ctx, f.wsID, unsupportedRun, "ep-unsupported", "scored", map[string]any{"score": 1.0}, "sha256:"+repeat("d", 64)), ErrGraphMemoryEvaluationEvidence)
}

func repeat(s string, n int) string {
	out := make([]byte, 0, n*len(s))
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}

// Condition 4 is server-verified: settle refuses while the episode channel's
// managed agent holds a running-status run claim, and unblocks once the run
// closes (submit path nulls active_run_id).
func TestGraphMemoryEvaluationSettleRefusesActiveGraphRunClaim(t *testing.T) {
	f := seedGatewayResearchWorkspace(t)
	evaluationTestEnv(t, f.workspaceID)
	svc := NewGraphMemoryEvaluationService(f.pool)
	ctx := context.Background()

	runID := "claim-run-" + uuid.NewString()[:8]
	require.NoError(t, svc.CreateRun(ctx, GraphMemoryEvaluationRunInput{WorkspaceID: f.workspaceID, RunID: runID, CreatedByActor: "harness"}))
	require.NoError(t, svc.CreateEpisode(ctx, GraphMemoryEvaluationEpisodeInput{
		WorkspaceID: f.workspaceID, RunID: runID, EpisodeID: "ep",
		ChannelID: f.channelID, PrimaryAgentID: f.agentID,
		Arm: GraphMemoryEvaluationArmGraphOn, SessionGeneration: "sgen",
	}))
	require.NoError(t, svc.StartEpisode(ctx, f.workspaceID, runID, "ep", uuid.NewString()))

	var graphRunID string
	require.NoError(t, f.pool.QueryRow(ctx, `
		INSERT INTO graph_memory_agent_run (workspace_id, channel_id, status, fencing_token)
		VALUES ($1::uuid, $2::uuid, 'running', 1) RETURNING id::text`,
		f.workspaceID, f.channelID).Scan(&graphRunID))
	_, err := f.pool.Exec(ctx, `UPDATE graph_memory_agent_state SET active_run_id=$1::uuid WHERE channel_id=$2::uuid`, graphRunID, f.channelID)
	require.NoError(t, err)

	require.ErrorIs(t, svc.SettleEpisode(ctx, f.workspaceID, runID, "ep", uuid.NewString(), fullClosureChecklist()), ErrGraphMemoryEvaluationClosure)

	_, err = f.pool.Exec(ctx, `UPDATE graph_memory_agent_run SET status='submitted' WHERE id=$1::uuid`, graphRunID)
	require.NoError(t, err)
	_, err = f.pool.Exec(ctx, `UPDATE graph_memory_agent_state SET active_run_id=NULL WHERE channel_id=$1::uuid`, f.channelID)
	require.NoError(t, err)
	require.NoError(t, svc.SettleEpisode(ctx, f.workspaceID, runID, "ep", uuid.NewString(), fullClosureChecklist()))
}

// Recall arm enforcement: a live persistence_off episode disables recall at
// Begin from durable state; settling the episode restores it.
func TestGraphMemoryRecallRefusesPersistenceOffEpisode(t *testing.T) {
	pool := dualShapeTestPool(t)
	f := newDualShapeFixture(t, pool)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		INSERT INTO graph_memory_profile (workspace_id, memory_type, graph_memory_mode)
		VALUES ($1::uuid, 'graph', 'agent')`, f.wsID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE agent SET managed_role='graph_memory_channel' WHERE id=$1::uuid`, f.inboxAgent)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO graph_memory_channel_agent (workspace_id, channel_id, agent_id, runtime_id, handle, display_name, status)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 'memory-eval', 'Memory eval', 'active')`,
		f.wsID, f.channelID, f.inboxAgent, f.runtimeID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO channel_member (workspace_id, channel_id, member_type, member_id, role)
		VALUES ($1::uuid, $2::uuid, 'agent', $3::uuid, 'member') ON CONFLICT DO NOTHING`,
		f.wsID, f.channelID, f.inboxAgent)
	require.NoError(t, err)

	evaluationTestEnv(t, f.wsID)
	evaluation := NewGraphMemoryEvaluationService(pool)
	svc := f.service()
	svc.WireGraphMemoryEvaluation(evaluation)

	// Baseline: the agent-mode managed exception lets recall begin.
	request := f.request(f.userMsgID)
	request.ManagedGraphMemoryAgentID = f.inboxAgent
	_, err = svc.Begin(ctx, request)
	require.NoError(t, err)

	runID := "recall-run-" + uuid.NewString()[:8]
	require.NoError(t, evaluation.CreateRun(ctx, GraphMemoryEvaluationRunInput{WorkspaceID: f.wsID, RunID: runID, CreatedByActor: "harness"}))
	require.NoError(t, evaluation.CreateEpisode(ctx, GraphMemoryEvaluationEpisodeInput{
		WorkspaceID: f.wsID, RunID: runID, EpisodeID: "ep",
		ChannelID: f.channelID, PrimaryAgentID: f.inboxAgent,
		Arm: GraphMemoryEvaluationArmPersistenceOff, SessionGeneration: "sgen",
	}))

	// The same authorized request now fails closed with the arm reason.
	_, err = svc.Begin(ctx, request)
	require.ErrorIs(t, err, ErrGraphMemoryRecallDisabled)
	require.ErrorContains(t, err, "persistence_off")

	// The refusal left ledger evidence on the episode.
	var denials int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*) FROM graph_memory_evaluation_usage_event
		WHERE workspace_id=$1::uuid AND run_id=$2 AND kind='policy_denial' AND payload->>'seam'='recall_begin'`,
		f.wsID, runID).Scan(&denials))
	assert.GreaterOrEqual(t, denials, 1)

	// graph_on episodes never narrow recall.
	require.NoError(t, evaluation.StartEpisode(ctx, f.wsID, runID, "ep", uuid.NewString()))
	require.NoError(t, evaluation.SettleEpisode(ctx, f.wsID, runID, "ep", uuid.NewString(), fullClosureChecklist()))
	require.NoError(t, evaluation.CreateEpisode(ctx, GraphMemoryEvaluationEpisodeInput{
		WorkspaceID: f.wsID, RunID: runID, EpisodeID: "ep-on",
		ChannelID: f.channelID, PrimaryAgentID: f.inboxAgent,
		Arm: GraphMemoryEvaluationArmGraphOn, SessionGeneration: "sgen-on",
	}))
	_, err = svc.Begin(ctx, request)
	require.NoError(t, err)
}

// Gateway arm enforcement: a live persistence_off episode refuses every
// operation — start and the daemon's auto-checkpoint alike — with ledger
// evidence, and settling restores service.
func TestGraphMemoryAgentGatewayRefusesPersistenceOffEpisode(t *testing.T) {
	f := seedGatewayResearchWorkspace(t)
	evaluationTestEnv(t, f.workspaceID)
	evaluation := NewGraphMemoryEvaluationService(f.pool)
	f.gateway.WireGraphMemoryEvaluation(evaluation)

	runID := "gw-run-" + uuid.NewString()[:8]
	require.NoError(t, evaluation.CreateRun(context.Background(), GraphMemoryEvaluationRunInput{WorkspaceID: f.workspaceID, RunID: runID, CreatedByActor: "harness"}))
	require.NoError(t, evaluation.CreateEpisode(context.Background(), GraphMemoryEvaluationEpisodeInput{
		WorkspaceID: f.workspaceID, RunID: runID, EpisodeID: "ep",
		ChannelID: f.channelID, PrimaryAgentID: f.agentID,
		Arm: GraphMemoryEvaluationArmPersistenceOff, SessionGeneration: "sgen",
	}))

	post := func(operation, body string) error {
		return f.gateway.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPost, "/graph-memory/"+operation, bytes.NewBufferString(body)),
			f.workspaceID, f.agentID, f.channelID, operation)
	}
	require.ErrorIs(t, post("start", `{"query":"cache pools"}`), ErrGraphMemoryAgentGatewayForbidden)
	require.ErrorIs(t, post("checkpoint", `{"idempotency_key":"auto:m1"}`), ErrGraphMemoryAgentGatewayForbidden)
	require.ErrorIs(t, post("submit", `{"trajectory_id":"00000000-0000-0000-0000-000000000000"}`), ErrGraphMemoryAgentGatewayForbidden)

	var denials int
	require.NoError(t, f.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM graph_memory_evaluation_usage_event
		WHERE workspace_id=$1::uuid AND run_id=$2 AND kind='policy_denial' AND payload->>'seam'='agent_gateway'`,
		f.workspaceID, runID).Scan(&denials))
	assert.GreaterOrEqual(t, denials, 3)

	// Settling the episode restores the data plane.
	require.NoError(t, evaluation.StartEpisode(context.Background(), f.workspaceID, runID, "ep", uuid.NewString()))
	require.NoError(t, evaluation.SettleEpisode(context.Background(), f.workspaceID, runID, "ep", uuid.NewString(), fullClosureChecklist()))
	require.NoError(t, post("start", `{"query":"cache pools","idempotency_key":"eval-restore-1"}`))
}
