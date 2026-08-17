package computer

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestHostProcessOwnsResidentControlAndDesiredBindings(t *testing.T) {
	child := &readySupervisorChild{supervisorTestChild: newSupervisorTestChild(7101)}
	host, err := NewHost(HostConfig{
		Spawn: func(workspaceID string, generation int64) (BindingChild, error) {
			if workspaceID != "workspace-a" || generation != 1 {
				t.Fatalf("spawn identity = (%q, %d)", workspaceID, generation)
			}
			return child, nil
		},
		ControlToken: "owner-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- host.RunProcess(ctx, HostProcessConfig{
			Listener: listener,
			Identity: HostProcessIdentity{
				ComputerID:         "computer-a",
				ComputerGeneration: 9,
				Environment:        "test",
				Version:            "v1.2.3",
			},
			DesiredWorkspaceIDs: func() ([]string, error) { return []string{"workspace-a"}, nil },
		})
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		record, pid, ok := host.Snapshot("workspace-a")
		if ok && record.Lifecycle == RunnerLifecycleRunning && pid == 7101 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if record, pid, ok := host.Snapshot("workspace-a"); !ok || record.Lifecycle != RunnerLifecycleRunning || pid != 7101 {
		t.Fatalf("Binding child was not supervised by Computer Host: record=%+v pid=%d ok=%v", record, pid, ok)
	}
	waitForHostHealth(t, "http://"+listener.Addr().String()+"/health")
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunProcess: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Computer Host process did not stop")
	}
}

func TestHostProcessProjectsPreviousPackageTakeoverUntilLauncherExits(t *testing.T) {
	var sourceAlive atomic.Bool
	sourceAlive.Store(true)
	state := &hostProcessState{
		identity: HostProcessIdentity{
			ComputerID: "computer-a", ComputerGeneration: 251, Version: "v0.4.24-alpha.57",
			MachineAttestationFrom: 57261,
		},
		startedAt: time.Now(), ready: true,
		previousPackageUpgradeBootstrap: true,
		sourceProcessAlive: func(pid int) (bool, bool) {
			if pid != 57261 {
				t.Fatalf("source pid = %d, want 57261", pid)
			}
			return sourceAlive.Load(), true
		},
	}
	handler := (&Host{}).processHealthHandler(state)
	readStatus := func() string {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))
		var health map[string]any
		if err := json.NewDecoder(recorder.Body).Decode(&health); err != nil {
			t.Fatal(err)
		}
		status, _ := health["status"].(string)
		return status
	}
	if got := readStatus(); got != "takeover_ready" {
		t.Fatalf("previous launcher alive: status = %q, want takeover_ready", got)
	}
	sourceAlive.Store(false)
	if got := readStatus(); got != "running" {
		t.Fatalf("previous launcher exited: status = %q, want running", got)
	}
}

func TestHostProcessOwnsMachineUpgradeAndReregistersBindingChild(t *testing.T) {
	const token = "owner-secret"
	childControl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != BindingReregisterRuntimePath || r.Header.Get("X-Multica-Control-Token") != token {
			http.Error(w, "unexpected child control request", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer childControl.Close()

	var acceptCount atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/daemon/runtimes/runtime-a/machine-upgrades/upgrade-a/accept":
			acceptCount.Add(1)
			t.Error("Host accepted a forwarded heartbeat Machine Upgrade")
			http.Error(w, "Host must not execute Machine Upgrade", http.StatusConflict)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	child := &readySupervisorChild{supervisorTestChild: newSupervisorTestChild(7201), controlURL: childControl.URL}
	host, err := NewHost(HostConfig{
		Spawn: func(string, int64) (BindingChild, error) { return child, nil }, ControlToken: token,
	})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- host.RunProcess(ctx, HostProcessConfig{
			Listener: listener,
			Identity: HostProcessIdentity{
				ComputerID: "computer-a", ComputerGeneration: 9, Environment: "test",
				Version: "v1.0.0", ServerURL: upstream.URL,
			},
			DesiredWorkspaceIDs: func() ([]string, error) { return []string{"workspace-a"}, nil },
		})
	}()
	waitForHostCurrent(t, host, BindingChildIdentity{WorkspaceID: "workspace-a", RunnerGeneration: 1, PID: 7201})
	hostClient := NewHostControlClient("http://"+listener.Addr().String(), token, BindingChildIdentity{
		WorkspaceID: "workspace-a", RunnerGeneration: 1, PID: 7201,
	})
	if err := hostClient.ReportRuntimeSet(context.Background(), []map[string]string{{
		"id": "runtime-a", "workspace_id": "workspace-a", "provider": "pi",
	}}, "runtime-token", time.Now().Add(time.Hour).Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("report Runtime set: %v", err)
	}
	if err := hostClient.ForwardMachineActions(context.Background(), protocol.DaemonHeartbeatAckPayload{
		RuntimeID:             "runtime-a",
		PendingMachineUpgrade: &protocol.DaemonHeartbeatPendingMachineUpgrade{ID: "upgrade-a", TargetVersion: "v1.0.0"},
	}); err != nil {
		t.Fatalf("forward Machine Upgrade: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if got := acceptCount.Load(); got != 0 {
		t.Fatalf("Host executed forwarded heartbeat upgrade %d times, want 0", got)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("RunProcess: %v", err)
	}
}

func waitForHostCurrent(t *testing.T, host *Host, identity BindingChildIdentity) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if host.Current(identity) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Binding child never became current: %+v", identity)
}

func stringPointer(value string) *string { return &value }

func waitForHostHealth(t *testing.T, endpoint string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(endpoint)
		if err == nil {
			var health map[string]any
			decodeErr := json.NewDecoder(response.Body).Decode(&health)
			response.Body.Close()
			if decodeErr == nil && health["status"] == "running" {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Computer Host never reported running at %s", endpoint)
}
