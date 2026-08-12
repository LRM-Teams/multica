package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTimeoutFreezeOriginIsInitialSubmissionNotProvisioning(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	require.False(t, run.TimeoutDeadlineAt.Valid, "deadline must not start during provisioning")
	advanceMixedRLRunToRunning(t, h, run.RunID)
	advanced, err := h.runs.GetRun(h.ctx, run.RunID)
	require.NoError(t, err)
	require.True(t, advanced.TimeoutDeadlineAt.Valid)
	require.True(t, advanced.InitialMessageSubmittedAt.Valid)
	expected := advanced.InitialMessageSubmittedAt.Time.Add(time.Duration(advanced.TotalTimeoutSeconds) * time.Second)
	assert.True(t, advanced.TimeoutDeadlineAt.Time.Equal(expected) || advanced.TimeoutDeadlineAt.Time.After(advanced.InitialMessageSubmittedAt.Time))
}

func TestTimeoutFreezeMarksFailedTimeoutAndKeepsPartialEligibleCalls(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 1, "offline_rl")
	turn := createMixedRLTurn(t, h, agent)
	advanceMixedRLRunToRunning(t, h, run.RunID)
	call := mixedRLProviderCallInput(run.RunID, agent, turn, "timeout-call-1", 1)
	call.TrainingEligible = true
	call.ResponseComplete = true
	call.Status = "completed"
	acceptMixedRLTrustedCapture(t, h, run, agent, turn, []ProviderCallInput{call}, nil, nil)

	freezer := NewMixedRLFreezeService(h.queries, h.txStarter)
	result, err := freezer.Freeze(h.ctx, run.RunID, true)
	require.NoError(t, err)
	assert.Equal(t, "failed_timeout", result.Run.Status)
	assert.NotEmpty(t, result.Snapshot.SnapshotID)
}

func TestTimeoutFreezeIsIdempotentAgainstAlreadyFrozenRun(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	advanceMixedRLRunToRunning(t, h, run.RunID)
	freezer := NewMixedRLFreezeService(h.queries, h.txStarter)
	_, err := freezer.Freeze(h.ctx, run.RunID, true)
	require.NoError(t, err)
	_, err = freezer.Freeze(h.ctx, run.RunID, true)
	require.Error(t, err)
}
