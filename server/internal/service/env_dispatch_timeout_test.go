package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/util"
)

func TestMixedRLTimeout_OriginIsInitialSubmitNotProvisioning(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	provisioningProbe := time.Date(2026, time.August, 10, 1, 0, 0, 0, time.UTC)
	submittedAt := time.Date(2026, time.August, 10, 1, 5, 0, 0, time.UTC)

	// Timeout must not start while the run is still provisioning.
	_, err := h.runs.StartTimeout(h.ctx, run.RunID, provisioningProbe)
	require.Error(t, err)
	stillProvisioning, err := h.runs.GetRun(h.ctx, run.RunID)
	require.NoError(t, err)
	assert.Equal(t, "provisioning", stillProvisioning.Status)
	assert.True(t, stillProvisioning.InitialMessageSubmittedAt.IsZero())
	assert.True(t, stillProvisioning.TimeoutDeadlineAt.IsZero())

	_, err = h.runs.TransitionStatus(h.ctx, run.RunID, "provisioning", "preflight")
	require.NoError(t, err)
	started, err := h.runs.StartTimeout(h.ctx, run.RunID, submittedAt)
	require.NoError(t, err)
	assert.Equal(t, "running", started.Status)
	assert.WithinDuration(t, submittedAt, started.InitialMessageSubmittedAt, time.Microsecond)
	assert.WithinDuration(t, submittedAt.Add(time.Duration(started.TotalTimeoutSeconds)*time.Second), started.TimeoutDeadlineAt, time.Microsecond)

	// Later retries cannot rewrite the initial-send origin.
	_, err = h.runs.StartTimeout(h.ctx, run.RunID, submittedAt.Add(time.Minute))
	require.Error(t, err)
	unchanged, err := h.runs.GetRun(h.ctx, run.RunID)
	require.NoError(t, err)
	assert.WithinDuration(t, submittedAt, unchanged.InitialMessageSubmittedAt, time.Microsecond)
}

func TestMixedRLTimeout_ProvisioningStatusCannotFreezeAsFailedTimeout(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)

	_, err := NewMixedRLFreezeService(h.runs.queries, h.tx).Freeze(h.ctx, run.RunID, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timeout freeze requires")
	persisted, err := h.runs.GetRun(h.ctx, run.RunID)
	require.NoError(t, err)
	assert.Equal(t, "provisioning", persisted.Status)
}

func TestMixedRLTimeout_PublishesFailedTimeoutWithPartialEligibleData(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 2, "offline_rl")
	turn := createMixedRLTurn(t, h, agent)
	eligible, err := h.ledger.InsertProviderCall(h.ctx, mixedRLProviderCallInput(run.RunID, agent, turn, "timeout-eligible", 1))
	require.NoError(t, err)
	unfinished := mixedRLProviderCallInput(run.RunID, agent, turn, "timeout-unfinished", 2)
	unfinished.Status = "in_progress"
	unfinished.StopReason = ""
	unfinished.ResponseComplete = false
	unfinished.TrainingEligible = false
	unfinished.CompletedAt = time.Time{}
	unfinished.FinalAssistantMessage = []byte(`{"role":"assistant","blocks":[]}`)
	unfinishedCall, err := h.ledger.InsertProviderCall(h.ctx, unfinished)
	require.NoError(t, err)
	advanceMixedRLRunToRunning(t, h, run.RunID)

	result, err := NewMixedRLFreezeService(h.runs.queries, h.tx).Freeze(h.ctx, run.RunID, true)
	require.NoError(t, err)
	assert.Equal(t, "failed_timeout", result.Run.Status)

	dag, err := h.ledger.GetFrozenDAG(h.ctx, run.RunID, result.Snapshot.SnapshotID)
	require.NoError(t, err)
	require.Len(t, dag.ProviderCalls, 2)
	byID := map[string]FrozenDAGProviderCallRecord{
		dag.ProviderCalls[0].CallID: dag.ProviderCalls[0],
		dag.ProviderCalls[1].CallID: dag.ProviderCalls[1],
	}
	assert.Equal(t, "completed", byID[eligible.CallID].Status)
	assert.True(t, byID[eligible.CallID].TrainingEligible)
	assert.Equal(t, "aborted", byID[unfinishedCall.CallID].Status)
	assert.False(t, byID[unfinishedCall.CallID].TrainingEligible)
	assert.False(t, byID[unfinishedCall.CallID].ResponseComplete)
}

func TestMixedRLTimeout_MissingBatchBecomesCaptureGapNotSyntheticCall(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 2, "offline_rl")
	turn := createMixedRLTurn(t, h, agent)
	advanceMixedRLRunToRunning(t, h, run.RunID)
	_, err := h.runs.AdjustActivity(h.ctx, run.RunID, ActivityCounterDelta{
		ActiveTurns: 1, UnfinishedCapture: 1,
	})
	require.NoError(t, err)

	result, err := NewMixedRLFreezeService(h.runs.queries, h.tx).Freeze(h.ctx, run.RunID, true)
	require.NoError(t, err)
	assert.Equal(t, "failed_timeout", result.Run.Status)
	assert.Equal(t, int64(1), result.Run.CaptureGapCount)

	dag, err := h.ledger.GetFrozenDAG(h.ctx, run.RunID, result.Snapshot.SnapshotID)
	require.NoError(t, err)
	assert.Empty(t, dag.ProviderCalls)
	require.Len(t, dag.CaptureGaps, 1)
	assert.Equal(t, turn.TurnID, dag.CaptureGaps[0].TurnID)
	assert.Equal(t, "run_timeout", dag.CaptureGaps[0].Reason)
}

func TestMixedRLTimeout_LateEventsDoNotMutateFrozenSnapshot(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 2, "offline_rl")
	turn := createMixedRLTurn(t, h, agent)
	_, err := h.ledger.InsertProviderCall(h.ctx, mixedRLProviderCallInput(run.RunID, agent, turn, "timeout-late-base", 1))
	require.NoError(t, err)
	advanceMixedRLRunToRunning(t, h, run.RunID)

	result, err := NewMixedRLFreezeService(h.runs.queries, h.tx).Freeze(h.ctx, run.RunID, true)
	require.NoError(t, err)
	before, err := h.ledger.GetFrozenDAG(h.ctx, run.RunID, result.Snapshot.SnapshotID)
	require.NoError(t, err)

	accepted, err := NewProviderCaptureService(h.runs.queries, h.tx).AcceptTrustedTurnCapture(h.ctx, TrustedTurnCapture{
		RunID: run.RunID, RunAgentID: agent.RunAgentID, TurnID: turn.TurnID, TurnOrdinal: turn.TurnOrdinal,
		Batch: TurnCaptureBatchInput{
			CaptureBatchID: util.MustParseUUID("70000000-0000-4000-8000-000000000280"),
			TurnID:         turn.TurnID, CaptureBoundary: agent.CaptureBoundary,
			CallCount: 0, ActionCount: 0, ConsumptionCount: 0, PayloadHash: "sha256:late-empty",
		},
		LateEventID: util.MustParseUUID("70000000-0000-4000-8000-000000000281"),
	})
	require.NoError(t, err)
	assert.True(t, accepted.Late)

	after, err := h.ledger.GetFrozenDAG(h.ctx, run.RunID, result.Snapshot.SnapshotID)
	require.NoError(t, err)
	assert.Equal(t, before.Snapshot.SnapshotHash, after.Snapshot.SnapshotHash)
	assert.Equal(t, before.ProviderCalls, after.ProviderCalls)
	assert.Equal(t, before.Associations, after.Associations)
	assert.Equal(t, before.Edges, after.Edges)

	events, err := h.runs.ListAuditEvents(h.ctx, run.RunID)
	require.NoError(t, err)
	require.NotEmpty(t, events)
	foundLate := false
	for _, event := range events {
		if event.Kind == "late_event" && event.Reason == "turn_capture_after_freeze" {
			foundLate = true
			assert.Equal(t, result.Snapshot.SnapshotID, event.SnapshotID)
		}
	}
	assert.True(t, foundLate)
}

func TestEvaluateMixedRLQuiescence_TimeoutIndependentOfQuietWindow(t *testing.T) {
	now := time.Date(2026, time.August, 12, 9, 0, 0, 0, time.UTC)
	run := quiescenceRun(now)
	run.TimeoutDeadlineAt = now.Add(500 * time.Millisecond)
	run.ActiveTurnCount = 1
	run.Status = "running"
	assert.Equal(t, MixedRLQuiescenceFreezeTimeout, EvaluateMixedRLQuiescence(run, now.Add(500*time.Millisecond)))
}
