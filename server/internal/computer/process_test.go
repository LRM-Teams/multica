package computer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestProcessShutdownHandlerLogsAuditMetadata(t *testing.T) {
	var logs bytes.Buffer
	computerCore := &ComputerCore{logger: slog.New(slog.NewTextHandler(&logs, nil))}
	canceled := make(chan struct{})
	state := &computerProcessState{cancel: func() { close(canceled) }}
	handler := computerCore.processShutdownHandler(state)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/shutdown", nil)
	request.Header.Set(shutdownSourceHeader, "desktop")
	request.Header.Set(shutdownActionHeader, "restart")
	request.Header.Set(shutdownRequestPIDHeader, "8123")

	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("shutdown handler did not cancel the Computer context")
	}
	got := logs.String()
	for _, want := range []string{
		"Computer shutdown requested",
		"source=desktop",
		"action=restart",
		"request_pid=8123",
		"remote_address=192.0.2.1:1234",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("shutdown log %q does not contain %q", got, want)
		}
	}
}

func TestProcessRoutesPreserveControlMethodErrors(t *testing.T) {
	computerCore := &ComputerCore{}
	state := &computerProcessState{}
	mux := http.NewServeMux()
	computerCore.registerProcessRoutes(mux, state)

	for _, path := range []string{
		"/shutdown",
		"/environment-switch/prepare",
		"/environment-switch/release",
	} {
		t.Run(path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))

			if recorder.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
			}
			if got := recorder.Body.String(); got != "method not allowed\n" {
				t.Fatalf("body = %q, want %q", got, "method not allowed\n")
			}
			if allow := recorder.Header().Get("Allow"); allow != "" {
				t.Fatalf("Allow = %q, want empty", allow)
			}
		})
	}
}

func TestComputerProcessOwnsResidentControlAndDesiredWorkspaces(t *testing.T) {
	child := &readyWorkspaceDaemonTestProcess{workspaceDaemonTestProcess: newWorkspaceDaemonTestProcess(7101)}
	computerCore, err := NewComputerCore(ComputerCoreConfig{
		Spawn: func(workspaceID string) (WorkspaceDaemonProcess, error) {
			if workspaceID != "workspace-a" {
				t.Fatalf("spawn workspace = %q", workspaceID)
			}
			child.workspaceID, child.daemonInstanceID = workspaceID, "child-7201"
			return child, nil
		},
		ControlToken: "owner-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := ServiceControlEndpoint(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- computerCore.Run(ctx, ComputerProcessConfig{
			ServiceEndpoint: endpoint,
			Identity: ComputerIdentity{
				ComputerID:        "computer-a",
				ServiceGeneration: "service-9",
				Environment:       "test",
				Version:           "v1.2.3",
			},
			DesiredWorkspaceIDs: func() ([]string, error) { return []string{"workspace-a"}, nil },
		})
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		record, pid, ok := computerCore.Snapshot("workspace-a")
		if ok && record.Status == WorkspaceDaemonRunning && pid == 7101 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if record, pid, ok := computerCore.Snapshot("workspace-a"); !ok || record.Status != WorkspaceDaemonRunning || pid != 7101 {
		t.Fatalf("WorkspaceDaemon was not supervised by DaemonCore: record=%+v pid=%d ok=%v", record, pid, ok)
	}
	waitForComputerHealth(t, endpoint)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunProcess: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Computer process did not stop")
	}
}

func TestComputerProcessOwnsMachineUpgradeAndReregistersWorkspaceDaemon(t *testing.T) {
	const token = "owner-secret"
	childControl := localControlTestServer(t, func(_ context.Context, operation string, headers map[string]string, _ json.RawMessage) (any, error) {
		if operation != "runner-ready" || headers["X-Multica-Control-Token"] != token {
			return nil, errors.New("unexpected child control request")
		}
		return nil, nil
	})

	upstream := httptest.NewServer(http.NotFoundHandler())
	defer upstream.Close()

	child := &readyWorkspaceDaemonTestProcess{workspaceDaemonTestProcess: newWorkspaceDaemonTestProcess(7201), controlEndpoint: childControl}
	computerCore, err := NewComputerCore(ComputerCoreConfig{
		Spawn: func(workspaceID string) (WorkspaceDaemonProcess, error) {
			child.workspaceID, child.daemonInstanceID = workspaceID, "child-7201"
			return child, nil
		}, ControlToken: token,
	})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := ServiceControlEndpoint(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- computerCore.Run(ctx, ComputerProcessConfig{
			ServiceEndpoint: endpoint,
			Identity: ComputerIdentity{
				ComputerID: "computer-a", ServiceGeneration: "service-9", Environment: "test",
				Version: "v1.0.0", ServerURL: upstream.URL,
			},
			DesiredWorkspaceIDs: func() ([]string, error) { return []string{"workspace-a"}, nil },
		})
	}()
	var record WorkspaceDaemonSnapshot
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if current, _, ok := computerCore.Snapshot("workspace-a"); ok && current.DaemonInstanceID != "" {
			record = current
			break
		}
		time.Sleep(time.Millisecond)
	}
	waitForComputerCurrent(t, computerCore, WorkspaceDaemonIdentity{WorkspaceID: "workspace-a", DaemonInstanceID: record.DaemonInstanceID, PID: 7201})
	computerClient := NewComputerControlClient(endpoint, token, WorkspaceDaemonIdentity{
		WorkspaceID: "workspace-a", DaemonInstanceID: record.DaemonInstanceID, PID: 7201,
	})
	if err := computerClient.ReportRuntimeSet(context.Background(), []map[string]string{{
		"id": "runtime-a", "workspace_id": "workspace-a", "provider": "pi",
	}}, "runtime-token", time.Now().Add(time.Hour).Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("report Runtime set: %v", err)
	}
	if err := computerClient.RequestComputerUpgrade(context.Background(), protocol.ComputerUpgradePayload{
		RequestID: "upgrade-a", TargetVersion: "v1.0.0",
	}); err != nil {
		t.Fatalf("request Computer upgrade: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("RunProcess: %v", err)
	}
}

func waitForComputerCurrent(t *testing.T, computerCore *ComputerCore, identity WorkspaceDaemonIdentity) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if computerCore.Current(identity) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("WorkspaceDaemon never became current: %+v", identity)
}

func stringPointer(value string) *string { return &value }

func waitForComputerHealth(t *testing.T, endpoint string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		health := ProbeHealth(context.Background(), endpoint)
		if health["status"] == "running" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Computer never reported running at %s", endpoint)
}
