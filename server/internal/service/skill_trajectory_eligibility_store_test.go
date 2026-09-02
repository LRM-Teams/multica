// SPDX-License-Identifier: Apache-2.0

package service

// Migration 496 behavior against the faithful schema: eligibility pins
// append once and are frozen by the update guard trigger (revocation is
// the only later write, and it can never be un-done or re-granted), and
// backfill checkpoints append immutably for dry-run and executed passes.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/skillevolution"
)

// A pin survives roundtrip, replays idempotently, refuses a different
// payload, and is frozen at the row level: tampering with pinned columns,
// deleting the row, or re-granting eligibility all fail.
func TestSkillTrajectoryEligibilityPinIsFrozenAndRevokeOnly(t *testing.T) {
	f := newTrajectoryFixture(t)
	ledger := NewPostgresSkillEvolutionLedger(f.pool)
	ctx := context.Background()
	taskID := f.createTask(t, "completed", "")
	pin := f.eligibility(taskID)
	pin.FixedAt = pin.FixedAt.Truncate(time.Microsecond)

	require.NoError(t, ledger.PinEligibility(ctx, pin))
	stored, err := ledger.GetEligibility(ctx, f.workspaceID, taskID)
	require.NoError(t, err)
	assert.Equal(t, pin.RunKind, stored.RunKind)
	assert.True(t, stored.EvolutionEligible)
	assert.Equal(t, pin.AllowedPurposes, stored.AllowedPurposes)
	assert.Equal(t, pin.TaskType, stored.TaskType)
	assert.Equal(t, pin.LineageID, stored.LineageID)
	assert.Equal(t, pin.FixedByActor, stored.FixedByActor)
	assert.True(t, stored.FixedAt.Equal(pin.FixedAt))
	assert.False(t, stored.Revoked())

	// Identical replay is a no-op, never an overwrite attempt.
	require.NoError(t, ledger.PinEligibility(ctx, pin))

	// A different payload for the same run is a conflict.
	conflicting := pin
	conflicting.FixedByActor = "someone-else"
	require.ErrorIs(t, ledger.PinEligibility(ctx, conflicting), skillevolution.ErrLedgerConflict)

	// Reads stay workspace-scoped and fail closed.
	_, err = ledger.GetEligibility(ctx, uuid.NewString(), taskID)
	require.ErrorIs(t, err, skillevolution.ErrLedgerNotFound)
	_, err = ledger.GetEligibility(ctx, f.workspaceID, uuid.NewString())
	require.ErrorIs(t, err, skillevolution.ErrLedgerNotFound)

	// The trigger freezes every pinned column.
	_, err = f.pool.Exec(ctx, `UPDATE skill_trajectory_eligibility SET task_type='tampered' WHERE run_id=$1::uuid`, taskID)
	require.Error(t, err, "pinned task_type must not be rewritable")
	_, err = f.pool.Exec(ctx, `UPDATE skill_trajectory_eligibility SET allowed_purposes='{skill_evolution,curator_review}' WHERE run_id=$1::uuid`, taskID)
	require.Error(t, err, "purposes must not widen after run start")
	_, err = f.pool.Exec(ctx, `DELETE FROM skill_trajectory_eligibility WHERE run_id=$1::uuid`, taskID)
	require.Error(t, err, "eligibility rows are ledger rows: no deletes")

	// Revocation through the store flips eligible and records the actor.
	revokedAt := time.Now().UTC().Truncate(time.Microsecond)
	require.NoError(t, ledger.RevokeEligibility(ctx, f.workspaceID, taskID, "admin:ops", "gdpr erasure request", revokedAt))
	stored, err = ledger.GetEligibility(ctx, f.workspaceID, taskID)
	require.NoError(t, err)
	assert.False(t, stored.EvolutionEligible)
	assert.True(t, stored.Revoked())
	assert.Equal(t, "admin:ops", stored.RevokedByActor)
	assert.Equal(t, "gdpr erasure request", stored.RevokedReason)
	assert.True(t, stored.FixedAt.Equal(pin.FixedAt), "the original pin stays for audit")

	// Re-granting eligibility after revocation is refused by the trigger.
	_, err = f.pool.Exec(ctx, `
		UPDATE skill_trajectory_eligibility
		SET evolution_eligible=true, revoked_at=NULL, revoked_by_actor='', revoked_reason=''
		WHERE run_id=$1::uuid`, taskID)
	require.Error(t, err, "eligibility is never re-granted after revocation")

	// A second revocation conflicts instead of rewriting history.
	require.ErrorIs(t, ledger.RevokeEligibility(ctx, f.workspaceID, taskID, "admin:other", "second opinion", time.Now().UTC()),
		skillevolution.ErrLedgerConflict)

	// Revoking an unknown run is not-found, not a silent no-op.
	require.ErrorIs(t, ledger.RevokeEligibility(ctx, f.workspaceID, uuid.NewString(), "admin:ops", "reason", time.Now().UTC()),
		skillevolution.ErrLedgerNotFound)

	// Pinning a revoked shape is refused before touching the ledger.
	revokedShape, err := pin.RevokeEligibility("admin:ops", "x", time.Now().UTC())
	require.NoError(t, err)
	require.ErrorIs(t, ledger.PinEligibility(ctx, revokedShape), skillevolution.ErrInvalidContract)
}

// Dry-run and executed passes both append immutable, replayable reports.
func TestSkillBackfillCheckpointsAppendImmutably(t *testing.T) {
	f := newTrajectoryFixture(t)
	ledger := NewPostgresSkillEvolutionLedger(f.pool)
	ctx := context.Background()

	dryRun := skillevolution.BackfillCheckpoint{
		WorkspaceID: f.workspaceID, JobID: "backfill-eligibility-001",
		Kind: skillevolution.BackfillTrajectoryEligibility, Mode: skillevolution.BackfillModeDryRun,
		Actor: "admin:ops", PolicyVersion: "policy-7", SourceWatermark: "agent_inbox_event:2026-08-31",
		SelectedCount: 12, RejectedCount: 3, Reason: "report-only pass",
	}
	executed := dryRun
	executed.JobID = "backfill-eligibility-002"
	executed.Mode = skillevolution.BackfillModeExecuted
	executed.SelectedCount = 11
	executed.RejectedCount = 4

	require.NoError(t, ledger.RecordBackfillCheckpoint(ctx, dryRun))
	require.NoError(t, ledger.RecordBackfillCheckpoint(ctx, executed))

	checkpoints, err := ledger.ListBackfillCheckpoints(ctx, f.workspaceID, 10)
	require.NoError(t, err)
	require.Len(t, checkpoints, 2)
	assert.Equal(t, executed.JobID, checkpoints[0].JobID, "newest first")
	assert.Equal(t, dryRun.JobID, checkpoints[1].JobID)
	assert.Equal(t, skillevolution.BackfillModeDryRun, checkpoints[1].Mode)

	// Identical replay is a no-op; a different payload for the same job
	// is a conflict.
	require.NoError(t, ledger.RecordBackfillCheckpoint(ctx, dryRun))
	divergent := dryRun
	divergent.SelectedCount = 99
	require.ErrorIs(t, ledger.RecordBackfillCheckpoint(ctx, divergent), skillevolution.ErrLedgerConflict)

	// The append-only trigger refuses rewrites and deletes.
	_, err = f.pool.Exec(ctx, `UPDATE skill_backfill_checkpoint SET reason='nothing happened' WHERE job_id=$1`, dryRun.JobID)
	require.Error(t, err, "checkpoints are immutable")
	_, err = f.pool.Exec(ctx, `DELETE FROM skill_backfill_checkpoint WHERE job_id=$1`, dryRun.JobID)
	require.Error(t, err, "checkpoints cannot be deleted")

	// Reads stay workspace-scoped.
	other, err := ledger.ListBackfillCheckpoints(ctx, uuid.NewString(), 10)
	require.NoError(t, err)
	assert.Empty(t, other)
}
