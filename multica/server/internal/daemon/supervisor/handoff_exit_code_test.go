package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestSupervisorHandoffExitRestartsImmediatelyWithFreshResolver is the
// positive case for the long-term fix Frank required (PR #1584 review):
// version handoff modeled as a first-class exit reason, not a wrapper
// process that blocks and accumulates. A worker that exits with
// Config.HandoffExitCode restarts immediately (no backoff), and
// Config.ResolveWorkerPath is consulted fresh for every generation — proven
// here by counting resolver calls independently of the worker's own exit
// behavior.
func TestSupervisorHandoffExitRestartsImmediatelyWithFreshResolver(t *testing.T) {
	dir := t.TempDir()
	countPath := filepath.Join(dir, "starts")
	const handoffCode = 77
	const handoffGenerations = 2 // generations 1-2 hand off; generation 3 exits clean

	env := append(os.Environ(),
		workerHelperEnv+"=1",
		workerHelperActionEnv+"=handoff-then-clean",
		workerHelperCountEnv+"="+countPath,
		workerHelperCrashEnv+"="+strconv.Itoa(handoffGenerations),
		workerHelperExitCodeEnv+"="+strconv.Itoa(handoffCode),
	)

	var resolveCalls int32
	resolve := func() (string, []string, error) {
		atomic.AddInt32(&resolveCalls, 1)
		return os.Args[0], []string{"-test.run=^TestSupervisorWorkerProcess$"}, nil
	}

	var sleepMu sync.Mutex
	var sleeps []time.Duration
	sleep := func(_ context.Context, d time.Duration) error {
		sleepMu.Lock()
		sleeps = append(sleeps, d)
		sleepMu.Unlock()
		return nil
	}

	s, err := New(Config{
		LockPath:          filepath.Join(dir, "supervisor.lock"),
		ResolveWorkerPath: resolve,
		WorkerEnv:         env,
		HandoffExitCode:   handoffCode,
		InitialBackoff:    10 * time.Millisecond,
		MaxBackoff:        25 * time.Millisecond,
		StableRunWindow:   time.Hour,
		GracefulStopWait:  50 * time.Millisecond,
		Sleep:             sleep,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := readWorkerCount(t, countPath); got != handoffGenerations+1 {
		t.Fatalf("worker starts = %d, want %d", got, handoffGenerations+1)
	}
	if got := atomic.LoadInt32(&resolveCalls); got != int32(handoffGenerations+1) {
		t.Fatalf("resolver calls = %d, want %d (once per generation, including the first)", got, handoffGenerations+1)
	}
	sleepMu.Lock()
	gotSleeps := append([]time.Duration(nil), sleeps...)
	sleepMu.Unlock()
	if len(gotSleeps) != 0 {
		t.Fatalf("backoff sleeps during handoff restarts = %v, want none — a deliberate handoff must not be throttled like a crash", gotSleeps)
	}

	snapshot := s.Snapshot()
	if snapshot.State != StateStopped || snapshot.LastExit != ExitClean {
		t.Fatalf("final snapshot = %+v, want a clean stop after the handoff generations finish", snapshot)
	}
	if snapshot.RestartCount != handoffGenerations {
		t.Fatalf("restart count = %d, want %d (one per handoff exit)", snapshot.RestartCount, handoffGenerations)
	}
	if snapshot.Generation != handoffGenerations+1 {
		t.Fatalf("generation = %d, want %d", snapshot.Generation, handoffGenerations+1)
	}
}

// TestSupervisorNonHandoffExitCodeStillTreatedAsCrash is the negative
// control: a nonzero exit that does NOT match Config.HandoffExitCode must
// still go through the ordinary crash/backoff path. This is what proves the
// classification above is discriminating on the actual exit code, not
// treating every nonzero exit as a handoff.
func TestSupervisorNonHandoffExitCodeStillTreatedAsCrash(t *testing.T) {
	dir := t.TempDir()
	countPath := filepath.Join(dir, "starts")
	const handoffCode = 77
	const otherNonZeroCode = 5 // deliberately does not match handoffCode

	env := append(os.Environ(),
		workerHelperEnv+"=1",
		workerHelperActionEnv+"=handoff-then-clean",
		workerHelperCountEnv+"="+countPath,
		workerHelperCrashEnv+"=1",
		workerHelperExitCodeEnv+"="+strconv.Itoa(otherNonZeroCode),
	)

	var sleepMu sync.Mutex
	var sleeps []time.Duration
	sleep := func(_ context.Context, d time.Duration) error {
		sleepMu.Lock()
		sleeps = append(sleeps, d)
		sleepMu.Unlock()
		return nil
	}

	s, err := New(Config{
		LockPath:         filepath.Join(dir, "supervisor.lock"),
		WorkerPath:       os.Args[0],
		WorkerArgs:       []string{"-test.run=^TestSupervisorWorkerProcess$"},
		WorkerEnv:        env,
		HandoffExitCode:  handoffCode,
		InitialBackoff:   10 * time.Millisecond,
		MaxBackoff:       25 * time.Millisecond,
		StableRunWindow:  time.Hour,
		GracefulStopWait: 50 * time.Millisecond,
		Sleep:            sleep,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	sleepMu.Lock()
	gotSleeps := append([]time.Duration(nil), sleeps...)
	sleepMu.Unlock()
	if len(gotSleeps) != 1 || gotSleeps[0] != 10*time.Millisecond {
		t.Fatalf("backoff sleeps = %v, want exactly one 10ms backoff — a non-matching nonzero exit must still be classified as a crash", gotSleeps)
	}

	snapshot := s.Snapshot()
	if snapshot.LastExit != ExitClean {
		t.Fatalf("final snapshot = %+v, want a clean stop after the crash recovers", snapshot)
	}
	if snapshot.RestartCount != 1 || snapshot.Generation != 2 {
		t.Fatalf("snapshot = %+v, want 1 restart across 2 generations", snapshot)
	}
}

// TestSupervisorResolveWorkerPathErrorEntersBackoff proves a resolver
// failure is handled the same way a Start() failure already is: backoff and
// retry, not a terminal Run() error.
func TestSupervisorResolveWorkerPathErrorEntersBackoff(t *testing.T) {
	dir := t.TempDir()
	resolveErr := errors.New("boom: version store unavailable")

	var resolveCalls int32
	resolve := func() (string, []string, error) {
		if atomic.AddInt32(&resolveCalls, 1) == 1 {
			return "", nil, resolveErr
		}
		return os.Args[0], []string{"-test.run=^TestSupervisorWorkerProcess$"}, nil
	}

	env := append(os.Environ(),
		workerHelperEnv+"=1",
		workerHelperActionEnv+"=clean",
		workerHelperCountEnv+"="+filepath.Join(dir, "starts"),
		workerHelperCrashEnv+"=0",
	)

	var sleepMu sync.Mutex
	var sleeps []time.Duration
	sleep := func(_ context.Context, d time.Duration) error {
		sleepMu.Lock()
		sleeps = append(sleeps, d)
		sleepMu.Unlock()
		return nil
	}

	s, err := New(Config{
		LockPath:          filepath.Join(dir, "supervisor.lock"),
		ResolveWorkerPath: resolve,
		WorkerEnv:         env,
		InitialBackoff:    10 * time.Millisecond,
		MaxBackoff:        25 * time.Millisecond,
		StableRunWindow:   time.Hour,
		GracefulStopWait:  50 * time.Millisecond,
		Sleep:             sleep,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	sleepMu.Lock()
	gotSleeps := append([]time.Duration(nil), sleeps...)
	sleepMu.Unlock()
	if len(gotSleeps) != 1 {
		t.Fatalf("backoff sleeps = %v, want exactly one for the resolver error", gotSleeps)
	}
	if got := atomic.LoadInt32(&resolveCalls); got != 2 {
		t.Fatalf("resolver calls = %d, want 2 (failed once, retried once)", got)
	}
	snapshot := s.Snapshot()
	if snapshot.LastExit != ExitClean {
		t.Fatalf("final snapshot = %+v, want clean stop after recovering from the resolver error", snapshot)
	}
}

// TestNewRequiresWorkerPathOrResolveWorkerPath locks in New()'s relaxed
// validation: either a static WorkerPath or a ResolveWorkerPath callback is
// enough; neither is an error.
func TestNewRequiresWorkerPathOrResolveWorkerPath(t *testing.T) {
	_, err := New(Config{LockPath: filepath.Join(t.TempDir(), "supervisor.lock")})
	if err == nil {
		t.Fatal("New with neither WorkerPath nor ResolveWorkerPath = nil error, want an error")
	}

	_, err = New(Config{
		LockPath:          filepath.Join(t.TempDir(), "supervisor.lock"),
		ResolveWorkerPath: func() (string, []string, error) { return "/bin/true", nil, nil },
	})
	if err != nil {
		t.Fatalf("New with only ResolveWorkerPath set = %v, want nil", err)
	}
}
