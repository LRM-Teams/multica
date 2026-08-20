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
	host := &Host{logger: slog.New(slog.NewTextHandler(&logs, nil))}
	canceled := make(chan struct{})
	state := &hostProcessState{cancel: func() { close(canceled) }}
	handler := host.processShutdownHandler(state)
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

func TestHostProcessOwnsResidentControlAndDesiredBindings(t *testing.T) {
	child := &readySupervisorChild{supervisorTestChild: newSupervisorTestChild(7101)}
	host, err := NewHost(HostConfig{
		Spawn: func(workspaceID string) (BindingChild, error) {
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
		done <- host.RunProcess(ctx, HostProcessConfig{
			ServiceEndpoint: endpoint,
			Identity: HostProcessIdentity{
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
		record, pid, ok := host.Snapshot("workspace-a")
		if ok && record.Lifecycle == RunnerLifecycleRunning && pid == 7101 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if record, pid, ok := host.Snapshot("workspace-a"); !ok || record.Lifecycle != RunnerLifecycleRunning || pid != 7101 {
		t.Fatalf("Binding child was not supervised by Computer Host: record=%+v pid=%d ok=%v", record, pid, ok)
	}
	waitForHostHealth(t, endpoint)
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

func TestHostProcessOwnsMachineUpgradeAndReregistersBindingChild(t *testing.T) {
	const token = "owner-secret"
	childControl := localControlTestServer(t, func(_ context.Context, operation string, headers map[string]string, _ json.RawMessage) (any, error) {
		if operation != "runner-ready" || headers["X-Multica-Control-Token"] != token {
			return nil, errors.New("unexpected child control request")
		}
		return nil, nil
	})

	upstream := httptest.NewServer(http.NotFoundHandler())
	defer upstream.Close()

	child := &readySupervisorChild{supervisorTestChild: newSupervisorTestChild(7201), controlEndpoint: childControl}
	host, err := NewHost(HostConfig{
		Spawn: func(workspaceID string) (BindingChild, error) {
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
		done <- host.RunProcess(ctx, HostProcessConfig{
			ServiceEndpoint: endpoint,
			Identity: HostProcessIdentity{
				ComputerID: "computer-a", ServiceGeneration: "service-9", Environment: "test",
				Version: "v1.0.0", ServerURL: upstream.URL,
			},
			DesiredWorkspaceIDs: func() ([]string, error) { return []string{"workspace-a"}, nil },
		})
	}()
	var record RunnerRecord
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if current, _, ok := host.Snapshot("workspace-a"); ok && current.DaemonInstanceID() != "" {
			record = current
			break
		}
		time.Sleep(time.Millisecond)
	}
	waitForHostCurrent(t, host, BindingChildIdentity{WorkspaceID: "workspace-a", DaemonInstanceID: record.DaemonInstanceID(), PID: 7201})
	hostClient := NewHostControlClient(endpoint, token, BindingChildIdentity{
		WorkspaceID: "workspace-a", DaemonInstanceID: record.DaemonInstanceID(), PID: 7201,
	})
	if err := hostClient.ReportRuntimeSet(context.Background(), []map[string]string{{
		"id": "runtime-a", "workspace_id": "workspace-a", "provider": "pi",
	}}, "runtime-token", time.Now().Add(time.Hour).Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("report Runtime set: %v", err)
	}
	if err := hostClient.RequestComputerUpgrade(context.Background(), protocol.ComputerUpgradePayload{
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
		health := ProbeHealth(context.Background(), endpoint)
		if health["status"] == "running" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Computer Host never reported running at %s", endpoint)
}
