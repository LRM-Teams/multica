package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	workerHelperEnv       = "MULTICA_SUPERVISOR_TEST_WORKER"
	workerHelperActionEnv = "MULTICA_SUPERVISOR_TEST_ACTION"
	workerHelperCountEnv  = "MULTICA_SUPERVISOR_TEST_COUNT_PATH"
	workerHelperCrashEnv  = "MULTICA_SUPERVISOR_TEST_CRASH_COUNT"
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
	if _, err := os.Stat(filepath.Join(dir, "second-starts")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("second worker unexpectedly started: %v", err)
	}

	cancel()
	if err := waitForRun(t, errCh); err != nil {
		t.Fatalf("first Run after cancellation: %v", err)
	}
}

func TestSupervisorRequestRestartRequiresActiveRun(t *testing.T) {
	s := newTestSupervisor(t, "clean", filepath.Join(t.TempDir(), "starts"), 0, nil)
	if err := s.RequestRestart(); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("RequestRestart error = %v, want ErrNotRunning", err)
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
