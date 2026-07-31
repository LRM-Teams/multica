package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
)

func newTestVersionStoreForHandoff(t *testing.T) *cli.VersionStore {
	t.Helper()
	verifier := func(ctx context.Context, binaryPath, expectedVersion string) error { return nil }
	store, err := cli.NewVersionStore(t.TempDir(), "linux", verifier)
	if err != nil {
		t.Fatalf("NewVersionStore: %v", err)
	}
	return store
}

// stageAndActivate stages a fake binary under version and commits it as the
// store's Active version, returning the staged binary's on-disk path.
func stageAndActivate(t *testing.T, store *cli.VersionStore, version string) string {
	t.Helper()
	data := []byte("fake-binary-" + version)
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])

	staged, err := store.StageBinary(context.Background(), version, data, digest, 0o755)
	if err != nil {
		t.Fatalf("StageBinary(%s): %v", version, err)
	}

	state, err := store.ReadActivationState()
	if err != nil {
		t.Fatalf("ReadActivationState: %v", err)
	}
	if _, err := store.CompareAndSwapActivation(context.Background(), state.Generation, version); err != nil {
		t.Fatalf("CompareAndSwapActivation(%s): %v", version, err)
	}
	return staged.BinaryPath
}

func TestResolveVersionHandoffBinaryNoActivationState(t *testing.T) {
	t.Parallel()
	store := newTestVersionStoreForHandoff(t)

	got, err := resolveVersionHandoffBinary(store, "v1.0.0")
	if err != nil {
		t.Fatalf("resolveVersionHandoffBinary: %v", err)
	}
	if got != "" {
		t.Fatalf("resolveVersionHandoffBinary with no committed Active = %q, want no handoff", got)
	}
}

func TestResolveVersionHandoffBinaryAlreadyOnActive(t *testing.T) {
	t.Parallel()
	store := newTestVersionStoreForHandoff(t)
	stageAndActivate(t, store, "v1.2.3")

	// Both an exact match and a "v"-prefix-insensitive match should be a no-op.
	for _, running := range []string{"v1.2.3", "1.2.3"} {
		got, err := resolveVersionHandoffBinary(store, running)
		if err != nil {
			t.Fatalf("resolveVersionHandoffBinary(%s): %v", running, err)
		}
		if got != "" {
			t.Fatalf("resolveVersionHandoffBinary(%s) = %q, want no handoff (already on Active)", running, got)
		}
	}
}

func TestResolveVersionHandoffBinaryHandsOffToStagedActive(t *testing.T) {
	t.Parallel()
	store := newTestVersionStoreForHandoff(t)
	wantPath := stageAndActivate(t, store, "v2.0.0")

	got, err := resolveVersionHandoffBinary(store, "v1.9.9")
	if err != nil {
		t.Fatalf("resolveVersionHandoffBinary: %v", err)
	}
	if got != wantPath {
		t.Fatalf("resolveVersionHandoffBinary = %q, want staged Active binary %q", got, wantPath)
	}
}

func TestResolveVersionHandoffBinarySkipsWhenStagedBinaryMissing(t *testing.T) {
	t.Parallel()
	store := newTestVersionStoreForHandoff(t)
	stagedPath := stageAndActivate(t, store, "v3.0.0")

	// Simulate a corrupted/partially-GC'd version store: Active is committed
	// but the binary itself is gone. Handoff must not point at a dead path.
	if err := os.Remove(stagedPath); err != nil {
		t.Fatalf("remove staged binary: %v", err)
	}

	got, err := resolveVersionHandoffBinary(store, "v1.0.0")
	if err != nil {
		t.Fatalf("resolveVersionHandoffBinary: %v", err)
	}
	if got != "" {
		t.Fatalf("resolveVersionHandoffBinary with missing staged binary = %q, want no handoff", got)
	}
}

func TestResolveVersionHandoffBinaryNilStore(t *testing.T) {
	t.Parallel()
	got, err := resolveVersionHandoffBinary(nil, "v1.0.0")
	if err != nil {
		t.Fatalf("resolveVersionHandoffBinary(nil store): %v", err)
	}
	if got != "" {
		t.Fatalf("resolveVersionHandoffBinary(nil store) = %q, want no handoff", got)
	}
}

func TestBuildSuperviseConfigDefaultProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var stdout, stderr bytes.Buffer
	cfg, err := buildSuperviseConfig("", "/usr/local/bin/multica", []string{"daemon", "start", "--foreground"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("buildSuperviseConfig: %v", err)
	}

	wantDir := filepath.Join(home, ".multica")
	if cfg.LockPath != filepath.Join(wantDir, "supervisor.lock") {
		t.Errorf("LockPath = %q, want under %q", cfg.LockPath, wantDir)
	}
	if cfg.WorkerPath != "/usr/local/bin/multica" {
		t.Errorf("WorkerPath = %q, want the resolved executable path", cfg.WorkerPath)
	}
	if got := strings.Join(cfg.WorkerArgs, " "); got != "daemon start --foreground" {
		t.Errorf("WorkerArgs = %q, want %q", got, "daemon start --foreground")
	}
	if cfg.Stdout != &stdout || cfg.Stderr != &stderr {
		t.Errorf("Stdout/Stderr not wired to the given writers")
	}
}

func TestBuildSuperviseConfigNamedProfileIsIsolatedFromDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	defaultCfg, err := buildSuperviseConfig("", "/bin/multica", nil, nil, nil)
	if err != nil {
		t.Fatalf("buildSuperviseConfig(default): %v", err)
	}
	stagingCfg, err := buildSuperviseConfig("staging", "/bin/multica", nil, nil, nil)
	if err != nil {
		t.Fatalf("buildSuperviseConfig(staging): %v", err)
	}

	if defaultCfg.LockPath == stagingCfg.LockPath {
		t.Fatalf("default and named-profile supervisor lock paths must differ, both = %q", defaultCfg.LockPath)
	}
	wantStagingDir := filepath.Join(home, ".multica", "profiles", "staging")
	if stagingCfg.LockPath != filepath.Join(wantStagingDir, "supervisor.lock") {
		t.Errorf("staging LockPath = %q, want under %q", stagingCfg.LockPath, wantStagingDir)
	}
}

const testGraceWindow = time.Hour

func TestVersionWatcherNoDivergenceNeverForces(t *testing.T) {
	t.Parallel()
	var state versionWatcherState
	now := time.Now()
	for i := 0; i < 5; i++ {
		if state.observe(now.Add(time.Duration(i)*testGraceWindow*2), "v1.0.0", "v1.0.0", testGraceWindow) {
			t.Fatalf("observe() forced restart with no divergence at step %d", i)
		}
	}
}

func TestVersionWatcherDivergenceDoesNotForceBeforeGraceWindow(t *testing.T) {
	t.Parallel()
	var state versionWatcherState
	now := time.Now()

	if state.observe(now, "v2.0.0", "v1.0.0", testGraceWindow) {
		t.Fatalf("observe() forced restart on first divergence, want it to start the grace clock instead")
	}
	if state.observe(now.Add(testGraceWindow/2), "v2.0.0", "v1.0.0", testGraceWindow) {
		t.Fatalf("observe() forced restart before the grace window elapsed")
	}
}

func TestVersionWatcherForcesRestartAfterGraceWindowElapses(t *testing.T) {
	t.Parallel()
	var state versionWatcherState
	now := time.Now()

	if state.observe(now, "v2.0.0", "v1.0.0", testGraceWindow) {
		t.Fatalf("observe() forced restart on first divergence")
	}
	if !state.observe(now.Add(testGraceWindow), "v2.0.0", "v1.0.0", testGraceWindow) {
		t.Fatalf("observe() did not force restart once the grace window elapsed")
	}
	// The daemon is still stuck: subsequent ticks keep signalling force
	// (RequestRestart on an already-restarting supervisor coalesces, so
	// repeated true results here are safe, not "restart storm").
	if !state.observe(now.Add(testGraceWindow*2), "v2.0.0", "v1.0.0", testGraceWindow) {
		t.Fatalf("observe() stopped signalling force restart while still diverged")
	}
}

func TestVersionWatcherResetsWhenVersionsConverge(t *testing.T) {
	t.Parallel()
	var state versionWatcherState
	now := time.Now()

	state.observe(now, "v2.0.0", "v1.0.0", testGraceWindow)
	// The graceful path succeeded on its own before the grace window elapsed.
	if state.observe(now.Add(testGraceWindow/4), "v2.0.0", "v2.0.0", testGraceWindow) {
		t.Fatalf("observe() forced restart right after versions converged")
	}
	// A later, unrelated divergence must start its own fresh grace window,
	// not inherit the earlier (already-resolved) one's clock.
	if state.observe(now.Add(testGraceWindow/4+time.Second), "v3.0.0", "v2.0.0", testGraceWindow) {
		t.Fatalf("observe() forced restart immediately on a fresh divergence")
	}
	if state.observe(now.Add(testGraceWindow/4+time.Second+testGraceWindow/2), "v3.0.0", "v2.0.0", testGraceWindow) {
		t.Fatalf("observe() forced restart before the new grace window elapsed")
	}
}

func TestVersionWatcherUnknownRunningVersionDoesNotResetOrAdvance(t *testing.T) {
	t.Parallel()
	var state versionWatcherState
	now := time.Now()

	state.observe(now, "v2.0.0", "v1.0.0", testGraceWindow)
	// A transient health-check failure (unreachable daemon mid-restart)
	// reports an unknown running version; it must not reset the divergence
	// clock (that would let a permanently-flaky health check indefinitely
	// postpone the force restart) nor tick it forward on its own.
	if state.observe(now.Add(testGraceWindow/2), "v2.0.0", "", testGraceWindow) {
		t.Fatalf("observe() forced restart on an unknown running version")
	}
	if !state.diverged {
		t.Fatalf("observe() cleared divergence state on an unknown running version")
	}
	if !state.observe(now.Add(testGraceWindow), "v2.0.0", "v1.0.0", testGraceWindow) {
		t.Fatalf("observe() lost track of the original divergence start time after a transient unknown reading")
	}
}

func TestVersionWatcherNoActiveVersionNeverForces(t *testing.T) {
	t.Parallel()
	var state versionWatcherState
	now := time.Now()
	if state.observe(now, "", "v1.0.0", testGraceWindow) {
		t.Fatalf("observe() forced restart with no committed Active version to compare against")
	}
	if state.observe(now.Add(testGraceWindow*2), "", "v1.0.0", testGraceWindow) {
		t.Fatalf("observe() forced restart with no committed Active version to compare against")
	}
}

func TestHandoffVersionsMatchIgnoresVPrefix(t *testing.T) {
	t.Parallel()
	cases := []struct{ a, b string }{
		{"v1.2.3", "1.2.3"},
		{"1.2.3", "v1.2.3"},
		{"v1.2.3", "v1.2.3"},
		{"1.2.3", "1.2.3"},
	}
	for _, c := range cases {
		if !handoffVersionsMatch(c.a, c.b) {
			t.Errorf("handoffVersionsMatch(%q, %q) = false, want true", c.a, c.b)
		}
	}
	if handoffVersionsMatch("v1.2.3", "v1.2.4") {
		t.Errorf("handoffVersionsMatch(v1.2.3, v1.2.4) = true, want false")
	}
	if handoffVersionsMatch("", "v1.2.3") {
		t.Errorf("handoffVersionsMatch(\"\", v1.2.3) = true, want false")
	}
}
