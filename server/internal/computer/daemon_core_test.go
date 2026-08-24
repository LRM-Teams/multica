package computer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type supervisorTestChild struct {
	pid      int
	wait     chan RunnerExitClass
	stopOnce sync.Once
}

type crashingSupervisorChild struct{ pid int }

type readySupervisorChild struct {
	*supervisorTestChild
	controlEndpoint  string
	workspaceID      string
	daemonInstanceID string
}

type activatingSupervisorChild struct {
	*supervisorTestChild
	activate func()
}

func (child *activatingSupervisorChild) Activate() { child.activate() }

func (child *readySupervisorChild) AwaitReady(context.Context) (BindingChildReady, error) {
	return BindingChildReady{
		ProtocolVersion: BindingChildProtocolVersion,
		WorkspaceID:     child.workspaceID, DaemonInstanceID: child.daemonInstanceID,
		PID:            child.pid,
		RunnerEndpoint: child.controlEndpoint,
	}, nil
}

func (child *supervisorTestChild) AwaitReady(context.Context) (BindingChildReady, error) {
	return BindingChildReady{
		ProtocolVersion:  BindingChildProtocolVersion,
		DaemonInstanceID: fmt.Sprintf("child-%d", child.pid),
		PID:              child.pid,
		RunnerEndpoint:   "unix:///tmp/multica-test-runner.sock",
	}, nil
}

func (child crashingSupervisorChild) PID() int        { return child.pid }
func (crashingSupervisorChild) Wait() RunnerExitClass { return RunnerExitCrash }
func (crashingSupervisorChild) Stop() error           { return nil }

func newSupervisorTestChild(pid int) *supervisorTestChild {
	return &supervisorTestChild{pid: pid, wait: make(chan RunnerExitClass, 1)}
}

func (child *supervisorTestChild) PID() int              { return child.pid }
func (child *supervisorTestChild) Wait() RunnerExitClass { return <-child.wait }
func (child *supervisorTestChild) Stop() error {
	child.stopOnce.Do(func() { child.wait <- RunnerExitGraceful })
	return nil
}

// spawnReclaimTestProcess starts a real short-lived OS process so reclaim
// tests exercise processAlive/SIGTERM/SIGKILL against a live PID instead of
// the test binary's own PID (which must never be signaled). The process is
// reaped in the background so it never becomes a zombie that would make
// processAlive report it as still alive after it has actually exited.
func spawnReclaimTestProcess(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot spawn reclaim test process: %v", err)
	}
	go func() { _ = cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return cmd
}

// reclaimTestTimings are small real durations (not a fake clock) so kill-path
// tests do not race the OS's actual signal delivery/reap timing; see
// terminateProcess and reclaimOrphanedRunner.
func reclaimTestTimings() (poll, grace time.Duration) {
	return 5 * time.Millisecond, 50 * time.Millisecond
}

// testDrainRunner mimics how the ComputerCore wires daemonCoreConfig.DrainRunner
// in production: close the control token over RequestBindingRunnerDrain so
// reclaim tests exercise the real runner:drain RPC round trip.
func testDrainRunner(token string) func(context.Context, string, BindingChildIdentity) error {
	return func(ctx context.Context, endpoint string, identity BindingChildIdentity) error {
		return RequestBindingRunnerDrain(ctx, endpoint, token, identity)
	}
}

func writeReclaimableRunnerFixture(t *testing.T, root, workspaceID string, pid int, endpoint string) {
	t.Helper()
	state := persistedRunnerState{
		WorkspaceID: workspaceID, DaemonInstanceID: "predecessor-start", OwnerPID: 999998,
		RunnerPID: pid, StartedAt: time.Now().UTC(),
	}
	if err := writeRunnerState(root, state); err != nil {
		t.Fatal(err)
	}
	if err := writeRunnerPID(root, workspaceID, pid); err != nil {
		t.Fatal(err)
	}
	if endpoint != "" {
		if err := writeRunnerConnected(root, workspaceID, persistedRunnerConnected{PID: pid, ConnectedAt: state.StartedAt, RunnerEndpoint: endpoint}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDaemonCoreReclaimsDrainedOrphanThenSpawnsOwnChild(t *testing.T) {
	root := t.TempDir()
	orphan := spawnReclaimTestProcess(t, "sleep", "30")

	var drains atomic.Int32
	control := localControlTestServer(t, func(_ context.Context, operation string, _ map[string]string, _ json.RawMessage) (any, error) {
		if operation == LocalControlRunnerDrainOperation {
			drains.Add(1)
		}
		return nil, nil
	})
	writeReclaimableRunnerFixture(t, root, "workspace-a", orphan.Process.Pid, control)

	poll, grace := reclaimTestTimings()
	spawned := 0
	supervisor, err := newDaemonCore(daemonCoreConfig{
		StateRoot: root, DrainRunner: testDrainRunner("control-token"),
		TerminatePollInterval: poll, TerminateGrace: grace,
		Spawn: func(string) (BindingChild, error) {
			spawned++
			return newSupervisorTestChild(101), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := drains.Load(); got != 1 {
		t.Fatalf("drain requests = %d, want 1", got)
	}
	if alive, known := processAlive(orphan.Process.Pid); known && alive {
		t.Fatal("orphaned Binding Runner process is still alive after reclaim")
	}
	if _, err := os.Stat(runnerStatePath(root, "workspace-a")); !os.IsNotExist(err) {
		t.Fatalf("reclaimed runner state was not cleared: %v", err)
	}

	supervisor.Reconcile(context.Background(), []string{"workspace-a"})
	waitForSupervisorLifecycle(t, supervisor, "workspace-a", RunnerLifecycleRunning)
	if spawned != 1 {
		t.Fatalf("spawned %d children after reclaiming an orphan, want 1", spawned)
	}
	record, pid, ok := supervisor.Snapshot("workspace-a")
	if !ok || !supervisor.Current(BindingChildIdentity{WorkspaceID: "workspace-a", DaemonInstanceID: record.DaemonInstanceID(), PID: pid}) {
		t.Fatalf("Computer did not recognize its own freshly spawned child after reclaim: record=%+v pid=%d", record, pid)
	}
	supervisor.Stop()
}

func TestDaemonCoreReclaimsOrphanBySignalWhenDrainEndpointUnreachable(t *testing.T) {
	root := t.TempDir()
	orphan := spawnReclaimTestProcess(t, "sleep", "30")
	// A dangling unix endpoint nothing is listening on: the drain request
	// must fail fast and fall through to signal-based termination.
	unreachable := ServiceControlEndpoint(t.TempDir())
	writeReclaimableRunnerFixture(t, root, "workspace-a", orphan.Process.Pid, unreachable)

	poll, grace := reclaimTestTimings()
	spawned := 0
	supervisor, err := newDaemonCore(daemonCoreConfig{
		StateRoot: root, DrainRunner: testDrainRunner("control-token"),
		TerminatePollInterval: poll, TerminateGrace: grace,
		Spawn: func(string) (BindingChild, error) {
			spawned++
			return newSupervisorTestChild(102), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if alive, known := processAlive(orphan.Process.Pid); known && alive {
		t.Fatal("orphaned Binding Runner process is still alive after reclaim")
	}

	supervisor.Reconcile(context.Background(), []string{"workspace-a"})
	waitForSupervisorLifecycle(t, supervisor, "workspace-a", RunnerLifecycleRunning)
	if spawned != 1 {
		t.Fatalf("spawned %d children after reclaiming an unreachable orphan, want 1", spawned)
	}
	supervisor.Stop()
}

func TestDaemonCoreReclaimsOrphanBySigkillWhenSigtermIsIgnored(t *testing.T) {
	root := t.TempDir()
	// The child ignores SIGTERM (and execs into the ignoring shell so the
	// disposition survives into the recorded pid), forcing terminateProcess
	// to escalate to SIGKILL.
	orphan := spawnReclaimTestProcess(t, "sh", "-c", `trap "" TERM; exec sleep 30`)
	writeReclaimableRunnerFixture(t, root, "workspace-a", orphan.Process.Pid, "")

	poll, grace := reclaimTestTimings()
	spawned := 0
	supervisor, err := newDaemonCore(daemonCoreConfig{
		StateRoot: root, DrainRunner: testDrainRunner("control-token"),
		TerminatePollInterval: poll, TerminateGrace: grace,
		Spawn: func(string) (BindingChild, error) {
			spawned++
			return newSupervisorTestChild(103), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if alive, known := processAlive(orphan.Process.Pid); known && alive {
		t.Fatal("orphaned Binding Runner process ignoring SIGTERM survived reclaim (SIGKILL escalation did not fire)")
	}

	supervisor.Reconcile(context.Background(), []string{"workspace-a"})
	waitForSupervisorLifecycle(t, supervisor, "workspace-a", RunnerLifecycleRunning)
	if spawned != 1 {
		t.Fatalf("spawned %d children after SIGKILL-reclaiming an orphan, want 1", spawned)
	}
	supervisor.Stop()
}

func TestDaemonCoreLeavesPredecessorOwnerAloneAndDoesNotReclaim(t *testing.T) {
	root := t.TempDir()
	orphan := spawnReclaimTestProcess(t, "sleep", "30")
	// OwnerPID is this test process's own pid: a live owner means a second
	// ComputerCore generation is (impossibly, but defensively) still running, so
	// this instance must not touch the workspace at all.
	state := persistedRunnerState{
		WorkspaceID: "workspace-a", DaemonInstanceID: "predecessor-start", OwnerPID: os.Getpid(),
		RunnerPID: orphan.Process.Pid, StartedAt: time.Now().UTC(),
	}
	if err := writeRunnerState(root, state); err != nil {
		t.Fatal(err)
	}
	if err := writeRunnerPID(root, state.WorkspaceID, orphan.Process.Pid); err != nil {
		t.Fatal(err)
	}

	spawned := 0
	supervisor, err := newDaemonCore(daemonCoreConfig{
		StateRoot: root,
		Spawn: func(string) (BindingChild, error) {
			spawned++
			return newSupervisorTestChild(104), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if alive, known := processAlive(orphan.Process.Pid); !known || !alive {
		t.Fatal("supervisor with a live predecessor owner killed a Runner it must leave alone")
	}
	if _, err := os.Stat(runnerStatePath(root, "workspace-a")); err != nil {
		t.Fatalf("live-owner runner state was removed: %v", err)
	}
	if _, _, ok := supervisor.Snapshot("workspace-a"); ok {
		t.Fatal("supervisor recorded a workspace it must leave to its still-live predecessor owner")
	}
	if spawned != 0 {
		t.Fatalf("spawned %d children while a predecessor ComputerCore owner is still alive", spawned)
	}
}

func TestDaemonCoreRegistersChildBeforeActivation(t *testing.T) {
	activated := make(chan bool, 1)
	var supervisor *DaemonCore
	var err error
	supervisor, err = newDaemonCore(daemonCoreConfig{
		Spawn: func(workspaceID string) (BindingChild, error) {
			const pid = 101
			return &activatingSupervisorChild{
				supervisorTestChild: newSupervisorTestChild(pid),
				activate: func() {
					activated <- supervisor.Current(BindingChildIdentity{
						WorkspaceID: workspaceID, DaemonInstanceID: fmt.Sprintf("child-%d", pid), PID: pid,
					})
				},
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	supervisor.Reconcile(context.Background(), []string{"workspace-a"})
	select {
	case current := <-activated:
		if !current {
			t.Fatal("in-process Binding activated before supervisor registration")
		}
	case <-time.After(time.Second):
		t.Fatal("in-process Binding was not activated")
	}
	supervisor.Stop()
}

func TestDaemonCoreRetainsSiblingAndFencesDaemonInstanceIDPID(t *testing.T) {
	var nextPID atomic.Int32
	children := make(map[string]*supervisorTestChild)
	var childrenMu sync.Mutex
	supervisor, err := newDaemonCore(daemonCoreConfig{
		Spawn: func(workspaceID string) (BindingChild, error) {
			child := newSupervisorTestChild(int(nextPID.Add(1)) + 100)
			childrenMu.Lock()
			children[workspaceID] = child
			childrenMu.Unlock()
			return child, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	supervisor.Reconcile(ctx, []string{"workspace-a", "workspace-b"})
	waitForSupervisorLifecycle(t, supervisor, "workspace-a", RunnerLifecycleRunning)
	waitForSupervisorLifecycle(t, supervisor, "workspace-b", RunnerLifecycleRunning)
	recordA, pidA, _ := supervisor.Snapshot("workspace-a")
	recordB, pidB, _ := supervisor.Snapshot("workspace-b")
	if !supervisor.Current(BindingChildIdentity{WorkspaceID: "workspace-a", DaemonInstanceID: recordA.DaemonInstanceID(), PID: pidA}) {
		t.Fatal("Computer rejected its current Binding child")
	}
	if supervisor.Current(BindingChildIdentity{WorkspaceID: "workspace-a", DaemonInstanceID: "stale-start", PID: pidA}) {
		t.Fatal("Computer accepted a stale Runner daemon instance")
	}

	supervisor.Reconcile(ctx, []string{"workspace-a"})
	waitForSupervisorLifecycle(t, supervisor, "workspace-b", RunnerLifecycleStopped)
	currentA, currentPIDA, _ := supervisor.Snapshot("workspace-a")
	if currentA.DaemonInstanceID() != recordA.DaemonInstanceID() || currentA.Lifecycle != RunnerLifecycleRunning || currentPIDA != pidA {
		t.Fatalf("removing sibling mutated workspace-a: record=%+v pid=%d", currentA, currentPIDA)
	}
	if recordB.DaemonInstanceID() == "" || pidB == 0 {
		t.Fatal("workspace-b never had a real supervised child identity")
	}
	supervisor.Stop()
}

func TestDaemonCoreBacksOffThenDegradesCrashLoop(t *testing.T) {
	base := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	var now atomic.Value
	now.Store(base)
	var spawns atomic.Int32
	supervisor, err := newDaemonCore(daemonCoreConfig{
		Now: func() time.Time { return now.Load().(time.Time) },
		Spawn: func(string) (BindingChild, error) {
			return crashingSupervisorChild{pid: int(spawns.Add(1)) + 200}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for attempt := int32(1); attempt <= RunnerDegradedThreshold; attempt++ {
		supervisor.Reconcile(ctx, []string{"workspace-a"})
		want := RunnerLifecycleCrashed
		if attempt == RunnerDegradedThreshold {
			want = RunnerLifecycleDegraded
		}
		waitForSupervisorLifecycle(t, supervisor, "workspace-a", want)
		if got := spawns.Load(); got != attempt {
			t.Fatalf("spawns after attempt %d = %d", attempt, got)
		}
		if want != RunnerLifecycleDegraded {
			supervisor.Reconcile(ctx, []string{"workspace-a"})
			if got := spawns.Load(); got != attempt {
				t.Fatal("Computer respawned a child during backoff")
			}
			now.Store(base.Add(time.Duration(attempt) * RunnerRestartBackoff))
		}
	}
	now.Store(base.Add(time.Hour))
	supervisor.Reconcile(ctx, []string{"workspace-a"})
	if got := spawns.Load(); got != RunnerDegradedThreshold {
		t.Fatalf("degraded Binding auto-spawned: %d", got)
	}
}

func TestDaemonCoreIgnoresExitFromPreviousDaemonInstanceID(t *testing.T) {
	var nextPID atomic.Int32
	supervisor, err := newDaemonCore(daemonCoreConfig{
		Spawn: func(string) (BindingChild, error) {
			return newSupervisorTestChild(int(nextPID.Add(1)) + 300), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	supervisor.Reconcile(ctx, []string{"workspace-a"})
	waitForSupervisorLifecycle(t, supervisor, "workspace-a", RunnerLifecycleRunning)
	first, _, _ := supervisor.Snapshot("workspace-a")
	supervisor.Reconcile(ctx, nil)
	waitForSupervisorLifecycle(t, supervisor, "workspace-a", RunnerLifecycleStopped)
	supervisor.Reconcile(ctx, []string{"workspace-a"})
	waitForSupervisorLifecycle(t, supervisor, "workspace-a", RunnerLifecycleRunning)
	second, secondPID, _ := supervisor.Snapshot("workspace-a")
	if second.DaemonInstanceID() == first.DaemonInstanceID() {
		t.Fatal("re-added Binding reused a previous daemon instance")
	}

	supervisor.observeExit("workspace-a", first.DaemonInstanceID(), nil, RunnerExitCrash)
	current, currentPID, _ := supervisor.Snapshot("workspace-a")
	if current.DaemonInstanceID() != second.DaemonInstanceID() || current.Lifecycle != RunnerLifecycleRunning || currentPID != secondPID {
		t.Fatalf("stale exit mutated live child: record=%+v pid=%d", current, currentPID)
	}
	supervisor.Stop()
}

func TestDaemonCorePreparesEveryBindingForMachineControls(t *testing.T) {
	var prepares atomic.Int32
	var releases atomic.Int32
	var environmentPrepares atomic.Int32
	var environmentReleases atomic.Int32
	control := localControlTestServer(t, func(_ context.Context, operation string, _ map[string]string, raw json.RawMessage) (any, error) {
		switch operation {
		case LocalControlRunnerDrainOperation:
			prepares.Add(1)
		case LocalControlRunnerReleaseOperation:
			releases.Add(1)
		case LocalControlWorkspaceEnvironmentOperation:
			var request struct {
				Action string `json:"action"`
			}
			if err := json.Unmarshal(raw, &request); err != nil {
				return nil, err
			}
			if request.Action == "prepare" {
				environmentPrepares.Add(1)
			} else {
				environmentReleases.Add(1)
			}
		}
		return nil, nil
	})

	supervisor, err := newDaemonCore(daemonCoreConfig{
		Spawn: func(workspaceID string) (BindingChild, error) {
			pid := 401
			if workspaceID == "workspace-b" {
				pid = 402
			}
			return &readySupervisorChild{supervisorTestChild: newSupervisorTestChild(pid), controlEndpoint: control, workspaceID: workspaceID, daemonInstanceID: fmt.Sprintf("child-%d", pid)}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	supervisor.Reconcile(context.Background(), []string{"workspace-a", "workspace-b"})
	waitForSupervisorLifecycle(t, supervisor, "workspace-a", RunnerLifecycleRunning)
	waitForSupervisorLifecycle(t, supervisor, "workspace-b", RunnerLifecycleRunning)
	if err := supervisor.PrepareMachineUpgrade(context.Background(), "control-token"); err != nil {
		t.Fatalf("PrepareMachineUpgrade: %v", err)
	}
	if got := prepares.Load(); got != 2 {
		t.Fatalf("prepare calls = %d, want 2", got)
	}
	if err := supervisor.ReleaseMachineUpgrade(context.Background(), "control-token"); err != nil {
		t.Fatalf("ReleaseMachineUpgrade: %v", err)
	}
	if got := releases.Load(); got != 2 {
		t.Fatalf("release calls = %d, want 2", got)
	}
	if err := supervisor.PrepareEnvironmentSwitch(context.Background(), "control-token"); err != nil {
		t.Fatalf("PrepareEnvironmentSwitch: %v", err)
	}
	if got := environmentPrepares.Load(); got != 2 {
		t.Fatalf("environment prepare calls = %d, want 2", got)
	}
	if err := supervisor.ReleaseEnvironmentSwitch(context.Background(), "control-token"); err != nil {
		t.Fatalf("ReleaseEnvironmentSwitch: %v", err)
	}
	if got := environmentReleases.Load(); got != 2 {
		t.Fatalf("environment release calls = %d, want 2", got)
	}
	supervisor.Stop()
}

func TestDaemonCoreReleasesPreparedSiblingsWhenMachineUpgradePrepareFails(t *testing.T) {
	var preparedA atomic.Bool
	var releasedA atomic.Bool
	controlA := localControlTestServer(t, func(_ context.Context, operation string, _ map[string]string, _ json.RawMessage) (any, error) {
		switch operation {
		case LocalControlRunnerDrainOperation:
			preparedA.Store(true)
		case LocalControlRunnerReleaseOperation:
			releasedA.Store(true)
		}
		return nil, nil
	})

	controlB := localControlTestServer(t, func(context.Context, string, map[string]string, json.RawMessage) (any, error) {
		return nil, ErrComputerControlBusy
	})

	supervisor, err := newDaemonCore(daemonCoreConfig{
		Spawn: func(workspaceID string) (BindingChild, error) {
			controlEndpoint, pid := controlA, 501
			if workspaceID == "workspace-b" {
				controlEndpoint, pid = controlB, 502
			}
			return &readySupervisorChild{supervisorTestChild: newSupervisorTestChild(pid), controlEndpoint: controlEndpoint, workspaceID: workspaceID, daemonInstanceID: fmt.Sprintf("child-%d", pid)}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	supervisor.Reconcile(context.Background(), []string{"workspace-a", "workspace-b"})
	waitForSupervisorLifecycle(t, supervisor, "workspace-a", RunnerLifecycleRunning)
	waitForSupervisorLifecycle(t, supervisor, "workspace-b", RunnerLifecycleRunning)
	if err := supervisor.PrepareMachineUpgrade(context.Background(), "control-token"); err == nil {
		t.Fatal("PrepareMachineUpgrade succeeded after one Binding rejected preparation")
	}
	if !preparedA.Load() || !releasedA.Load() {
		t.Fatalf("prepared sibling state: prepared=%v released=%v", preparedA.Load(), releasedA.Load())
	}
	supervisor.Stop()
}

func TestDaemonCoreMachineUpgradeFailsForMissingDesiredChildButReleaseIsBestEffort(t *testing.T) {
	var prepares atomic.Int32
	var releases atomic.Int32
	control := localControlTestServer(t, func(_ context.Context, operation string, _ map[string]string, _ json.RawMessage) (any, error) {
		switch operation {
		case LocalControlRunnerDrainOperation:
			prepares.Add(1)
		case LocalControlRunnerReleaseOperation:
			releases.Add(1)
		}
		return nil, nil
	})

	supervisor, err := newDaemonCore(daemonCoreConfig{
		Spawn: func(workspaceID string) (BindingChild, error) {
			if workspaceID == "workspace-b" {
				return crashingSupervisorChild{pid: 702}, nil
			}
			return &readySupervisorChild{supervisorTestChild: newSupervisorTestChild(701), controlEndpoint: control, workspaceID: workspaceID, daemonInstanceID: "child-701"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	supervisor.Reconcile(context.Background(), []string{"workspace-a", "workspace-b"})
	waitForSupervisorLifecycle(t, supervisor, "workspace-a", RunnerLifecycleRunning)
	waitForSupervisorLifecycle(t, supervisor, "workspace-b", RunnerLifecycleCrashed)
	if err := supervisor.PrepareMachineUpgrade(context.Background(), "control-token"); err == nil {
		t.Fatal("Machine Upgrade prepared while one desired Binding child was absent")
	}
	if got := prepares.Load(); got != 0 {
		t.Fatalf("prepare calls = %d, want none before complete desired-set validation", got)
	}
	if err := supervisor.ReleaseMachineUpgrade(context.Background(), "control-token"); err != nil {
		t.Fatalf("best-effort release: %v", err)
	}
	if got := releases.Load(); got != 1 {
		t.Fatalf("release calls = %d, want live sibling only", got)
	}
	supervisor.Stop()
}

func TestComputerHostWaitsForRealBindingReady(t *testing.T) {
	control := localControlTestServer(t, func(context.Context, string, map[string]string, json.RawMessage) (any, error) {
		return nil, nil
	})

	host, err := NewComputerCore(ComputerCoreConfig{
		ControlToken: "control-token",
		Spawn: func(workspaceID string) (BindingChild, error) {
			return &readySupervisorChild{supervisorTestChild: newSupervisorTestChild(601), controlEndpoint: control, workspaceID: workspaceID, daemonInstanceID: "child-601"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	host.Reconcile(context.Background(), []string{"workspace-a"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := host.WaitReady(ctx, []string{"workspace-a"}); err != nil {
		t.Fatal(err)
	}
	record, _, _ := host.Snapshot("workspace-a")
	if !host.Current(BindingChildIdentity{WorkspaceID: "workspace-a", DaemonInstanceID: record.DaemonInstanceID(), PID: 601}) {
		t.Fatal("ComputerCore readiness did not preserve its process identity fence")
	}
	host.Stop()
}

func waitForSupervisorLifecycle(t *testing.T, supervisor *DaemonCore, workspaceID string, want RunnerLifecycle) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if record, _, ok := supervisor.Snapshot(workspaceID); ok && record.Lifecycle == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	record, pid, ok := supervisor.Snapshot(workspaceID)
	t.Fatalf("Binding %s lifecycle = %+v pid=%d exists=%v, want %s", workspaceID, record, pid, ok, want)
}
