package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallLauncherReleaseUsesVersionStoreAndRetainsRollback(t *testing.T) {
	oldProbe := probeStagedCandidate
	probeStagedCandidate = func(context.Context, string, string, string) error { return nil }
	t.Cleanup(func() { probeStagedCandidate = oldProbe })

	root := t.TempDir()
	store, err := NewVersionStore(root, "linux", func(context.Context, string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	oldBytes := []byte("old-release")
	old, err := store.StageBinary(ctx, "v1.0.0", oldBytes, bytesSHA256(oldBytes), 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompareAndSwapActivation(ctx, 0, old.Version); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(root, "bin", "multica")
	if err := os.MkdirAll(filepath.Dir(launcher), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, oldBytes, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := store.RememberLauncherPath(launcher); err != nil {
		t.Fatal(err)
	}

	candidateBytes := []byte("new-release")
	candidate := filepath.Join(root, "candidate")
	if err := os.WriteFile(candidate, candidateBytes, 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := store.InstallLauncherRelease(ctx, candidate, "v1.1.0-alpha.2", bytesSHA256(candidateBytes), launcher)
	if err != nil {
		t.Fatal(err)
	}
	if result.State.ActiveVersion != "v1.1.0-alpha.2" || result.State.PreviousVersion != "v1.0.0" {
		t.Fatalf("activation = %+v", result.State)
	}
	if got, err := os.ReadFile(launcher); err != nil || string(got) != string(candidateBytes) {
		t.Fatalf("launcher = %q, %v", got, err)
	}
	if got, err := os.ReadFile(result.Staged.BinaryPath); err != nil || string(got) != string(candidateBytes) {
		t.Fatalf("staged candidate = %q, %v", got, err)
	}

	// Exact-version installer reruns repair the same state without another
	// activation generation or duplicate version directory.
	again, err := store.InstallLauncherRelease(ctx, candidate, "v1.1.0-alpha.2", bytesSHA256(candidateBytes), launcher)
	if err != nil {
		t.Fatal(err)
	}
	if again.State.Generation != result.State.Generation {
		t.Fatalf("idempotent generation = %d, want %d", again.State.Generation, result.State.Generation)
	}
}

func TestInstallLauncherReleaseInitializesFreshVersionStore(t *testing.T) {
	oldProbe := probeStagedCandidate
	probeStagedCandidate = func(context.Context, string, string, string) error { return nil }
	t.Cleanup(func() { probeStagedCandidate = oldProbe })

	root := t.TempDir()
	store, err := NewVersionStore(root, "linux", func(context.Context, string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	candidateBytes := []byte("first-release")
	candidate := filepath.Join(root, "candidate")
	if err := os.WriteFile(candidate, candidateBytes, 0o755); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(root, "bin", "multica")
	result, err := store.InstallLauncherRelease(context.Background(), candidate, "v1.0.0", bytesSHA256(candidateBytes), launcher)
	if err != nil {
		t.Fatal(err)
	}
	if result.State.Generation != 1 || result.State.ActiveVersion != "v1.0.0" || result.State.PreviousVersion != "" {
		t.Fatalf("fresh activation = %+v", result.State)
	}
	if remembered, ok, err := store.LauncherPath(); err != nil || !ok || remembered != launcher {
		t.Fatalf("launcher state = %q, %v, %v", remembered, ok, err)
	}
}

func TestInstallLauncherReleaseReplacesCorruptActiveWithoutKeepingItRollbackable(t *testing.T) {
	oldProbe := probeStagedCandidate
	probeStagedCandidate = func(context.Context, string, string, string) error { return nil }
	t.Cleanup(func() { probeStagedCandidate = oldProbe })

	root := t.TempDir()
	store, err := NewVersionStore(root, "linux", func(context.Context, string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	oldBytes := []byte("official-old-release")
	old, err := store.StageBinary(ctx, "v1.0.0", oldBytes, bytesSHA256(oldBytes), 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompareAndSwapActivation(ctx, 0, old.Version); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(root, "bin", "multica")
	if err := os.MkdirAll(filepath.Dir(launcher), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, oldBytes, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := store.RememberLauncherPath(launcher); err != nil {
		t.Fatal(err)
	}

	// Reproduce the field failure: a test-machine hotfix overwrote the
	// committed Active binary without updating its immutable metadata.
	if err := os.WriteFile(old.BinaryPath, []byte("locally-replaced-old-release"), 0o755); err != nil {
		t.Fatal(err)
	}

	candidateBytes := []byte("verified-new-release")
	candidate := filepath.Join(root, "candidate")
	if err := os.WriteFile(candidate, candidateBytes, 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := store.InstallLauncherRelease(
		ctx,
		candidate,
		"v1.1.0-alpha.2",
		bytesSHA256(candidateBytes),
		launcher,
	)
	if err != nil {
		t.Fatalf("verified installer candidate must recover from an unusable predecessor: %v", err)
	}
	if result.State.ActiveVersion != "v1.1.0-alpha.2" || result.State.PreviousVersion != "v1.0.0" {
		t.Fatalf("activation = %+v", result.State)
	}
	if !result.State.PreviousVersionUnusable {
		t.Fatalf("corrupt predecessor was advertised as rollbackable: %+v", result.State)
	}
	if _, _, err := store.RollbackToPreviousActive(ctx, "rollback-corrupt"); err == nil ||
		!strings.Contains(err.Error(), "unusable") {
		t.Fatalf("rollback to corrupt predecessor error = %v, want explicit unusable rejection", err)
	}
	if got, err := os.ReadFile(launcher); err != nil || string(got) != string(candidateBytes) {
		t.Fatalf("launcher = %q, %v", got, err)
	}
}

func TestInstallLauncherReleaseRejectsChecksumBeforeMutation(t *testing.T) {
	root := t.TempDir()
	store, err := NewVersionStore(root, "linux", func(context.Context, string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(root, "candidate")
	if err := os.WriteFile(candidate, []byte("candidate"), 0o755); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(root, "bin", "multica")
	if _, err := store.InstallLauncherRelease(context.Background(), candidate, "v1.2.0", bytesSHA256([]byte("different")), launcher); err == nil {
		t.Fatal("checksum mismatch was accepted")
	}
	state, err := store.ReadActivationState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Generation != 0 {
		t.Fatalf("checksum failure mutated activation: %+v", state)
	}
	if _, err := os.Stat(launcher); !os.IsNotExist(err) {
		t.Fatalf("checksum failure created launcher: %v", err)
	}
}
