package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestActivationJournalPrepareAdvanceAbortClear(t *testing.T) {
	store := testVersionStore(t, func(context.Context, string, string) error { return nil })
	for _, version := range []string{"v0.3.77", "v0.3.78"} {
		data := []byte("multica-" + version)
		if _, err := store.StageBinary(
			context.Background(),
			version,
			data,
			testBinaryDigest(data),
			0o755,
		); err != nil {
			t.Fatalf("stage %s: %v", version, err)
		}
	}
	// Bootstrap gen1 Active so committed_active is meaningful.
	if _, err := store.CompareAndSwapActivation(context.Background(), 0, "v0.3.77"); err != nil {
		t.Fatalf("bootstrap active: %v", err)
	}

	if _, err := store.ReadActivationJournal(); !errors.Is(err, ErrNoActivationJournal) {
		t.Fatalf("empty journal error = %v, want ErrNoActivationJournal", err)
	}

	prepared, err := store.PrepareActivationAttempt(
		"attempt-1",
		1,
		"v0.3.77",
		"v0.3.78",
	)
	if err != nil {
		t.Fatalf("PrepareActivationAttempt: %v", err)
	}
	if prepared.Phase != ActivationPhasePrepared ||
		prepared.BaseGeneration != 1 ||
		prepared.CommittedActive != "v0.3.77" ||
		prepared.CandidateTag != "v0.3.78" {
		t.Fatalf("prepared = %+v", prepared)
	}

	// Concurrent prepare while non-terminal must fail.
	if _, err := store.PrepareActivationAttempt("attempt-2", 1, "v0.3.77", "v0.3.78"); err == nil {
		t.Fatal("second prepare succeeded while first non-terminal")
	}

	// Illegal skip prepared → candidate_healthy.
	if _, err := store.AdvanceActivationPhase("attempt-1", ActivationPhaseCandidateHealthy, ""); !errors.Is(err, ErrActivationPhaseOrder) {
		t.Fatalf("illegal advance error = %v, want ErrActivationPhaseOrder", err)
	}

	// Happy path phases through committed.
	for _, phase := range []ActivationAttemptPhase{
		ActivationPhaseDraining,
		ActivationPhaseCandidateRunning,
		ActivationPhaseCandidateHealthy,
		ActivationPhaseCommitted,
	} {
		got, err := store.AdvanceActivationPhase("attempt-1", phase, "")
		if err != nil {
			t.Fatalf("advance to %s: %v", phase, err)
		}
		if got.Phase != phase {
			t.Fatalf("phase = %s, want %s", got.Phase, phase)
		}
	}

	// After committed, clear allowed; Active generation untouched by journal alone.
	activeBefore, err := store.ReadActivationState()
	if err != nil {
		t.Fatalf("ReadActivationState: %v", err)
	}
	if err := store.ClearActivationJournal(); err != nil {
		t.Fatalf("ClearActivationJournal: %v", err)
	}
	if _, err := store.ReadActivationJournal(); !errors.Is(err, ErrNoActivationJournal) {
		t.Fatalf("after clear: %v", err)
	}
	activeAfter, err := store.ReadActivationState()
	if err != nil {
		t.Fatalf("ReadActivationState after clear: %v", err)
	}
	if activeAfter != activeBefore {
		t.Fatalf("journal clear mutated ActivationState: before=%+v after=%+v", activeBefore, activeAfter)
	}
}

func TestActivationJournalAbortDoesNotCAS(t *testing.T) {
	store := testVersionStore(t, func(context.Context, string, string) error { return nil })
	for _, version := range []string{"v0.3.77", "v0.3.78"} {
		data := []byte("multica-" + version)
		if _, err := store.StageBinary(
			context.Background(),
			version,
			data,
			testBinaryDigest(data),
			0o755,
		); err != nil {
			t.Fatalf("stage %s: %v", version, err)
		}
	}
	first, err := store.CompareAndSwapActivation(context.Background(), 0, "v0.3.77")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	if _, err := store.PrepareActivationAttempt("a-abort", 1, "v0.3.77", "v0.3.78"); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	aborted, err := store.AdvanceActivationPhase("a-abort", ActivationPhaseAborted, "drain_timeout")
	if err != nil {
		t.Fatalf("abort: %v", err)
	}
	if aborted.Phase != ActivationPhaseAborted || aborted.ErrorCode != "drain_timeout" {
		t.Fatalf("aborted = %+v", aborted)
	}

	// CUT-T1: committed Active unchanged after abort (no CAS).
	state, err := store.ReadActivationState()
	if err != nil {
		t.Fatalf("ReadActivationState: %v", err)
	}
	if state.Generation != first.Generation || state.ActiveVersion != first.ActiveVersion {
		t.Fatalf("abort mutated Active: got %+v want %+v", state, first)
	}

	// New prepare allowed after terminal abort.
	if _, err := store.PrepareActivationAttempt("a-retry", 1, "v0.3.77", "v0.3.78"); err != nil {
		t.Fatalf("prepare after abort: %v", err)
	}
}

func TestActivationJournalPrepareRequiresStagedCandidate(t *testing.T) {
	store := testVersionStore(t, func(context.Context, string, string) error { return nil })
	data := []byte("multica-v0.3.77")
	if _, err := store.StageBinary(
		context.Background(),
		"v0.3.77",
		data,
		testBinaryDigest(data),
		0o755,
	); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if _, err := store.PrepareActivationAttempt("a1", 0, "", "v0.3.99"); err == nil {
		t.Fatal("prepare with unstaged candidate succeeded")
	}
}

func TestActivationJournalRefuseClearNonTerminal(t *testing.T) {
	store := testVersionStore(t, func(context.Context, string, string) error { return nil })
	for _, version := range []string{"v0.3.77", "v0.3.78"} {
		data := []byte("multica-" + version)
		if _, err := store.StageBinary(
			context.Background(),
			version,
			data,
			testBinaryDigest(data),
			0o755,
		); err != nil {
			t.Fatalf("stage %s: %v", version, err)
		}
	}
	if _, err := store.PrepareActivationAttempt("a1", 0, "", "v0.3.78"); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if err := store.ClearActivationJournal(); err == nil {
		t.Fatal("clear non-terminal journal succeeded")
	}
	// File must still exist.
	if _, err := os.Stat(filepath.Join(store.Root(), activationJournalName)); err != nil {
		t.Fatalf("journal missing after refused clear: %v", err)
	}
}
