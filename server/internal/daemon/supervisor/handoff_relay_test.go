package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestSupervisorSurvivesAttachedHandoffRelay locks in the fix for the bug
// Barry found reviewing task #815's supervisor wiring: a worker that hands
// off to a new version by spawning a detached sibling and exiting(0)
// immediately looks, to the supervisor, exactly like a real, permanent,
// user-intended stop (see TestSupervisorCleanExitDoesNotRestart) — so the
// very first version handoff on a busy machine (the whole reason this
// supervisor exists) kills the supervisor's Run() loop for good, leaving
// the newly-handed-off daemon as an unmanaged bare process.
//
// The fix: a worker that needs to hand off spawns the real work as a plain
// (non-detached) child sharing this process's process group, blocks on it,
// and only exits once that child does — propagating its real outcome. The
// supervisor's tracked worker (this process) never reports "done" while
// the actual daemon work is still alive underneath it.
func TestSupervisorSurvivesAttachedHandoffRelay(t *testing.T) {
	dir := t.TempDir()
	relayMarkPath := filepath.Join(dir, "relay-grandchild-pid")

	env := append(os.Environ(),
		workerHelperEnv+"=1",
		workerHelperActionEnv+"=handoff-relay-wait",
		workerHelperCountEnv+"="+filepath.Join(dir, "starts"),
		workerHelperCrashEnv+"=0",
		workerHelperRelayEnv+"="+relayMarkPath,
	)
	s, err := New(Config{
		LockPath:         filepath.Join(dir, "supervisor.lock"),
		WorkerPath:       os.Args[0],
		WorkerArgs:       []string{"-test.run=^TestSupervisorWorkerProcess$"},
		WorkerEnv:        env,
		InitialBackoff:   10 * time.Millisecond,
		MaxBackoff:       25 * time.Millisecond,
		StableRunWindow:  time.Hour,
		GracefulStopWait: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- s.Run(ctx) }()

	waitForFile(t, relayMarkPath)
	// Give the relay a moment past spawning its grandchild — the bug this
	// guards against manifests as the wrapper exiting right after this
	// point, not during it, so the real assertion is below.
	time.Sleep(50 * time.Millisecond)

	if snap := s.Snapshot(); snap.State == StateStopped {
		t.Fatalf("supervisor already considers itself stopped mid-handoff: %+v", snap)
	}

	// The property under test: while the relay is still blocked waiting on
	// its grandchild (the real, still-running daemon work), the supervisor
	// must still consider itself actively governing a worker.
	if err := s.RequestRestart(); err != nil {
		t.Fatalf("RequestRestart mid-handoff = %v, want nil — supervisor incorrectly believes the worker permanently stopped", err)
	}

	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation — relay or grandchild leaked")
	}
}

// TestSupervisorDetachedHandoffLooksLikePermanentStop is the negative
// control for the test above: it reproduces the ORIGINAL buggy behavior
// (spawn the real work as a sibling, exit immediately without waiting) and
// confirms the supervisor really does mistake it for a permanent stop.
// This is what proves the fix above is exercising a real distinction, not a
// vacuously-passing assertion.
func TestSupervisorDetachedHandoffLooksLikePermanentStop(t *testing.T) {
	dir := t.TempDir()
	relayMarkPath := filepath.Join(dir, "relay-grandchild-pid")

	env := append(os.Environ(),
		workerHelperEnv+"=1",
		workerHelperActionEnv+"=handoff-relay-detach-buggy",
		workerHelperCountEnv+"="+filepath.Join(dir, "starts"),
		workerHelperCrashEnv+"=0",
		workerHelperRelayEnv+"="+relayMarkPath,
	)
	s, err := New(Config{
		LockPath:         filepath.Join(dir, "supervisor.lock"),
		WorkerPath:       os.Args[0],
		WorkerArgs:       []string{"-test.run=^TestSupervisorWorkerProcess$"},
		WorkerEnv:        env,
		InitialBackoff:   10 * time.Millisecond,
		MaxBackoff:       25 * time.Millisecond,
		StableRunWindow:  time.Hour,
		GracefulStopWait: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	snap := s.Snapshot()
	if snap.State != StateStopped || snap.LastExit != ExitClean {
		t.Fatalf("snapshot after detached handoff = %+v, want clean stopped state (confirming the bug reproduces)", snap)
	}
	if err := s.RequestRestart(); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("RequestRestart after detached handoff = %v, want ErrNotRunning (this is the bug Barry found: supervisor thinks it permanently stopped)", err)
	}

	// Best-effort cleanup of the orphaned grandchild the buggy path leaks —
	// exactly the "unmanaged bare process" Barry described.
	if pidBytes, err := os.ReadFile(relayMarkPath); err == nil {
		if pid, perr := strconv.Atoi(strings.TrimSpace(string(pidBytes))); perr == nil {
			if p, ferr := os.FindProcess(pid); ferr == nil {
				_ = p.Kill()
			}
		}
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("file %s never appeared within timeout", path)
}
