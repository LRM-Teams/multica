package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestMixedRLFreezeService_CreatesStableTerminalForUnassignedCalls(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	first := bindMixedRLAgent(t, h, 2, "offline_rl")
	second := bindMixedRLAgent(t, h, 3, "none")
	firstTurn := createMixedRLTurn(t, h, first)
	secondTurn := createMixedRLTurnWithID(t, h, run.RunID, second, "70000000-0000-4000-8000-000000000260")
	firstCall, err := h.ledger.InsertProviderCall(h.ctx, mixedRLProviderCallInput(run.RunID, first, firstTurn, "freeze-terminal-first", 1))
	require.NoError(t, err)
	secondCall, err := h.ledger.InsertProviderCall(h.ctx, mixedRLProviderCallInput(run.RunID, second, secondTurn, "freeze-terminal-second", 1))
	require.NoError(t, err)
	advanceMixedRLRunToQuietCandidate(t, h, run.RunID)

	result, err := NewMixedRLFreezeService(h.runs.queries, h.tx).Freeze(h.ctx, run.RunID, false)
	require.NoError(t, err)
	assert.Equal(t, "completed", result.Run.Status)
	assert.NotEmpty(t, result.Snapshot.SnapshotHash)
	dag, err := h.ledger.GetFrozenDAG(h.ctx, run.RunID, result.Snapshot.SnapshotID)
	require.NoError(t, err)
	require.Len(t, dag.Segments, 2)
	assert.Equal(t, "terminal:"+first.RunAgentID.String(), dag.Segments[0].SegmentID)
	assert.Equal(t, "terminal:"+second.RunAgentID.String(), dag.Segments[1].SegmentID)
	assert.Equal(t, []string{firstCall.CallID, secondCall.CallID}, []string{dag.Associations[0].ProviderCallID, dag.Associations[1].ProviderCallID})
	assert.Empty(t, dag.Edges)
}

func TestCanonicalMixedRLManifest_BindsCaptureGapsDeterministically(t *testing.T) {
	first := db.EnvDispatchRunAuditEvent{
		EventID:    util.MustParseUUID("70000000-0000-4000-8000-000000000271"),
		RunAgentID: util.MustParseUUID("70000000-0000-4000-8000-000000000272"),
		TurnID:     util.MustParseUUID("70000000-0000-4000-8000-000000000273"),
		Kind:       "capture_gap", Reason: "run_timeout",
	}
	second := db.EnvDispatchRunAuditEvent{
		EventID:    util.MustParseUUID("70000000-0000-4000-8000-000000000274"),
		RunAgentID: util.MustParseUUID("70000000-0000-4000-8000-000000000275"),
		TurnID:     util.MustParseUUID("70000000-0000-4000-8000-000000000276"),
		Kind:       "capture_gap", Reason: "daemon_capture_failed",
	}
	manifest, hash, err := canonicalMixedRLManifest(nil, nil, nil, nil, []db.EnvDispatchRunAuditEvent{second, first})
	require.NoError(t, err)
	reversed, reversedHash, err := canonicalMixedRLManifest(nil, nil, nil, nil, []db.EnvDispatchRunAuditEvent{first, second})
	require.NoError(t, err)
	assert.Equal(t, hash, reversedHash)
	assert.JSONEq(t, string(manifest), string(reversed))
	assert.Contains(t, string(manifest), `"capture_gaps"`)
	assert.NotContains(t, string(manifest), first.EventID.String(), "event IDs must not introduce random timeout hashes")
}

func TestMixedRLFreezeService_TimeoutPublishesPartialSnapshot(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	advanceMixedRLRunToRunning(t, h, run.RunID)

	result, err := NewMixedRLFreezeService(h.runs.queries, h.tx).Freeze(h.ctx, run.RunID, true)
	require.NoError(t, err)
	assert.Equal(t, "failed_timeout", result.Run.Status)
	assert.Equal(t, "failed_timeout", result.Snapshot.RunStatus)
}

func TestMixedRLFreezeService_TimeoutSettlesActiveTurnsAsCaptureGaps(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 2, "offline_rl")
	turn := createMixedRLTurn(t, h, agent)
	advanceMixedRLRunToRunning(t, h, run.RunID)
	_, err := h.runs.AdjustActivity(h.ctx, run.RunID, ActivityCounterDelta{
		ActiveTurns: 1, InflightTools: 1, UnfinishedCapture: 1,
	})
	require.NoError(t, err)

	result, err := NewMixedRLFreezeService(h.runs.queries, h.tx).Freeze(h.ctx, run.RunID, true)
	require.NoError(t, err)
	assert.Equal(t, "failed_timeout", result.Run.Status)
	assert.Zero(t, result.Run.ActiveTurnCount)
	assert.Zero(t, result.Run.InflightToolCount)
	assert.Zero(t, result.Run.UnfinishedCaptureBatchCount)
	assert.Equal(t, int64(1), result.Run.CaptureGapCount)

	dag, err := h.ledger.GetFrozenDAG(h.ctx, run.RunID, result.Snapshot.SnapshotID)
	require.NoError(t, err)
	require.Len(t, dag.CaptureGaps, 1)
	assert.Equal(t, turn.TurnID, dag.CaptureGaps[0].TurnID)
}

func TestMixedRLFreezeService_ReapQuiescenceFreezesQuietRun(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	advanceMixedRLRunToQuietCandidate(t, h, run.RunID)
	require.NoError(t, h.tx.QueryRow(h.ctx, `
		UPDATE env_dispatch_run
		SET quiet_candidate_since = now() - make_interval(secs => quiet_window_ms / 1000 + 1),
		    timeout_deadline_at = now() + interval '1 hour'
		WHERE run_id = $1
		RETURNING run_id`, run.RunID).Scan(&run.RunID))

	results, err := NewMixedRLFreezeService(h.runs.queries, h.tx).ReapMixedRLQuiescence(h.ctx, time.Now().UTC())
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "completed", results[0].Run.Status)
}

func TestMixedRLFreezeService_RollsBackTerminalClosureOnBuilderFailure(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 2, "offline_rl")
	turn := createMixedRLTurn(t, h, agent)
	_, err := h.ledger.InsertProviderCall(h.ctx, mixedRLProviderCallInput(run.RunID, agent, turn, "freeze-rollback-call", 1))
	require.NoError(t, err)
	_, err = h.ledger.InsertSegment(h.ctx, SegmentInput{
		SegmentID: "terminal:" + agent.RunAgentID.String(), RunID: run.RunID,
		RunAgentID: agent.RunAgentID, Kind: "terminal", SegmentOrdinal: 1,
	})
	require.NoError(t, err)
	advanceMixedRLRunToQuietCandidate(t, h, run.RunID)

	_, err = NewMixedRLFreezeService(h.runs.queries, h.tx).Freeze(h.ctx, run.RunID, false)
	require.Error(t, err)
	persisted, err := h.runs.GetRun(h.ctx, run.RunID)
	require.NoError(t, err)
	assert.Equal(t, "quiet_candidate", persisted.Status)
	var segments, snapshots int
	require.NoError(t, h.tx.QueryRow(h.ctx, "SELECT count(*) FROM interaction_dag_run_segment WHERE run_id = $1", run.RunID).Scan(&segments))
	require.NoError(t, h.tx.QueryRow(h.ctx, "SELECT count(*) FROM interaction_dag_frozen_snapshot WHERE run_id = $1", run.RunID).Scan(&snapshots))
	assert.Equal(t, 1, segments, "failed builder must not persist a second terminal")
	assert.Zero(t, snapshots)
}

func TestMixedRLFreezeService_AuditAssociationDoesNotSuppressTerminalOwnership(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 2, "offline_rl")
	turn := createMixedRLTurn(t, h, agent)
	call, err := h.ledger.InsertProviderCall(h.ctx, mixedRLProviderCallInput(run.RunID, agent, turn, "audit-only-call", 1))
	require.NoError(t, err)
	_, err = h.ledger.InsertSegment(h.ctx, SegmentInput{
		SegmentID: "message:audit-only", RunID: run.RunID, RunAgentID: agent.RunAgentID,
		Kind: "message", CanonicalActionID: "70000000-0000-4000-8000-000000000277", SegmentOrdinal: 1,
	})
	require.NoError(t, err)
	require.NoError(t, h.ledger.AssociateProviderCall(h.ctx, SegmentCallAssociationInput{
		SegmentID: "message:audit-only", ProviderCallID: call.CallID, CallOrdinal: call.CallOrdinal, AssociationKind: "audit",
	}))
	advanceMixedRLRunToQuietCandidate(t, h, run.RunID)

	result, err := NewMixedRLFreezeService(h.runs.queries, h.tx).Freeze(h.ctx, run.RunID, false)
	require.NoError(t, err)
	dag, err := h.ledger.GetFrozenDAG(h.ctx, run.RunID, result.Snapshot.SnapshotID)
	require.NoError(t, err)
	assert.Len(t, dag.Segments, 2)
	assert.Equal(t, "terminal:"+agent.RunAgentID.String(), dag.Segments[1].SegmentID)
	assert.Equal(t, "owned", dag.Associations[1].AssociationKind)
}
