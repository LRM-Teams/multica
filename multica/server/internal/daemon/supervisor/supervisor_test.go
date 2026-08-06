package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	workerHelperEnv         = "MULTICA_SUPERVISOR_TEST_WORKER"
	workerHelperActionEnv   = "MULTICA_SUPERVISOR_TEST_ACTION"
	workerHelperCountEnv    = "MULTICA_SUPERVISOR_TEST_COUNT_PATH"
	workerHelperCrashEnv    = "MULTICA_SUPERVISOR_TEST_CRASH_COUNT"
	workerHelperSleepEnv    = "MULTICA_SUPERVISOR_TEST_SLEEP_SEQUENCE_MS"
	workerHelperTermEnv     = "MULTICA_SUPERVISOR_TEST_TERM_DELAY_MS"
	workerHelperReadyEnv    = "MULTICA_SUPERVISOR_TEST_READY_PATH"
	workerHelperClaimEnv    = "MULTICA_SUPERVISOR_TEST_CLAIM_PATH"
	workerHelperExitCodeEnv = "MULTICA_SUPERVISOR_TEST_EXIT_CODE"
)

func TestSupervisorWorkerProcess(t *testing.T) {
	if os.Getenv(workerHelperEnv) != "1" {
		return
	}

	countPath := os.Getenv(workerHelperCountEnv)
	count, err := incrementWorkerCount(countPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(97)
	}
	if err := sleepWorkerAttempt(count, os.Getenv(workerHelperSleepEnv)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(96)
	}

	switch os.Getenv(workerHelperActionEnv) {
	case "clean":
		os.Exit(0)
	case "crash":
		crashCount, err := strconv.Atoi(os.Getenv(workerHelperCrashEnv))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(98)
		}
		if count <= crashCount {
			os.Exit(23)
		}
		os.Exit(0)
	case "block":
		for {
			time.Sleep(time.Hour)
		}
	case "claim-then-block":
		// Simulates a daemon that has claimed a task (written proof of the
		// claim somewhere durable — a delivery lease, in the real daemon)
		// and is now mid-execution. It never writes anything else and never
		// exits cleanly, matching a real in-flight task: no "done" signal is
		// sent until work completes, and this worker is killed before that
		// ever happens.
		if err := os.WriteFile(os.Getenv(workerHelperClaimEnv), []byte("claimed"), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(93)
		}
		for {
			time.Sleep(time.Hour)
		}
	case "handoff-then-clean":
		// Simulates a worker that deliberately hands off to a new version:
		// the first workerHelperCrashEnv generations exit with the
		// configured handoff code (workerHelperExitCodeEnv), then it exits
		// cleanly so a test can observe a bounded number of handoff
		// restarts before the run ends.
		handoffGenerations, err := strconv.Atoi(os.Getenv(workerHelperCrashEnv))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(98)
		}
		exitCode, err := strconv.Atoi(os.Getenv(workerHelperExitCodeEnv))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(98)
		}
		if count <= handoffGenerations {
			os.Exit(exitCode)
		}
		os.Exit(0)
	case "term-delay":
		delay, err := time.ParseDuration(os.Getenv(workerHelperTermEnv) + "ms")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(95)
		}
		signalCh := make(chan os.Signal, 1)
		signal.Notify(signalCh)
		if err := os.WriteFile(os.Getenv(workerHelperReadyEnv), []byte("ready"), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(94)
		}
		<-signalCh
		time.Sleep(delay)
		os.Exit(0)
	default:
		fmt.Fprintln(os.Stderr, "unknown test worker action")
		os.Exit(99)
	}
}

func TestSupervisorCleanExitDoesNotRestart(t *testing.T) {
	countPath := filepath.Join(t.TempDir(), "starts")
	s := newTestSupervisor(t, "clean", countPath, 0, nil)

	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := readWorkerCount(t, countPath); got != 1 {
		t.Fatalf("worker starts = %d, want 1", got)
	}
	snapshot := s.Snapshot()
	if snapshot.Generation != 1 || snapshot.RestartCount != 0 {
		t.Fatalf("snapshot = %+v, want generation 1 with no restarts", snapshot)
	}
	if snapshot.LastExit != ExitClean || snapshot.State != StateStopped {
		t.Fatalf("snapshot = %+v, want clean stopped state", snapshot)
	}
	if err := s.RequestRestart(); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("RequestRestart after clean exit = %v, want ErrNotRunning", err)
	}
}

func TestSupervisorCrashRestartsWithBoundedExponentialBackoff(t *testing.T) {
	countPath := filepath.Join(t.TempDir(), "starts")
	var (
		sleepMu sync.Mutex
		sleeps  []time.Duration
	)
	sleep := func(ctx context.Context, delay time.Duration) error {
		sleepMu.Lock()
		sleeps = append(sleeps, delay)
		sleepMu.Unlock()
		return nil
	}
	s := newTestSupervisor(t, "crash", countPath, 4, sleep)

	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := readWorkerCount(t, countPath); got != 5 {
		t.Fatalf("worker starts = %d, want 5", got)
	}
	sleepMu.Lock()
	gotSleeps := append([]time.Duration(nil), sleeps...)
	sleepMu.Unlock()
	wantSleeps := []time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		25 * time.Millisecond,
		25 * time.Millisecond,
	}
	if fmt.Sprint(gotSleeps) != fmt.Sprint(wantSleeps) {
		t.Fatalf("backoff delays = %v, want %v", gotSleeps, wantSleeps)
	}
	snapshot := s.Snapshot()
	if snapshot.Generation != 5 || snapshot.RestartCount != 4 {
		t.Fatalf("snapshot = %+v, want generation 5 and 4 restarts", snapshot)
	}
	if snapshot.LastExit != ExitClean {
		t.Fatalf("last exit = %q, want %q", snapshot.LastExit, ExitClean)
	}
}

func TestSupervisorStableRunResetsCrashBackoff(t *testing.T) {
	countPath := filepath.Join(t.TempDir(), "starts")
	var (
		sleepMu sync.Mutex
		sleeps  []time.Duration
	)
	sleep := func(_ context.Context, delay time.Duration) error {
		sleepMu.Lock()
		sleeps = append(sleeps, delay)
		sleepMu.Unlock()
		return nil
	}
	s := newTestSupervisor(t, "crash", countPath, 2, sleep)
	s.config.StableRunWindow = 40 * time.Millisecond
	s.config.WorkerEnv = append(s.config.WorkerEnv, workerHelperSleepEnv+"=0,80,0")

	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := readWorkerCount(t, countPath); got != 3 {
		t.Fatalf("worker starts = %d, want 3", got)
	}
	sleepMu.Lock()
	gotSleeps := append([]time.Duration(nil), sleeps...)
	sleepMu.Unlock()
	wantSleeps := []time.Duration{10 * time.Millisecond, 10 * time.Millisecond}
	if fmt.Sprint(gotSleeps) != fmt.Sprint(wantSleeps) {
		t.Fatalf("backoff delays = %v, want reset %v", gotSleeps, wantSleeps)
	}
}

func TestSupervisorRealStartFailureEntersBackoff(t *testing.T) {
	dir := t.TempDir()
	sleepEntered := make(chan struct{}, 1)
	sleep := func(ctx context.Context, _ time.Duration) error {
		sleepEntered <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}
	s, err := New(Config{
		LockPath:         filepath.Join(dir, "supervisor.lock"),
		WorkerPath:       filepath.Join(dir, "missing-worker"),
		InitialBackoff:   17 * time.Millisecond,
		MaxBackoff:       34 * time.Millisecond,
		StableRunWindow:  time.Hour,
		GracefulStopWait: 50 * time.Millisecond,
		Sleep:            sleep,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Run(ctx)
	}()

	select {
	case <-sleepEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for start-failure backoff")
	}
	snapshot := s.Snapshot()
	if snapshot.State != StateBackingOff ||
		snapshot.LastExit != ExitStartFailed ||
		snapshot.Generation != 0 ||
		snapshot.NextBackoff != 17*time.Millisecond {
		t.Fatalf("start-failure snapshot = %+v", snapshot)
	}
	cancel()
	if err := waitForRun(t, errCh); err != nil {
		t.Fatalf("Run after cancellation: %v", err)
	}
}

func TestSupervisorCancellationStopsWorkerWithoutRestart(t *testing.T) {
	countPath := filepath.Join(t.TempDir(), "starts")
	s := newTestSupervisor(t, "block", countPath, 0, nil)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Run(ctx)
	}()

	waitForWorkerCount(t, countPath, 1)
	cancel()
	if err := waitForRun(t, errCh); err != nil {
		t.Fatalf("Run after cancellation: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if got := readWorkerCount(t, countPath); got != 1 {
		t.Fatalf("worker starts after cancellation = %d, want 1", got)
	}
	snapshot := s.Snapshot()
	if snapshot.LastExit != ExitStopped || snapshot.RestartCount != 0 {
		t.Fatalf("snapshot = %+v, want one stopped generation", snapshot)
	}
}

func TestSupervisorCancellationRejectsRestartDuringWorkerStop(t *testing.T) {
	countPath := filepath.Join(t.TempDir(), "starts")
	readyPath := filepath.Join(t.TempDir(), "ready")
	s := newTestSupervisor(t, "term-delay", countPath, 0, nil)
	s.config.WorkerEnv = append(
		s.config.WorkerEnv,
		workerHelperTermEnv+"=150",
		workerHelperReadyEnv+"="+readyPath,
	)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Run(ctx)
	}()

	waitForWorkerCount(t, countPath, 1)
	waitForWorkerReady(t, readyPath)
	cancel()
	waitForSupervisorState(t, s, StateStopping)
	if err := s.RequestRestart(); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("RequestRestart during terminal worker stop = %v, want ErrNotRunning", err)
	}
	if err := waitForRun(t, errCh); err != nil {
		t.Fatalf("Run after cancellation: %v", err)
	}
}

func TestSupervisorCancellationDuringBackoffDoesNotRestart(t *testing.T) {
	countPath := filepath.Join(t.TempDir(), "starts")
	sleepEntered := make(chan struct{}, 1)
	sleep := func(ctx context.Context, _ time.Duration) error {
		sleepEntered <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}
	s := newTestSupervisor(t, "crash", countPath, 1, sleep)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Run(ctx)
	}()

	select {
	case <-sleepEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for supervisor backoff")
	}
	cancel()
	if err := waitForRun(t, errCh); err != nil {
		t.Fatalf("Run after backoff cancellation: %v", err)
	}
	if got := readWorkerCount(t, countPath); got != 1 {
		t.Fatalf("worker starts after backoff cancellation = %d, want 1", got)
	}
	snapshot := s.Snapshot()
	if snapshot.Generation != 1 || snapshot.RestartCount != 0 {
		t.Fatalf("snapshot = %+v, want one generation and no restart", snapshot)
	}
}

func TestSupervisorCancellationRejectsRestartDuringBackoffCleanup(t *testing.T) {
	countPath := filepath.Join(t.TempDir(), "starts")
	sleepEntered := make(chan struct{}, 1)
	sleepCanceled := make(chan struct{}, 1)
	releaseSleep := make(chan struct{})
	sleep := func(ctx context.Context, _ time.Duration) error {
		sleepEntered <- struct{}{}
		<-ctx.Done()
		sleepCanceled <- struct{}{}
		<-releaseSleep
		return ctx.Err()
	}
	s := newTestSupervisor(t, "crash", countPath, 1, sleep)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Run(ctx)
	}()

	select {
	case <-sleepEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for supervisor backoff")
	}
	cancel()
	select {
	case <-sleepCanceled:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for canceled backoff cleanup")
	}
	waitForSupervisorState(t, s, StateStopping)
	if err := s.RequestRestart(); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("RequestRestart during terminal backoff cleanup = %v, want ErrNotRunning", err)
	}
	close(releaseSleep)
	if err := waitForRun(t, errCh); err != nil {
		t.Fatalf("Run after backoff cancellation: %v", err)
	}
}

func TestSupervisorBackoffSleepFailureCommitsTerminalState(t *testing.T) {
	countPath := filepath.Join(t.TempDir(), "starts")
	sleepErr := errors.New("sleep failed")
	sleep := func(context.Context, time.Duration) error {
		return sleepErr
	}
	s := newTestSupervisor(t, "crash", countPath, 1, sleep)

	err := s.Run(context.Background())
	if !errors.Is(err, sleepErr) {
		t.Fatalf("Run error = %v, want %v", err, sleepErr)
	}
	snapshot := s.Snapshot()
	if snapshot.State != StateStopped ||
		snapshot.LastExit != ExitCrashed ||
		snapshot.WorkerPID != 0 {
		t.Fatalf("snapshot after backoff failure = %+v, want terminal crashed state", snapshot)
	}
	if err := s.RequestRestart(); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("RequestRestart after backoff failure = %v, want ErrNotRunning", err)
	}
}

func TestSupervisorRestartRequestInterruptsBackoffWithoutSpuriousGeneration(t *testing.T) {
	countPath := filepath.Join(t.TempDir(), "starts")
	sleepEntered := make(chan struct{}, 1)
	sleep := func(ctx context.Context, _ time.Duration) error {
		sleepEntered <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}
	s := newTestSupervisor(t, "crash", countPath, 1, sleep)
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Run(context.Background())
	}()

	select {
	case <-sleepEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for supervisor backoff")
	}
	if err := s.RequestRestart(); err != nil {
		t.Fatalf("RequestRestart during backoff: %v", err)
	}
	if err := waitForRun(t, errCh); err != nil {
		t.Fatalf("Run after backoff restart request: %v", err)
	}
	if got := readWorkerCount(t, countPath); got != 2 {
		t.Fatalf("worker starts after backoff restart request = %d, want 2", got)
	}
	snapshot := s.Snapshot()
	if snapshot.Generation != 2 || snapshot.RestartCount != 1 {
		t.Fatalf("snapshot = %+v, want exactly two generations and one restart", snapshot)
	}
}

func TestSupervisorRestartRequestAdvancesGeneration(t *testing.T) {
	countPath := filepath.Join(t.TempDir(), "starts")
	s := newTestSupervisor(t, "block", countPath, 0, nil)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Run(ctx)
	}()

	waitForWorkerCount(t, countPath, 1)
	if err := s.RequestRestart(); err != nil {
		t.Fatalf("RequestRestart: %v", err)
	}
	waitForWorkerCount(t, countPath, 2)
	cancel()
	if err := waitForRun(t, errCh); err != nil {
		t.Fatalf("Run after restart and cancellation: %v", err)
	}

	snapshot := s.Snapshot()
	if snapshot.Generation != 2 || snapshot.RestartCount != 1 {
		t.Fatalf("snapshot = %+v, want generation 2 and 1 restart", snapshot)
	}
	if snapshot.LastExit != ExitStopped {
		t.Fatalf("last exit = %q, want %q", snapshot.LastExit, ExitStopped)
	}
}

func TestSupervisorDuplicateRestartRequestsCoalesceUntilReplacementStarts(t *testing.T) {
	countPath := filepath.Join(t.TempDir(), "starts")
	readyPath := filepath.Join(t.TempDir(), "ready")
	s := newTestSupervisor(t, "term-delay", countPath, 0, nil)
	s.config.WorkerEnv = append(
		s.config.WorkerEnv,
		workerHelperTermEnv+"=150",
		workerHelperReadyEnv+"="+readyPath,
	)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Run(ctx)
	}()

	waitForWorkerCount(t, countPath, 1)
	waitForWorkerReady(t, readyPath)
	if err := s.RequestRestart(); err != nil {
		t.Fatalf("first RequestRestart: %v", err)
	}
	waitForSupervisorState(t, s, StateStopping)
	for i := 0; i < 8; i++ {
		if err := s.RequestRestart(); err != nil {
			t.Fatalf("duplicate RequestRestart %d: %v", i+1, err)
		}
	}
	waitForWorkerCount(t, countPath, 2)
	cancel()
	if err := waitForRun(t, errCh); err != nil {
		t.Fatalf("Run after replacement and cancellation: %v", err)
	}
	snapshot := s.Snapshot()
	if snapshot.Generation != 2 || snapshot.RestartCount != 1 {
		t.Fatalf("snapshot = %+v, want one replacement for all requests", snapshot)
	}
}

func TestSupervisorDuplicateRestartRequestsCoalesceAcrossBackoffWake(t *testing.T) {
	countPath := filepath.Join(t.TempDir(), "starts")
	sleepEntered := make(chan struct{}, 1)
	sleepCanceled := make(chan struct{}, 1)
	releaseSleep := make(chan struct{})
	sleep := func(ctx context.Context, _ time.Duration) error {
		sleepEntered <- struct{}{}
		<-ctx.Done()
		sleepCanceled <- struct{}{}
		<-releaseSleep
		return ctx.Err()
	}
	s := newTestSupervisor(t, "crash", countPath, 1, sleep)
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Run(context.Background())
	}()

	select {
	case <-sleepEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for supervisor backoff")
	}
	if err := s.RequestRestart(); err != nil {
		t.Fatalf("first RequestRestart: %v", err)
	}
	select {
	case <-sleepCanceled:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for backoff cancellation")
	}
	for i := 0; i < 8; i++ {
		if err := s.RequestRestart(); err != nil {
			t.Fatalf("duplicate RequestRestart %d: %v", i+1, err)
		}
	}
	close(releaseSleep)
	if err := waitForRun(t, errCh); err != nil {
		t.Fatalf("Run after backoff replacement: %v", err)
	}
	snapshot := s.Snapshot()
	if snapshot.Generation != 2 || snapshot.RestartCount != 1 {
		t.Fatalf("snapshot = %+v, want one replacement for all requests", snapshot)
	}
}

func TestSupervisorSecondInstanceFailsClosed(t *testing.T) {
	dir := t.TempDir()
	countPath := filepath.Join(dir, "starts")
	lockPath := filepath.Join(dir, "profile.lock")
	first := newTestSupervisorWithLock(t, "block", countPath, 0, nil, lockPath)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- first.Run(ctx)
	}()
	waitForWorkerCount(t, countPath, 1)

	second := newTestSupervisorWithLock(
		t,
		"clean",
		filepath.Join(dir, "second-starts"),
		0,
		nil,
		lockPath,
	)
	if err := second.Run(context.Background()); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Run error = %v, want ErrAlreadyRunning", err)
	}
	if err := second.RequestRestart(); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("second RequestRestart after failed lock = %v, want ErrNotRunning", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "second-starts")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("second worker unexpectedly started: %v", err)
	}

	cancel()
	if err := waitForRun(t, errCh); err != nil {
		t.Fatalf("first Run after cancellation: %v", err)
	}
	if err := first.RequestRestart(); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("first RequestRestart after terminal exit = %v, want ErrNotRunning", err)
	}
}

func TestSupervisorRequestRestartRequiresActiveRun(t *testing.T) {
	s := newTestSupervisor(t, "clean", filepath.Join(t.TempDir(), "starts"), 0, nil)
	if err := s.RequestRestart(); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("RequestRestart error = %v, want ErrNotRunning", err)
	}
	if err := s.beginRun(); err != nil {
		t.Fatalf("beginRun: %v", err)
	}
	if err := s.RequestRestart(); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("RequestRestart before profile lock = %v, want ErrNotRunning", err)
	}
	s.finishRun()
}

func TestSupervisorStaleRestartSignalDoesNotAffectNextRun(t *testing.T) {
	countPath := filepath.Join(t.TempDir(), "starts")
	s := newTestSupervisor(t, "clean", countPath, 0, nil)

	if err := s.beginRun(); err != nil {
		t.Fatalf("beginRun: %v", err)
	}
	s.beginProfileOwnership()
	if err := s.RequestRestart(); err != nil {
		t.Fatalf("RequestRestart: %v", err)
	}
	s.transitionStopped(ExitStopped)
	s.endProfileOwnership()
	s.finishRun()

	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("next Run: %v", err)
	}
	snapshot := s.Snapshot()
	if snapshot.Generation != 1 || snapshot.RestartCount != 0 || snapshot.LastExit != ExitClean {
		t.Fatalf("snapshot = %+v, want clean next run without stale restart", snapshot)
	}
}

func newTestSupervisor(
	t *testing.T,
	action string,
	countPath string,
	crashCount int,
	sleep SleepFunc,
) *Supervisor {
	t.Helper()
	return newTestSupervisorWithLock(
		t,
		action,
		countPath,
		crashCount,
		sleep,
		filepath.Join(filepath.Dir(countPath), "supervisor.lock"),
	)
}

func newTestSupervisorWithLock(
	t *testing.T,
	action string,
	countPath string,
	crashCount int,
	sleep SleepFunc,
	lockPath string,
) *Supervisor {
	t.Helper()
	env := append(os.Environ(),
		workerHelperEnv+"=1",
		workerHelperActionEnv+"="+action,
		workerHelperCountEnv+"="+countPath,
		workerHelperCrashEnv+"="+strconv.Itoa(crashCount),
	)
	s, err := New(Config{
		LockPath:         lockPath,
		WorkerPath:       os.Args[0],
		WorkerArgs:       []string{"-test.run=^TestSupervisorWorkerProcess$"},
		WorkerEnv:        env,
		InitialBackoff:   10 * time.Millisecond,
		MaxBackoff:       25 * time.Millisecond,
		StableRunWindow:  time.Hour,
		GracefulStopWait: 50 * time.Millisecond,
		Sleep:            sleep,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func incrementWorkerCount(path string) (int, error) {
	current := 0
	data, err := os.ReadFile(path)
	if err == nil {
		current, err = strconv.Atoi(strings.TrimSpace(string(data)))
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	current++
	if err := os.WriteFile(path, []byte(strconv.Itoa(current)), 0o600); err != nil {
		return 0, err
	}
	return current, nil
}

func sleepWorkerAttempt(count int, raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	if count < 1 || count > len(parts) {
		return nil
	}
	delayMillis, err := strconv.Atoi(strings.TrimSpace(parts[count-1]))
	if err != nil {
		return fmt.Errorf("parse worker sleep attempt %d: %w", count, err)
	}
	if delayMillis > 0 {
		time.Sleep(time.Duration(delayMillis) * time.Millisecond)
	}
	return nil
}

func readWorkerCount(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read worker count: %v", err)
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse worker count %q: %v", data, err)
	}
	return count
}

func waitForWorkerCount(t *testing.T, path string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			count, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr == nil && count >= want {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if data, err := os.ReadFile(path); err == nil {
		t.Fatalf("timed out waiting for worker count %d; got %q", want, data)
	}
	t.Fatalf("timed out waiting for worker count %d", want)
}

func waitForSupervisorState(t *testing.T, supervisor *Supervisor, want State) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if supervisor.Snapshot().State == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for supervisor state %q; got %+v", want, supervisor.Snapshot())
}

func waitForWorkerReady(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for worker readiness file %s", path)
}

func waitForRun(t *testing.T, errCh <-chan error) error {
	t.Helper()
	select {
	case err := <-errCh:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for supervisor")
		return nil
	}
}
