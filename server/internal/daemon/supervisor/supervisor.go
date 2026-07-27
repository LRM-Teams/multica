package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

const (
	defaultInitialBackoff   = time.Second
	defaultMaxBackoff       = 30 * time.Second
	defaultStableRunWindow  = time.Minute
	defaultGracefulStopWait = 5 * time.Second
)

var (
	ErrAlreadyRunning = errors.New("supervisor already running for profile")
	ErrNotRunning     = errors.New("supervisor is not running")
)

type State string

const (
	StateIdle       State = "idle"
	StateStarting   State = "starting"
	StateRunning    State = "running"
	StateBackingOff State = "backing_off"
	StateStopping   State = "stopping"
	StateStopped    State = "stopped"
)

type ExitKind string

const (
	ExitNone        ExitKind = ""
	ExitClean       ExitKind = "clean"
	ExitCrashed     ExitKind = "crashed"
	ExitStartFailed ExitKind = "start_failed"
	ExitRestarted   ExitKind = "restart_requested"
	ExitStopped     ExitKind = "stopped"
)

type Snapshot struct {
	State          State
	Generation     uint64
	WorkerPID      int
	RestartCount   uint64
	LastExit       ExitKind
	NextBackoff    time.Duration
	WorkerStarted  time.Time
	StateChangedAt time.Time
}

type SleepFunc func(context.Context, time.Duration) error

type Config struct {
	LockPath   string
	WorkerPath string
	WorkerArgs []string
	WorkerEnv  []string
	WorkerDir  string
	Stdout     io.Writer
	Stderr     io.Writer

	InitialBackoff   time.Duration
	MaxBackoff       time.Duration
	StableRunWindow  time.Duration
	GracefulStopWait time.Duration
	Sleep            SleepFunc
}

type Supervisor struct {
	config    Config
	restartCh chan struct{}

	mu sync.Mutex
	// runActive prevents concurrent Run calls on one Supervisor value. It is
	// deliberately separate from profileOwned: Run must reserve the value
	// before attempting the OS lock, while restart requests are only valid
	// after that lock has actually been acquired.
	runActive bool
	// profileOwned is true only while this Run owns the OS profile lock.
	// restartable becomes false as soon as Run commits to a terminal exit,
	// before the lock is released.
	profileOwned   bool
	restartable    bool
	restartPending bool
	snapshot       Snapshot
}

func New(config Config) (*Supervisor, error) {
	if config.LockPath == "" {
		return nil, errors.New("supervisor lock path is required")
	}
	if config.WorkerPath == "" {
		return nil, errors.New("supervisor worker path is required")
	}
	if config.InitialBackoff < 0 ||
		config.MaxBackoff < 0 ||
		config.StableRunWindow < 0 ||
		config.GracefulStopWait < 0 {
		return nil, errors.New("supervisor durations cannot be negative")
	}
	if config.InitialBackoff == 0 {
		config.InitialBackoff = defaultInitialBackoff
	}
	if config.MaxBackoff == 0 {
		config.MaxBackoff = defaultMaxBackoff
	}
	if config.MaxBackoff < config.InitialBackoff {
		return nil, errors.New("supervisor max backoff cannot be less than initial backoff")
	}
	if config.StableRunWindow == 0 {
		config.StableRunWindow = defaultStableRunWindow
	}
	if config.GracefulStopWait == 0 {
		config.GracefulStopWait = defaultGracefulStopWait
	}
	if config.Sleep == nil {
		config.Sleep = sleepContext
	}
	config.WorkerArgs = append([]string(nil), config.WorkerArgs...)
	if config.WorkerEnv != nil {
		config.WorkerEnv = append([]string{}, config.WorkerEnv...)
	}

	now := time.Now()
	return &Supervisor{
		config:    config,
		restartCh: make(chan struct{}, 1),
		snapshot: Snapshot{
			State:          StateIdle,
			StateChangedAt: now,
		},
	}, nil
}

func (s *Supervisor) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot
}

// RequestRestart asks the active supervisor to replace its current worker.
// Multiple pending requests coalesce because a generation transition is the
// only externally meaningful effect.
func (s *Supervisor) RequestRestart() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.runActive || !s.profileOwned || !s.restartable {
		return ErrNotRunning
	}
	if s.restartPending {
		return nil
	}
	s.restartPending = true
	select {
	case s.restartCh <- struct{}{}:
	default:
	}
	return nil
}

// Run owns the profile lock and every worker generation until the context is
// canceled or a worker exits cleanly.
func (s *Supervisor) Run(ctx context.Context) error {
	if err := s.beginRun(); err != nil {
		return err
	}
	defer s.finishRun()

	lock, err := acquireProfileLock(s.config.LockPath)
	if err != nil {
		return err
	}
	s.beginProfileOwnership()
	defer func() {
		s.endProfileOwnership()
		lock.release()
	}()

	backoff := s.config.InitialBackoff
	for {
		if ctx.Err() != nil {
			s.transitionStopped(ExitStopped)
			return nil
		}

		s.updateSnapshot(func(snapshot *Snapshot) {
			snapshot.State = StateStarting
			snapshot.WorkerPID = 0
			snapshot.NextBackoff = 0
		})
		cmd := s.workerCommand()
		configureWorkerProcess(cmd)
		if err := cmd.Start(); err != nil {
			s.updateSnapshot(func(snapshot *Snapshot) {
				snapshot.State = StateBackingOff
				snapshot.LastExit = ExitStartFailed
				snapshot.NextBackoff = backoff
			})
			restartRequested, err := s.waitBeforeRestart(ctx, backoff)
			if err != nil {
				if ctx.Err() != nil {
					s.transitionStopped(ExitStopped)
					return nil
				}
				return err
			}
			s.incrementRestartCount()
			if restartRequested {
				s.markRestartRequested()
				backoff = s.config.InitialBackoff
			} else {
				backoff = nextBackoff(backoff, s.config.MaxBackoff)
			}
			continue
		}

		startedAt := time.Now()
		s.establishGeneration(cmd.Process.Pid, startedAt)

		waitCh := make(chan error, 1)
		go func() {
			waitCh <- cmd.Wait()
		}()

		select {
		case waitErr := <-waitCh:
			if ctx.Err() != nil {
				s.transitionStopped(ExitStopped)
				return nil
			}
			if s.commitWorkerExit(waitErr == nil) {
				backoff = s.config.InitialBackoff
				continue
			}
			if waitErr == nil {
				return nil
			}
			if time.Since(startedAt) >= s.config.StableRunWindow {
				backoff = s.config.InitialBackoff
			}
			s.updateSnapshot(func(snapshot *Snapshot) {
				snapshot.State = StateBackingOff
				snapshot.WorkerPID = 0
				snapshot.LastExit = ExitCrashed
				snapshot.NextBackoff = backoff
			})
			restartRequested, err := s.waitBeforeRestart(ctx, backoff)
			if err != nil {
				if ctx.Err() != nil {
					s.transitionStopped(ExitStopped)
					return nil
				}
				return err
			}
			s.incrementRestartCount()
			if restartRequested {
				s.markRestartRequested()
				backoff = s.config.InitialBackoff
			} else {
				backoff = nextBackoff(backoff, s.config.MaxBackoff)
			}

		case <-ctx.Done():
			s.beginTerminalStop()
			stopWorker(cmd, waitCh, s.config.GracefulStopWait)
			s.transitionStopped(ExitStopped)
			return nil

		case <-s.restartCh:
			s.updateSnapshot(func(snapshot *Snapshot) {
				snapshot.State = StateStopping
				snapshot.NextBackoff = 0
			})
			stopWorker(cmd, waitCh, s.config.GracefulStopWait)
			s.updateSnapshot(func(snapshot *Snapshot) {
				snapshot.State = StateStarting
				snapshot.WorkerPID = 0
				snapshot.LastExit = ExitRestarted
				snapshot.RestartCount++
			})
			backoff = s.config.InitialBackoff
		}
	}
}

func (s *Supervisor) workerCommand() *exec.Cmd {
	cmd := exec.Command(s.config.WorkerPath, s.config.WorkerArgs...)
	if s.config.WorkerEnv != nil {
		cmd.Env = s.config.WorkerEnv
	}
	cmd.Dir = s.config.WorkerDir
	cmd.Stdout = s.config.Stdout
	cmd.Stderr = s.config.Stderr
	return cmd
}

func (s *Supervisor) beginRun() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runActive {
		return ErrAlreadyRunning
	}
	s.drainRestartSignalLocked()
	s.runActive = true
	s.profileOwned = false
	s.restartable = false
	s.restartPending = false
	return nil
}

func (s *Supervisor) finishRun() {
	s.mu.Lock()
	s.runActive = false
	s.profileOwned = false
	s.restartable = false
	s.restartPending = false
	s.drainRestartSignalLocked()
	s.mu.Unlock()
}

func (s *Supervisor) beginProfileOwnership() {
	s.mu.Lock()
	s.profileOwned = true
	s.restartable = true
	s.mu.Unlock()
}

func (s *Supervisor) endProfileOwnership() {
	s.mu.Lock()
	s.profileOwned = false
	s.restartable = false
	s.restartPending = false
	s.drainRestartSignalLocked()
	s.mu.Unlock()
}

func (s *Supervisor) establishGeneration(pid int, startedAt time.Time) {
	s.mu.Lock()
	previousState := s.snapshot.State
	s.snapshot.State = StateRunning
	s.snapshot.Generation++
	s.snapshot.WorkerPID = pid
	s.snapshot.WorkerStarted = startedAt
	s.snapshot.NextBackoff = 0
	// A requested replacement is not fulfilled until the new process has
	// started. Keep the intent owned across stopping/backoff/starting, then
	// clear it atomically with publishing the replacement generation.
	s.restartPending = false
	s.drainRestartSignalLocked()
	if s.snapshot.State != previousState {
		s.snapshot.StateChangedAt = time.Now()
	}
	s.mu.Unlock()
}

func (s *Supervisor) commitWorkerExit(clean bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.restartPending {
		previousState := s.snapshot.State
		s.snapshot.State = StateStarting
		s.snapshot.WorkerPID = 0
		s.snapshot.LastExit = ExitRestarted
		s.snapshot.NextBackoff = 0
		s.snapshot.RestartCount++
		if s.snapshot.State != previousState {
			s.snapshot.StateChangedAt = time.Now()
		}
		return true
	}
	if !clean {
		return false
	}

	// Clean worker exit is terminal unless a restart was already pending.
	// This is the same mutex/linearization point used by RequestRestart, so a
	// request cannot report success in the gap between observing the clean exit
	// and committing StateStopped.
	previousState := s.snapshot.State
	s.snapshot.State = StateStopped
	s.snapshot.WorkerPID = 0
	s.snapshot.LastExit = ExitClean
	s.snapshot.NextBackoff = 0
	s.disableRestartLocked()
	if s.snapshot.State != previousState {
		s.snapshot.StateChangedAt = time.Now()
	}
	return false
}

func (s *Supervisor) drainRestartSignalLocked() {
	for {
		select {
		case <-s.restartCh:
		default:
			return
		}
	}
}

func (s *Supervisor) updateSnapshot(update func(*Snapshot)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	previousState := s.snapshot.State
	update(&s.snapshot)
	if s.snapshot.State != previousState {
		s.snapshot.StateChangedAt = time.Now()
	}
}

func (s *Supervisor) incrementRestartCount() {
	s.updateSnapshot(func(snapshot *Snapshot) {
		snapshot.RestartCount++
	})
}

func (s *Supervisor) markRestartRequested() {
	s.updateSnapshot(func(snapshot *Snapshot) {
		snapshot.State = StateStarting
		snapshot.LastExit = ExitRestarted
		snapshot.NextBackoff = 0
	})
}

func (s *Supervisor) beginTerminalStop() {
	s.mu.Lock()
	previousState := s.snapshot.State
	s.snapshot.State = StateStopping
	s.snapshot.NextBackoff = 0
	s.disableRestartLocked()
	if s.snapshot.State != previousState {
		s.snapshot.StateChangedAt = time.Now()
	}
	s.mu.Unlock()
}

func (s *Supervisor) transitionStopped(exit ExitKind) {
	s.mu.Lock()
	previousState := s.snapshot.State
	s.snapshot.State = StateStopped
	s.snapshot.WorkerPID = 0
	s.snapshot.LastExit = exit
	s.snapshot.NextBackoff = 0
	s.disableRestartLocked()
	if s.snapshot.State != previousState {
		s.snapshot.StateChangedAt = time.Now()
	}
	s.mu.Unlock()
}

func (s *Supervisor) transitionStoppedPreservingExit() {
	s.mu.Lock()
	previousState := s.snapshot.State
	s.snapshot.State = StateStopped
	s.snapshot.WorkerPID = 0
	s.snapshot.NextBackoff = 0
	s.disableRestartLocked()
	if s.snapshot.State != previousState {
		s.snapshot.StateChangedAt = time.Now()
	}
	s.mu.Unlock()
}

func (s *Supervisor) disableRestartLocked() {
	// Linearization point for terminal exit: no restart request may report
	// success after the supervisor has committed to stopping, even when worker
	// or backoff cleanup still has to finish before Run returns.
	s.restartable = false
	s.restartPending = false
	s.drainRestartSignalLocked()
}

func (s *Supervisor) waitBeforeRestart(
	ctx context.Context,
	delay time.Duration,
) (bool, error) {
	sleepCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	sleepCh := make(chan error, 1)
	go func() {
		sleepCh <- s.config.Sleep(sleepCtx, delay)
	}()

	select {
	case err := <-sleepCh:
		if err != nil {
			if ctx.Err() != nil {
				s.beginTerminalStop()
			} else {
				s.transitionStoppedPreservingExit()
			}
			return false, fmt.Errorf("supervisor backoff: %w", err)
		}
		return false, nil
	case <-s.restartCh:
		cancel()
		<-sleepCh
		return true, nil
	case <-ctx.Done():
		s.beginTerminalStop()
		cancel()
		<-sleepCh
		return false, fmt.Errorf("supervisor backoff: %w", ctx.Err())
	}
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func nextBackoff(current, maximum time.Duration) time.Duration {
	if current >= maximum || current > maximum-current {
		return maximum
	}
	next := current * 2
	if next > maximum {
		return maximum
	}
	return next
}

func stopWorker(cmd *exec.Cmd, waitCh <-chan error, grace time.Duration) {
	_ = signalWorker(cmd)
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-waitCh:
		return
	case <-timer.C:
		_ = killWorker(cmd)
		<-waitCh
	}
}

type profileLock struct {
	file *os.File
}

func acquireProfileLock(path string) (*profileLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create supervisor lock directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open supervisor lock: %w", err)
	}
	if err := lockProfileFile(file); err != nil {
		_ = file.Close()
		if errors.Is(err, errProfileLocked) {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("lock supervisor profile: %w", err)
	}
	return &profileLock{file: file}, nil
}

func (l *profileLock) release() {
	unlockProfileFile(l.file)
	_ = l.file.Close()
}
