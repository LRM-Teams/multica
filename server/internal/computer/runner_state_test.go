package computer

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunnerStateRoundTripAndStartIdentityFence(t *testing.T) {
	root := t.TempDir()
	state := persistedRunnerState{
		WorkspaceID: "workspace-a", StartIdentity: "start-2", OwnerPID: 5678,
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
	if got.WorkspaceID != state.WorkspaceID || got.StartIdentity != state.StartIdentity || !got.StartedAt.Equal(state.StartedAt) {
		t.Fatalf("runner state = %+v, want %+v", got, state)
	}
	if err := removeRunnerState(root, state.WorkspaceID, "stale-start", 1234); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stale start identity removed current state: %v", err)
	}
	if err := removeRunnerState(root, state.WorkspaceID, state.StartIdentity, 1234); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("runner state still exists: %v", err)
	}
}

func TestRecoverRunnerStatesRemovesDeadOwnerState(t *testing.T) {
	root := t.TempDir()
	state := persistedRunnerState{
		WorkspaceID: "workspace-a", StartIdentity: "start-1", OwnerPID: 999998,
		StartedAt: time.Now().UTC(),
	}
	if err := writeRunnerState(root, state); err != nil {
		t.Fatal(err)
	}
	if err := writeRunnerPID(root, state.WorkspaceID, 999999); err != nil {
		t.Fatal(err)
	}
	adopted, err := recoverRunnerStates(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(adopted) != 0 {
		t.Fatalf("dead runner was adopted: %+v", adopted)
	}
	if _, err := os.Stat(filepath.Dir(runnerStatePath(root, state.WorkspaceID))); !os.IsNotExist(err) {
		t.Fatalf("orphan runner directory still exists: %v", err)
	}
	if _, err := os.Stat(runnerStatePath(root, state.WorkspaceID)); !os.IsNotExist(err) {
		t.Fatalf("dead runner state still exists: %v", err)
	}
}

func TestRecoverRunnerStatesAdoptsLivePIDWithoutProcessIdentity(t *testing.T) {
	root := t.TempDir()
	pid := os.Getpid()
	state := persistedRunnerState{
		WorkspaceID: "workspace-live", StartIdentity: "start-live", OwnerPID: 999998,
		RunnerPID: pid, StartedAt: time.Now().UTC(),
	}
	if err := writeRunnerState(root, state); err != nil {
		t.Fatal(err)
	}
	if err := writeRunnerPID(root, state.WorkspaceID, pid); err != nil {
		t.Fatal(err)
	}
	adopted, err := recoverRunnerStates(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(adopted) != 1 || adopted[0].WorkspaceID != state.WorkspaceID || adopted[0].PID != pid {
		t.Fatalf("adopted = %+v, want live pidfile runner", adopted)
	}
	if _, err := os.Stat(runnerStatePath(root, state.WorkspaceID)); err != nil {
		t.Fatalf("live runner state was deleted: %v", err)
	}
}
