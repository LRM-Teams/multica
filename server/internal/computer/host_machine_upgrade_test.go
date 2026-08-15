package computer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestHostMachineUpgradeJournalIsPrivateAndRoundTrips(t *testing.T) {
	upgrade := newHostMachineUpgrade(&Host{}, hostMachineUpgradeConfig{residentRoot: t.TempDir()})
	want := hostMachineUpgradeJournal{Target: "v1.1.0"}
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
	if _, ok := persisted["id"]; ok {
		t.Fatalf("activated marker still persisted id: %s", raw)
	}
	if _, ok := persisted["generation"]; ok {
		t.Fatalf("activated marker still persisted generation: %s", raw)
	}
	got, err := upgrade.readJournal()
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Target != want.Target {
		t.Fatalf("Machine Upgrade journal = %+v, want %+v", got, want)
	}
}

func TestHostMachineUpgradeAcceptanceRequiresCompleteComputerSet(t *testing.T) {
	generation := "generation-a"
	receipt := hostMachineUpgradeReceipt{
		ID: "upgrade-a", AcceptedGeneration: &generation,
		AcceptedRuntimeIDs:   []string{"runtime-b", "runtime-a"},
		AcceptedWorkspaceIDs: []string{"workspace-b", "workspace-a"},
	}
	if err := validateHostMachineUpgradeReceipt(receipt, "upgrade-a",
		[]string{"runtime-a", "runtime-b"}, []string{"workspace-a", "workspace-b"}); err != nil {
		t.Fatalf("complete acceptance rejected: %v", err)
	}
	receipt.AcceptedWorkspaceIDs = []string{"workspace-a"}
	err := validateHostMachineUpgradeReceipt(receipt, "upgrade-a",
		[]string{"runtime-a", "runtime-b"}, []string{"workspace-a", "workspace-b"})
	if err == nil || !strings.Contains(err.Error(), "complete Computer set") {
		t.Fatalf("partial acceptance error = %v", err)
	}
}

func TestHostMachineUpgradeLocalDeliveryHandsOffToCurrentBinding(t *testing.T) {
	const controlToken = "owner-secret"
	var humanIntentCalls atomic.Int32
	delivered := make(chan string, 2)
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/daemons/computer-a/upgrades":
			humanIntentCalls.Add(1)
			http.Error(w, "Host must not create human lifecycle intents", http.StatusForbidden)
		case "/api/daemon/runtimes/runtime-a/machine-upgrades/upgrade-a/accept",
			"/api/daemon/runtimes/runtime-a/machine-upgrades/upgrade-b/accept":
			t.Error("Host accepted Machine Upgrade itself")
			http.Error(w, "Host must not execute Machine Upgrade", http.StatusConflict)
		default:
			http.NotFound(w, r)
		}
	}))
	defer cloud.Close()

	childControl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Multica-Control-Token") != controlToken {
			http.Error(w, "bad token", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == BindingComputerUpgradePath {
			var request struct {
				Command protocol.ComputerUpgradePayload `json:"command"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			delivered <- request.Command.Operation()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path != BindingReregisterRuntimePath {
			http.Error(w, "unexpected child control request", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer childControl.Close()

	host, err := NewHost(HostConfig{
		ControlToken: controlToken,
		Spawn: func(string, int64) (BindingChild, error) {
			return &readySupervisorChild{supervisorTestChild: newSupervisorTestChild(8051), controlURL: childControl.URL}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Stop()
	host.Reconcile(context.Background(), []string{"workspace-a"})
	waitForSupervisorLifecycle(t, host.supervisor, "workspace-a", RunnerLifecycleRunning)
	record, pid, ok := host.Snapshot("workspace-a")
	if !ok {
		t.Fatal("missing live Binding child")
	}
	host.runtimeSets["workspace-a"] = hostBindingRuntimeSet{
		Identity:    BindingChildIdentity{WorkspaceID: "workspace-a", RunnerGeneration: record.Generation(), PID: pid},
		Runtimes:    []hostBindingRuntime{{ID: "runtime-a", WorkspaceID: "workspace-a", Provider: "pi"}},
		DaemonToken: "runtime-token", ExpiresAt: time.Now().Add(time.Hour),
	}
	upgrade := newHostMachineUpgrade(host, hostMachineUpgradeConfig{identity: HostProcessIdentity{
		ComputerID: "computer-a", ComputerGeneration: 7, Environment: "test",
		Version: "v1.0.0", ReleaseChannel: "latest", ServerURL: cloud.URL,
	}})
	host.upgrade = upgrade

	for _, operationID := range []string{"upgrade-a", "upgrade-b"} {
		body, err := json.Marshal(protocol.ComputerUpgradePayload{
			RequestID: "request-" + operationID, OperationID: operationID, TargetVersion: "v1.0.0",
		})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/machine-upgrades", bytes.NewReader(body))
		request.Header.Set("X-Multica-Control-Token", controlToken)
		response := httptest.NewRecorder()
		upgrade.localRequestHandler()(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("local delivery %s status = %d body=%s", operationID, response.Code, response.Body.String())
		}
		select {
		case got := <-delivered:
			if got != operationID {
				t.Fatalf("delivered operation = %q, want %q", got, operationID)
			}
		case <-time.After(time.Second):
			t.Fatalf("local delivery did not hand off %s", operationID)
		}
		deadline := time.Now().Add(time.Second)
		for {
			upgrade.mu.Lock()
			activeID := upgrade.activeID
			upgrade.mu.Unlock()
			if activeID == "" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("active Machine Upgrade %q was not released after attesting %s", activeID, operationID)
			}
			time.Sleep(time.Millisecond)
		}
	}
	if got := humanIntentCalls.Load(); got != 0 {
		t.Fatalf("Host created %d human lifecycle intents, want zero", got)
	}
}

func TestHostMachineUpgradeSameOperationIsIgnoredWhileActive(t *testing.T) {
	identity := BindingChildIdentity{WorkspaceID: "workspace-a", RunnerGeneration: 3, PID: 8051}
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
	identity := BindingChildIdentity{WorkspaceID: "workspace-a", RunnerGeneration: 3, PID: 8051}
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
	identity := BindingChildIdentity{WorkspaceID: "workspace-a", RunnerGeneration: 3, PID: 8051}
	control := NewHostControl("owner-secret", NewProcessCapacity(1), HostControlCallbacks{
		Current: func(got BindingChildIdentity) bool { return got == identity },
		MachineActions: func(context.Context, BindingChildIdentity, json.RawMessage) error {
			return ErrComputerControlBusy
		},
	})
	mux := http.NewServeMux()
	control.RegisterRoutes(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := NewHostControlClient(server.URL, "owner-secret", identity)
	err := client.ForwardMachineActions(context.Background(), protocol.DaemonHeartbeatAckPayload{
		PendingMachineUpgrade: &protocol.DaemonHeartbeatPendingMachineUpgrade{ID: "upgrade-b", TargetVersion: "v10.0.0"},
	})
	if !errors.Is(err, ErrComputerControlBusy) {
		t.Fatalf("ForwardMachineActions = %v, want ErrComputerControlBusy", err)
	}
}

func TestHostMachineUpgradeEmptyRuntimeUsesCurrentBindingRuntime(t *testing.T) {
	identity := BindingChildIdentity{WorkspaceID: "workspace-a", RunnerGeneration: 3, PID: 8051}
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
	childControl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Multica-Control-Token") != controlToken {
			http.Error(w, "bad token", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case BindingPrepareMachineUpgradePath:
			prepares.Add(1)
		case BindingReregisterRuntimePath:
			reregistrations.Add(1)
		default:
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer childControl.Close()

	acceptedGeneration := "generation-a"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/daemon/runtimes/runtime-a/machine-upgrades/upgrade-a/accept":
			_ = json.NewEncoder(w).Encode(hostMachineUpgradeReceipt{
				ID: "upgrade-a", Phase: "accepted", AcceptedGeneration: &acceptedGeneration,
				AcceptedRuntimeIDs: runtimeIDs, AcceptedWorkspaceIDs: workspaceIDs,
			})
		case "/api/daemon/runtimes/runtime-a/machine-upgrades/upgrade-a/progress":
			w.WriteHeader(http.StatusNoContent)
		case "/api/daemon/computer/machine-upgrades/upgrade-a/attest":
			t.Error("successor attested over HTTP")
			http.Error(w, "successor must not attest over HTTP", http.StatusConflict)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	newReadyHost := func(pidBase int) *Host {
		host, err := NewHost(HostConfig{
			ControlToken: controlToken,
			Spawn: func(workspaceID string, _ int64) (BindingChild, error) {
				pid := pidBase
				if workspaceID == workspaceIDs[1] {
					pid++
				}
				return &readySupervisorChild{
					supervisorTestChild: newSupervisorTestChild(pid), controlURL: childControl.URL,
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
				Identity:    BindingChildIdentity{WorkspaceID: workspaceID, RunnerGeneration: record.Generation(), PID: pid},
				Runtimes:    []hostBindingRuntime{{ID: runtimeIDs[index], WorkspaceID: workspaceID, Provider: "pi"}},
				DaemonToken: "runtime-token-" + workspaceID, ExpiresAt: time.Now().Add(time.Hour),
			}
		}
		host.runtimeMu.Unlock()
		return host
	}

	root := t.TempDir()
	incumbent := newReadyHost(8101)
	hostMux := http.NewServeMux()
	incumbent.RegisterRoutes(hostMux)
	hostControl := httptest.NewServer(hostMux)
	defer hostControl.Close()
	upgradeCancelled := make(chan struct{}, 1)
	upgrade := newHostMachineUpgrade(incumbent, hostMachineUpgradeConfig{
		identity: HostProcessIdentity{
			ComputerID: "computer-a", ComputerGeneration: 31, Environment: "test",
			Version: "v1.0.0", ReleaseChannel: "latest", ServerURL: server.URL,
		},
		residentRoot: root,
		cancel:       func() { upgradeCancelled <- struct{}{} },
	})
	incumbent.upgrade = upgrade
	var staged, verified, swapped atomic.Bool
	executor := NewBindingMachineUpgradeExecutor(BindingMachineUpgradeConfig{
		Identity: HostProcessIdentity{
			ComputerID: "computer-a", ComputerGeneration: 31, Environment: "test",
			Version: "v1.0.0", ReleaseChannel: "latest", ServerURL: server.URL,
		},
		ResidentRoot: root,
		ControlURL:   hostControl.URL,
		ControlToken: controlToken,
		Child:        incumbent.runtimeSets[workspaceIDs[0]].Identity,
		RuntimeID:    runtimeIDs[0],
		DaemonToken:  "runtime-token-workspace-a",
		Exit: func() {
			upgrade.observeInitiatorExit(incumbent.runtimeSets[workspaceIDs[0]].Identity)
		},
		StageRelease: func(target string, _ time.Duration, _ string) (string, error) {
			if target != "v2.0.0" {
				t.Fatalf("stage target = %q", target)
			}
			staged.Store(true)
			return "/tmp/staged-computer", nil
		},
		VerifyBinary: func(_ context.Context, path, target string) error {
			if path != "/tmp/staged-computer" || target != "v2.0.0" {
				t.Fatalf("verify path/target = %q/%q", path, target)
			}
			verified.Store(true)
			return nil
		},
		InstallPath: func() (string, error) { return "/tmp/active-computer", nil },
		SwapExecutable: func(current, candidate string) error {
			if current != "/tmp/active-computer" || candidate != "/tmp/staged-computer" {
				t.Fatalf("swap current/candidate = %q/%q", current, candidate)
			}
			swapped.Store(true)
			return nil
		},
	})
	if err := executor.Execute(context.Background(), protocol.ComputerUpgradePayload{
		RequestID: "upgrade-a", TargetVersion: "v2.0.0",
	}); err != nil {
		t.Fatalf("Binding executor: %v", err)
	}
	if got := prepares.Load(); got != 1 {
		t.Fatalf("Machine Upgrade sibling prepare calls = %d, want 1", got)
	}
	if !staged.Load() || !verified.Load() || !swapped.Load() {
		t.Fatalf("Machine Upgrade phases stage=%t verify=%t swap=%t", staged.Load(), verified.Load(), swapped.Load())
	}
	select {
	case <-upgradeCancelled:
	case <-time.After(time.Second):
		t.Fatal("activated Machine Upgrade did not stop incumbent Computer")
	}
	journal, err := upgrade.readJournal()
	if err != nil || journal == nil || journal.Target != "v2.0.0" {
		t.Fatalf("activated Machine Upgrade journal = %+v, error=%v", journal, err)
	}
	incumbent.Stop()

	successor := newReadyHost(8201)
	successorUpgrade := newHostMachineUpgrade(successor, hostMachineUpgradeConfig{
		identity: HostProcessIdentity{
			ComputerID: "computer-a", ComputerGeneration: 32, Environment: "test",
			Version: "v2.0.0", ReleaseChannel: "latest", ServerURL: server.URL,
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
	if journal, err := successorUpgrade.readJournal(); err != nil || journal != nil {
		t.Fatalf("successor left Machine Upgrade journal = %+v, error=%v", journal, err)
	}
}

func TestHostMachineUpgradeRecoversPreviousPackageHandoffJournal(t *testing.T) {
	const controlToken = "owner-secret"
	home := t.TempDir()
	t.Setenv("HOME", home)
	previousJournalDir := filepath.Join(home, ".local", "share", "multica", "machine-upgrades")
	if err := os.MkdirAll(previousJournalDir, 0o700); err != nil {
		t.Fatal(err)
	}
	previousJournalPath := filepath.Join(previousJournalDir, "upgrade-a.json")
	previousJournal := map[string]any{
		"id": "upgrade-a", "generation": "generation-a", "source_version": "v1.0.0", "target_version": "v2.0.0",
		"runtime_ids": []string{"runtime-a"}, "workspace_ids": []string{"workspace-a"}, "phase": "handoff",
		"updated_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	data, err := json.Marshal(previousJournal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(previousJournalPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	var reregistered atomic.Int32
	childControl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != BindingReregisterRuntimePath || r.Header.Get("X-Multica-Control-Token") != controlToken {
			http.Error(w, "unexpected child control request", http.StatusBadRequest)
			return
		}
		reregistered.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer childControl.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/daemon/computer/machine-upgrades/upgrade-a/attest" {
			t.Error("previous-package successor attested over HTTP")
			http.Error(w, "successor must not attest over HTTP", http.StatusConflict)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	host, err := NewHost(HostConfig{
		ControlToken: controlToken,
		Spawn: func(string, int64) (BindingChild, error) {
			return &readySupervisorChild{
				supervisorTestChild: newSupervisorTestChild(8301), controlURL: childControl.URL,
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
		t.Fatal("missing successor Binding")
	}
	host.runtimeSets["workspace-a"] = hostBindingRuntimeSet{
		Identity:    BindingChildIdentity{WorkspaceID: "workspace-a", RunnerGeneration: record.Generation(), PID: pid},
		Runtimes:    []hostBindingRuntime{{ID: "runtime-a", WorkspaceID: "workspace-a", Provider: "pi"}},
		DaemonToken: "runtime-token", ExpiresAt: time.Now().Add(time.Hour),
	}
	defer host.Stop()

	upgrade := newHostMachineUpgrade(host, hostMachineUpgradeConfig{
		identity: HostProcessIdentity{
			ComputerID: "computer-a", ComputerGeneration: 251, Environment: "test",
			Version: "v2.0.0", ReleaseChannel: "latest", ServerURL: server.URL,
		},
		residentRoot:                    filepath.Join(home, ".multica", "computer"),
		previousPackageUpgradeBootstrap: true,
	})
	host.upgrade = upgrade
	if err := upgrade.recoverSuccessor(context.Background()); err != nil {
		t.Fatalf("recover previous-package successor: %v", err)
	}
	if got := reregistered.Load(); got != 0 {
		t.Fatalf("successor re-registration calls = %d, want 0", got)
	}
	if _, err := os.Stat(previousJournalPath); !os.IsNotExist(err) {
		t.Fatalf("previous-package handoff journal remains after successor start: %v", err)
	}
}
