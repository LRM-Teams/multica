package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestBootstrapActiveFromBinaryHappyPath(t *testing.T) {
	store := testVersionStore(t, func(context.Context, string, string) error { return nil })
	binPath := filepath.Join(t.TempDir(), "multica")
	payload := []byte("running-release-binary-v0.3.77")
	if err := os.WriteFile(binPath, payload, 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}

	state, err := store.BootstrapActiveFromBinary(context.Background(), binPath, "v0.3.77")
	if err != nil {
		t.Fatalf("BootstrapActiveFromBinary: %v", err)
	}
	if state.Generation != 1 || state.ActiveVersion != "v0.3.77" || state.PreviousVersion != "" {
		t.Fatalf("state = %+v", state)
	}

	// Idempotent re-bootstrap same tag.
	again, err := store.BootstrapActiveFromBinary(context.Background(), binPath, "0.3.77")
	if err != nil {
		t.Fatalf("idempotent bootstrap: %v", err)
	}
	if again.Generation != 1 || again.ActiveVersion != "v0.3.77" {
		t.Fatalf("idempotent state = %+v", again)
	}

	// Staged path must exist under versions/.
	staged, err := store.stagedVersion("v0.3.77")
	if err != nil {
		t.Fatalf("stagedVersion: %v", err)
	}
	if _, err := os.Stat(staged.BinaryPath); err != nil {
		t.Fatalf("staged binary missing: %v", err)
	}
}

func TestBootstrapActiveRejectsDevVersion(t *testing.T) {
	store := testVersionStore(t, func(context.Context, string, string) error { return nil })
	binPath := filepath.Join(t.TempDir(), "multica")
	if err := os.WriteFile(binPath, []byte("dev"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := store.BootstrapActiveFromBinary(context.Background(), binPath, "v0.3.77-12-gabcdef")
	if !errors.Is(err, ErrBootstrapActiveUnverifiable) {
		t.Fatalf("error = %v, want ErrBootstrapActiveUnverifiable", err)
	}
	state, err := store.ReadActivationState()
	if err != nil {
		t.Fatalf("ReadActivationState: %v", err)
	}
	if state.Generation != 0 {
		t.Fatalf("failed bootstrap mutated generation: %+v", state)
	}
}

func TestBootstrapActiveRefuseOverwriteDifferentActive(t *testing.T) {
	store := testVersionStore(t, func(context.Context, string, string) error { return nil })
	for _, version := range []string{"v0.3.77", "v0.3.78"} {
		data := []byte("bin-" + version)
		if _, err := store.StageBinary(context.Background(), version, data, testBinaryDigest(data), 0o755); err != nil {
			t.Fatalf("stage %s: %v", version, err)
		}
	}
	if _, err := store.CompareAndSwapActivation(context.Background(), 0, "v0.3.77"); err != nil {
		t.Fatalf("cas: %v", err)
	}
	binPath := filepath.Join(t.TempDir(), "multica")
	if err := os.WriteFile(binPath, []byte("other"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := store.BootstrapActiveFromBinary(context.Background(), binPath, "v0.3.78")
	if err == nil {
		t.Fatal("expected refuse overwrite different Active")
	}
	state, _ := store.ReadActivationState()
	if state.ActiveVersion != "v0.3.77" || state.Generation != 1 {
		t.Fatalf("Active mutated: %+v", state)
	}
}
