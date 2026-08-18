package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const bindingChildRuntimeHelperEnv = "MULTICA_BINDING_CHILD_RUNTIME_HELPER"

func TestBindingChildProcessFallbackRunsTheRealWorkspaceRunner(t *testing.T) {
	const (
		workspaceID  = "workspace-a"
		computerID   = "computer-a"
		controlToken = "host-control-token"
	)
	readyFrames := make(chan protocol.WorkspaceRunnerReadyPayload, 1)
	runtimeWakeConnected := make(chan struct{}, 1)
	var registerCalls atomic.Int32
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/daemon/computer/heartbeat":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "generation": 11})
		case r.URL.Path == "/api/daemon/register":
			registerCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"runtimes":                []map[string]any{{"id": "runtime-a", "workspace_id": workspaceID, "provider": "pi"}},
				"daemon_token":            "scoped-daemon-token",
				"daemon_token_expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano),
			})
		case r.URL.Path == "/api/daemon/runtimes/runtime-a/recover-orphans":
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/daemon/deregister":
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/daemon/connect" && r.URL.Query().Get("workspace_id") == workspaceID:
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var frame protocol.Message
			var ready protocol.WorkspaceRunnerReadyPayload
			if json.Unmarshal(raw, &frame) != nil || frame.Type != protocol.EventWorkspaceRunnerReady || json.Unmarshal(frame.Payload, &ready) != nil {
				return
			}
			readyFrames <- ready
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		case r.URL.Path == "/api/daemon/connect" && r.URL.Query().Get("runtime_ids") == "runtime-a":
			if got := r.Header.Get("Authorization"); got != "Bearer scoped-daemon-token" {
				http.Error(w, "runtime wake socket used the wrong Binding credential", http.StatusUnauthorized)
				return
			}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			runtimeWakeConnected <- struct{}{}
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	root := t.TempDir()
	workspacesRoot := filepath.Join(root, "workspaces")
	if err := os.MkdirAll(workspacesRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	providerPath := filepath.Join(root, "fake-pi")
	if err := os.WriteFile(providerPath, []byte("#!/bin/sh\necho 9.9.9\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := computer.NewBindingsStore(root).AddOrRepair(computer.WorkspaceBinding{
		Environment: "test", WorkspaceID: workspaceID, ComputerID: computerID,
		Credential: "binding-token", CredentialExpiresAt: time.Now().Add(time.Hour), Active: true,
	}); err != nil {
		t.Fatal(err)
	}

	host := newBindingControlTestHost(t, controlToken, 0, computer.HostControlCallbacks{})
	hostServerURL, hostListener := localHostControlRPCListener(t, host.host)
	t.Setenv(bindingChildRuntimeHelperEnv, providerPath)
	t.Setenv("MULTICA_BINDING_CHILD_CONTROL_TOKEN", controlToken)
	bootstrap := computer.BindingChildBootstrap{
		ProtocolVersion: computer.BindingChildProtocolVersion, WorkspaceID: workspaceID,
		ComputerID: computerID, ComputerGeneration: 11, RunnerGeneration: 1,
		Environment: "test", ServerBaseURL: server.URL, ServiceEndpoint: hostServerURL,
		BindingsRoot: root, WorkspacesRoot: workspacesRoot,
	}
	child, err := computer.StartBindingProcess(os.Args[0], []string{"-test.run=TestRunBindingChildProcessHelper"}, bootstrap)
	if err != nil {
		t.Fatalf("start Binding child process: %v", err)
	}
	t.Cleanup(func() { _ = child.Stop() })
	installLiveBindingChild(t, host, workspaceID, child.PID())
	child.Activate()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ready, err := child.AwaitReady(ctx)
	if err != nil {
		t.Fatalf("await real Binding child Ready: %v", err)
	}
	if ready.PID != child.PID() || ready.WorkspaceID != workspaceID || ready.RunnerGeneration != 1 {
		t.Fatalf("Binding child Ready = %+v, pid=%d", ready, child.PID())
	}
	if err := computer.RequestBindingReregisterRuntime(ctx, ready.RunnerEndpoint, controlToken, computer.BindingChildIdentity{
		WorkspaceID: workspaceID, RunnerGeneration: ready.RunnerGeneration, PID: ready.PID,
	}); err != nil {
		t.Fatalf("request child-owned Runtime re-registration: %v", err)
	}
	if got := registerCalls.Load(); got != 2 {
		t.Fatalf("child-owned Runtime registrations = %d, want initial + machine-control refresh", got)
	}
	select {
	case frame := <-readyFrames:
		if frame.WorkspaceID != workspaceID || frame.DaemonInstanceID == "" {
			t.Fatalf("real Workspace Runner Ready frame = %+v", frame)
		}
	case <-ctx.Done():
		t.Fatal("real child never connected its Workspace Runner")
	}
	select {
	case <-runtimeWakeConnected:
	case <-ctx.Done():
		t.Fatal("real child never authenticated its runtime wake socket with the scoped Binding credential")
	}
	_ = hostListener.Close()
	exited := make(chan computer.RunnerExitClass, 1)
	go func() { exited <- child.Wait() }()
	select {
	case class := <-exited:
		t.Fatalf("Binding child exited after Host loss with class %s", class)
	case <-time.After(1500 * time.Millisecond):
	}
	if err := child.Stop(); err != nil {
		t.Fatalf("stop Binding child after Host loss: %v", err)
	}
	select {
	case <-exited:
	case <-ctx.Done():
		t.Fatal("Binding child did not stop through its process handle")
	}
}

func TestComputerHostRunsTwoRealIsolatedBindingChildProcesses(t *testing.T) {
	const (
		computerID   = "computer-a"
		controlToken = "host-control-token"
	)
	workspaceIDs := []string{"workspace-a", "workspace-b"}
	readyFrames := make(chan string, len(workspaceIDs))
	disconnected := make(chan string, len(workspaceIDs))
	var registerCalls atomic.Int32
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/daemon/computer/heartbeat":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "generation": 21})
		case r.URL.Path == "/api/daemon/register":
			var request struct {
				WorkspaceID string `json:"workspace_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if request.WorkspaceID != workspaceIDs[0] && request.WorkspaceID != workspaceIDs[1] {
				http.Error(w, "unexpected Workspace", http.StatusBadRequest)
				return
			}
			registerCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"runtimes": []map[string]any{{
					"id":           "runtime-" + strings.TrimPrefix(request.WorkspaceID, "workspace-"),
					"workspace_id": request.WorkspaceID, "provider": "pi",
				}},
				"daemon_token":            "scoped-token-" + request.WorkspaceID,
				"daemon_token_expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano),
			})
		case strings.HasPrefix(r.URL.Path, "/api/daemon/runtimes/runtime-") && strings.HasSuffix(r.URL.Path, "/recover-orphans"):
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/daemon/deregister":
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/daemon/connect":
			workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			observedReady := false
			defer func() {
				_ = conn.Close()
				if observedReady {
					disconnected <- workspaceID
				}
			}()
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var frame protocol.Message
			if json.Unmarshal(raw, &frame) != nil || frame.Type != protocol.EventWorkspaceRunnerReady {
				return
			}
			observedReady = true
			readyFrames <- workspaceID
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	root := t.TempDir()
	workspacesRoot := filepath.Join(root, "workspaces")
	if err := os.MkdirAll(workspacesRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	providerPath := filepath.Join(root, "fake-pi")
	if err := os.WriteFile(providerPath, []byte("#!/bin/sh\necho 9.9.9\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	store := computer.NewBindingsStore(root)
	for _, workspaceID := range workspaceIDs {
		if err := store.AddOrRepair(computer.WorkspaceBinding{
			Environment: "test", WorkspaceID: workspaceID, ComputerID: computerID,
			Credential:          "binding-token-" + workspaceID,
			CredentialExpiresAt: time.Now().Add(time.Hour), Active: true,
		}); err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv(bindingChildRuntimeHelperEnv, providerPath)
	t.Setenv("MULTICA_BINDING_CHILD_CONTROL_TOKEN", controlToken)
	var serviceEndpoint string
	host, err := computer.NewHost(computer.HostConfig{
		ControlToken: controlToken,
		Spawn: func(workspaceID string, runnerGeneration int64) (computer.BindingChild, error) {
			return computer.StartBindingProcess(os.Args[0], []string{"-test.run=TestRunBindingChildProcessHelper"}, computer.BindingChildBootstrap{
				ProtocolVersion: computer.BindingChildProtocolVersion, WorkspaceID: workspaceID,
				ComputerID: computerID, ComputerGeneration: 21, RunnerGeneration: runnerGeneration,
				Environment: "test", ServerBaseURL: server.URL, ServiceEndpoint: serviceEndpoint,
				BindingsRoot: root, WorkspacesRoot: workspacesRoot,
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	serviceEndpoint = localHostControlRPC(t, host)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	host.Reconcile(ctx, workspaceIDs)
	t.Cleanup(func() { host.Reconcile(context.Background(), nil) })
	if err := host.WaitReady(ctx, workspaceIDs); err != nil {
		t.Fatalf("wait for two real Binding children: %v", err)
	}
	recordA, pidA, okA := host.Snapshot(workspaceIDs[0])
	recordB, pidB, okB := host.Snapshot(workspaceIDs[1])
	if !okA || !okB || recordA.Lifecycle != computer.RunnerLifecycleRunning || recordB.Lifecycle != computer.RunnerLifecycleRunning {
		t.Fatalf("real Binding children not running: A=%+v/%t B=%+v/%t", recordA, okA, recordB, okB)
	}
	if pidA <= 0 || pidB <= 0 || pidA == pidB || pidA == os.Getpid() || pidB == os.Getpid() {
		t.Fatalf("Binding child PIDs = %d/%d, Host PID = %d", pidA, pidB, os.Getpid())
	}
	ready := map[string]bool{}
	for len(ready) < len(workspaceIDs) {
		select {
		case workspaceID := <-readyFrames:
			ready[workspaceID] = true
		case <-ctx.Done():
			t.Fatalf("WorkspaceRunner Ready set = %v", ready)
		}
	}
	if got := registerCalls.Load(); got != int32(len(workspaceIDs)) {
		t.Fatalf("Binding Runtime registrations = %d, want %d", got, len(workspaceIDs))
	}

	host.Reconcile(ctx, []string{workspaceIDs[0]})
	waitForBindingLifecycle(t, ctx, host, workspaceIDs[1], computer.RunnerLifecycleStopped)
	afterA, afterPIDA, ok := host.Snapshot(workspaceIDs[0])
	if !ok || afterA.Lifecycle != computer.RunnerLifecycleRunning || afterA.Generation() != recordA.Generation() || afterPIDA != pidA {
		t.Fatalf("removing Binding B mutated A: before=%+v/%d after=%+v/%d", recordA, pidA, afterA, afterPIDA)
	}
	select {
	case workspaceID := <-disconnected:
		if workspaceID != workspaceIDs[1] {
			t.Fatalf("removing B disconnected sibling %q", workspaceID)
		}
	case <-ctx.Done():
		t.Fatal("removed Binding child B did not close its WorkspaceRunner connection")
	}
}

func waitForBindingLifecycle(t *testing.T, ctx context.Context, host *computer.Host, workspaceID string, want computer.RunnerLifecycle) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		record, _, ok := host.Snapshot(workspaceID)
		if ok && record.Lifecycle == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("Binding %s lifecycle = %+v/%t, want %s", workspaceID, record, ok, want)
		case <-ticker.C:
		}
	}
}

func TestBindingChildPublishesReadyWithoutAgentRuntimesOrWorkspaceRunnerWS(t *testing.T) {
	const (
		workspaceID  = "workspace-a"
		computerID   = "computer-a"
		controlToken = "host-control-token"
	)
	var registerCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/daemon/computer/heartbeat":
			t.Errorf("DaemonCore liveness must not POST /api/daemon/computer/heartbeat")
			http.Error(w, "heartbeat retired for liveness", http.StatusGone)
		case r.URL.Path == "/api/daemon/register":
			registerCalls.Add(1)
			http.Error(w, `{"error":"at least one runtime is required"}`, http.StatusBadRequest)
		case r.URL.Path == "/api/daemon/connect":
			upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	root := t.TempDir()
	workspacesRoot := filepath.Join(root, "workspaces")
	if err := os.MkdirAll(workspacesRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := computer.NewBindingsStore(root).AddOrRepair(computer.WorkspaceBinding{
		Environment: "test", WorkspaceID: workspaceID, ComputerID: computerID,
		Credential: "binding-token", CredentialExpiresAt: time.Now().Add(time.Hour), Active: true,
	}); err != nil {
		t.Fatal(err)
	}

	host := newBindingControlTestHost(t, controlToken, 0, computer.HostControlCallbacks{})
	hostServerURL := localHostControlRPC(t, host.host)
	t.Setenv("MULTICA_BINDING_CHILD_CONTROL_TOKEN", controlToken)
	t.Setenv("MULTICA_BINDING_CHILD_ZERO_RUNTIME", "1")
	bootstrap := computer.BindingChildBootstrap{
		ProtocolVersion: computer.BindingChildProtocolVersion, WorkspaceID: workspaceID,
		ComputerID: computerID, ComputerGeneration: 31, RunnerGeneration: 1,
		Environment: "test", ServerBaseURL: server.URL, ServiceEndpoint: hostServerURL,
		BindingsRoot: root, WorkspacesRoot: workspacesRoot,
	}
	child, err := computer.StartBindingProcess(os.Args[0], []string{"-test.run=TestRunBindingChildProcessHelper"}, bootstrap)
	if err != nil {
		t.Fatalf("start Binding child process: %v", err)
	}
	t.Cleanup(func() { _ = child.Stop() })
	installLiveBindingChild(t, host, workspaceID, child.PID())
	child.Activate()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ready, err := child.AwaitReady(ctx)
	if err != nil {
		t.Fatalf("zero-runtime DaemonCore must publish Ready after Workspace Runner connect: %v", err)
	}
	if ready.PID != child.PID() || ready.WorkspaceID != workspaceID || ready.RunnerGeneration != 1 {
		t.Fatalf("Binding child Ready = %+v, pid=%d", ready, child.PID())
	}
	if got := registerCalls.Load(); got != 0 {
		t.Fatalf("zero-runtime Binding child posted register %d times", got)
	}
}

func TestRunBindingChildProcessHelper(t *testing.T) {
	providerPath := os.Getenv(bindingChildRuntimeHelperEnv)
	zeroRuntime := os.Getenv("MULTICA_BINDING_CHILD_ZERO_RUNTIME") == "1"
	if providerPath == "" && !zeroRuntime {
		return
	}
	bootstrap, err := computer.ReadBindingChildBootstrap(os.Stdin)
	if err != nil {
		os.Exit(2)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	agents := map[string]AgentEntry{}
	if !zeroRuntime {
		agents["pi"] = AgentEntry{Path: providerPath}
	}
	err = RunBindingChild(context.Background(), BindingChildRunConfig{
		Daemon: Config{
			DaemonID: bootstrap.ComputerID, ComputerGeneration: bootstrap.ComputerGeneration,
			Environment: bootstrap.Environment, ServerBaseURL: bootstrap.ServerBaseURL,
			BindingsRoot: bootstrap.BindingsRoot, WorkspacesRoot: bootstrap.WorkspacesRoot,
			LocalControlToken: os.Getenv("MULTICA_BINDING_CHILD_CONTROL_TOKEN"),
			Agents:            agents,
			PollInterval:      time.Hour, HeartbeatInterval: time.Hour,
		},
		Bootstrap: bootstrap,
		Logger:    logger,
		PublishReady: func(ready computer.BindingChildReady) error {
			return computer.WriteBindingChildReady(os.Stdout, ready)
		},
	})
	if err != nil {
		logger.Error("Binding child helper failed", "error", err)
		os.Exit(3)
	}
}

func TestBindingChildCredentialProxyHasAChildOwnedListener(t *testing.T) {
	d := New(Config{HealthPort: DefaultHealthPort}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	listener, err := d.listenBindingCredentialProxy()
	if err != nil {
		t.Fatalf("listenBindingCredentialProxy: %v", err)
	}
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok || !addr.IP.IsLoopback() || addr.Port < 1 {
		t.Fatalf("Binding child Credential Proxy addr = %v, want loopback ephemeral port", listener.Addr())
	}
	if d.cfg.HealthPort != addr.Port {
		t.Fatalf("Binding child launch port = %d, want listener port %d", d.cfg.HealthPort, addr.Port)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.serveBindingCredentialProxy(ctx, listener)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("Binding child Credential Proxy did not stop")
		}
	})

	response, err := http.Get("http://" + listener.Addr().String() + "/credential-proxy/messages/check")
	if err != nil {
		t.Fatalf("GET Binding child Credential Proxy: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		t.Fatal("Binding child Credential Proxy route is not installed")
	}

	health, err := http.Get("http://" + listener.Addr().String() + "/health")
	if err != nil {
		t.Fatalf("GET Binding child machine health route: %v", err)
	}
	_ = health.Body.Close()
	if health.StatusCode != http.StatusNotFound {
		t.Fatalf("Binding child exposed Host /health with status %d", health.StatusCode)
	}
}

func TestExpiredBindingDoesNotInstallWorkspaceToken(t *testing.T) {
	d := newDaemonForRole(Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), daemonProcessBindingChild)
	err := d.prepareBindingExecutionCredential(computer.WorkspaceBinding{
		WorkspaceID: "workspace-a", ComputerID: "computer-a",
		Credential: "expired-binding-token", CredentialExpiresAt: time.Now().Add(-time.Hour), Active: true,
	})
	if err == nil || !strings.Contains(err.Error(), "no live execution credential") {
		t.Fatalf("expired Binding should fail closed without a human PAT, got %v", err)
	}
	if d.client.tokenForWorkspace("workspace-a") != "" {
		t.Fatal("expired Binding must not install a workspace token")
	}
}

func TestCurrentBindingBootstrapRejectsExpiredCredential(t *testing.T) {
	d := newDaemonForRole(Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), daemonProcessBindingChild)
	err := d.prepareBindingExecutionCredential(computer.WorkspaceBinding{
		WorkspaceID: "workspace-a", ComputerID: "computer-a",
		Credential: "expired-binding-token", CredentialExpiresAt: time.Now().Add(-time.Hour), Active: true,
	})
	if err == nil || !strings.Contains(err.Error(), "no live execution credential") {
		t.Fatalf("current-package expired Binding error = %v", err)
	}
}

func TestBindingChildMembershipRefreshStopsRevokedBinding(t *testing.T) {
	const workspaceID = "workspace-a"
	root := t.TempDir()
	expiresAt := time.Now().Add(2 * time.Hour)
	store := computer.NewBindingsStore(root)
	if err := store.AddOrRepair(computer.WorkspaceBinding{
		Environment: "test", WorkspaceID: workspaceID, ComputerID: "computer-a",
		Credential: "binding-token", CredentialExpiresAt: expiresAt, Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/daemon/computer/heartbeat" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	d := newDaemonForRole(Config{
		BindingsRoot: root, Environment: "test", DaemonID: "computer-a", ComputerGeneration: 1,
		ServerBaseURL: server.URL,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), daemonProcessBindingChild)
	d.client.SetWorkspaceDaemonToken(workspaceID, "binding-token", expiresAt)

	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		done <- d.bindingWorkspaceRefreshLoop(ctx, workspaceID, "binding-token", 5*time.Millisecond)
	}()
	time.Sleep(20 * time.Millisecond)
	if err := store.RemoveForEnvironment("test", workspaceID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "no longer active") {
			t.Fatalf("membership refresh error = %v", err)
		}
	case <-ctx.Done():
		t.Fatal("Binding child did not stop after its connection was revoked")
	}
}

func TestBindingChildrenUseIsolatedDurableExecutionState(t *testing.T) {
	root := t.TempDir()
	workspacesRoot := filepath.Join(root, "workspaces")
	firstRoot := filepath.Join(root, "bindings", "workspace-a")
	secondRoot := filepath.Join(root, "bindings", "workspace-b")
	first := newDaemonForRole(Config{WorkspacesRoot: workspacesRoot, BindingStateRoot: firstRoot, WorkspaceID: "workspace-a"}, slog.New(slog.NewTextHandler(io.Discard, nil)), daemonProcessBindingChild)
	second := newDaemonForRole(Config{WorkspacesRoot: workspacesRoot, BindingStateRoot: secondRoot, WorkspaceID: "workspace-b"}, slog.New(slog.NewTextHandler(io.Discard, nil)), daemonProcessBindingChild)

	for label, pair := range map[string][2]string{
		"Reminder cache":  {first.reminderCache.path, second.reminderCache.path},
		"Activity outbox": {first.mixedRunActivityOutbox.path, second.mixedRunActivityOutbox.path},
	} {
		if pair[0] == "" || pair[1] == "" || pair[0] == pair[1] {
			t.Fatalf("%s paths are not isolated: %q / %q", label, pair[0], pair[1])
		}
		if !strings.HasPrefix(pair[0], firstRoot) || !strings.HasPrefix(pair[1], secondRoot) {
			t.Fatalf("%s escaped Binding state roots: %q / %q", label, pair[0], pair[1])
		}
	}
}
