package computer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
)

func TestDiagnoseReadOnlyAndReflectsResident(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lc := &Lifecycle{}
	lc.Probe = func(context.Context, string) map[string]any {
		return map[string]any{"status": "running", "connected": true, "daemon_id": "d1"}
	}

	d := lc.Diagnose()
	if d.Resident != "running" || !d.Connected {
		t.Fatalf("doctor = %+v, want running+connected from resident (not agent)", d)
	}
	if d.IdentityState == "" {
		t.Fatalf("doctor missing identity_state")
	}
}

func TestDiagnoseRunningButServerDisconnected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lc := &Lifecycle{}
	lc.Probe = func(context.Context, string) map[string]any {
		return map[string]any{"status": "running", "connected": false, "agents": []any{"fresh-agent"}}
	}

	d := lc.Diagnose()
	if d.Resident != "running" || d.Connected {
		t.Fatalf("doctor = %+v, local process or Agent health must not imply server connectivity", d)
	}
}

func TestDiagnoseStoppedIsDisconnected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lc := &Lifecycle{}
	lc.Probe = func(context.Context, string) map[string]any { return map[string]any{"status": "stopped"} }

	d := lc.Diagnose()
	if d.Resident != "stopped" || d.Connected {
		t.Fatalf("doctor = %+v, want stopped+disconnected", d)
	}
	// doctor never goes through the fix path, and never writes anything.
	if len(d.FixApplied) != 0 {
		t.Fatalf("read-only diagnose must report no mutations: %+v", d)
	}
}

func TestDiagnoseReportsConfigResidentDriftAndPreservedMigrationEvidence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := cli.SaveCLIConfig(cli.CLIConfig{
		Environment: "test",
		ServerURL:   "https://test.leagent.me",
		AppURL:      "https://test.leagent.me",
	}); err != nil {
		t.Fatal(err)
	}
	legacyDir := filepath.Join(home, ".multica", "profiles", "old")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyID := "e0d441a0-897c-40be-b303-f2fc2877bd2f"
	if err := os.WriteFile(filepath.Join(legacyDir, "daemon.id"), []byte(legacyID+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	lc := &Lifecycle{}
	lc.Probe = func(context.Context, string) map[string]any {
		return map[string]any{
			"status":         "running",
			"connected":      true,
			"environment":    "production",
			"serverUrl":      "https://api.leagent.me",
			"releaseChannel": "latest",
		}
	}
	d := lc.Diagnose()
	if !d.ConfigurationDrift || d.ResidentEnvironment != "production" || d.ResidentPackageSource != "stable" {
		t.Fatalf("doctor did not report configured/resident drift: %+v", d)
	}
	if d.IdentityState != "ambiguous" || len(d.LegacyIdentityCandidates) != 1 || d.LegacyIdentityCandidates[0] != legacyID {
		t.Fatalf("doctor did not report preserved migration evidence: %+v", d)
	}
}

func TestFixRemovesExpiredUpgradeStagingAndKeepsRecentStaging(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	machineRoot, err := cli.MachineStateRoot()
	if err != nil {
		t.Fatal(err)
	}
	oldStage := filepath.Join(machineRoot, "upgrade-staging", "v1.0.0")
	recentStage := filepath.Join(machineRoot, "upgrade-staging", "v1.0.1")
	for _, stage := range []string{oldStage, recentStage} {
		if err := os.MkdirAll(stage, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stage, "multica"), []byte("candidate"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(oldStage, old, old); err != nil {
		t.Fatal(err)
	}

	lc := &Lifecycle{}
	lc.Probe = func(context.Context, string) map[string]any { return map[string]any{"status": "running"} }
	got := lc.Fix(Diagnosis{Resident: "running"})

	if _, err := os.Stat(oldStage); !os.IsNotExist(err) {
		t.Fatalf("expired upgrade staging still exists: %v", err)
	}
	if _, err := os.Stat(recentStage); err != nil {
		t.Fatalf("recent upgrade staging was removed: %v", err)
	}
	if len(got.FixApplied) != 1 || !strings.Contains(got.FixApplied[0], oldStage) {
		t.Fatalf("fix receipt = %#v, want removed expired staging", got.FixApplied)
	}
}

func TestFixQuarantinesOnlyExpiredBindingStateWithoutABinding(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := RootDir("")
	store := NewBindingsStore(root)
	if err := store.AddOrRepair(WorkspaceBinding{
		Environment: "production",
		WorkspaceID: "workspace-active",
		ComputerID:  "computer-a",
		Active:      true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrRepair(WorkspaceBinding{
		Environment: "production",
		WorkspaceID: "workspace-inactive",
		ComputerID:  "computer-a",
	}); err != nil {
		t.Fatal(err)
	}
	activeState := filepath.Join(root, "binding-children", "production", "workspace-active")
	inactiveState := filepath.Join(root, "binding-children", "production", "workspace-inactive")
	abandonedState := filepath.Join(root, "binding-children", "production", "workspace-abandoned")
	recentState := filepath.Join(root, "binding-children", "production", "workspace-recent")
	for _, state := range []string{activeState, inactiveState, abandonedState, recentState} {
		if err := os.MkdirAll(state, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(state, "outbox.json"), []byte("[]"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-25 * time.Hour)
	for _, state := range []string{activeState, inactiveState, abandonedState} {
		if err := os.Chtimes(state, old, old); err != nil {
			t.Fatal(err)
		}
	}

	got := (&Lifecycle{}).Fix(Diagnosis{Resident: "running"})

	if _, err := os.Stat(abandonedState); !os.IsNotExist(err) {
		t.Fatalf("expired abandoned Binding state still exists: %v", err)
	}
	for _, state := range []string{activeState, inactiveState, recentState} {
		if _, err := os.Stat(state); err != nil {
			t.Fatalf("preserved Binding state %s is missing: %v", state, err)
		}
	}
	quarantined, err := filepath.Glob(filepath.Join(root, ".quarantine", "*-production-workspace-abandoned"))
	if err != nil || len(quarantined) != 1 {
		t.Fatalf("quarantined Binding state = %#v, %v", quarantined, err)
	}
	if len(got.FixApplied) != 1 || !strings.Contains(got.FixApplied[0], abandonedState) {
		t.Fatalf("fix receipt = %#v, want quarantined abandoned state", got.FixApplied)
	}
}

func TestDiagnoseReportsRunnerOwnedByTheLiveResident(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pid := os.Getpid()
	if err := writeRunnerState(RootDir(""), persistedRunnerState{
		WorkspaceID: "workspace-a", DaemonInstanceID: "start-a", OwnerPID: pid,
		RunnerPID: pid, StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	lc := &Lifecycle{}
	lc.Probe = func(context.Context, string) map[string]any {
		return map[string]any{"status": "running", "connected": true, "pid": float64(pid)}
	}
	d := lc.Diagnose()
	if len(d.Runners) != 1 || !d.Runners[0].Alive || !d.Runners[0].Owned || d.Runners[0].WorkspaceID != "workspace-a" || d.Runners[0].PID != pid {
		t.Fatalf("doctor runners = %+v, want one owned live runner", d.Runners)
	}
	if len(d.UnownedLive) != 0 {
		t.Fatalf("doctor reported an owned runner as unowned: %+v", d.UnownedLive)
	}
}

func TestDiagnoseReportsUnownedLiveRunnerAsDegraded(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pid := os.Getpid()
	// Owned by a different (dead) generation's pid, not the currently
	// probed resident: this is the split-brain evidence doctor must surface.
	if err := writeRunnerState(RootDir(""), persistedRunnerState{
		WorkspaceID: "workspace-a", DaemonInstanceID: "start-a", OwnerPID: 999998,
		RunnerPID: pid, StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	lc := &Lifecycle{}
	lc.Probe = func(context.Context, string) map[string]any {
		return map[string]any{"status": "running", "connected": true, "pid": float64(os.Getpid() + 1)}
	}
	d := lc.Diagnose()
	if len(d.Runners) != 1 || !d.Runners[0].Alive || d.Runners[0].Owned {
		t.Fatalf("doctor runners = %+v, want one alive, unowned runner", d.Runners)
	}
	if len(d.UnownedLive) != 1 || d.UnownedLive[0].WorkspaceID != "workspace-a" || d.UnownedLive[0].PID != pid {
		t.Fatalf("doctor UnownedLive = %+v, want the alive unowned runner", d.UnownedLive)
	}
}

func TestDiagnoseReportsDeadRunnerAsNotAliveOrOwned(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := writeRunnerState(RootDir(""), persistedRunnerState{
		WorkspaceID: "workspace-a", DaemonInstanceID: "start-a", OwnerPID: 999998,
		RunnerPID: 999999, StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	lc := &Lifecycle{}
	lc.Probe = func(context.Context, string) map[string]any {
		return map[string]any{"status": "running", "connected": true, "pid": float64(os.Getpid())}
	}
	d := lc.Diagnose()
	if len(d.Runners) != 1 || d.Runners[0].Alive || d.Runners[0].Owned {
		t.Fatalf("doctor runners = %+v, want the dead runner reported as neither alive nor owned", d.Runners)
	}
	if len(d.UnownedLive) != 0 {
		t.Fatalf("doctor flagged a dead runner as unowned-live: %+v", d.UnownedLive)
	}
}

func TestFixRemovesStaleResidentPIDOnlyWhenResidentIsStopped(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pidPath := PIDPath("")
	if err := os.MkdirAll(filepath.Dir(pidPath), 0o700); err != nil {
		t.Fatal(err)
	}
	writePID := func() {
		if err := os.WriteFile(pidPath, []byte("999999999\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writePID()
	lc := &Lifecycle{}
	lc.Probe = func(context.Context, string) map[string]any { return map[string]any{"status": "stopped"} }
	got := lc.Fix(Diagnosis{Resident: "stopped"})
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("stale resident PID still exists: %v", err)
	}
	if len(got.FixApplied) != 1 || !strings.Contains(got.FixApplied[0], pidPath) {
		t.Fatalf("fix receipt = %#v, want removed stale PID", got.FixApplied)
	}

	writePID()
	lc.Fix(Diagnosis{Resident: "running"})
	if _, err := os.Stat(pidPath); err != nil {
		t.Fatalf("running resident PID was removed: %v", err)
	}
}

// TestReclaimOrphanedRunnersTerminatesDeadOwnerRunner covers the doctor --fix
// escape hatch: a Workspace Runner whose owning Host is gone is the
// self-locking state that previously required a manual kill, so Fix must be
// able to clear it.
func TestReclaimOrphanedRunnersTerminatesDeadOwnerRunner(t *testing.T) {
	root := t.TempDir()
	orphan := spawnReclaimTestProcess(t, "sleep", "30")
	pid := orphan.Process.Pid
	writeReclaimableRunnerFixture(t, root, "workspace-orphan", pid, "")

	applied := reclaimOrphanedRunners(root)

	if len(applied) != 1 {
		t.Fatalf("expected one reported mutation, got %v", applied)
	}
	if !strings.Contains(applied[0], "terminated orphaned Workspace Runner") {
		t.Fatalf("unexpected report: %q", applied[0])
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if alive, known := processAlive(pid); known && !alive {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("orphan pid %d still alive after reclaim", pid)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if remaining, err := findReclaimableRunners(root, nil); err != nil || len(remaining) != 0 {
		t.Fatalf("expected persisted state to be cleared, got %v (err %v)", remaining, err)
	}
}

// TestReclaimOrphanedRunnersLeavesLiveOwnerAlone is the safety fence: a Runner
// whose recorded owner is still alive belongs to a running Host and must never
// be signaled by doctor --fix.
func TestReclaimOrphanedRunnersLeavesLiveOwnerAlone(t *testing.T) {
	root := t.TempDir()
	runner := spawnReclaimTestProcess(t, "sleep", "30")
	pid := runner.Process.Pid
	state := persistedRunnerState{
		WorkspaceID: "workspace-owned", DaemonInstanceID: "live-start",
		OwnerPID: os.Getpid(), RunnerPID: pid, StartedAt: time.Now().UTC(),
	}
	if err := writeRunnerState(root, state); err != nil {
		t.Fatal(err)
	}
	if err := writeRunnerPID(root, "workspace-owned", pid); err != nil {
		t.Fatal(err)
	}

	if applied := reclaimOrphanedRunners(root); len(applied) != 0 {
		t.Fatalf("expected no mutation for a live-owner runner, got %v", applied)
	}
	if alive, known := processAlive(pid); !known || !alive {
		t.Fatal("runner with a live owner must not be signaled")
	}
}
