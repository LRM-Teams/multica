package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// TestStopManagedAgentPublishesInactiveAndRetainsTerminationFence pins the
// "no half-stop" requirement (LRM-1571 follow-up): a resident that never
// confirms its own kill (OS process wedged past SIGKILL, or our own turn
// goroutine never releasing the slot) must still publish AgentStatusInactive,
// while retaining the existing APM stopping record so a later Start cannot
// bypass provider termination. stopManagedAgent must return nil so the caller
// (workspace_daemon.go's EventDaemonAgentStop handler) does not tear down the
// whole WorkspaceDaemon connection over one agent's unconfirmed kill.
func TestStopManagedAgentPublishesInactiveAndRetainsTerminationFence(t *testing.T) {
	const (
		workspaceID     = "ws-1"
		runtimeID       = "runtime-a"
		agentID         = "agent-a"
		agentInstanceID = "launch-a"
	)
	d := New(Config{WorkspacesRoot: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.mu.Lock()
	d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
	d.mu.Unlock()
	runner, _ := attachTestWorkspaceDaemon(t, d, workspaceID, nil)
	runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error { return nil }

	_, status, _, err := runner.startManagedAgent(context.Background(), protocol.AgentStartPayload{
		AgentID: agentID, RuntimeID: runtimeID})
	if err != nil || status.Status != protocol.AgentStatusActive {
		t.Fatalf("start managed Agent: status %+v, error %v", status, err)
	}

	// Register a real resident slot that never releases: ForceKill is
	// answered but nothing ever completes the "turn" or reports the process
	// dead. This models a process wedged past SIGKILL (D-state) or our own
	// bookkeeping failing to observe the kill -- exactly the case a bounded
	// termination wait must not be allowed to corrupt the Stop transition.
	probe := &canonicalRuntimeFactoryProbe{}
	identity := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": workspaceID,
		"MULTICA_AGENT_ID":     agentID,
		"MULTICA_TASK_ID":      "turn-a",
	})
	if _, err := runner.runtimes.acquire(agentRuntimeAcquireRequest{
		Identity: identity,
		Factory:  probe.factory,
	}); err != nil {
		t.Fatalf("acquire resident slot: %v", err)
	}
	// The lease is intentionally never released.

	statuses := make(chan protocol.AgentStatusPayload, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	stopErr := runner.stopManagedAgent(ctx, protocol.AgentStopPayload{
		AgentID: agentID}, nil, func(eventType string, payload any) error {
		if eventType == protocol.EventAgentStatus {
			statuses <- payload.(protocol.AgentStatusPayload)
		}
		return nil
	})
	if stopErr != nil {
		t.Fatalf("stopManagedAgent returned %v, want nil: a stop that cannot confirm the kill must still leave the system consistent instead of failing the whole connection", stopErr)
	}
	select {
	case got := <-statuses:
		if got.AgentID != agentID || got.Status != protocol.AgentStatusInactive {
			t.Fatalf("stop status = %+v, want terminal Inactive for %s/%s", got, agentID, agentInstanceID)
		}
	default:
		t.Fatal("stopManagedAgent did not publish AgentStatusInactive despite the resident termination timeout")
	}
	if _, found := runner.processes.Snapshot(agentID); found {
		t.Fatal("stopped launch survived in the process manager despite a resident termination timeout")
	}
	if stopping, found := runner.processes.stoppingSnapshot(agentID); !found || stopping.RuntimeID != runtimeID {
		t.Fatalf("provider termination fence = %+v, found %v; want stopping Runtime %s", stopping, found, runtimeID)
	}
}

func TestRuntimeChangeRejectsStartWhileOldProviderTerminationIsUnconfirmed(t *testing.T) {
	const (
		workspaceID = "ws-1"
		agentID     = "agent-a"
		oldRuntime  = "runtime-a"
		newRuntime  = "runtime-b"
		oldLaunch   = "launch-a"
	)
	d := New(Config{WorkspacesRoot: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.mu.Lock()
	d.runtimeIndex[oldRuntime] = Runtime{ID: oldRuntime, WorkspaceID: workspaceID}
	d.runtimeIndex[newRuntime] = Runtime{ID: newRuntime, WorkspaceID: workspaceID}
	d.mu.Unlock()
	runner, _ := attachTestWorkspaceDaemon(t, d, workspaceID, nil)
	runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error { return nil }

	_, status, _, err := runner.startManagedAgent(context.Background(), protocol.AgentStartPayload{
		AgentID: agentID, RuntimeID: oldRuntime})
	if err != nil || status.Status != protocol.AgentStatusActive {
		t.Fatalf("start old Runtime: status %+v, error %v", status, err)
	}

	probe := &canonicalRuntimeFactoryProbe{}
	identity := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": workspaceID,
		"MULTICA_AGENT_ID":     agentID,
		"MULTICA_TASK_ID":      "turn-a",
	})
	oldLease, err := runner.runtimes.acquire(agentRuntimeAcquireRequest{
		Identity: identity,
		Factory:  probe.factory,
	})
	if err != nil {
		t.Fatalf("acquire old resident slot: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err = runner.restartAgentForRuntimeChange(ctx, protocol.AgentStartPayload{
		AgentID: agentID, RuntimeID: newRuntime}, nil, func(string, any) error { return nil })
	if err == nil {
		t.Fatal("runtime change proceeded while the old provider termination was unconfirmed")
	}
	if !runner.runtimes.hasResidentBackend(agentID, oldRuntime) {
		t.Fatal("test did not retain the old provider past the termination wait")
	}

	replayCtx, replayCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	err = runner.restartAgentForRuntimeChange(replayCtx, protocol.AgentStartPayload{
		AgentID: agentID, RuntimeID: newRuntime}, nil, func(string, any) error { return nil })
	replayCancel()
	if err == nil {
		t.Fatal("replayed runtime change bypassed the unsettled old provider termination")
	}
	if _, _, _, startErr := runner.startManagedAgent(context.Background(), protocol.AgentStartPayload{
		AgentID: agentID, RuntimeID: newRuntime}); startErr == nil {
		t.Fatal("ordinary Start created a replacement while the old provider was still alive")
	}

	oldLease.release(true)
	if err := runner.restartAgentForRuntimeChange(context.Background(), protocol.AgentStartPayload{
		AgentID: agentID, RuntimeID: newRuntime}, nil, func(string, any) error { return nil }); err != nil {
		t.Fatalf("runtime change remained blocked after old provider exit: %v", err)
	}
	_, newStatus, _, err := runner.startManagedAgent(context.Background(), protocol.AgentStartPayload{
		AgentID: agentID, RuntimeID: newRuntime})
	if err != nil || newStatus.Status != protocol.AgentStatusActive {
		t.Fatalf("start new Runtime after old provider exit: status %+v, error %v", newStatus, err)
	}
}

// TestStopManagedAgentSingleClearSurvivesRacingProviderStartupWrite verifies
// that an old provider callback cannot restore residency after Stop releases
// the exact AgentInstanceID from active APM ownership.
func TestStopManagedAgentSingleClearSurvivesRacingProviderStartupWrite(t *testing.T) {
	const (
		workspaceID     = "ws-1"
		runtimeID       = "runtime-a"
		agentID         = "agent-a"
		agentInstanceID = "launch-a"
	)
	d := New(Config{WorkspacesRoot: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.mu.Lock()
	d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
	d.mu.Unlock()
	runner, _ := attachTestWorkspaceDaemon(t, d, workspaceID, nil)
	runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error { return nil }

	_, status, _, err := runner.startManagedAgent(context.Background(), protocol.AgentStartPayload{
		AgentID: agentID, RuntimeID: runtimeID})
	if err != nil || status.Status != protocol.AgentStatusActive {
		t.Fatalf("start managed Agent: status %+v, error %v", status, err)
	}

	callback := currentTestAgentProcessCallback(t, runner, agentID)

	// A real resident slot that never releases gives awaitResidentTerminated
	// a genuine multi-millisecond window to block in, same as the timeout
	// test above -- room for the racing goroutine below to fire after clear.
	probe := &canonicalRuntimeFactoryProbe{}
	identity := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": workspaceID,
		"MULTICA_AGENT_ID":     agentID,
		"MULTICA_TASK_ID":      "turn-a",
	})
	if _, err := runner.runtimes.acquire(agentRuntimeAcquireRequest{
		Identity: identity,
		Factory:  probe.factory,
	}); err != nil {
		t.Fatalf("acquire resident slot: %v", err)
	}

	raced := make(chan struct{})
	pause := func() {
		go func() {
			defer close(raced)
			// Wait for the sole clear to actually run, then fire the racing
			// write -- this must land strictly after clear for the test to
			// exercise anything.
			for {
				if _, ok := runner.residency.get(agentID); !ok {
					break
				}
				time.Sleep(time.Millisecond)
			}
			runner.processes.withActiveAgentInstance(callback, func() {
				runner.residency.rememberFailure(agentID, runtimeID, agentInstanceID, managedRuntimeFailureRuntime, "provider_spawn_failed", "")
			})
		}()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	stopErr := runner.stopManagedAgent(ctx, protocol.AgentStopPayload{
		AgentID: agentID}, pause, func(string, any) error { return nil })
	if stopErr != nil {
		t.Fatalf("stopManagedAgent returned %v, want nil", stopErr)
	}

	select {
	case <-raced:
	case <-time.After(time.Second):
		t.Fatal("racing write never fired -- test did not exercise the race window")
	}

	if res, ok := runner.residency.get(agentID); ok {
		t.Fatalf("residency survived a racing stale write past the sole clear: %+v", res)
	}
}

// TestCanonicalAgentRuntimeTerminateResidentReturnsOnSignalNotPoll pins the
// "no busy-wait" requirement: terminateResident must return as soon as the
// slot's own completion signal fires, driven by the in-flight turn's own
// goroutine observing the force-kill and releasing unhealthily -- exactly as
// Execute()'s own goroutine would once it sees the killed process fail. It
// must not depend on any fixed poll interval.
func TestCanonicalAgentRuntimeTerminateResidentReturnsOnSignalNotPoll(t *testing.T) {
	pool := newAgentRuntimePool()
	probe := &canonicalRuntimeFactoryProbe{}
	identity := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-a",
	})
	lease, err := pool.acquire(agentRuntimeAcquireRequest{
		Identity: identity,
		Factory:  probe.factory,
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	go func() {
		time.Sleep(15 * time.Millisecond)
		lease.release(false)
	}()

	start := time.Now()
	if err := pool.terminateResident(context.Background(), "agent-a", "runtime-a"); err != nil {
		t.Fatalf("terminateResident: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("terminateResident took %v to return after the signal fired, want a prompt signal-driven return", elapsed)
	}
	backend := probe.backends[0]
	if got := backend.forceKillCount(); got != 1 {
		t.Fatalf("ForceKill called %d times, want 1", got)
	}
	_, closed := probe.counts()
	if closed != 1 {
		t.Fatalf("closed count = %d, want 1 (terminateResident must observe the released, closed backend)", closed)
	}
}

// TestCanonicalAgentRuntimeTerminateResidentTimeoutReportsBothConditions pins
// the wedged-turn case: the in-flight turn never releases the slot and the
// process itself is confirmed alive, so both conditions in
// residentTerminationTimeout must be true.
func TestCanonicalAgentRuntimeTerminateResidentTimeoutReportsBothConditions(t *testing.T) {
	pool := newAgentRuntimePool()
	backend := &canonicalRuntimeLivenessTestBackend{}
	backend.setLiveness(true, true)
	identity := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-a",
	})
	lease, err := pool.acquire(agentRuntimeAcquireRequest{
		Identity: identity,
		Factory:  newLivenessFactory(backend),
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer lease.release(false)
	// The lease is intentionally never released before terminateResident's
	// bounded wait elapses: this models a turn goroutine that never observes
	// the force-kill.

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	err = pool.terminateResident(ctx, "agent-a", "runtime-a")
	var timeoutErr *residentTerminationTimeout
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("terminateResident error = %v, want *residentTerminationTimeout", err)
	}
	if !timeoutErr.ProcessAlive || !timeoutErr.TurnRunning {
		t.Fatalf("timeout = %+v, want ProcessAlive=true TurnRunning=true", timeoutErr)
	}
	if backend.forceKillCount() != 1 {
		t.Fatalf("ForceKill called %d times, want 1", backend.forceKillCount())
	}
}

// TestCanonicalAgentRuntimeTerminateResidentTimeoutReportsProcessDeadOnly
// pins the confirmed-dead-process case: the process itself reports dead, but
// our own turn goroutine never released the slot (a code wedge), so only
// TurnRunning must be true.
func TestCanonicalAgentRuntimeTerminateResidentTimeoutReportsProcessDeadOnly(t *testing.T) {
	pool := newAgentRuntimePool()
	backend := &canonicalRuntimeLivenessTestBackend{}
	backend.setLiveness(false, true) // known dead
	identity := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-a",
	})
	lease, err := pool.acquire(agentRuntimeAcquireRequest{
		Identity: identity,
		Factory:  newLivenessFactory(backend),
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer lease.release(false)

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	err = pool.terminateResident(ctx, "agent-a", "runtime-a")
	var timeoutErr *residentTerminationTimeout
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("terminateResident error = %v, want *residentTerminationTimeout", err)
	}
	if timeoutErr.ProcessAlive || !timeoutErr.TurnRunning {
		t.Fatalf("timeout = %+v, want ProcessAlive=false TurnRunning=true", timeoutErr)
	}
}

// TestCanonicalAgentRuntimeTerminateResidentTimeoutReportsProcessAliveOnly
// pins the opposite split: the in-flight turn's own goroutine correctly
// observes the force-kill and releases the slot (running goes false), but
// the process itself never actually died (e.g. still alive per the liveness
// checker) -- the OS-process-level condition this design exists to surface
// distinctly from a code wedge.
func TestCanonicalAgentRuntimeTerminateResidentTimeoutReportsProcessAliveOnly(t *testing.T) {
	pool := newAgentRuntimePool()
	backend := &canonicalRuntimeLivenessTestBackend{}
	backend.setLiveness(true, true)
	identity := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-a",
	})
	lease, err := pool.acquire(agentRuntimeAcquireRequest{
		Identity: identity,
		Factory:  newLivenessFactory(backend),
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// Healthy release (unlike the force-killed-turn tests above) sets
	// running=false without calling closeBackend -- exactly the state a
	// process that ignored the kill signal would leave behind: our own
	// bookkeeping is done, but the process is still alive.
	go func() {
		time.Sleep(15 * time.Millisecond)
		lease.release(true)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	err = pool.terminateResident(ctx, "agent-a", "runtime-a")
	var timeoutErr *residentTerminationTimeout
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("terminateResident error = %v, want *residentTerminationTimeout", err)
	}
	if !timeoutErr.ProcessAlive || timeoutErr.TurnRunning {
		t.Fatalf("timeout = %+v, want ProcessAlive=true TurnRunning=false", timeoutErr)
	}
}

// TestAgentRuntimeSlotAwaitTerminatedRearms pins the re-arm
// requirement: a slot that goes empty and then gets a new backend must not
// let a stale closed signal from the previous process make the next
// awaitTerminated return instantly.
func TestAgentRuntimeSlotAwaitTerminatedRearms(t *testing.T) {
	pool := newAgentRuntimePool()
	probe := &canonicalRuntimeFactoryProbe{}
	identity := canonicalRuntimeIdentityForTest(t, "model-a", map[string]string{
		"MULTICA_SERVER_URL":   "https://multica.example",
		"MULTICA_WORKSPACE_ID": "workspace-a",
		"MULTICA_AGENT_ID":     "agent-a",
		"MULTICA_TASK_ID":      "turn-a",
	})
	lease1, err := pool.acquire(agentRuntimeAcquireRequest{
		Identity: identity,
		Factory:  probe.factory,
	})
	if err != nil {
		t.Fatalf("acquire (1st): %v", err)
	}
	lease1.release(true) // healthy idle release: process alive, running false

	// Idle-slot termination closes the backend immediately (nothing to
	// interrupt), which closes the signal.
	if err := pool.terminateResident(context.Background(), "agent-a", "runtime-a"); err != nil {
		t.Fatalf("terminateResident (1st, idle): %v", err)
	}
	if _, closed := probe.counts(); closed != 1 {
		t.Fatalf("closed count after 1st terminateResident = %d, want 1", closed)
	}

	// Re-acquire: same slot key, new backend. The slot's terminated channel
	// must be re-armed by this new backend's creation.
	if _, err := pool.acquire(agentRuntimeAcquireRequest{
		Identity: identity,
		Factory:  probe.factory,
	}); err != nil {
		t.Fatalf("acquire (2nd): %v", err)
	}
	// lease2 is intentionally never released -- the slot stays busy.

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	err = pool.terminateResident(ctx, "agent-a", "runtime-a")
	var timeoutErr *residentTerminationTimeout
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("terminateResident (2nd) error = %v, want *residentTerminationTimeout -- a stale closed signal would have returned nil instantly instead of waiting", err)
	}
}

// hungSpawnTestBackend models a resident whose provider spawn
// (EnsureResidentProcess) blocks until explicitly killed/closed. Its
// EnsureResidentProcess ctx is deliberately independent of any per-request
// ctx in the test, mirroring production: startAgentNow runs on runner.life
// (workspace_daemon_state.go), not the stop's connection ctx, so ctx
// cancellation on the stop side must never be what unblocks a hung spawn.
type hungSpawnTestBackend struct {
	mu             sync.Mutex
	unblock        chan struct{}
	unblocked      bool
	forceKillCalls int
}

func newHungSpawnTestBackend() *hungSpawnTestBackend {
	return &hungSpawnTestBackend{unblock: make(chan struct{})}
}

func (b *hungSpawnTestBackend) Execute(_ context.Context, _ string, opts agent.ExecOptions) (*agent.Session, error) {
	messages := make(chan agent.Message)
	result := make(chan agent.Result, 1)
	close(messages)
	result <- agent.Result{Status: "completed", SessionID: opts.ResumeSessionID}
	close(result)
	return &agent.Session{Messages: messages, Result: result}, nil
}

func (b *hungSpawnTestBackend) EnsureResidentProcess(ctx context.Context) error {
	select {
	case <-b.unblock:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *hungSpawnTestBackend) ForceKill() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.forceKillCalls++
	if !b.unblocked {
		b.unblocked = true
		close(b.unblock)
	}
	return nil
}

func (b *hungSpawnTestBackend) forceKillCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.forceKillCalls
}

// hungProviderSpawnEnsureResidentRuntime stands in for
// d.ensureResidentMessageRuntime: it acquires+releases a resident slot (as
// production does before EnsureResidentProcess) and then blocks in the
// spawn, on spawnCtx rather than the caller's ctx -- modeling runner.life.
func hungProviderSpawnEnsureResidentRuntime(runner *WorkspaceDaemon, backend *hungSpawnTestBackend, spawnCtx context.Context, spawnStarted chan<- struct{}) func(context.Context, string, string, *agent.PiRunIdentity) error {
	var once sync.Once
	return func(_ context.Context, agentID, runtimeID string, _ *agent.PiRunIdentity) error {
		identity, err := newCanonicalAgentRuntimeIdentity(canonicalAgentRuntimeIdentityParams{
			AgentID: agentID, RuntimeID: runtimeID, Provider: "pi",
			Executable: "/usr/local/bin/pi", WorkDir: "/var/lib/multica/" + agentID + "/workspace",
		})
		if err != nil {
			return err
		}
		lease, err := runner.runtimes.acquire(agentRuntimeAcquireRequest{
			Identity: identity,
			Factory: func(agent.Config) (agent.Backend, func(), error) {
				return backend, func() { _ = backend.ForceKill() }, nil
			},
		})
		if err != nil {
			return err
		}
		lease.release(true)
		once.Do(func() { close(spawnStarted) })
		return backend.EnsureResidentProcess(spawnCtx)
	}
}

// TestStopManagedAgentDispatchesKillBeforeWaitingOnHungProviderSpawn pins the
// ordering requirement: a managed start blocked inside provider spawn is
// bound to runner.life, not the stop's ctx (see hungSpawnTestBackend), so
// stopManagedAgent's own kill dispatch is the only thing that can ever
// unblock it. If the kill is deferred until after waiting on startupDone,
// the stop deadlocks against its own precondition: startupDone can only
// close once the spawn returns, and the spawn only returns once killed.
func TestStopManagedAgentDispatchesKillBeforeWaitingOnHungProviderSpawn(t *testing.T) {
	const (
		workspaceID     = "ws-1"
		runtimeID       = "runtime-a"
		agentID         = "agent-a"
		agentInstanceID = "launch-a"
	)
	d := New(Config{WorkspacesRoot: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.mu.Lock()
	d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
	d.mu.Unlock()
	runner, _ := attachTestWorkspaceDaemon(t, d, workspaceID, nil)

	backend := newHungSpawnTestBackend()
	spawnStarted := make(chan struct{})
	spawnCtx, cancelSpawn := context.WithCancel(context.Background())
	defer cancelSpawn()
	runner.ensureResidentRuntime = hungProviderSpawnEnsureResidentRuntime(runner, backend, spawnCtx, spawnStarted)

	startDone := make(chan error, 1)
	go func() {
		_, _, _, err := runner.startManagedAgent(context.Background(), protocol.AgentStartPayload{
			AgentID: agentID, RuntimeID: runtimeID})
		startDone <- err
	}()
	select {
	case <-spawnStarted:
	case <-time.After(time.Second):
		t.Fatal("managed start did not reach the provider spawn")
	}
	time.Sleep(20 * time.Millisecond) // let the spawn goroutine actually park

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	stopErr := runner.stopManagedAgent(ctx, protocol.AgentStopPayload{
		AgentID: agentID}, nil, func(string, any) error { return nil })
	elapsed := time.Since(start)

	if stopErr != nil {
		t.Fatalf("stopManagedAgent = %v (took %v), want nil: an early kill dispatch must unblock the hung spawn well inside the 1s budget", stopErr, elapsed)
	}
	if elapsed > 700*time.Millisecond {
		t.Fatalf("stopManagedAgent took %v against a 1s budget -- the kill was not dispatched before waiting on the hung spawn's startupDone", elapsed)
	}
	if backend.forceKillCount() < 1 {
		t.Fatal("stopManagedAgent never dispatched a kill against the hung provider spawn")
	}
	select {
	case err := <-startDone:
		if err == nil {
			t.Fatal("blocked managed start unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("managed start goroutine never returned after its spawn was killed")
	}
}
