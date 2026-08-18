package computer

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunnerStateRoundTripAndGenerationFence(t *testing.T) {
	root := t.TempDir()
	state := persistedRunnerState{
		WorkspaceID: "workspace-a", RunnerGeneration: 2, OwnerPID: 5678,
		StartedAt: time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC),
	}
	if err := writeRunnerState(root, state); err != nil {
		t.Fatal(err)
	}
	if err := writeRunnerPID(root, state.WorkspaceID, 1234); err != nil {
		t.Fatal(err)
	}
	if err := writeRunnerConnected(root, state.WorkspaceID, persistedRunnerConnected{PID: 1234, ConnectedAt: state.StartedAt, RunnerEndpoint: "unix:///tmp/runner.sock"}); err != nil {
		t.Fatal(err)
	}
	path := runnerStatePath(root, state.WorkspaceID)
	if _, err := os.Stat(runnerPIDPath(root, state.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(runnerConnectedPath(root, state.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	if runnerStatePath(root, state.WorkspaceID) == runnerPIDPath(root, state.WorkspaceID) || runnerPIDPath(root, state.WorkspaceID) == runnerConnectedPath(root, state.WorkspaceID) {
		t.Fatal("runner PID, state, and connected evidence must be separate files")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("runner state permissions = %o, want 600", got)
	}
	got, err := readRunnerState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkspaceID != state.WorkspaceID || got.RunnerGeneration != state.RunnerGeneration || !got.StartedAt.Equal(state.StartedAt) {
		t.Fatalf("runner state = %+v, want %+v", got, state)
	}
	if err := removeRunnerState(root, state.WorkspaceID, 1, 1234); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stale generation removed current state: %v", err)
	}
	if err := removeRunnerState(root, state.WorkspaceID, state.RunnerGeneration, 1234); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("runner state still exists: %v", err)
	}
}

func TestRecoverRunnerStatesRemovesDeadOwnerState(t *testing.T) {
	root := t.TempDir()
	state := persistedRunnerState{
		WorkspaceID: "workspace-a", RunnerGeneration: 1, OwnerPID: 999998,
		StartedAt: time.Now().UTC(),
	}
	if err := writeRunnerState(root, state); err != nil {
		t.Fatal(err)
	}
	if err := writeRunnerPID(root, state.WorkspaceID, 999999); err != nil {
		t.Fatal(err)
	}
	if err := recoverRunnerStates(root, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(runnerStatePath(root, state.WorkspaceID))); !os.IsNotExist(err) {
		t.Fatalf("orphan runner directory still exists: %v", err)
	}
	if _, err := os.Stat(runnerStatePath(root, state.WorkspaceID)); !os.IsNotExist(err) {
		t.Fatalf("dead runner state still exists: %v", err)
	}
}

func TestRecoverRunnerStatesRefusesMismatchedPIDIdentity(t *testing.T) {
	root := t.TempDir()
	state := persistedRunnerState{
		WorkspaceID: "workspace-mismatch", RunnerGeneration: 1, OwnerPID: 999998,
		RunnerPID: os.Getpid(), RunnerIdentity: "not-this-process",
		StartedAt: time.Now().UTC(),
	}
	if err := writeRunnerState(root, state); err != nil {
		t.Fatal(err)
	}
	if err := writeRunnerPID(root, state.WorkspaceID, state.RunnerPID); err != nil {
		t.Fatal(err)
	}
	if err := recoverRunnerStates(root, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(runnerStatePath(root, state.WorkspaceID)); !os.IsNotExist(err) {
		t.Fatalf("mismatched runner state still exists: %v", err)
	}
}
