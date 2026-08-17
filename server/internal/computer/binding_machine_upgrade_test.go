package computer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestBindingMachineUpgradeExitStopsChildAfterSwap(t *testing.T) {
	const controlToken = "owner-secret"
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != bindingChildPrepareUpgradePath || r.Header.Get("X-Multica-Control-Token") != controlToken {
			http.Error(w, "unexpected Host prepare", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(BindingMachineUpgradePrepared{
			RuntimeIDs: []string{"runtime-a"}, WorkspaceIDs: []string{"workspace-a"},
		})
	}))
	defer host.Close()

	exited := make(chan struct{}, 1)
	var swapped atomic.Bool
	var progressPhases []string
	root := t.TempDir()
	executor := NewBindingMachineUpgradeExecutor(BindingMachineUpgradeConfig{
		Identity: HostProcessIdentity{
			ComputerID: "computer-a", ComputerGeneration: 7, Environment: "test",
			Version: "v1.0.0",
		},
		ResidentRoot: root,
		ControlURL:   host.URL,
		ControlToken: controlToken,
		Child:        BindingChildIdentity{WorkspaceID: "workspace-a", RunnerGeneration: 1, PID: 101},
		Emit: func(eventType string, payload any) {
			if eventType == protocol.EventComputerUpgradeProgress {
				progressPhases = append(progressPhases, payload.(protocol.ComputerUpgradeProgressPayload).Phase)
			}
		},
		Exit: func() {
			select {
			case exited <- struct{}{}:
			default:
			}
		},
		StageRelease: func(string, time.Duration, string) (string, error) { return "/tmp/staged", nil },
		VerifyBinary: func(context.Context, string, string) error { return nil },
		InstallPath:  func() (string, error) { return "/tmp/active", nil },
		SwapExecutable: func(string, string) error {
			swapped.Store(true)
			return nil
		},
	})
	if err := executor.Execute(context.Background(), protocol.ComputerUpgradePayload{
		RequestID: "upgrade-a", TargetVersion: "v2.0.0",
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !swapped.Load() {
		t.Fatal("Binding child did not swap the Computer binary")
	}
	wantPhases := []string{"downloading", "verifying", "applying", "restarting"}
	if len(progressPhases) != len(wantPhases) {
		t.Fatalf("Machine Upgrade progress phases = %v, want %v", progressPhases, wantPhases)
	}
	for index := range wantPhases {
		if progressPhases[index] != wantPhases[index] {
			t.Fatalf("Machine Upgrade progress phases = %v, want %v", progressPhases, wantPhases)
		}
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("Binding child Machine Upgrade did not exit after swap")
	}
	journal, err := readMachineUpgradeJournal(root)
	if err != nil || journal == nil || journal.TargetVersion != "v2.0.0" {
		t.Fatalf("activated marker = %+v err=%v", journal, err)
	}
	raw, err := os.ReadFile(machineUpgradeJournalPath(root))
	if err != nil {
		t.Fatal(err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	for field, want := range map[string]any{
		"requestId": "upgrade-a", "fromVersion": "v1.0.0", "targetVersion": "v2.0.0",
	} {
		if got := persisted[field]; got != want {
			t.Fatalf("activated marker %s = %v, want %v: %s", field, got, want, raw)
		}
	}
	if persisted["startedAt"] == nil || persisted["schemaVersion"] != float64(1) {
		t.Fatalf("activated marker lacks Raft lifecycle fields: %s", raw)
	}
}

func TestBindingMachineUpgradeLatestUsesTestEnvironmentRelease(t *testing.T) {
	const controlToken = "owner-secret"
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metainfo.json" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(cli.ReleaseMetainfo{
			SchemaVersion: 1,
			Environments: map[string]cli.ReleaseManifest{
				"production": {TagName: "v0.4.23", Version: "0.4.23"},
				"test":       {TagName: "v0.4.24-alpha.74", Version: "0.4.24-alpha.74"},
			},
		})
	}))
	defer feed.Close()

	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != bindingChildPrepareUpgradePath || r.Header.Get("X-Multica-Control-Token") != controlToken {
			http.Error(w, "unexpected Host prepare", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(BindingMachineUpgradePrepared{
			RuntimeIDs: []string{"runtime-a"}, WorkspaceIDs: []string{"workspace-a"}, ManifestURL: feed.URL,
		})
	}))
	defer host.Close()

	var stagedTarget string
	executor := NewBindingMachineUpgradeExecutor(BindingMachineUpgradeConfig{
		Identity: HostProcessIdentity{
			ComputerID: "computer-a", ComputerGeneration: 7, Environment: "test",
			Version: "v0.4.24-alpha.73",
		},
		ResidentRoot: t.TempDir(),
		ControlURL:   host.URL,
		ControlToken: controlToken,
		Child:        BindingChildIdentity{WorkspaceID: "workspace-a", RunnerGeneration: 1, PID: 101},
		StageRelease: func(target string, _ time.Duration, _ string) (string, error) {
			stagedTarget = target
			return "/tmp/staged", nil
		},
		VerifyBinary:   func(context.Context, string, string) error { return nil },
		InstallPath:    func() (string, error) { return "/tmp/active", nil },
		SwapExecutable: func(string, string) error { return nil },
	})
	if err := executor.Execute(context.Background(), protocol.ComputerUpgradePayload{
		RequestID: "upgrade-a", TargetVersion: "latest",
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if stagedTarget != "v0.4.24-alpha.74" {
		t.Fatalf("staged target = %q, want test release v0.4.24-alpha.74", stagedTarget)
	}
}
