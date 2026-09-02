// SPDX-License-Identifier: Apache-2.0

package skillevolution

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var orchestratorTestTime = time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)

// repeatedHex builds a valid sha256 hex body distinct from testHash.
func repeatedHex(ch byte) string { return strings.Repeat(string(ch), 64) }

func completePinnedInputs() OrchestratorPinnedInputs {
	return OrchestratorPinnedInputs{
		SourceEvidenceSetHash: testHash,
		GraphVersion:          "graph-v42",
		GraphWatermark:        "2026-09-02T08:00:00Z",
		BaseSkillHash:         testHash,
		ManifestHash:          testHash,
		ModelID:               "model-x",
		ProviderID:            "provider-y",
		RuntimeID:             "runtime-z",
		PolicyVersion:         "policy-3",
		TargetScope:           "agent",
		Budget:                DefaultExplorationBudget(),
		DataResidency:         "eu-west-1",
	}
}

func manualCreation() ManualRunCreation {
	return ManualRunCreation{
		RunID:                   "run-" + orchestratorTestTime.Format("150405"),
		WorkspaceID:             "workspace-1",
		TargetAgentID:           "agent-7",
		TaskType:                "spreadsheet_export",
		EnvironmentMajorVersion: "v3",
		PinnedInputs:            completePinnedInputs(),
		CuratorActor:            "curator:alice",
		Reason:                  "two supported failure patterns on the export lane",
		CreatedAt:               orchestratorTestTime,
	}
}

// A manual creation freezes a COMPLETE pin — every facet of spec §12.6 —
// and an incomplete pin may not start, whatever the curator's reason.
func TestSkillEvolutionOrchestratorManualCreatePinsAllInputs(t *testing.T) {
	run, err := CreateManualRun(manualCreation())
	require.NoError(t, err)
	assert.Equal(t, EvolutionRunQueued, run.Status)
	assert.Equal(t, "curator:alice", run.CreatedByActor)

	// The stored pin round-trips and hashes deterministically.
	var pin OrchestratorPinnedInputs
	require.NoError(t, json.Unmarshal(run.PinnedInputs, &pin))
	assert.Equal(t, completePinnedInputs(), pin)
	hash, err := RunPinnedInputsHash(run.PinnedInputs)
	require.NoError(t, err)
	assert.Equal(t, pin.Hash(), hash)
	assert.Equal(t, hash, completePinnedInputs().Hash())

	// Every facet is individually load-bearing.
	facets := map[string]func(OrchestratorPinnedInputs) OrchestratorPinnedInputs{
		"source evidence hash": func(p OrchestratorPinnedInputs) OrchestratorPinnedInputs { p.SourceEvidenceSetHash = ""; return p },
		"base skill hash":      func(p OrchestratorPinnedInputs) OrchestratorPinnedInputs { p.BaseSkillHash = "sha256:short"; return p },
		"manifest hash":        func(p OrchestratorPinnedInputs) OrchestratorPinnedInputs { p.ManifestHash = ""; return p },
		"graph version":        func(p OrchestratorPinnedInputs) OrchestratorPinnedInputs { p.GraphVersion = ""; return p },
		"graph watermark":      func(p OrchestratorPinnedInputs) OrchestratorPinnedInputs { p.GraphWatermark = ""; return p },
		"model":                func(p OrchestratorPinnedInputs) OrchestratorPinnedInputs { p.ModelID = ""; return p },
		"provider":             func(p OrchestratorPinnedInputs) OrchestratorPinnedInputs { p.ProviderID = ""; return p },
		"runtime":              func(p OrchestratorPinnedInputs) OrchestratorPinnedInputs { p.RuntimeID = ""; return p },
		"policy":               func(p OrchestratorPinnedInputs) OrchestratorPinnedInputs { p.PolicyVersion = ""; return p },
		"data residency":       func(p OrchestratorPinnedInputs) OrchestratorPinnedInputs { p.DataResidency = ""; return p },
		"target scope":         func(p OrchestratorPinnedInputs) OrchestratorPinnedInputs { p.TargetScope = "org"; return p },
		"budget":               func(p OrchestratorPinnedInputs) OrchestratorPinnedInputs { p.Budget = ExplorationBudget{}; return p },
	}
	for name, breakFacet := range facets {
		creation := manualCreation()
		creation.PinnedInputs = breakFacet(creation.PinnedInputs)
		_, err := CreateManualRun(creation)
		assert.ErrorIs(t, err, ErrInvalidContract, "missing %s must refuse admission", name)
	}

	// A manual creation is a decision: curator and reason are mandatory.
	noCurator := manualCreation()
	noCurator.CuratorActor = ""
	_, err = CreateManualRun(noCurator)
	assert.ErrorIs(t, err, ErrInvalidContract)

	noReason := manualCreation()
	noReason.Reason = ""
	_, err = CreateManualRun(noReason)
	assert.ErrorIs(t, err, ErrInvalidContract)

	// Corrupted stored pins never revalidate (resume must fail closed).
	_, err = RunPinnedInputsHash(json.RawMessage(`{"graph_version":"only-one-facet"}`))
	assert.ErrorIs(t, err, ErrInvalidContract)
	_, err = RunPinnedInputsHash(json.RawMessage(`not json`))
	assert.ErrorIs(t, err, ErrInvalidContract)
}

// The lease is the fence: only the exact (owner, attempt) of the durable
// lease may act; an old owner's superseded attempt is refused even while
// its own expiry clock has not lapsed.
func TestSkillEvolutionOrchestratorLeaseFencingOldOwnerCannotAdvance(t *testing.T) {
	now := orchestratorTestTime
	durable := RunLease{
		RunID: "run-1", OwnerID: "worker-a", Attempt: 2,
		AcquiredAt: now, ExpiresAt: now.Add(10 * time.Minute),
	}
	require.NoError(t, durable.Validate())

	// The current owner, in time: authorized.
	assert.True(t, LeaseMatches(durable, durable, now))

	// The zombie: the same run's first owner, whose lease by its own
	// clock has not expired, comes back after a re-acquisition.
	zombie := durable
	zombie.OwnerID = "worker-a"
	zombie.Attempt = 1
	assert.False(t, LeaseMatches(durable, zombie, now),
		"a superseded attempt never matches, regardless of its local expiry")
	assert.ErrorIs(t, ClassifyLeaseFailure(durable, zombie), ErrLeaseSuperseded)

	// A foreign owner under the same attempt is a live conflict.
	foreign := durable
	foreign.OwnerID = "worker-b"
	assert.False(t, LeaseMatches(durable, foreign, now))
	assert.ErrorIs(t, ClassifyLeaseFailure(durable, foreign), ErrLeaseHeld)

	// Expiry is judged against the durable lease only.
	later := now.Add(11 * time.Minute)
	assert.False(t, LeaseMatches(durable, durable, later))

	// Lease shape: attempts are positive, expiry strictly after acquire.
	assert.ErrorIs(t, RunLease{RunID: "run-1", OwnerID: "w", Attempt: 0,
		AcquiredAt: now, ExpiresAt: now.Add(time.Minute)}.Validate(), ErrInvalidContract)
	assert.ErrorIs(t, RunLease{RunID: "run-1", OwnerID: "w", Attempt: 1,
		AcquiredAt: now, ExpiresAt: now}.Validate(), ErrInvalidContract)
	assert.ErrorIs(t, RunLease{RunID: "run-1", Attempt: 1,
		AcquiredAt: now, ExpiresAt: now.Add(time.Minute)}.Validate(), ErrInvalidContract)
}

// Crash/response-loss recovery resumes from the newest checkpoint of the
// CURRENT phase — never from another run, another phase, or another pin.
func TestSkillEvolutionOrchestratorCheckpointRecovery(t *testing.T) {
	run, err := CreateManualRun(manualCreation())
	require.NoError(t, err)
	run.Status = EvolutionRunConsolidatingPatterns
	pinHash, err := RunPinnedInputsHash(run.PinnedInputs)
	require.NoError(t, err)

	older := RunCheckpoint{
		RunID: run.RunID, Phase: EvolutionRunConsolidatingPatterns, Attempt: 1,
		PinnedInputsHash: pinHash, Summary: "compared 4 runs", RecordedAt: orchestratorTestTime,
	}
	newer := older
	newer.Attempt = 2
	newer.Summary = "compared 9 runs, two lineages"
	newer.RecordedAt = orchestratorTestTime.Add(time.Minute)

	// Newest wins; older same-phase checkpoints are superseded.
	resumed, ok, err := ResumeFromCheckpoint(run, []RunCheckpoint{older, newer})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "compared 9 runs, two lineages", resumed.Summary)

	// A checkpoint of a phase the run already left is not a resume point.
	past := older
	past.Phase = EvolutionRunSnapshotting
	_, ok, err = ResumeFromCheckpoint(run, []RunCheckpoint{past})
	require.NoError(t, err)
	assert.False(t, ok)

	// Another run's checkpoint is not ours to resume from.
	foreign := older
	foreign.RunID = "run-other"
	_, ok, err = ResumeFromCheckpoint(run, []RunCheckpoint{foreign})
	require.NoError(t, err)
	assert.False(t, ok)

	// A checkpoint derived from a DIFFERENT pin is a hard error — spec
	// §12.6 forbids auto-rebasing onto changed validation.
	movedPin := older
	movedPin.PinnedInputsHash = "sha256:" + repeatedHex('c')
	_, _, err = ResumeFromCheckpoint(run, []RunCheckpoint{movedPin})
	assert.ErrorIs(t, err, ErrLedgerConflict)

	// Terminal runs never resume, checkpoints or not.
	terminalRun := run
	terminalRun.Status = EvolutionRunFailed
	_, ok, err = ResumeFromCheckpoint(terminalRun, []RunCheckpoint{newer})
	require.NoError(t, err)
	assert.False(t, ok)

	// Checkpoint shape: non-terminal phase, positive attempt, pin hash,
	// summary, time.
	assert.ErrorIs(t, RunCheckpoint{RunID: run.RunID, Phase: EvolutionRunFailed, Attempt: 1,
		PinnedInputsHash: pinHash, Summary: "s", RecordedAt: orchestratorTestTime}.Validate(), ErrInvalidContract)
	assert.ErrorIs(t, RunCheckpoint{RunID: run.RunID, Phase: EvolutionRunQueued, Attempt: 0,
		PinnedInputsHash: pinHash, Summary: "s", RecordedAt: orchestratorTestTime}.Validate(), ErrInvalidContract)
	assert.ErrorIs(t, RunCheckpoint{RunID: run.RunID, Phase: EvolutionRunQueued, Attempt: 1,
		PinnedInputsHash: "", Summary: "s", RecordedAt: orchestratorTestTime}.Validate(), ErrInvalidContract)
}

// The reconciler defers to terminal stability and live owners, resumes
// from checkpoints when the lease lapses, fails abandoned phases, and
// marks pin-changed runs stale — never auto-rebases them.
func TestSkillEvolutionOrchestratorReconcilerDecisions(t *testing.T) {
	run, err := CreateManualRun(manualCreation())
	require.NoError(t, err)
	run.Status = EvolutionRunConsolidatingPatterns
	run.UpdatedAt = orchestratorTestTime
	pinHash, err := RunPinnedInputsHash(run.PinnedInputs)
	require.NoError(t, err)
	base := ReconcilerInput{Now: orchestratorTestTime, Run: run}

	// Terminal runs are stable: no action, no error.
	failed := run
	failed.Status = EvolutionRunFailed
	decision, err := ReconcileRun(ReconcilerInput{Now: base.Now, Run: failed})
	require.NoError(t, err)
	assert.Equal(t, ReconcileNone, decision.Action)

	// A live lease owns the run: await, do not interfere.
	lease := RunLease{RunID: run.RunID, OwnerID: "worker-a", Attempt: 1,
		AcquiredAt: base.Now, ExpiresAt: base.Now.Add(10 * time.Minute)}
	decision, err = ReconcileRun(ReconcilerInput{Now: base.Now, Run: run, Lease: &lease})
	require.NoError(t, err)
	assert.Equal(t, ReconcileAwaitOwner, decision.Action)

	// Expired lease + usable checkpoint: resume.
	expired := lease
	expired.ExpiresAt = base.Now.Add(-time.Minute)
	checkpoint := RunCheckpoint{RunID: run.RunID, Phase: run.Status, Attempt: 1,
		PinnedInputsHash: pinHash, Summary: "compared 9 runs", RecordedAt: base.Now.Add(-2 * time.Minute)}
	decision, err = ReconcileRun(ReconcilerInput{Now: base.Now, Run: run, Lease: &expired, Checkpoints: []RunCheckpoint{checkpoint}})
	require.NoError(t, err)
	require.Equal(t, ReconcileResumeCheckpoint, decision.Action)
	require.NotNil(t, decision.Checkpoint)
	assert.Equal(t, "compared 9 runs", decision.Checkpoint.Summary)

	// No checkpoint, phase within deadline: requeue.
	decision, err = ReconcileRun(ReconcilerInput{Now: base.Now, Run: run, Lease: &expired})
	require.NoError(t, err)
	assert.Equal(t, ReconcileRequeuePhase, decision.Action)

	// Deadline exceeded without a live lease: fail — even with a
	// checkpoint (checkpoints accelerate recovery, they do not extend
	// deadlines).
	late := base
	late.Now = base.Now.Add(DefaultPhaseDeadline + time.Minute)
	decision, err = ReconcileRun(ReconcilerInput{Now: late.Now, Run: run, Lease: &expired, Checkpoints: []RunCheckpoint{checkpoint}})
	require.NoError(t, err)
	assert.Equal(t, ReconcileMarkFailed, decision.Action)
	assert.Equal(t, EvolutionRunFailed, decision.NextStatus)
	// A configured shorter deadline fails the same run earlier.
	decision, err = ReconcileRun(ReconcilerInput{Now: base.Now.Add(2 * time.Minute), Run: run, PhaseDeadline: time.Minute})
	require.NoError(t, err)
	assert.Equal(t, ReconcileMarkFailed, decision.Action)

	// A checkpoint from a different pin: stale, never rebased.
	moved := checkpoint
	moved.PinnedInputsHash = "sha256:" + repeatedHex('d')
	decision, err = ReconcileRun(ReconcilerInput{Now: base.Now, Run: run, Checkpoints: []RunCheckpoint{moved}})
	require.NoError(t, err)
	assert.Equal(t, ReconcileMarkStale, decision.Action)
	assert.Equal(t, EvolutionRunStale, decision.NextStatus)

	// A corrupted pin on the run itself: stale as well.
	broken := run
	broken.PinnedInputs = json.RawMessage(`{}`)
	decision, err = ReconcileRun(ReconcilerInput{Now: base.Now, Run: broken})
	require.NoError(t, err)
	assert.Equal(t, ReconcileMarkStale, decision.Action)

	// The clock is mandatory.
	_, err = ReconcileRun(ReconcilerInput{Run: run})
	assert.ErrorIs(t, err, ErrInvalidContract)
}

// All seven terminals are stable: interruptions refuse, resumes refuse,
// reconciliation no-ops — and only the four safety interrupts exist.
func TestSkillEvolutionOrchestratorTerminalsAreStable(t *testing.T) {
	run, err := CreateManualRun(manualCreation())
	require.NoError(t, err)
	pinHash, err := RunPinnedInputsHash(run.PinnedInputs)
	require.NoError(t, err)
	checkpoint := RunCheckpoint{RunID: run.RunID, Phase: EvolutionRunQueued, Attempt: 1,
		PinnedInputsHash: pinHash, Summary: "s", RecordedAt: orchestratorTestTime}

	terminals := []EvolutionRunStatus{
		EvolutionRunCompleted, EvolutionRunNoAction, EvolutionRunRejected,
		EvolutionRunCancelled, EvolutionRunFailed, EvolutionRunStale, EvolutionRunFenced,
	}
	for _, terminal := range terminals {
		terminalRun := run
		terminalRun.Status = terminal

		assert.ErrorIs(t, InterruptRun(terminalRun, EvolutionRunCancelled, "op", orchestratorTestTime),
			ErrLedgerConflict, "%s refuses interruption", terminal)
		_, ok, err := ResumeFromCheckpoint(terminalRun, []RunCheckpoint{checkpoint})
		require.NoError(t, err)
		assert.False(t, ok, "%s never resumes", terminal)
		decision, err := ReconcileRun(ReconcilerInput{Now: orchestratorTestTime, Run: terminalRun})
		require.NoError(t, err)
		assert.Equal(t, ReconcileNone, decision.Action, "%s reconciles to none", terminal)
		assert.False(t, terminal.CanTransition(EvolutionRunQueued), "%s cannot revive", terminal)
	}

	// Non-terminal runs accept the safety interrupts...
	for _, interrupt := range []EvolutionRunStatus{EvolutionRunCancelled, EvolutionRunFailed, EvolutionRunStale, EvolutionRunFenced} {
		assert.NoError(t, InterruptRun(run, interrupt, "operator:1", orchestratorTestTime))
	}
	// ...but the flow outcomes are not interrupts, and neither is an
	// unknown status.
	for _, notInterrupt := range []EvolutionRunStatus{EvolutionRunCompleted, EvolutionRunNoAction, EvolutionRunRejected, EvolutionRunStatus("wat")} {
		assert.ErrorIs(t, InterruptRun(run, notInterrupt, "operator:1", orchestratorTestTime), ErrInvalidContract)
	}
	// Interrupts carry an actor and a time.
	assert.ErrorIs(t, InterruptRun(run, EvolutionRunCancelled, "", orchestratorTestTime), ErrInvalidContract)
	assert.ErrorIs(t, InterruptRun(run, EvolutionRunCancelled, "op", time.Time{}), ErrInvalidContract)
}
