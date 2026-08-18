package computer

import (
	"context"
	"encoding/json"
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
	controlEndpoint string
	workspaceID     string
	startIdentity   string
}

type activatingSupervisorChild struct {
	*supervisorTestChild
	activate func()
}

func (child *activatingSupervisorChild) Activate() { child.activate() }

func (child *readySupervisorChild) AwaitReady(context.Context) (BindingChildReady, error) {
	return BindingChildReady{
		ProtocolVersion: BindingChildProtocolVersion,
		WorkspaceID:     child.workspaceID, StartIdentity: child.startIdentity,
		PID:            child.pid,
		RunnerEndpoint: child.controlEndpoint,
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

func TestBindingSupervisorRegistersChildBeforeActivation(t *testing.T) {
	activated := make(chan bool, 1)
	var supervisor *BindingSupervisor
	var err error
	supervisor, err = NewBindingSupervisor(BindingSupervisorConfig{
		Spawn: func(workspaceID, startIdentity string) (BindingChild, error) {
			const pid = 101
			return &activatingSupervisorChild{
				supervisorTestChild: newSupervisorTestChild(pid),
				activate: func() {
					activated <- supervisor.Current(BindingChildIdentity{
						WorkspaceID: workspaceID, StartIdentity: startIdentity, PID: pid,
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

func TestBindingSupervisorRetainsSiblingAndFencesStartIdentityPID(t *testing.T) {
	var nextPID atomic.Int32
	children := make(map[string]*supervisorTestChild)
	var childrenMu sync.Mutex
	supervisor, err := NewBindingSupervisor(BindingSupervisorConfig{
		Spawn: func(workspaceID, _ string) (BindingChild, error) {
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
	if !supervisor.Current(BindingChildIdentity{WorkspaceID: "workspace-a", StartIdentity: recordA.StartIdentity(), PID: pidA}) {
		t.Fatal("Computer rejected its current Binding child")
	}
	if supervisor.Current(BindingChildIdentity{WorkspaceID: "workspace-a", StartIdentity: "stale-start", PID: pidA}) {
		t.Fatal("Computer accepted a stale Runner start identity")
	}

	supervisor.Reconcile(ctx, []string{"workspace-a"})
	waitForSupervisorLifecycle(t, supervisor, "workspace-b", RunnerLifecycleStopped)
	currentA, currentPIDA, _ := supervisor.Snapshot("workspace-a")
	if currentA.StartIdentity() != recordA.StartIdentity() || currentA.Lifecycle != RunnerLifecycleRunning || currentPIDA != pidA {
		t.Fatalf("removing sibling mutated workspace-a: record=%+v pid=%d", currentA, currentPIDA)
	}
	if recordB.StartIdentity() == "" || pidB == 0 {
		t.Fatal("workspace-b never had a real supervised child identity")
	}
	supervisor.Stop()
}

func TestBindingSupervisorBacksOffThenDegradesCrashLoop(t *testing.T) {
	base := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	var now atomic.Value
	now.Store(base)
	var spawns atomic.Int32
	supervisor, err := NewBindingSupervisor(BindingSupervisorConfig{
		Now: func() time.Time { return now.Load().(time.Time) },
		Spawn: func(string, string) (BindingChild, error) {
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

func TestBindingSupervisorIgnoresExitFromPreviousStartIdentity(t *testing.T) {
	var nextPID atomic.Int32
	supervisor, err := NewBindingSupervisor(BindingSupervisorConfig{
		Spawn: func(string, string) (BindingChild, error) {
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
	if second.StartIdentity() == first.StartIdentity() {
		t.Fatal("re-added Binding reused a previous start identity")
	}

	supervisor.observeExit("workspace-a", first.StartIdentity(), nil, RunnerExitCrash)
	current, currentPID, _ := supervisor.Snapshot("workspace-a")
	if current.StartIdentity() != second.StartIdentity() || current.Lifecycle != RunnerLifecycleRunning || currentPID != secondPID {
		t.Fatalf("stale exit mutated live child: record=%+v pid=%d", current, currentPID)
	}
	supervisor.Stop()
}

func TestBindingSupervisorPreparesEveryBindingForMachineControls(t *testing.T) {
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

	supervisor, err := NewBindingSupervisor(BindingSupervisorConfig{
		Spawn: func(workspaceID, _ string) (BindingChild, error) {
			pid := 401
			if workspaceID == "workspace-b" {
				pid = 402
			}
			return &readySupervisorChild{supervisorTestChild: newSupervisorTestChild(pid), controlEndpoint: control}, nil
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

func TestBindingSupervisorReleasesPreparedSiblingsWhenMachineUpgradePrepareFails(t *testing.T) {
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

	supervisor, err := NewBindingSupervisor(BindingSupervisorConfig{
		Spawn: func(workspaceID, _ string) (BindingChild, error) {
			controlEndpoint, pid := controlA, 501
			if workspaceID == "workspace-b" {
				controlEndpoint, pid = controlB, 502
			}
			return &readySupervisorChild{supervisorTestChild: newSupervisorTestChild(pid), controlEndpoint: controlEndpoint}, nil
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

func TestBindingSupervisorMachineUpgradeFailsForMissingDesiredChildButReleaseIsBestEffort(t *testing.T) {
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

	supervisor, err := NewBindingSupervisor(BindingSupervisorConfig{
		Spawn: func(workspaceID, _ string) (BindingChild, error) {
			if workspaceID == "workspace-b" {
				return crashingSupervisorChild{pid: 702}, nil
			}
			return &readySupervisorChild{supervisorTestChild: newSupervisorTestChild(701), controlEndpoint: control}, nil
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

	host, err := NewHost(HostConfig{
		ControlToken: "control-token",
		Spawn: func(workspaceID, startIdentity string) (BindingChild, error) {
			return &readySupervisorChild{supervisorTestChild: newSupervisorTestChild(601), controlEndpoint: control, workspaceID: workspaceID, startIdentity: startIdentity}, nil
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
	if !host.Current(BindingChildIdentity{WorkspaceID: "workspace-a", StartIdentity: record.StartIdentity(), PID: 601}) {
		t.Fatal("Computer Host readiness did not preserve its process identity fence")
	}
	host.Stop()
}

func waitForSupervisorLifecycle(t *testing.T, supervisor *BindingSupervisor, workspaceID string, want RunnerLifecycle) {
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
