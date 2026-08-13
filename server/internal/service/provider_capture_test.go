package service

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/util"
)

func TestProviderCaptureService_AcceptsTrustedBatchWithoutMutatingWSActivity(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 2, "offline_rl")
	turn := createMixedRLTurn(t, h, agent)
	advanceMixedRLRunToRunning(t, h, run.RunID)
	_, err := h.runs.AdjustActivity(h.ctx, run.RunID, ActivityCounterDelta{ActiveTurns: 1, UnfinishedCapture: 1})
	require.NoError(t, err)

	capture := TrustedTurnCapture{
		RunID: run.RunID, RunAgentID: agent.RunAgentID, TurnID: turn.TurnID, TurnOrdinal: turn.TurnOrdinal,
		Batch: TurnCaptureBatchInput{
			CaptureBatchID: util.MustParseUUID("70000000-0000-4000-8000-000000000250"),
			TurnID:         turn.TurnID, CaptureBoundary: agent.CaptureBoundary,
			CallCount: 1, PayloadHash: "sha256:trusted-batch",
		},
		Calls:       []ProviderCallInput{mixedRLProviderCallInput(run.RunID, agent, turn, "capture-call-1", 1)},
		CompletedAt: time.Date(2026, time.August, 12, 12, 0, 0, 0, time.UTC),
	}

	accepted, err := NewProviderCaptureService(h.runs.queries, h.tx).AcceptTrustedTurnCapture(h.ctx, capture)
	require.NoError(t, err)
	assert.False(t, accepted.Late)
	assert.Equal(t, "settled", accepted.Turn.Status)
	assert.Equal(t, int64(1), accepted.Run.ActiveTurnCount)
	assert.Equal(t, int64(1), accepted.Run.UnfinishedCaptureBatchCount)

	var calls, batches int
	require.NoError(t, h.tx.QueryRow(h.ctx, "SELECT count(*) FROM pi_provider_call WHERE run_id = $1", run.RunID).Scan(&calls))
	require.NoError(t, h.tx.QueryRow(h.ctx, "SELECT count(*) FROM env_dispatch_turn_capture_batch WHERE turn_id = $1", turn.TurnID).Scan(&batches))
	assert.Equal(t, 1, calls)
	assert.Equal(t, 1, batches)
}

func TestProviderCaptureService_RejectsMismatchedBatchWithoutPartialWrites(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 2, "offline_rl")
	turn := createMixedRLTurn(t, h, agent)
	badCall := mixedRLProviderCallInput(run.RunID, agent, turn, "capture-call-bad", 1)
	badCall.FinalAssistantMessage = []byte("not-json")

	_, err := NewProviderCaptureService(h.runs.queries, h.tx).AcceptTrustedTurnCapture(h.ctx, TrustedTurnCapture{
		RunID: run.RunID, RunAgentID: agent.RunAgentID, TurnID: turn.TurnID, TurnOrdinal: turn.TurnOrdinal,
		Batch: TurnCaptureBatchInput{
			CaptureBatchID: util.MustParseUUID("70000000-0000-4000-8000-000000000251"),
			TurnID:         turn.TurnID, CaptureBoundary: agent.CaptureBoundary,
			CallCount: 1, PayloadHash: "sha256:bad-batch",
		},
		Calls: []ProviderCallInput{badCall},
	})
	require.Error(t, err)

	var calls, batches int
	require.NoError(t, h.tx.QueryRow(h.ctx, "SELECT count(*) FROM pi_provider_call WHERE run_id = $1", run.RunID).Scan(&calls))
	require.NoError(t, h.tx.QueryRow(h.ctx, "SELECT count(*) FROM env_dispatch_turn_capture_batch WHERE turn_id = $1", turn.TurnID).Scan(&batches))
	assert.Zero(t, calls)
	assert.Zero(t, batches)
}

func TestProviderCaptureService_CreatesMissingTurnAndRejectsMismatchedReplay(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 2, "offline_rl")
	advanceMixedRLRunToRunning(t, h, run.RunID)
	turn := ResidentTurnRecord{
		TurnID: util.MustParseUUID("70000000-0000-4000-8000-000000000253"),
		RunID:  run.RunID, RunAgentID: agent.RunAgentID, TurnOrdinal: 1,
	}
	capture := TrustedTurnCapture{
		RunID: run.RunID, RunAgentID: agent.RunAgentID, TurnID: turn.TurnID, TurnOrdinal: turn.TurnOrdinal,
		Batch: TurnCaptureBatchInput{CaptureBatchID: util.MustParseUUID("70000000-0000-4000-8000-000000000254"), TurnID: turn.TurnID, CaptureBoundary: agent.CaptureBoundary, CallCount: 1, PayloadHash: "sha256:replay"},
		Calls: []ProviderCallInput{mixedRLProviderCallInput(run.RunID, agent, turn, "capture-replay-call", 1)},
	}
	service := NewProviderCaptureService(h.runs.queries, h.tx)
	accepted, err := service.AcceptTrustedTurnCapture(h.ctx, capture)
	require.NoError(t, err)
	assert.Equal(t, "settled", accepted.Turn.Status)

	_, err = service.AcceptTrustedTurnCapture(h.ctx, capture)
	require.NoError(t, err, "identical replay after a lost response is accepted")
	changed := capture
	changed.Batch.PayloadHash = "sha256:tampered"
	_, err = service.AcceptTrustedTurnCapture(h.ctx, changed)
	require.Error(t, err, "replay must bind the accepted batch hash")
}

func TestProviderCaptureService_RoutesPostFreezeCaptureToLateAudit(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 2, "offline_rl")
	turn := createMixedRLTurn(t, h, agent)
	advanceMixedRLRunToQuietCandidate(t, h, run.RunID)
	snapshot, _, err := h.ledger.FreezeAndComplete(h.ctx, FrozenSnapshotInput{
		SnapshotID: "sha256:capture-late", RunID: run.RunID, RunStatus: "completed",
		SchemaVersion: "1", NormalizationVersion: "1", CanonicalManifest: []byte(`{"calls":[],"segments":[],"edges":[]}`), SnapshotHash: "sha256:capture-late",
	})
	require.NoError(t, err)

	accepted, err := NewProviderCaptureService(h.runs.queries, h.tx).AcceptTrustedTurnCapture(h.ctx, TrustedTurnCapture{
		RunID: run.RunID, RunAgentID: agent.RunAgentID, TurnID: turn.TurnID, TurnOrdinal: turn.TurnOrdinal,
		Batch:       TurnCaptureBatchInput{CaptureBatchID: util.MustParseUUID("70000000-0000-4000-8000-000000000252"), TurnID: turn.TurnID, CaptureBoundary: agent.CaptureBoundary, PayloadHash: "sha256:late"},
		LateEventID: pgtype.UUID{Bytes: [16]byte{0x70, 0, 0, 0, 0, 0, 0x40, 0, 0x80, 0, 0, 0, 0, 0, 0, 0x53}, Valid: true},
	})
	require.NoError(t, err)
	assert.True(t, accepted.Late)
	assert.Equal(t, snapshot.SnapshotID, accepted.SnapshotID)
}

func TestProviderCaptureService_RoutesPostFreezeGapToIdempotentLateAudit(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 2, "offline_rl")
	turn := createMixedRLTurn(t, h, agent)
	advanceMixedRLRunToQuietCandidate(t, h, run.RunID)
	snapshot, _, err := h.ledger.FreezeAndComplete(h.ctx, FrozenSnapshotInput{
		SnapshotID: "sha256:gap-late", RunID: run.RunID, RunStatus: "completed",
		SchemaVersion: "1", NormalizationVersion: "1", CanonicalManifest: []byte(`{"calls":[]}`), SnapshotHash: "sha256:gap-late",
	})
	require.NoError(t, err)
	input := TrustedTurnCaptureGap{RunID: run.RunID, RunAgentID: agent.RunAgentID, TurnID: turn.TurnID, TurnOrdinal: turn.TurnOrdinal, Reason: "capture_unavailable", Summary: []byte(`{"source":"daemon"}`)}
	accepted, err := NewProviderCaptureService(h.runs.queries, h.tx).AcceptTrustedTurnCaptureGap(h.ctx, input)
	require.NoError(t, err)
	assert.True(t, accepted.Late)
	assert.Equal(t, snapshot.SnapshotID, accepted.SnapshotID)
	_, err = NewProviderCaptureService(h.runs.queries, h.tx).AcceptTrustedTurnCaptureGap(h.ctx, input)
	require.NoError(t, err, "lost response replay must not duplicate late audit")
	var events int
	require.NoError(t, h.tx.QueryRow(h.ctx, `SELECT count(*) FROM env_dispatch_run_audit_event WHERE run_id = $1 AND kind = 'late_event'`, run.RunID).Scan(&events))
	assert.Equal(t, 1, events)
}
