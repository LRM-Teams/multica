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

func TestComputerMachineUpgradeJournalIsPrivateAndRoundTrips(t *testing.T) {
	upgrade := newComputerMachineUpgrade(&ComputerCore{}, computerMachineUpgradeConfig{residentRoot: t.TempDir()})
	want := computerMachineUpgradeJournal{
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

func TestComputerMachineUpgradeSameOperationIsIgnoredWhileActive(t *testing.T) {
	identity := WorkspaceDaemonIdentity{WorkspaceID: "workspace-a", DaemonInstanceID: "start-3", PID: 8051}
	computerCore := &ComputerCore{runtimeSets: map[string]workspaceDaemonRuntimeSet{
		"workspace-a": {
			Identity: identity,
			Runtimes: []workspaceDaemonRuntime{
				{ID: "runtime-a", WorkspaceID: "workspace-a", Provider: "pi"},
			},
			DaemonToken: "runtime-token",
			ExpiresAt:   time.Now().Add(time.Hour),
		},
	}}
	upgrade := newComputerMachineUpgrade(computerCore, computerMachineUpgradeConfig{})
	upgrade.activeID = "upgrade-a"

	if err := upgrade.startServiceUpgrade(identity, protocol.ComputerUpgradePayload{
		RequestID: "upgrade-a", TargetVersion: "v9.9.9",
	}); err != nil {
		t.Fatalf("same upgrade request = %v", err)
	}
	if upgrade.activeID != "upgrade-a" {
		t.Fatalf("activeID = %q, want upgrade-a", upgrade.activeID)
	}
}

func TestComputerMachineUpgradeDifferentOperationReturnsBusy(t *testing.T) {
	identity := WorkspaceDaemonIdentity{WorkspaceID: "workspace-a", DaemonInstanceID: "start-3", PID: 8051}
	computerCore := &ComputerCore{runtimeSets: map[string]workspaceDaemonRuntimeSet{
		"workspace-a": {
			Identity: identity,
			Runtimes: []workspaceDaemonRuntime{
				{ID: "runtime-a", WorkspaceID: "workspace-a", Provider: "pi"},
			},
			DaemonToken: "runtime-token",
			ExpiresAt:   time.Now().Add(time.Hour),
		},
	}}
	upgrade := newComputerMachineUpgrade(computerCore, computerMachineUpgradeConfig{})
	upgrade.activeID = "upgrade-a"

	if err := upgrade.startServiceUpgrade(identity, protocol.ComputerUpgradePayload{
		RequestID: "upgrade-b", TargetVersion: "v10.0.0",
	}); !errors.Is(err, ErrComputerControlBusy) {
		t.Fatalf("different upgrade request = %v, want busy", err)
	}
}

func TestComputerMachineUpgradeStatusRetainsRealProgressAndFailure(t *testing.T) {
	upgrade := newComputerMachineUpgrade(&ComputerCore{}, computerMachineUpgradeConfig{})
	upgrade.activeID = "upgrade-a"
	upgrade.targetVersion = "v2.0.0"
	upgrade.recordProgress("upgrade-a", "verifying", "Verifying release")
	if status := upgrade.status(); status.ID != "upgrade-a" || status.Phase != "verifying" || status.TargetVersion != "v2.0.0" {
		t.Fatalf("active status = %+v", status)
	}

	upgrade.recordDone("upgrade-a", "", "verification_failed")
	status := upgrade.status()
	if status.ID != "upgrade-a" || status.Phase != "failed" || status.Error != "verification_failed" {
		t.Fatalf("terminal status = %+v", status)
	}
}

func TestComputerControlForwardsBusy(t *testing.T) {
	identity := WorkspaceDaemonIdentity{WorkspaceID: "workspace-a", DaemonInstanceID: "start-3", PID: 8051}
	control := NewComputerControl("owner-secret", ComputerControlCallbacks{
		Current: func(got WorkspaceDaemonIdentity) bool { return got == identity },
		MachineActions: func(context.Context, WorkspaceDaemonIdentity, json.RawMessage) error {
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

	client := NewComputerControlClient(endpoint, "owner-secret", identity)
	err := client.ForwardComputerControl(context.Background(), protocol.DaemonHeartbeatAckPayload{
		PendingRestart: &protocol.DaemonHeartbeatPendingRestart{ID: "restart-b"},
	})
	if !errors.Is(err, ErrComputerControlBusy) {
		t.Fatalf("ForwardComputerControl = %v, want ErrComputerControlBusy", err)
	}
}

func TestComputerMachineUpgradeEmptyRuntimeUsesCurrentWorkspaceRuntime(t *testing.T) {
	identity := WorkspaceDaemonIdentity{WorkspaceID: "workspace-a", DaemonInstanceID: "start-3", PID: 8051}
	computerCore := &ComputerCore{runtimeSets: map[string]workspaceDaemonRuntimeSet{
		"workspace-a": {
			Identity: identity,
			Runtimes: []workspaceDaemonRuntime{
				{ID: "runtime-a", WorkspaceID: "workspace-a", Provider: "pi"},
			},
			DaemonToken: "runtime-token",
			ExpiresAt:   time.Now().Add(time.Hour),
		},
	}}
	upgrade := newComputerMachineUpgrade(computerCore, computerMachineUpgradeConfig{})

	runtime, token, ok := upgrade.currentRuntime(identity, "")
	if !ok {
		t.Fatal("current WorkspaceDaemon could not resolve its Runtime for a connect-socket machine action")
	}
	if runtime.ID != "runtime-a" || token != "runtime-token" {
		t.Fatalf("resolved runtime = %+v token=%q", runtime, token)
	}
}

func TestComputerMachineUpgradePreparesEveryWorkspaceDaemonAndSuccessorConverges(t *testing.T) {
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

	newReadyComputer := func(pidBase int) *ComputerCore {
		computerCore, err := NewComputerCore(ComputerCoreConfig{
			ControlToken: controlToken,
			Spawn: func(workspaceID string) (WorkspaceDaemonProcess, error) {
				pid := pidBase
				if workspaceID == workspaceIDs[1] {
					pid++
				}
				return &readyWorkspaceDaemonTestProcess{
					workspaceDaemonTestProcess: newWorkspaceDaemonTestProcess(pid), controlEndpoint: childControl,
					workspaceID: workspaceID, daemonInstanceID: fmt.Sprintf("child-%d", pid),
				}, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		computerCore.Reconcile(context.Background(), workspaceIDs)
		for _, workspaceID := range workspaceIDs {
			waitForDaemonStatus(t, computerCore.daemonCore, workspaceID, WorkspaceDaemonRunning)
		}
		computerCore.runtimeMu.Lock()
		for index, workspaceID := range workspaceIDs {
			record, pid, ok := computerCore.Snapshot(workspaceID)
			if !ok {
				t.Fatalf("missing Binding %s", workspaceID)
			}
			computerCore.runtimeSets[workspaceID] = workspaceDaemonRuntimeSet{
				Identity:    WorkspaceDaemonIdentity{WorkspaceID: workspaceID, DaemonInstanceID: record.DaemonInstanceID, PID: pid},
				Runtimes:    []workspaceDaemonRuntime{{ID: runtimeIDs[index], WorkspaceID: workspaceID, Provider: "pi"}},
				DaemonToken: "runtime-token-" + workspaceID, ExpiresAt: time.Now().Add(time.Hour),
			}
		}
		computerCore.runtimeMu.Unlock()
		return computerCore
	}

	root := t.TempDir()
	incumbent := newReadyComputer(8101)
	upgradeCancelled := make(chan struct{}, 1)
	upgrade := newComputerMachineUpgrade(incumbent, computerMachineUpgradeConfig{
		identity: ComputerIdentity{
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

	successor := newReadyComputer(8201)
	successorUpgrade := newComputerMachineUpgrade(successor, computerMachineUpgradeConfig{
		identity: ComputerIdentity{
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
	journal := computerMachineUpgradeJournal{
		RequestID: "upgrade-live", FromVersion: "v1.0.0", TargetVersion: "v2.0.0",
		StartedAt: "2026-08-18T00:00:00Z", SchemaVersion: 1, SourceServicePID: os.Getpid(),
		AcceptedManagedWorkspaceIDs: []string{"workspace-a"},
		AcceptedManagedSetRevision:  managedSetRevision([]string{"workspace-a"}),
	}
	if err := writeMachineUpgradeJournal(root, journal); err != nil {
		t.Fatal(err)
	}
	computerCore, err := NewComputerCore(ComputerCoreConfig{
		ControlToken: "token",
		Spawn: func(workspaceID string) (WorkspaceDaemonProcess, error) {
			return &readyWorkspaceDaemonTestProcess{
				workspaceDaemonTestProcess: newWorkspaceDaemonTestProcess(9201),
				workspaceID:                workspaceID, daemonInstanceID: "child-9201",
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	computerCore.Reconcile(context.Background(), []string{"workspace-a"})
	waitForDaemonStatus(t, computerCore.daemonCore, "workspace-a", WorkspaceDaemonRunning)
	record, pid, ok := computerCore.Snapshot("workspace-a")
	if !ok {
		t.Fatal("missing Binding workspace-a")
	}
	computerCore.runtimeMu.Lock()
	computerCore.runtimeSets["workspace-a"] = workspaceDaemonRuntimeSet{
		Identity:    WorkspaceDaemonIdentity{WorkspaceID: "workspace-a", DaemonInstanceID: record.DaemonInstanceID, PID: pid},
		Runtimes:    []workspaceDaemonRuntime{{ID: "runtime-a", WorkspaceID: "workspace-a", Provider: "pi"}},
		DaemonToken: "runtime-token", ExpiresAt: time.Now().Add(time.Hour),
	}
	computerCore.runtimeMu.Unlock()
	upgrade := newComputerMachineUpgrade(computerCore, computerMachineUpgradeConfig{
		identity: ComputerIdentity{
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
	if err := writeMachineUpgradeJournal(root, computerMachineUpgradeJournal{
		RequestID: "upgrade-abort", FromVersion: "v1.0.0", TargetVersion: "v2.0.0",
		StartedAt: "2026-08-18T00:00:00Z", SchemaVersion: 1, SourceServicePID: 101,
		AcceptedManagedWorkspaceIDs: []string{"workspace-a"},
		AcceptedManagedSetRevision:  managedSetRevision([]string{"workspace-a"}),
	}); err != nil {
		t.Fatal(err)
	}
	upgrade := newComputerMachineUpgrade(&ComputerCore{}, computerMachineUpgradeConfig{
		identity: ComputerIdentity{
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

func TestCurrentBinaryRestartCapturesDedicatedHandoff(t *testing.T) {
	computerCore := &ComputerCore{}
	upgrade := newComputerMachineUpgrade(computerCore, computerMachineUpgradeConfig{
		identity: ComputerIdentity{Version: "alpha.8"},
	})
	computerCore.upgrade = upgrade
	upgrade.installPath = func() (string, error) { return "/tmp/current-computer", nil }

	if err := upgrade.scheduleCurrentBinaryRestart(); err != nil {
		t.Fatal(err)
	}
	plan := computerCore.RestartPlan()
	if plan.BinaryPath != "/tmp/current-computer" {
		t.Fatalf("restart binary = %q", plan.BinaryPath)
	}
	handoff := plan.CurrentBinaryHandoff
	if handoff == nil {
		t.Fatal("same-binary restart did not capture a dedicated handoff")
	}
	if handoff.Version != "alpha.8" || handoff.SourceServicePID != os.Getpid() || handoff.AcceptedManagedSetRevision == "" {
		t.Fatalf("same-binary restart handoff = %+v", handoff)
	}
}

func TestRecoverSuccessorKeepsActiveHandoffOnUnrelatedFromVersion(t *testing.T) {
	root := t.TempDir()
	if err := writeMachineUpgradeJournal(root, computerMachineUpgradeJournal{
		RequestID: "upgrade-active", FromVersion: "v1.0.0", TargetVersion: "v2.0.0",
		StartedAt: "2026-08-18T00:00:00Z", SchemaVersion: 1, SourceServicePID: 101,
		AcceptedManagedWorkspaceIDs: []string{"workspace-a"},
		AcceptedManagedSetRevision:  managedSetRevision([]string{"workspace-a"}),
		Phase:                       MachineUpgradePhaseTargetReady,
	}); err != nil {
		t.Fatal(err)
	}
	upgrade := newComputerMachineUpgrade(&ComputerCore{}, computerMachineUpgradeConfig{
		identity: ComputerIdentity{
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
	if err := writeMachineUpgradeJournal(root, computerMachineUpgradeJournal{
		RequestID: "upgrade-rollback", FromVersion: "v1.0.0", TargetVersion: "v2.0.0",
		StartedAt: "2026-08-18T00:00:00Z", SchemaVersion: 1, SourceServicePID: 101,
		AcceptedManagedWorkspaceIDs: []string{"workspace-a"},
		AcceptedManagedSetRevision:  managedSetRevision([]string{"workspace-a"}),
		Phase:                       MachineUpgradePhaseRollingBack,
	}); err != nil {
		t.Fatal(err)
	}
	upgrade := newComputerMachineUpgrade(&ComputerCore{}, computerMachineUpgradeConfig{
		identity: ComputerIdentity{
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
