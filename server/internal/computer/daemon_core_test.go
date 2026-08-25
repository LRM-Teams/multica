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

type workspaceDaemonTestProcess struct {
	pid      int
	wait     chan WorkspaceDaemonExitClass
	stopOnce sync.Once
}

type crashingWorkspaceDaemonTestProcess struct{ pid int }

type readyWorkspaceDaemonTestProcess struct {
	*workspaceDaemonTestProcess
	controlEndpoint  string
	workspaceID      string
	daemonInstanceID string
}

type activatingWorkspaceDaemonTestProcess struct {
	*workspaceDaemonTestProcess
	activate func()
}

type observedStopChild struct {
	pid     int
	stopped chan struct{}
	exited  chan struct{}
}

func (child *activatingWorkspaceDaemonTestProcess) Activate() { child.activate() }

func (child *readyWorkspaceDaemonTestProcess) AwaitReady(context.Context) (WorkspaceDaemonReady, error) {
	return WorkspaceDaemonReady{
		ProtocolVersion: WorkspaceDaemonProtocolVersion,
		WorkspaceID:     child.workspaceID, DaemonInstanceID: child.daemonInstanceID,
		PID:            child.pid,
		RunnerEndpoint: child.controlEndpoint,
	}, nil
}

func (child *workspaceDaemonTestProcess) AwaitReady(context.Context) (WorkspaceDaemonReady, error) {
	return WorkspaceDaemonReady{
		ProtocolVersion:  WorkspaceDaemonProtocolVersion,
		DaemonInstanceID: fmt.Sprintf("child-%d", child.pid),
		PID:              child.pid,
		RunnerEndpoint:   "unix:///tmp/multica-test-runner.sock",
	}, nil
}

func (child crashingWorkspaceDaemonTestProcess) PID() int { return child.pid }
func (crashingWorkspaceDaemonTestProcess) Wait() WorkspaceDaemonExitClass {
	return WorkspaceDaemonExitCrash
}
func (crashingWorkspaceDaemonTestProcess) Stop() error { return nil }

func newWorkspaceDaemonTestProcess(pid int) *workspaceDaemonTestProcess {
	return &workspaceDaemonTestProcess{pid: pid, wait: make(chan WorkspaceDaemonExitClass, 1)}
}

func (child *workspaceDaemonTestProcess) PID() int                       { return child.pid }
func (child *workspaceDaemonTestProcess) Wait() WorkspaceDaemonExitClass { return <-child.wait }
func (child *workspaceDaemonTestProcess) Stop() error {
	child.stopOnce.Do(func() { child.wait <- WorkspaceDaemonExitGraceful })
	return nil
}

func (child *observedStopChild) PID() int { return child.pid }
func (child *observedStopChild) AwaitReady(context.Context) (WorkspaceDaemonReady, error) {
	return WorkspaceDaemonReady{
		ProtocolVersion:  WorkspaceDaemonProtocolVersion,
		DaemonInstanceID: fmt.Sprintf("child-%d", child.pid),
		PID:              child.pid,
		RunnerEndpoint:   "unix:///tmp/multica-observed-stop.sock",
	}, nil
}
func (child *observedStopChild) Wait() WorkspaceDaemonExitClass {
	<-child.exited
	return WorkspaceDaemonExitGraceful
}
func (child *observedStopChild) Stop() error {
	close(child.stopped)
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

// testDrainRunner mimics how ComputerCore wires DaemonCoreConfig.DrainWorkspaceDaemon
// in production: close the control token over RequestWorkspaceDaemonDrain so
// reclaim tests exercise the real runner:drain RPC round trip.
func testDrainRunner(token string) func(context.Context, string, WorkspaceDaemonIdentity) error {
	return func(ctx context.Context, endpoint string, identity WorkspaceDaemonIdentity) error {
		return RequestWorkspaceDaemonDrain(ctx, endpoint, token, identity)
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

func TestDaemonCoreReclaimsDrainedOrphanThenSpawnsOwnProcess(t *testing.T) {
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
	daemonCore, err := NewDaemonCore(DaemonCoreConfig{
		StateRoot: root, DrainWorkspaceDaemon: testDrainRunner("control-token"),
		TerminatePollInterval: poll, TerminateGrace: grace,
		Spawn: func(string) (WorkspaceDaemonProcess, error) {
			spawned++
			return newWorkspaceDaemonTestProcess(101), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := drains.Load(); got != 1 {
		t.Fatalf("drain requests = %d, want 1", got)
	}
	if alive, known := processAlive(orphan.Process.Pid); known && alive {
		t.Fatal("orphaned WorkspaceDaemon process is still alive after reclaim")
	}
	if _, err := os.Stat(runnerStatePath(root, "workspace-a")); !os.IsNotExist(err) {
		t.Fatalf("reclaimed runner state was not cleared: %v", err)
	}

	daemonCore.Reconcile(context.Background(), []string{"workspace-a"})
	waitForDaemonStatus(t, daemonCore, "workspace-a", WorkspaceDaemonRunning)
	if spawned != 1 {
		t.Fatalf("spawned %d children after reclaiming an orphan, want 1", spawned)
	}
	record, pid, ok := daemonCore.Snapshot("workspace-a")
	if !ok || !daemonCore.Current(WorkspaceDaemonIdentity{WorkspaceID: "workspace-a", DaemonInstanceID: record.DaemonInstanceID, PID: pid}) {
		t.Fatalf("Computer did not recognize its own freshly spawned child after reclaim: record=%+v pid=%d", record, pid)
	}
	daemonCore.Stop()
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
	daemonCore, err := NewDaemonCore(DaemonCoreConfig{
		StateRoot: root, DrainWorkspaceDaemon: testDrainRunner("control-token"),
		TerminatePollInterval: poll, TerminateGrace: grace,
		Spawn: func(string) (WorkspaceDaemonProcess, error) {
			spawned++
			return newWorkspaceDaemonTestProcess(102), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if alive, known := processAlive(orphan.Process.Pid); known && alive {
		t.Fatal("orphaned WorkspaceDaemon process is still alive after reclaim")
	}

	daemonCore.Reconcile(context.Background(), []string{"workspace-a"})
	waitForDaemonStatus(t, daemonCore, "workspace-a", WorkspaceDaemonRunning)
	if spawned != 1 {
		t.Fatalf("spawned %d children after reclaiming an unreachable orphan, want 1", spawned)
	}
	daemonCore.Stop()
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
	daemonCore, err := NewDaemonCore(DaemonCoreConfig{
		StateRoot: root, DrainWorkspaceDaemon: testDrainRunner("control-token"),
		TerminatePollInterval: poll, TerminateGrace: grace,
		Spawn: func(string) (WorkspaceDaemonProcess, error) {
			spawned++
			return newWorkspaceDaemonTestProcess(103), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if alive, known := processAlive(orphan.Process.Pid); known && alive {
		t.Fatal("orphaned WorkspaceDaemon process ignoring SIGTERM survived reclaim (SIGKILL escalation did not fire)")
	}

	daemonCore.Reconcile(context.Background(), []string{"workspace-a"})
	waitForDaemonStatus(t, daemonCore, "workspace-a", WorkspaceDaemonRunning)
	if spawned != 1 {
		t.Fatalf("spawned %d children after SIGKILL-reclaiming an orphan, want 1", spawned)
	}
	daemonCore.Stop()
}

func TestDaemonCoreLeavesPredecessorOwnerAloneAndDoesNotReclaim(t *testing.T) {
	root := t.TempDir()
	orphan := spawnReclaimTestProcess(t, "sleep", "30")
	// OwnerPID is this test process's own pid: a live owner means a second
	// Computer generation is (impossibly, but defensively) still running, so
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
	daemonCore, err := NewDaemonCore(DaemonCoreConfig{
		StateRoot: root,
		Spawn: func(string) (WorkspaceDaemonProcess, error) {
			spawned++
			return newWorkspaceDaemonTestProcess(104), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if alive, known := processAlive(orphan.Process.Pid); !known || !alive {
		t.Fatal("DaemonCore killed a WorkspaceDaemon owned by a live predecessor")
	}
	if _, err := os.Stat(runnerStatePath(root, "workspace-a")); err != nil {
		t.Fatalf("live-owner runner state was removed: %v", err)
	}
	if _, _, ok := daemonCore.Snapshot("workspace-a"); ok {
		t.Fatal("DaemonCore recorded a Workspace owned by a live predecessor")
	}
	if spawned != 0 {
		t.Fatalf("spawned %d WorkspaceDaemons while a predecessor Computer is still alive", spawned)
	}
}

func TestDaemonCoreRegistersProcessBeforeActivation(t *testing.T) {
	activated := make(chan bool, 1)
	var daemonCore *DaemonCore
	var err error
	daemonCore, err = NewDaemonCore(DaemonCoreConfig{
		Spawn: func(workspaceID string) (WorkspaceDaemonProcess, error) {
			const pid = 101
			return &activatingWorkspaceDaemonTestProcess{
				workspaceDaemonTestProcess: newWorkspaceDaemonTestProcess(pid),
				activate: func() {
					activated <- daemonCore.Current(WorkspaceDaemonIdentity{
						WorkspaceID: workspaceID, DaemonInstanceID: fmt.Sprintf("child-%d", pid), PID: pid,
					})
				},
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	daemonCore.Reconcile(context.Background(), []string{"workspace-a"})
	select {
	case current := <-activated:
		if !current {
			t.Fatal("WorkspaceDaemon process activated before DaemonCore registration")
		}
	case <-time.After(time.Second):
		t.Fatal("WorkspaceDaemon process was not activated")
	}
	daemonCore.Stop()
}

func TestDaemonCoreStopWaitsForWorkspaceDaemonExit(t *testing.T) {
	child := &observedStopChild{pid: 101, stopped: make(chan struct{}), exited: make(chan struct{})}
	daemonCore, err := NewDaemonCore(DaemonCoreConfig{
		Spawn: func(string) (WorkspaceDaemonProcess, error) { return child, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	daemonCore.Reconcile(context.Background(), []string{"workspace-a"})
	waitForDaemonStatus(t, daemonCore, "workspace-a", WorkspaceDaemonRunning)

	stopDone := make(chan struct{})
	go func() {
		daemonCore.Stop()
		close(stopDone)
	}()
	select {
	case <-child.stopped:
	case <-time.After(time.Second):
		t.Fatal("Daemon did not ask WorkspaceDaemon to stop")
	}
	select {
	case <-stopDone:
		t.Fatal("Daemon reported stopped before WorkspaceDaemon exited")
	case <-time.After(20 * time.Millisecond):
	}
	close(child.exited)
	select {
	case <-stopDone:
	case <-time.After(time.Second):
		t.Fatal("Daemon did not report stopped after WorkspaceDaemon exited")
	}
}

func TestDaemonCoreDoesNotReplaceWorkspaceDaemonBeforeExit(t *testing.T) {
	first := &observedStopChild{pid: 101, stopped: make(chan struct{}), exited: make(chan struct{})}
	second := newWorkspaceDaemonTestProcess(102)
	var spawns atomic.Int32
	daemonCore, err := NewDaemonCore(DaemonCoreConfig{
		Spawn: func(string) (WorkspaceDaemonProcess, error) {
			if spawns.Add(1) == 1 {
				return first, nil
			}
			return second, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	daemonCore.Reconcile(context.Background(), []string{"workspace-a"})
	waitForDaemonStatus(t, daemonCore, "workspace-a", WorkspaceDaemonRunning)

	daemonCore.Reconcile(context.Background(), nil)
	<-first.stopped
	daemonCore.Reconcile(context.Background(), []string{"workspace-a"})
	if got := spawns.Load(); got != 1 {
		t.Fatalf("spawned replacement before previous WorkspaceDaemon exited: %d", got)
	}

	close(first.exited)
	waitForDaemonStatus(t, daemonCore, "workspace-a", WorkspaceDaemonStopped)
	daemonCore.Reconcile(context.Background(), []string{"workspace-a"})
	waitForDaemonStatus(t, daemonCore, "workspace-a", WorkspaceDaemonRunning)
	if got := spawns.Load(); got != 2 {
		t.Fatalf("spawns after previous WorkspaceDaemon exited = %d, want 2", got)
	}
	daemonCore.Stop()
}

func TestDaemonCoreRetainsSiblingAndFencesDaemonInstanceIDPID(t *testing.T) {
	var nextPID atomic.Int32
	children := make(map[string]*workspaceDaemonTestProcess)
	var childrenMu sync.Mutex
	daemonCore, err := NewDaemonCore(DaemonCoreConfig{
		Spawn: func(workspaceID string) (WorkspaceDaemonProcess, error) {
			child := newWorkspaceDaemonTestProcess(int(nextPID.Add(1)) + 100)
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
	daemonCore.Reconcile(ctx, []string{"workspace-a", "workspace-b"})
	waitForDaemonStatus(t, daemonCore, "workspace-a", WorkspaceDaemonRunning)
	waitForDaemonStatus(t, daemonCore, "workspace-b", WorkspaceDaemonRunning)
	recordA, pidA, _ := daemonCore.Snapshot("workspace-a")
	recordB, pidB, _ := daemonCore.Snapshot("workspace-b")
	if !daemonCore.Current(WorkspaceDaemonIdentity{WorkspaceID: "workspace-a", DaemonInstanceID: recordA.DaemonInstanceID, PID: pidA}) {
		t.Fatal("Computer rejected its current WorkspaceDaemon")
	}
	if daemonCore.Current(WorkspaceDaemonIdentity{WorkspaceID: "workspace-a", DaemonInstanceID: "stale-start", PID: pidA}) {
		t.Fatal("Computer accepted a stale WorkspaceDaemon instance")
	}

	daemonCore.Reconcile(ctx, []string{"workspace-a"})
	waitForDaemonStatus(t, daemonCore, "workspace-b", WorkspaceDaemonStopped)
	currentA, currentPIDA, _ := daemonCore.Snapshot("workspace-a")
	if currentA.DaemonInstanceID != recordA.DaemonInstanceID || currentA.Status != WorkspaceDaemonRunning || currentPIDA != pidA {
		t.Fatalf("removing sibling mutated workspace-a: record=%+v pid=%d", currentA, currentPIDA)
	}
	if recordB.DaemonInstanceID == "" || pidB == 0 {
		t.Fatal("workspace-b never had a real WorkspaceDaemon process identity")
	}
	daemonCore.Stop()
}

func TestDaemonCoreBacksOffThenDegradesCrashLoop(t *testing.T) {
	base := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	var now atomic.Value
	now.Store(base)
	var spawns atomic.Int32
	daemonCore, err := NewDaemonCore(DaemonCoreConfig{
		Now: func() time.Time { return now.Load().(time.Time) },
		Spawn: func(string) (WorkspaceDaemonProcess, error) {
			return crashingWorkspaceDaemonTestProcess{pid: int(spawns.Add(1)) + 200}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for attempt := int32(1); attempt <= WorkspaceDaemonCrashLimit; attempt++ {
		daemonCore.Reconcile(ctx, []string{"workspace-a"})
		want := WorkspaceDaemonCrashed
		if attempt == WorkspaceDaemonCrashLimit {
			want = WorkspaceDaemonDegraded
		}
		waitForDaemonStatus(t, daemonCore, "workspace-a", want)
		if got := spawns.Load(); got != attempt {
			t.Fatalf("spawns after attempt %d = %d", attempt, got)
		}
		if want != WorkspaceDaemonDegraded {
			daemonCore.Reconcile(ctx, []string{"workspace-a"})
			if got := spawns.Load(); got != attempt {
				t.Fatal("Computer respawned a child during backoff")
			}
			now.Store(base.Add(time.Duration(attempt) * WorkspaceDaemonRestartBackoff))
		}
	}
	now.Store(base.Add(time.Hour))
	daemonCore.Reconcile(ctx, []string{"workspace-a"})
	if got := spawns.Load(); got != WorkspaceDaemonCrashLimit {
		t.Fatalf("degraded WorkspaceDaemon auto-spawned: %d", got)
	}
}

func TestDaemonCoreIgnoresExitFromPreviousDaemonInstanceID(t *testing.T) {
	var nextPID atomic.Int32
	daemonCore, err := NewDaemonCore(DaemonCoreConfig{
		Spawn: func(string) (WorkspaceDaemonProcess, error) {
			return newWorkspaceDaemonTestProcess(int(nextPID.Add(1)) + 300), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	daemonCore.Reconcile(ctx, []string{"workspace-a"})
	waitForDaemonStatus(t, daemonCore, "workspace-a", WorkspaceDaemonRunning)
	first, _, _ := daemonCore.Snapshot("workspace-a")
	daemonCore.Reconcile(ctx, nil)
	waitForDaemonStatus(t, daemonCore, "workspace-a", WorkspaceDaemonStopped)
	daemonCore.Reconcile(ctx, []string{"workspace-a"})
	waitForDaemonStatus(t, daemonCore, "workspace-a", WorkspaceDaemonRunning)
	second, secondPID, _ := daemonCore.Snapshot("workspace-a")
	if second.DaemonInstanceID == first.DaemonInstanceID {
		t.Fatal("re-added Workspace reused a previous WorkspaceDaemon instance")
	}

	daemonCore.observeExit("workspace-a", first.DaemonInstanceID, nil, WorkspaceDaemonExitCrash)
	current, currentPID, _ := daemonCore.Snapshot("workspace-a")
	if current.DaemonInstanceID != second.DaemonInstanceID || current.Status != WorkspaceDaemonRunning || currentPID != secondPID {
		t.Fatalf("stale exit mutated live child: record=%+v pid=%d", current, currentPID)
	}
	daemonCore.Stop()
}

func TestDaemonCorePreparesEveryWorkspaceDaemonForMachineControls(t *testing.T) {
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

	daemonCore, err := NewDaemonCore(DaemonCoreConfig{
		Spawn: func(workspaceID string) (WorkspaceDaemonProcess, error) {
			pid := 401
			if workspaceID == "workspace-b" {
				pid = 402
			}
			return &readyWorkspaceDaemonTestProcess{workspaceDaemonTestProcess: newWorkspaceDaemonTestProcess(pid), controlEndpoint: control, workspaceID: workspaceID, daemonInstanceID: fmt.Sprintf("child-%d", pid)}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	daemonCore.Reconcile(context.Background(), []string{"workspace-a", "workspace-b"})
	waitForDaemonStatus(t, daemonCore, "workspace-a", WorkspaceDaemonRunning)
	waitForDaemonStatus(t, daemonCore, "workspace-b", WorkspaceDaemonRunning)
	if err := daemonCore.PrepareMachineUpgrade(context.Background(), "control-token"); err != nil {
		t.Fatalf("PrepareMachineUpgrade: %v", err)
	}
	if got := prepares.Load(); got != 2 {
		t.Fatalf("prepare calls = %d, want 2", got)
	}
	if err := daemonCore.ReleaseMachineUpgrade(context.Background(), "control-token"); err != nil {
		t.Fatalf("ReleaseMachineUpgrade: %v", err)
	}
	if got := releases.Load(); got != 2 {
		t.Fatalf("release calls = %d, want 2", got)
	}
	if err := daemonCore.PrepareEnvironmentSwitch(context.Background(), "control-token"); err != nil {
		t.Fatalf("PrepareEnvironmentSwitch: %v", err)
	}
	if got := environmentPrepares.Load(); got != 2 {
		t.Fatalf("environment prepare calls = %d, want 2", got)
	}
	if err := daemonCore.ReleaseEnvironmentSwitch(context.Background(), "control-token"); err != nil {
		t.Fatalf("ReleaseEnvironmentSwitch: %v", err)
	}
	if got := environmentReleases.Load(); got != 2 {
		t.Fatalf("environment release calls = %d, want 2", got)
	}
	daemonCore.Stop()
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

	daemonCore, err := NewDaemonCore(DaemonCoreConfig{
		Spawn: func(workspaceID string) (WorkspaceDaemonProcess, error) {
			controlEndpoint, pid := controlA, 501
			if workspaceID == "workspace-b" {
				controlEndpoint, pid = controlB, 502
			}
			return &readyWorkspaceDaemonTestProcess{workspaceDaemonTestProcess: newWorkspaceDaemonTestProcess(pid), controlEndpoint: controlEndpoint, workspaceID: workspaceID, daemonInstanceID: fmt.Sprintf("child-%d", pid)}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	daemonCore.Reconcile(context.Background(), []string{"workspace-a", "workspace-b"})
	waitForDaemonStatus(t, daemonCore, "workspace-a", WorkspaceDaemonRunning)
	waitForDaemonStatus(t, daemonCore, "workspace-b", WorkspaceDaemonRunning)
	if err := daemonCore.PrepareMachineUpgrade(context.Background(), "control-token"); err == nil {
		t.Fatal("PrepareMachineUpgrade succeeded after one WorkspaceDaemon rejected preparation")
	}
	if !preparedA.Load() || !releasedA.Load() {
		t.Fatalf("prepared sibling state: prepared=%v released=%v", preparedA.Load(), releasedA.Load())
	}
	daemonCore.Stop()
}

func TestDaemonCoreMachineUpgradeFailsForMissingDesiredProcessButReleaseIsBestEffort(t *testing.T) {
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

	daemonCore, err := NewDaemonCore(DaemonCoreConfig{
		Spawn: func(workspaceID string) (WorkspaceDaemonProcess, error) {
			if workspaceID == "workspace-b" {
				return crashingWorkspaceDaemonTestProcess{pid: 702}, nil
			}
			return &readyWorkspaceDaemonTestProcess{workspaceDaemonTestProcess: newWorkspaceDaemonTestProcess(701), controlEndpoint: control, workspaceID: workspaceID, daemonInstanceID: "child-701"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	daemonCore.Reconcile(context.Background(), []string{"workspace-a", "workspace-b"})
	waitForDaemonStatus(t, daemonCore, "workspace-a", WorkspaceDaemonRunning)
	waitForDaemonStatus(t, daemonCore, "workspace-b", WorkspaceDaemonCrashed)
	if err := daemonCore.PrepareMachineUpgrade(context.Background(), "control-token"); err == nil {
		t.Fatal("Machine Upgrade prepared while one desired WorkspaceDaemon was absent")
	}
	if got := prepares.Load(); got != 0 {
		t.Fatalf("prepare calls = %d, want none before complete desired-set validation", got)
	}
	if err := daemonCore.ReleaseMachineUpgrade(context.Background(), "control-token"); err != nil {
		t.Fatalf("best-effort release: %v", err)
	}
	if got := releases.Load(); got != 1 {
		t.Fatalf("release calls = %d, want live sibling only", got)
	}
	daemonCore.Stop()
}

func TestComputerCoreWaitsForWorkspaceDaemonReady(t *testing.T) {
	control := localControlTestServer(t, func(context.Context, string, map[string]string, json.RawMessage) (any, error) {
		return nil, nil
	})

	computerCore, err := NewComputerCore(ComputerCoreConfig{
		ControlToken: "control-token",
		Spawn: func(workspaceID string) (WorkspaceDaemonProcess, error) {
			return &readyWorkspaceDaemonTestProcess{workspaceDaemonTestProcess: newWorkspaceDaemonTestProcess(601), controlEndpoint: control, workspaceID: workspaceID, daemonInstanceID: "child-601"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	computerCore.Reconcile(context.Background(), []string{"workspace-a"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := computerCore.WaitReady(ctx, []string{"workspace-a"}); err != nil {
		t.Fatal(err)
	}
	record, _, _ := computerCore.Snapshot("workspace-a")
	if !computerCore.Current(WorkspaceDaemonIdentity{WorkspaceID: "workspace-a", DaemonInstanceID: record.DaemonInstanceID, PID: 601}) {
		t.Fatal("ComputerCore readiness did not preserve its process identity fence")
	}
	computerCore.Stop()
}

func waitForDaemonStatus(t *testing.T, daemonCore *DaemonCore, workspaceID string, want WorkspaceDaemonStatus) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if record, _, ok := daemonCore.Snapshot(workspaceID); ok && record.Status == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	record, pid, ok := daemonCore.Snapshot(workspaceID)
	t.Fatalf("WorkspaceDaemon %s lifecycle = %+v pid=%d exists=%v, want %s", workspaceID, record, pid, ok, want)
}
