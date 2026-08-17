package computer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestBindingMachineUpgradeExitStopsChildAfterSwap(t *testing.T) {
	const controlToken = "owner-secret"
	acceptedGeneration := "generation-a"
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/daemon/runtimes/runtime-a/machine-upgrades/upgrade-a/accept":
			_ = json.NewEncoder(w).Encode(hostMachineUpgradeReceipt{
				ID: "upgrade-a", Phase: "accepted", AcceptedGeneration: &acceptedGeneration,
				AcceptedRuntimeIDs: []string{"runtime-a"}, AcceptedWorkspaceIDs: []string{"workspace-a"},
			})
		case "/api/daemon/runtimes/runtime-a/machine-upgrades/upgrade-a/progress":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer cloud.Close()

	host := localControlTestServer(t, func(_ context.Context, operation string, headers map[string]string, _ json.RawMessage) (any, error) {
		if operation != "runner-drain" || headers["X-Multica-Control-Token"] != controlToken {
			return nil, fmt.Errorf("unexpected Host prepare")
		}
		return BindingMachineUpgradePrepared{
			RuntimeIDs: []string{"runtime-a"}, WorkspaceIDs: []string{"workspace-a"},
		}, nil
	})

	exited := make(chan struct{}, 1)
	var swapped atomic.Bool
	root := t.TempDir()
	executor := NewBindingMachineUpgradeExecutor(BindingMachineUpgradeConfig{
		Identity: HostProcessIdentity{
			ComputerID: "computer-a", ComputerGeneration: 7, Environment: "test",
			Version: "v1.0.0", ServerURL: cloud.URL,
		},
		ResidentRoot:    root,
		ServiceEndpoint: host,
		ControlToken:    controlToken,
		Child:           BindingChildIdentity{WorkspaceID: "workspace-a", RunnerGeneration: 1, PID: 101},
		RuntimeID:       "runtime-a",
		DaemonToken:     "runtime-token",
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
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("Binding child Machine Upgrade did not exit after swap")
	}
	journal, err := readMachineUpgradeJournal(root)
	if err != nil || journal == nil || journal.TargetVersion != "v2.0.0" {
		t.Fatalf("activated marker = %+v err=%v", journal, err)
	}
}

func TestBindingMachineUpgradeLatestUsesTestEnvironmentRelease(t *testing.T) {
	const controlToken = "owner-secret"
	acceptedGeneration := "generation-a"
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/daemon/runtimes/runtime-a/machine-upgrades/upgrade-a/accept":
			_ = json.NewEncoder(w).Encode(hostMachineUpgradeReceipt{
				ID: "upgrade-a", Phase: "accepted", AcceptedGeneration: &acceptedGeneration,
				AcceptedRuntimeIDs: []string{"runtime-a"}, AcceptedWorkspaceIDs: []string{"workspace-a"},
			})
		case "/api/daemon/runtimes/runtime-a/machine-upgrades/upgrade-a/progress":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer cloud.Close()

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

	host := localControlTestServer(t, func(_ context.Context, operation string, headers map[string]string, _ json.RawMessage) (any, error) {
		if operation != "runner-drain" || headers["X-Multica-Control-Token"] != controlToken {
			return nil, fmt.Errorf("unexpected Host prepare")
		}
		return BindingMachineUpgradePrepared{
			RuntimeIDs: []string{"runtime-a"}, WorkspaceIDs: []string{"workspace-a"}, ManifestURL: feed.URL,
		}, nil
	})

	var stagedTarget string
	executor := NewBindingMachineUpgradeExecutor(BindingMachineUpgradeConfig{
		Identity: HostProcessIdentity{
			ComputerID: "computer-a", ComputerGeneration: 7, Environment: "test",
			Version: "v0.4.24-alpha.73", ServerURL: cloud.URL,
		},
		ResidentRoot:    t.TempDir(),
		ServiceEndpoint: host,
		ControlToken:    controlToken,
		Child:           BindingChildIdentity{WorkspaceID: "workspace-a", RunnerGeneration: 1, PID: 101},
		RuntimeID:       "runtime-a",
		DaemonToken:     "runtime-token",
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
