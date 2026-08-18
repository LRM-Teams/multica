package computer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestHostMachineUpgradeJournalIsPrivateAndRoundTrips(t *testing.T) {
	upgrade := newHostMachineUpgrade(&Host{}, hostMachineUpgradeConfig{residentRoot: t.TempDir()})
	want := hostMachineUpgradeJournal{
		RequestID: "request-a", FromVersion: "v1.0.0", TargetVersion: "v1.1.0",
		StartedAt: "2026-08-17T00:00:00Z", SchemaVersion: 1, SourceServicePID: 101,
		AcceptedManagedWorkspaceIDs: []string{"workspace-a"}, AcceptedManagedSetRevision: managedSetRevision([]string{"workspace-a"}),
	}
	if err := upgrade.writeJournal(want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(upgrade.journalPath())
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("Machine Upgrade journal permissions = %o, want 600", got)
	}
	raw, err := os.ReadFile(upgrade.journalPath())
	if err != nil {
		t.Fatal(err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"requestId", "fromVersion", "targetVersion", "startedAt", "schemaVersion", "sourceServicePid", "acceptedManagedSetRevision"} {
		if _, ok := persisted[key]; !ok {
			t.Fatalf("journal missing Raft field %q: %s", key, raw)
		}
	}
	got, err := upgrade.readJournal()
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.TargetVersion != want.TargetVersion || got.RequestID != want.RequestID || got.SchemaVersion != want.SchemaVersion {
		t.Fatalf("Machine Upgrade journal = %+v, want %+v", got, want)
	}
}

func TestHostMachineUpgradeSameOperationIsIgnoredWhileActive(t *testing.T) {
	identity := BindingChildIdentity{WorkspaceID: "workspace-a", StartIdentity: "start-3", PID: 8051}
	host := &Host{runtimeSets: map[string]hostBindingRuntimeSet{
		"workspace-a": {
			Identity: identity,
			Runtimes: []hostBindingRuntime{
				{ID: "runtime-a", WorkspaceID: "workspace-a", Provider: "pi"},
			},
			DaemonToken: "runtime-token",
			ExpiresAt:   time.Now().Add(time.Hour),
		},
	}}
	upgrade := newHostMachineUpgrade(host, hostMachineUpgradeConfig{})
	upgrade.activeID = "upgrade-a"

	raw, err := json.Marshal(protocol.DaemonHeartbeatAckPayload{
		PendingMachineUpgrade: &protocol.DaemonHeartbeatPendingMachineUpgrade{ID: "upgrade-a", TargetVersion: "v9.9.9"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := upgrade.handleChildAction(context.Background(), identity, raw); err != nil {
		t.Fatalf("forwarded heartbeat upgrade = %v", err)
	}
	if upgrade.activeID != "upgrade-a" {
		t.Fatalf("activeID = %q, want upgrade-a", upgrade.activeID)
	}
}

func TestHostMachineUpgradeDifferentOperationReturnsBusy(t *testing.T) {
	identity := BindingChildIdentity{WorkspaceID: "workspace-a", StartIdentity: "start-3", PID: 8051}
	host := &Host{runtimeSets: map[string]hostBindingRuntimeSet{
		"workspace-a": {
			Identity: identity,
			Runtimes: []hostBindingRuntime{
				{ID: "runtime-a", WorkspaceID: "workspace-a", Provider: "pi"},
			},
			DaemonToken: "runtime-token",
			ExpiresAt:   time.Now().Add(time.Hour),
		},
	}}
	upgrade := newHostMachineUpgrade(host, hostMachineUpgradeConfig{})
	upgrade.activeID = "upgrade-a"

	raw, err := json.Marshal(protocol.DaemonHeartbeatAckPayload{
		PendingMachineUpgrade: &protocol.DaemonHeartbeatPendingMachineUpgrade{ID: "upgrade-b", TargetVersion: "v10.0.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := upgrade.handleChildAction(context.Background(), identity, raw); err != nil {
		t.Fatalf("forwarded heartbeat upgrade = %v, want ignore", err)
	}
}

func TestHostControlForwardsComputerControlBusy(t *testing.T) {
	identity := BindingChildIdentity{WorkspaceID: "workspace-a", StartIdentity: "start-3", PID: 8051}
	control := NewHostControl("owner-secret", NewProcessCapacity(1), HostControlCallbacks{
		Current: func(got BindingChildIdentity) bool { return got == identity },
		MachineActions: func(context.Context, BindingChildIdentity, json.RawMessage) error {
			return ErrComputerControlBusy
		},
	})
	registry := NewLocalControlRegistry()
	control.RegisterRPCHandlers(registry)
	endpoint := localControlTestServer(t, func(ctx context.Context, operation string, headers map[string]string, raw json.RawMessage) (any, error) {
		handler, ok := registry.handler(operation)
		if !ok {
			return nil, fmt.Errorf("unknown operation %s", operation)
		}
		return handler(ctx, headers, raw)
	})

	client := NewHostControlClient(endpoint, "owner-secret", identity)
	err := client.ForwardComputerControl(context.Background(), protocol.DaemonHeartbeatAckPayload{
		PendingMachineUpgrade: &protocol.DaemonHeartbeatPendingMachineUpgrade{ID: "upgrade-b", TargetVersion: "v10.0.0"},
	})
	if !errors.Is(err, ErrComputerControlBusy) {
		t.Fatalf("ForwardComputerControl = %v, want ErrComputerControlBusy", err)
	}
}

func TestHostMachineUpgradeEmptyRuntimeUsesCurrentBindingRuntime(t *testing.T) {
	identity := BindingChildIdentity{WorkspaceID: "workspace-a", StartIdentity: "start-3", PID: 8051}
	host := &Host{runtimeSets: map[string]hostBindingRuntimeSet{
		"workspace-a": {
			Identity: identity,
			Runtimes: []hostBindingRuntime{
				{ID: "runtime-a", WorkspaceID: "workspace-a", Provider: "pi"},
			},
			DaemonToken: "runtime-token",
			ExpiresAt:   time.Now().Add(time.Hour),
		},
	}}
	upgrade := newHostMachineUpgrade(host, hostMachineUpgradeConfig{})

	runtime, token, ok := upgrade.currentRuntime(identity, "")
	if !ok {
		t.Fatal("current Binding child could not resolve its runtime for a connect-socket machine action")
	}
	if runtime.ID != "runtime-a" || token != "runtime-token" {
		t.Fatalf("resolved runtime = %+v token=%q", runtime, token)
	}
}

func TestHostMachineUpgradePreparesEveryChildAndSuccessorConverges(t *testing.T) {
	const controlToken = "owner-secret"
	workspaceIDs := []string{"workspace-a", "workspace-b"}
	runtimeIDs := []string{"runtime-a", "runtime-b"}
	var prepares atomic.Int32
	var reregistrations atomic.Int32
	childControl := localControlTestServer(t, func(_ context.Context, operation string, headers map[string]string, _ json.RawMessage) (any, error) {
		if headers["X-Multica-Control-Token"] != controlToken {
			return nil, errors.New("bad token")
		}
		switch operation {
		case LocalControlRunnerDrainOperation:
			prepares.Add(1)
		case "runner-ready":
			reregistrations.Add(1)
		}
		return nil, nil
	})

	newReadyHost := func(pidBase int) *Host {
		host, err := NewHost(HostConfig{
			ControlToken: controlToken,
			Spawn: func(workspaceID, startIdentity string) (BindingChild, error) {
				pid := pidBase
				if workspaceID == workspaceIDs[1] {
					pid++
				}
				return &readySupervisorChild{
					supervisorTestChild: newSupervisorTestChild(pid), controlEndpoint: childControl,
					workspaceID: workspaceID, startIdentity: startIdentity,
				}, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		host.Reconcile(context.Background(), workspaceIDs)
		for _, workspaceID := range workspaceIDs {
			waitForSupervisorLifecycle(t, host.supervisor, workspaceID, RunnerLifecycleRunning)
		}
		host.runtimeMu.Lock()
		for index, workspaceID := range workspaceIDs {
			record, pid, ok := host.Snapshot(workspaceID)
			if !ok {
				t.Fatalf("missing Binding %s", workspaceID)
			}
			host.runtimeSets[workspaceID] = hostBindingRuntimeSet{
				Identity:    BindingChildIdentity{WorkspaceID: workspaceID, StartIdentity: record.StartIdentity(), PID: pid},
				Runtimes:    []hostBindingRuntime{{ID: runtimeIDs[index], WorkspaceID: workspaceID, Provider: "pi"}},
				DaemonToken: "runtime-token-" + workspaceID, ExpiresAt: time.Now().Add(time.Hour),
			}
		}
		host.runtimeMu.Unlock()
		return host
	}

	root := t.TempDir()
	incumbent := newReadyHost(8101)
	upgradeCancelled := make(chan struct{}, 1)
	upgrade := newHostMachineUpgrade(incumbent, hostMachineUpgradeConfig{
		identity: HostProcessIdentity{
			ComputerID: "computer-a", ServiceGeneration: "service-31", Environment: "test",
			Version: "v1.0.0",
		},
		residentRoot: root,
		cancel:       func() { upgradeCancelled <- struct{}{} },
	})
	incumbent.upgrade = upgrade
	var staged, verified, swapped atomic.Bool
	upgrade.stageRelease = func(target string, _ time.Duration, _ string) (string, error) {
		if target != "v2.0.0" {
			t.Fatalf("stage target = %q", target)
		}
		staged.Store(true)
		return "/tmp/staged-computer", nil
	}
	upgrade.verifyBinary = func(_ context.Context, path, target string) error {
		if path != "/tmp/staged-computer" || target != "v2.0.0" {
			t.Fatalf("verify path/target = %q/%q", path, target)
		}
		verified.Store(true)
		return nil
	}
	upgrade.installPath = func() (string, error) { return "/tmp/active-computer", nil }
	upgrade.swapExecutable = func(current, candidate string) error {
		if current != "/tmp/active-computer" || candidate != "/tmp/staged-computer" {
			t.Fatalf("swap current/candidate = %q/%q", current, candidate)
		}
		swapped.Store(true)
		return nil
	}
	if err := upgrade.startServiceUpgrade(incumbent.runtimeSets[workspaceIDs[0]].Identity, protocol.ComputerUpgradePayload{
		RequestID: "upgrade-a", TargetVersion: "v2.0.0",
	}); err != nil {
		t.Fatalf("start service upgrade: %v", err)
	}
	select {
	case <-upgradeCancelled:
	case <-time.After(time.Second):
		t.Fatal("activated Machine Upgrade did not stop incumbent Computer")
	}
	if got := prepares.Load(); got != 1 {
		t.Fatalf("Machine Upgrade sibling prepare calls = %d, want 1", got)
	}
	if !staged.Load() || !verified.Load() || !swapped.Load() {
		t.Fatalf("Machine Upgrade phases stage=%t verify=%t swap=%t", staged.Load(), verified.Load(), swapped.Load())
	}
	journal, err := upgrade.readJournal()
	if err != nil || journal == nil || journal.TargetVersion != "v2.0.0" {
		t.Fatalf("activated Machine Upgrade journal = %+v, error=%v", journal, err)
	}
	journal.SourceServicePID = math.MaxInt32
	if err := upgrade.writeJournal(*journal); err != nil {
		t.Fatal(err)
	}
	incumbent.Stop()

	successor := newReadyHost(8201)
	successorUpgrade := newHostMachineUpgrade(successor, hostMachineUpgradeConfig{
		identity: HostProcessIdentity{
			ComputerID: "computer-a", ServiceGeneration: "service-32", Environment: "test",
			Version: "v2.0.0", SourceServicePID: math.MaxInt32,
		},
		residentRoot: root,
	})
	successor.upgrade = successorUpgrade
	if err := successorUpgrade.recoverSuccessor(context.Background()); err != nil {
		t.Fatalf("recover successor: %v", err)
	}
	defer successor.Stop()
	if got := reregistrations.Load(); got != 0 {
		t.Fatalf("successor re-registration calls = %d, want 0", got)
	}
	if journal, err := successorUpgrade.readJournal(); err != nil || journal == nil || journal.Phase != MachineUpgradePhaseTargetReady || journal.ObservedTargetGeneration != "service-32" {
		t.Fatalf("converged successor must keep journal until coordinator finalizes = %+v, error=%v", journal, err)
	}
}

func TestRecoverSuccessorRejectsLivePredecessor(t *testing.T) {
	root := t.TempDir()
	journal := hostMachineUpgradeJournal{
		RequestID: "upgrade-live", FromVersion: "v1.0.0", TargetVersion: "v2.0.0",
		StartedAt: "2026-08-18T00:00:00Z", SchemaVersion: 1, SourceServicePID: os.Getpid(),
		AcceptedManagedWorkspaceIDs: []string{"workspace-a"},
		AcceptedManagedSetRevision:  managedSetRevision([]string{"workspace-a"}),
	}
	if err := writeMachineUpgradeJournal(root, journal); err != nil {
		t.Fatal(err)
	}
	host, err := NewHost(HostConfig{
		ControlToken: "token",
		Spawn: func(workspaceID, startIdentity string) (BindingChild, error) {
			return &readySupervisorChild{
				supervisorTestChild: newSupervisorTestChild(9201),
				workspaceID:         workspaceID, startIdentity: startIdentity,
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	host.Reconcile(context.Background(), []string{"workspace-a"})
	waitForSupervisorLifecycle(t, host.supervisor, "workspace-a", RunnerLifecycleRunning)
	record, pid, ok := host.Snapshot("workspace-a")
	if !ok {
		t.Fatal("missing Binding workspace-a")
	}
	host.runtimeMu.Lock()
	host.runtimeSets["workspace-a"] = hostBindingRuntimeSet{
		Identity:    BindingChildIdentity{WorkspaceID: "workspace-a", StartIdentity: record.StartIdentity(), PID: pid},
		Runtimes:    []hostBindingRuntime{{ID: "runtime-a", WorkspaceID: "workspace-a", Provider: "pi"}},
		DaemonToken: "runtime-token", ExpiresAt: time.Now().Add(time.Hour),
	}
	host.runtimeMu.Unlock()
	upgrade := newHostMachineUpgrade(host, hostMachineUpgradeConfig{
		identity: HostProcessIdentity{
			ComputerID: "computer-a", ServiceGeneration: "service-40",
			Version: "v2.0.0", SourceServicePID: os.Getpid(),
		},
		residentRoot: root,
	})
	if err := upgrade.recoverSuccessor(context.Background()); err == nil {
		t.Fatal("live predecessor must fail closed")
	}
	got, err := upgrade.readJournal()
	if err != nil || got == nil || got.ObservedTargetGeneration != "" {
		t.Fatalf("live predecessor must not record target identity = %+v, error=%v", got, err)
	}
}

func TestRecoverSuccessorClearsAbortedActivationOnFromVersion(t *testing.T) {
	root := t.TempDir()
	if err := writeMachineUpgradeJournal(root, hostMachineUpgradeJournal{
		RequestID: "upgrade-abort", FromVersion: "v1.0.0", TargetVersion: "v2.0.0",
		StartedAt: "2026-08-18T00:00:00Z", SchemaVersion: 1, SourceServicePID: 101,
		AcceptedManagedWorkspaceIDs: []string{"workspace-a"},
		AcceptedManagedSetRevision:  managedSetRevision([]string{"workspace-a"}),
	}); err != nil {
		t.Fatal(err)
	}
	upgrade := newHostMachineUpgrade(&Host{}, hostMachineUpgradeConfig{
		identity: HostProcessIdentity{
			ComputerID: "computer-a", ServiceGeneration: "service-41",
			Version: "v1.0.0",
		},
		residentRoot: root,
	})
	if err := upgrade.recoverSuccessor(context.Background()); err != nil {
		t.Fatalf("from-version recovery: %v", err)
	}
	got, err := upgrade.readJournal()
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("aborted activation journal = %+v, want cleared", got)
	}
}

func TestRecoverSuccessorKeepsActiveHandoffOnUnrelatedFromVersion(t *testing.T) {
	root := t.TempDir()
	if err := writeMachineUpgradeJournal(root, hostMachineUpgradeJournal{
		RequestID: "upgrade-active", FromVersion: "v1.0.0", TargetVersion: "v2.0.0",
		StartedAt: "2026-08-18T00:00:00Z", SchemaVersion: 1, SourceServicePID: 101,
		AcceptedManagedWorkspaceIDs: []string{"workspace-a"},
		AcceptedManagedSetRevision:  managedSetRevision([]string{"workspace-a"}),
		Phase:                       MachineUpgradePhaseTargetReady,
	}); err != nil {
		t.Fatal(err)
	}
	upgrade := newHostMachineUpgrade(&Host{}, hostMachineUpgradeConfig{
		identity: HostProcessIdentity{
			ComputerID: "computer-a", ServiceGeneration: "service-42",
			Version: "v1.0.0",
		},
		residentRoot: root,
	})
	if err := upgrade.recoverSuccessor(context.Background()); err == nil {
		t.Fatal("unrelated from-version process must not steal an active handoff")
	}
	got, err := upgrade.readJournal()
	if err != nil || got == nil || got.Phase != MachineUpgradePhaseTargetReady {
		t.Fatalf("active handoff journal = %+v, error=%v", got, err)
	}
}

func TestRecoverSuccessorClearsCoordinatorRollbackOnFromVersion(t *testing.T) {
	root := t.TempDir()
	if err := writeMachineUpgradeJournal(root, hostMachineUpgradeJournal{
		RequestID: "upgrade-rollback", FromVersion: "v1.0.0", TargetVersion: "v2.0.0",
		StartedAt: "2026-08-18T00:00:00Z", SchemaVersion: 1, SourceServicePID: 101,
		AcceptedManagedWorkspaceIDs: []string{"workspace-a"},
		AcceptedManagedSetRevision:  managedSetRevision([]string{"workspace-a"}),
		Phase:                       MachineUpgradePhaseRollingBack,
	}); err != nil {
		t.Fatal(err)
	}
	upgrade := newHostMachineUpgrade(&Host{}, hostMachineUpgradeConfig{
		identity: HostProcessIdentity{
			ComputerID: "computer-a", ServiceGeneration: "service-43",
			Version: "v1.0.0",
		},
		residentRoot: root,
	})
	if err := upgrade.recoverSuccessor(context.Background()); err != nil {
		t.Fatalf("authorized rollback recovery: %v", err)
	}
	got, err := upgrade.readJournal()
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("authorized rollback journal = %+v, want cleared", got)
	}
}
