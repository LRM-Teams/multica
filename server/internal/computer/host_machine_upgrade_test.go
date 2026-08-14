package computer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestHostMachineUpgradeJournalIsPrivateAndRoundTrips(t *testing.T) {
	upgrade := newHostMachineUpgrade(&Host{}, hostMachineUpgradeConfig{residentRoot: t.TempDir()})
	want := hostMachineUpgradeJournal{
		ID: "upgrade-a", Generation: "generation-a", Source: "v1.0.0", Target: "v1.1.0",
		RuntimeIDs: []string{"runtime-a"}, WorkspaceIDs: []string{"workspace-a"}, Phase: "activated",
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
	got, err := upgrade.readJournal()
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != want.ID || got.Generation != want.Generation || got.Target != want.Target || got.Phase != want.Phase {
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

	attested := make(chan struct{}, 1)
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
			var body struct {
				GenerationID string   `json:"generation_id"`
				RuntimeIDs   []string `json:"runtime_ids"`
				WorkspaceIDs []string `json:"workspace_ids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if body.GenerationID != acceptedGeneration || !sameHostStringSet(body.RuntimeIDs, runtimeIDs) || !sameHostStringSet(body.WorkspaceIDs, workspaceIDs) {
				http.Error(w, "incomplete successor attestation", http.StatusConflict)
				return
			}
			attested <- struct{}{}
			w.WriteHeader(http.StatusNoContent)
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
	upgrade.execute(context.Background(), incumbent.runtimeSets[workspaceIDs[0]].Runtimes[0], "runtime-token-workspace-a", protocol.DaemonHeartbeatPendingMachineUpgrade{
		ID: "upgrade-a", TargetVersion: "v2.0.0",
	})
	if got := prepares.Load(); got != int32(len(workspaceIDs)) {
		t.Fatalf("Machine Upgrade prepare calls = %d, want %d", got, len(workspaceIDs))
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
	if err != nil || journal == nil || journal.Phase != "activated" || !sameHostStringSet(journal.RuntimeIDs, runtimeIDs) || !sameHostStringSet(journal.WorkspaceIDs, workspaceIDs) {
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
	if got := reregistrations.Load(); got != int32(len(workspaceIDs)) {
		t.Fatalf("successor re-registration calls = %d, want %d", got, len(workspaceIDs))
	}
	select {
	case <-attested:
	case <-time.After(time.Second):
		t.Fatal("successor did not attest complete Computer set")
	}
	if journal, err := successorUpgrade.readJournal(); err != nil || journal != nil {
		t.Fatalf("successor left Machine Upgrade journal = %+v, error=%v", journal, err)
	}
}
