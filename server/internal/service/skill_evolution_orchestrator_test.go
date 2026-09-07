// SPDX-License-Identifier: Apache-2.0

package service

// Manual orchestrator service behavior against the real ledger and lease
// tables (migration 497): manual creation under the single-active fence,
// attempt-fenced leases, response-loss replay, checkpoint recovery, and
// reconciliation that applies only the safety terminals (plan Slice 3.3).

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/skillevolution"
)

func orchestratorTestHash(ch byte) string {
	return "sha256:" + strings.Repeat(string(ch), 64)
}

func orchestratorPinnedInputs() skillevolution.OrchestratorPinnedInputs {
	return skillevolution.OrchestratorPinnedInputs{
		SourceEvidenceSetHash: orchestratorTestHash('e'),
		GraphVersion:          "graph-v7",
		GraphWatermark:        "2026-09-02T08:00:00Z",
		BaseSkillHash:         orchestratorTestHash('b'),
		ManifestHash:          orchestratorTestHash('c'),
		ModelID:               "model-x",
		ProviderID:            "provider-y",
		RuntimeID:             "runtime-z",
		PolicyVersion:         "policy-1",
		TargetScope:           "agent",
		Budget:                skillevolution.DefaultExplorationBudget(),
		DataResidency:         "eu-west-1",
	}
}

func manualCreationFor(f *skillEvolutionLedgerFixture, runID, taskType string) skillevolution.ManualRunCreation {
	return skillevolution.ManualRunCreation{
		RunID:                   runID,
		WorkspaceID:             f.workspaceID,
		TargetAgentID:           f.agentID,
		TaskType:                taskType,
		EnvironmentMajorVersion: "v3",
		PinnedInputs:            orchestratorPinnedInputs(),
		CuratorActor:            "curator:alice",
		Reason:                  "supported failure patterns on the export lane",
		CreatedAt:               time.Now().UTC(),
	}
}

func newOrchestratorFixture(t *testing.T) (*skillEvolutionLedgerFixture, *SkillEvolutionOrchestratorService) {
	t.Helper()
	f := newSkillEvolutionLedgerFixture(t)
	return f, NewSkillEvolutionOrchestratorService(f.pool, f.ledger)
}

func expireOrchestratorLease(t *testing.T, f *skillEvolutionLedgerFixture, runID string) {
	t.Helper()
	// The table CHECK (expires_at > acquired_at) forbids moving the
	// expiry into the past, so simulate the crash by shortening the lease
	// to a hair past its acquisition and letting that moment pass.
	_, err := f.pool.Exec(context.Background(),
		`UPDATE skill_evolution_run_lease SET expires_at = acquired_at + interval '50 milliseconds'
		 WHERE workspace_id = $1::uuid AND run_id = $2::uuid`, f.workspaceID, runID)
	require.NoError(t, err)
	time.Sleep(80 * time.Millisecond)
}

// Curator manual creation admits exactly one active run per evolution
// key, with a complete pin frozen at admission.
func TestSkillEvolutionOrchestratorManualCreateFencedByKey(t *testing.T) {
	f, orchestrator := newOrchestratorFixture(t)
	ctx := context.Background()

	run, err := orchestrator.CreateManualRun(ctx, manualCreationFor(f, uuid.NewString(), "spreadsheet_export"))
	require.NoError(t, err)
	assert.Equal(t, skillevolution.EvolutionRunQueued, run.Status)
	assert.Equal(t, "curator:alice", run.CreatedByActor)
	pinHash, err := skillevolution.RunPinnedInputsHash(run.PinnedInputs)
	require.NoError(t, err)
	assert.Equal(t, orchestratorPinnedInputs().Hash(), pinHash)

	// Same evolution key: refused while the first run is active.
	_, err = orchestrator.CreateManualRun(ctx, manualCreationFor(f, uuid.NewString(), "spreadsheet_export"))
	assert.ErrorIs(t, err, skillevolution.ErrActiveRunExists)

	// A different key runs in parallel.
	other, err := orchestrator.CreateManualRun(ctx, manualCreationFor(f, uuid.NewString(), "sheet_formula_audit"))
	require.NoError(t, err)
	assert.NotEqual(t, run.RunID, other.RunID)

	// Incomplete pins never start.
	broken := manualCreationFor(f, uuid.NewString(), "spreadsheet_export")
	broken.PinnedInputs.ManifestHash = ""
	_, err = orchestrator.CreateManualRun(ctx, broken)
	assert.ErrorIs(t, err, skillevolution.ErrInvalidContract)

	// Once the first run reaches a terminal state the key frees up —
	// through the ledger's own CAS, exactly as a driver would.
	_, err = f.pool.Exec(ctx,
		`UPDATE skill_evolution_run SET status='no_action' WHERE id=$1::uuid`, run.RunID)
	require.NoError(t, err)
	_, err = orchestrator.CreateManualRun(ctx, manualCreationFor(f, uuid.NewString(), "spreadsheet_export"))
	require.NoError(t, err)
}

// The lease fences execution: a live lease blocks foreign owners, a
// re-acquisition fences the old attempt off, and a response loss replays
// the recorded decision instead of re-executing it.
func TestSkillEvolutionOrchestratorLeaseFencingAndReplay(t *testing.T) {
	f, orchestrator := newOrchestratorFixture(t)
	ctx := context.Background()
	runID := uuid.NewString()
	_, err := orchestrator.CreateManualRun(ctx, manualCreationFor(f, runID, "spreadsheet_export"))
	require.NoError(t, err)

	lease, err := orchestrator.AcquireLease(ctx, f.workspaceID, runID, "worker-a", 5*time.Minute)
	require.NoError(t, err)
	assert.EqualValues(t, 1, lease.Attempt)

	// A second owner cannot take a live lease.
	_, err = orchestrator.AcquireLease(ctx, f.workspaceID, runID, "worker-b", 5*time.Minute)
	assert.ErrorIs(t, err, skillevolution.ErrLeaseHeld)

	// The leased step advances the run and records a checkpoint.
	advanced, replayed, err := orchestrator.AdvanceRunPhase(ctx, f.workspaceID, runID, lease, PhaseStepOutcome{
		NextStatus: skillevolution.EvolutionRunSnapshotting,
		Checkpoint: &skillevolution.RunCheckpoint{Summary: "snapshot pinned"},
	})
	require.NoError(t, err)
	assert.False(t, replayed)
	assert.Equal(t, skillevolution.EvolutionRunSnapshotting, advanced.Status)

	// Response loss: the same request replays the recorded decision.
	_, replayed, err = orchestrator.AdvanceRunPhase(ctx, f.workspaceID, runID, lease, PhaseStepOutcome{
		NextStatus: skillevolution.EvolutionRunSnapshotting,
		Checkpoint: &skillevolution.RunCheckpoint{Summary: "snapshot pinned"},
	})
	require.NoError(t, err)
	assert.True(t, replayed, "a lost response replays instead of re-executing")

	// Same step token, different payload: refused, never overwritten.
	_, _, err = orchestrator.AdvanceRunPhase(ctx, f.workspaceID, runID, lease, PhaseStepOutcome{
		NextStatus: skillevolution.EvolutionRunSnapshotting,
		Checkpoint: &skillevolution.RunCheckpoint{Summary: "a different decision"},
	})
	assert.ErrorIs(t, err, skillevolution.ErrLedgerConflict)

	// Crash: the lease lapses, a new worker re-acquires with attempt 2.
	expireOrchestratorLease(t, f, runID)
	lease2, err := orchestrator.AcquireLease(ctx, f.workspaceID, runID, "worker-b", 5*time.Minute)
	require.NoError(t, err)
	assert.EqualValues(t, 2, lease2.Attempt)

	// The zombie old owner cannot advance — its attempt is fenced off.
	_, _, err = orchestrator.AdvanceRunPhase(ctx, f.workspaceID, runID, lease, PhaseStepOutcome{
		NextStatus: skillevolution.EvolutionRunConsolidatingPatterns,
	})
	assert.ErrorIs(t, err, skillevolution.ErrLeaseSuperseded,
		"an old owner's lease cannot advance the run after re-acquisition")

	// The new owner continues, and releasing lets the next attempt in.
	_, _, err = orchestrator.AdvanceRunPhase(ctx, f.workspaceID, runID, lease2, PhaseStepOutcome{
		NextStatus: skillevolution.EvolutionRunConsolidatingPatterns,
	})
	require.NoError(t, err)
	require.NoError(t, orchestrator.ReleaseLease(ctx, f.workspaceID, runID, lease2))
	lease3, err := orchestrator.AcquireLease(ctx, f.workspaceID, runID, "worker-c", 5*time.Minute)
	require.NoError(t, err)
	assert.EqualValues(t, 3, lease3.Attempt)

	// An expired-own-lease renewal is a conflict: re-acquire instead.
	expireOrchestratorLease(t, f, runID)
	err = orchestrator.RenewLease(ctx, f.workspaceID, runID, lease3, 5*time.Minute)
	require.ErrorIs(t, err, skillevolution.ErrLedgerConflict)

	// Terminal runs neither lease nor advance.
	_, err = f.pool.Exec(ctx, `UPDATE skill_evolution_run SET status='failed' WHERE id=$1::uuid`, runID)
	require.NoError(t, err)
	_, err = orchestrator.AcquireLease(ctx, f.workspaceID, runID, "worker-d", 5*time.Minute)
	assert.ErrorIs(t, err, skillevolution.ErrLedgerConflict)
	_, err = orchestrator.AcquireLease(ctx, f.workspaceID, uuid.NewString(), "worker-d", time.Minute)
	assert.ErrorIs(t, err, skillevolution.ErrLedgerNotFound, "acquire on unknown run fails closed")
}

// Crash recovery: the run reports its checkpoint as the resume point, and
// the next attempt continues from it under a fresh lease.
func TestSkillEvolutionOrchestratorRecoveryFromCheckpoint(t *testing.T) {
	f, orchestrator := newOrchestratorFixture(t)
	ctx := context.Background()
	runID := uuid.NewString()
	created, err := orchestrator.CreateManualRun(ctx, manualCreationFor(f, runID, "spreadsheet_export"))
	require.NoError(t, err)

	lease, err := orchestrator.AcquireLease(ctx, f.workspaceID, runID, "worker-a", 5*time.Minute)
	require.NoError(t, err)

	// Mid-phase progress: a checkpoint without a status move.
	_, _, err = orchestrator.AdvanceRunPhase(ctx, f.workspaceID, runID, lease, PhaseStepOutcome{
		Checkpoint: &skillevolution.RunCheckpoint{Summary: "compared 9 runs, two lineages"},
	})
	require.NoError(t, err)

	// Crash: the lease lapses. Recovery reports the resume point.
	expireOrchestratorLease(t, f, runID)
	decision, err := orchestrator.RecoverRun(ctx, f.workspaceID, runID)
	require.NoError(t, err)
	require.Equal(t, skillevolution.ReconcileResumeCheckpoint, decision.Action)
	require.NotNil(t, decision.Checkpoint)
	assert.Equal(t, "compared 9 runs, two lineages", decision.Checkpoint.Summary)
	assert.EqualValues(t, 1, decision.Checkpoint.Attempt)

	// The next attempt resumes and finishes the phase.
	lease2, err := orchestrator.AcquireLease(ctx, f.workspaceID, runID, "worker-b", 5*time.Minute)
	require.NoError(t, err)
	advanced, _, err := orchestrator.AdvanceRunPhase(ctx, f.workspaceID, runID, lease2, PhaseStepOutcome{
		NextStatus: skillevolution.EvolutionRunSnapshotting,
		Checkpoint: &skillevolution.RunCheckpoint{Summary: "snapshot resumed from checkpoint"},
	})
	require.NoError(t, err)
	assert.Equal(t, skillevolution.EvolutionRunSnapshotting, advanced.Status)

	// A live owner is awaited, never disturbed.
	live, err := orchestrator.RecoverRun(ctx, f.workspaceID, runID)
	require.NoError(t, err)
	assert.Equal(t, skillevolution.ReconcileAwaitOwner, live.Action)

	// Terminal runs recover to none.
	_, err = f.pool.Exec(ctx, `UPDATE skill_evolution_run SET status='cancelled' WHERE id=$1::uuid`, created.RunID)
	require.NoError(t, err)
	decision, err = orchestrator.RecoverRun(ctx, f.workspaceID, runID)
	require.NoError(t, err)
	assert.Equal(t, skillevolution.ReconcileNone, decision.Action)
}

// Workspace reconciliation applies ONLY the safety terminals: idle phases
// fail, live leases are awaited, and the sweep is observable.
func TestSkillEvolutionOrchestratorReconcileWorkspaceSafetyOnly(t *testing.T) {
	f, orchestrator := newOrchestratorFixture(t)
	ctx := context.Background()

	idleID := uuid.NewString()
	_, err := orchestrator.CreateManualRun(ctx, manualCreationFor(f, idleID, "spreadsheet_export"))
	require.NoError(t, err)

	drivenID := uuid.NewString()
	_, err = orchestrator.CreateManualRun(ctx, manualCreationFor(f, drivenID, "sheet_formula_audit"))
	require.NoError(t, err)
	_, err = orchestrator.AcquireLease(ctx, f.workspaceID, drivenID, "worker-a", 5*time.Minute)
	require.NoError(t, err)

	// A nanosecond deadline fails the idle phase immediately; the leased
	// run is awaited regardless.
	summary, err := orchestrator.ReconcileWorkspace(ctx, f.workspaceID, time.Nanosecond)
	require.NoError(t, err)
	assert.Equal(t, 2, summary.Examined)
	assert.Equal(t, 1, summary.Awaited)
	assert.Equal(t, 1, summary.Failed)

	idle, err := f.ledger.GetRun(ctx, f.workspaceID, idleID)
	require.NoError(t, err)
	assert.Equal(t, skillevolution.EvolutionRunFailed, idle.Status)
	require.NotNil(t, idle.TerminalAt)

	driven, err := f.ledger.GetRun(ctx, f.workspaceID, drivenID)
	require.NoError(t, err)
	assert.Equal(t, skillevolution.EvolutionRunQueued, driven.Status)

	// The sweep recorded its lane.
	var lane string
	var pending int
	err = f.pool.QueryRow(ctx,
		`SELECT lane, pending_count FROM skill_evolution_reconciliation
		 WHERE workspace_id=$1::uuid AND lane='orchestrator'`, f.workspaceID).Scan(&lane, &pending)
	require.NoError(t, err)
	assert.Equal(t, "orchestrator", lane)
	assert.Equal(t, 1, pending)

	// A second sweep of the now-terminal idle run examines only the live
	// one and stays idempotent.
	summary, err = orchestrator.ReconcileWorkspace(ctx, f.workspaceID, skillevolution.DefaultPhaseDeadline)
	require.NoError(t, err)
	assert.Equal(t, 1, summary.Examined)
	assert.Equal(t, 1, summary.Awaited)
}
