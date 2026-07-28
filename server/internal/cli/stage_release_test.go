package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestStageReleaseBytesDoesNotTouchSiblingExecutable(t *testing.T) {
	// CUT-T1 foundation: stage lands only under versions/; a sibling "Active"
	// path that mimics today's self-replace target must stay byte-identical.
	root := t.TempDir()
	fakeExe := filepath.Join(root, "bin", "multica")
	if err := os.MkdirAll(filepath.Dir(fakeExe), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	original := []byte("original-running-binary-v0.3.77")
	if err := os.WriteFile(fakeExe, original, 0o755); err != nil {
		t.Fatalf("write fake exe: %v", err)
	}

	store, err := NewVersionStore(filepath.Join(root, "store"), "linux", func(context.Context, string, string) error {
		return nil
	})
	if err != nil {
		t.Fatalf("NewVersionStore: %v", err)
	}

	candidate := []byte("candidate-binary-v0.3.78-payload")
	result, err := StageReleaseBytes(
		context.Background(),
		store,
		"v0.3.78",
		candidate,
		"multica_0.3.78_linux_amd64.tar.gz",
	)
	if err != nil {
		t.Fatalf("StageReleaseBytes: %v", err)
	}
	if result.Staged.Version != "v0.3.78" {
		t.Fatalf("staged version = %q", result.Staged.Version)
	}
	if _, err := os.Stat(result.Staged.BinaryPath); err != nil {
		t.Fatalf("staged binary missing: %v", err)
	}
	stagedBytes, err := os.ReadFile(result.Staged.BinaryPath)
	if err != nil {
		t.Fatalf("read staged: %v", err)
	}
	if string(stagedBytes) != string(candidate) {
		t.Fatalf("staged bytes mismatch")
	}

	// Fake exe must be unchanged (no self-replace).
	got, err := os.ReadFile(fakeExe)
	if err != nil {
		t.Fatalf("read fake exe: %v", err)
	}
	if string(got) != string(original) {
		t.Fatalf("executable was mutated by stage path; self-replace leak")
	}

	// Activation still empty — stage alone does not CAS.
	state, err := store.ReadActivationState()
	if err != nil {
		t.Fatalf("ReadActivationState: %v", err)
	}
	if state.Generation != 0 || state.ActiveVersion != "" {
		t.Fatalf("stage mutated ActivationState: %+v", state)
	}
}

func TestStageReleaseBytesRejectsNonReleaseTag(t *testing.T) {
	store := testVersionStore(t, func(context.Context, string, string) error { return nil })
	_, err := StageReleaseBytes(context.Background(), store, "bootstrap/v0.3.78-deadbeef", []byte("x"), "a")
	if err == nil {
		t.Fatal("expected non-release tag reject")
	}
}
