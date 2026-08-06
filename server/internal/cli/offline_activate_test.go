package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOfflineActivateStagedProbesCandidateThenCAS(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell candidate script is unix-oriented")
	}
	root := t.TempDir()
	store, err := NewVersionStore(root, "linux", func(context.Context, string, string) error {
		// StageBinary still runs verifier on publish; use a real script binary.
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Stage two "binaries" that print multica version when executed.
	writeVersionScript := func(tag string) string {
		t.Helper()
		dir := filepath.Join(store.VersionsRoot(), tag)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "multica")
		script := "#!/bin/sh\necho multica " + tag + "\n"
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		// Write metadata so verifyExisting/Resolve works with StageBinary path shape.
		// Use StageBinary via raw bytes instead so store is consistent.
		return path
	}
	_ = writeVersionScript

	// Proper StageBinary with scripts as content; verifier is no-op at stage,
	// probeCandidateVersion runs the script at activate time.
	for _, tag := range []string{"v0.3.77", "v0.3.78"} {
		script := []byte("#!/bin/sh\necho multica " + tag + "\n")
		if _, err := store.StageBinary(context.Background(), tag, script, testBinaryDigest(script), 0o755); err != nil {
			t.Fatalf("stage %s: %v", tag, err)
		}
		// Ensure executable bit after stage (StageBinary writes mode).
		staged, _ := store.ResolveStagedVersion(tag)
		_ = os.Chmod(staged.BinaryPath, 0o755)
	}

	// Bootstrap Active gen1 = v0.3.77 without going through OfflineActivate
	// (CompareAndSwap from 0).
	if _, err := store.CompareAndSwapActivation(context.Background(), 0, "v0.3.77"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	next, path, err := store.OfflineActivateStaged(context.Background(), "v0.3.78", "test-attempt-1")
	if err != nil {
		t.Fatalf("OfflineActivateStaged: %v", err)
	}
	if next.Generation != 2 || next.ActiveVersion != "v0.3.78" || next.PreviousVersion != "v0.3.77" {
		t.Fatalf("state = %+v", next)
	}
	if path == "" {
		t.Fatal("empty staged path")
	}

	// Journal should be terminal committed (or clearable).
	j, err := store.ReadActivationJournal()
	if err != nil && !errors.Is(err, ErrNoActivationJournal) {
		t.Fatalf("journal: %v", err)
	}
	if err == nil && j.Phase != ActivationPhaseCommitted && j.Phase != ActivationPhaseAborted {
		// committed expected
		if j.Phase != ActivationPhaseCommitted {
			t.Fatalf("journal phase = %s, want committed", j.Phase)
		}
	}
}

func TestOfflineActivateAbortsOnBadCandidate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix script")
	}
	store := testVersionStore(t, func(context.Context, string, string) error { return nil })
	// Stage a non-executable-looking payload that will fail --version.
	bad := []byte("not-a-binary")
	if _, err := store.StageBinary(context.Background(), "v0.3.78", bad, testBinaryDigest(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := store.OfflineActivateStaged(context.Background(), "v0.3.78", "bad")
	if err == nil {
		t.Fatal("expected candidate probe failure")
	}
	state, _ := store.ReadActivationState()
	if state.Generation != 0 {
		t.Fatalf("Active should be unchanged on abort: %+v", state)
	}
}

func TestRollbackToPreviousActiveUsesVerifiedCASPath(t *testing.T) {
	previousProbe := probeStagedCandidate
	probeStagedCandidate = func(context.Context, string, string, string) error { return nil }
	t.Cleanup(func() { probeStagedCandidate = previousProbe })
	store := testVersionStore(t, func(context.Context, string, string) error { return nil })
	stageAndActivate(t, store, "v0.3.77")
	stageAndActivate(t, store, "v0.3.78")
	rolledBack, _, err := store.RollbackToPreviousActive(context.Background(), "rollback-1")
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.ActiveVersion != "v0.3.77" || rolledBack.PreviousVersion != "v0.3.78" {
		t.Fatalf("rollback activation = %+v", rolledBack)
	}
}
