package daemon

import (
	"context"
	"encoding/json"
	"fmt"
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

const workspaceDaemonRuntimeHelperEnv = "MULTICA_WORKSPACE_DAEMON_RUNTIME_HELPER"

func TestWorkspaceDaemonProcessFallbackRunsTheRealWorkspaceDaemon(t *testing.T) {
	const (
		workspaceID  = "workspace-a"
		computerID   = "computer-a"
		controlToken = "computer-control-token"
	)
	readyFrames := make(chan protocol.WorkspaceReadyPayload, 1)
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
			var ready protocol.WorkspaceReadyPayload
			if json.Unmarshal(raw, &frame) != nil || frame.Type != protocol.EventWorkspaceDaemonReady || json.Unmarshal(frame.Payload, &ready) != nil {
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

	computerCore := newComputerControlTestHarness(t, controlToken, computer.ComputerControlCallbacks{})
	computerControlURL, computerControlListener := localComputerControlRPCListener(t, computerCore.computerCore)
	t.Setenv(workspaceDaemonRuntimeHelperEnv, providerPath)
	t.Setenv("MULTICA_WORKSPACE_DAEMON_CONTROL_TOKEN", controlToken)
	bootstrap := computer.WorkspaceDaemonBootstrap{
		ProtocolVersion: computer.WorkspaceDaemonProtocolVersion, WorkspaceID: workspaceID,
		ComputerID:  computerID,
		Environment: "test", ServerBaseURL: server.URL, ServiceEndpoint: computerControlURL,
		BindingsRoot: root, WorkspacesRoot: workspacesRoot,
	}
	child, err := computer.StartWorkspaceDaemonProcess(os.Args[0], []string{"-test.run=TestRunWorkspaceDaemonProcessHelper"}, bootstrap)
	if err != nil {
		t.Fatalf("start WorkspaceDaemon process: %v", err)
	}
	t.Cleanup(func() { _ = child.Stop() })
	computerCore.state.mu.Lock()
	computerCore.state.pids[workspaceID] = child.PID()
	computerCore.state.mu.Unlock()
	installStartingWorkspaceDaemon(t, computerCore, workspaceID, child.PID())
	child.Activate()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ready, err := child.AwaitReady(ctx)
	if err != nil {
		t.Fatalf("await real WorkspaceDaemon Ready: %v", err)
	}
	if ready.PID != child.PID() || ready.WorkspaceID != workspaceID || ready.DaemonInstanceID == "" {
		t.Fatalf("WorkspaceDaemon Ready = %+v, pid=%d", ready, child.PID())
	}
	if err := computer.RequestWorkspaceDaemonReregisterRuntime(ctx, ready.RunnerEndpoint, controlToken, computer.WorkspaceDaemonIdentity{
		WorkspaceID: workspaceID, DaemonInstanceID: ready.DaemonInstanceID, PID: ready.PID,
	}); err != nil {
		t.Fatalf("request child-owned Runtime re-registration: %v", err)
	}
	if got := registerCalls.Load(); got != 2 {
		t.Fatalf("child-owned Runtime registrations = %d, want initial + machine-control refresh", got)
	}
	select {
	case frame := <-readyFrames:
		if frame.WorkspaceID != workspaceID || frame.DaemonInstanceID == "" {
			t.Fatalf("real WorkspaceDaemon Ready frame = %+v", frame)
		}
	case <-ctx.Done():
		t.Fatal("real child never connected its WorkspaceDaemon")
	}
	select {
	case <-runtimeWakeConnected:
	case <-ctx.Done():
		t.Fatal("real child never authenticated its runtime wake socket with the scoped Binding credential")
	}
	_ = computerControlListener.Close()
	exited := make(chan computer.WorkspaceDaemonExitClass, 1)
	go func() { exited <- child.Wait() }()
	select {
	case class := <-exited:
		t.Fatalf("WorkspaceDaemon exited after Computer loss with class %s", class)
	case <-time.After(1500 * time.Millisecond):
	}
	if err := child.Stop(); err != nil {
		t.Fatalf("stop WorkspaceDaemon after Computer loss: %v", err)
	}
	select {
	case <-exited:
	case <-ctx.Done():
		t.Fatal("WorkspaceDaemon did not stop through its process handle")
	}
}

func TestWorkspaceDaemonOrphanRecoveryUsesOneBoundedParallelWindow(t *testing.T) {
	const runtimeCount = 4
	var started atomic.Int32
	release := make(chan struct{})
	defer close(release)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/recover-orphans") {
			http.NotFound(w, r)
			return
		}
		started.Add(1)
		<-release
	}))
	t.Cleanup(server.Close)

	d := New(Config{ServerBaseURL: server.URL}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	runtimeIDs := make([]string, 0, runtimeCount)
	for i := range runtimeCount {
		runtimeIDs = append(runtimeIDs, fmt.Sprintf("runtime-%d", i))
	}

	startedAt := time.Now()
	d.recoverWorkspaceDaemonOrphans(context.Background(), "workspace-a", runtimeIDs, 100*time.Millisecond)
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("orphan recovery exceeded one bounded window: %v", elapsed)
	}
	if got := started.Load(); got != runtimeCount {
		t.Fatalf("parallel orphan recovery requests = %d, want %d", got, runtimeCount)
	}
}

func TestComputerRunsTwoRealIsolatedWorkspaceDaemonProcesses(t *testing.T) {
	const (
		computerID   = "computer-a"
		controlToken = "computer-control-token"
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
			if json.Unmarshal(raw, &frame) != nil || frame.Type != protocol.EventWorkspaceDaemonReady {
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

	t.Setenv(workspaceDaemonRuntimeHelperEnv, providerPath)
	t.Setenv("MULTICA_WORKSPACE_DAEMON_CONTROL_TOKEN", controlToken)
	var serviceEndpoint string
	computerCore, err := computer.NewComputerCore(computer.ComputerCoreConfig{
		ControlToken: controlToken,
		Spawn: func(workspaceID string) (computer.WorkspaceDaemonProcess, error) {
			return computer.StartWorkspaceDaemonProcess(os.Args[0], []string{"-test.run=TestRunWorkspaceDaemonProcessHelper"}, computer.WorkspaceDaemonBootstrap{
				ProtocolVersion: computer.WorkspaceDaemonProtocolVersion, WorkspaceID: workspaceID,
				ComputerID:  computerID,
				Environment: "test", ServerBaseURL: server.URL, ServiceEndpoint: serviceEndpoint,
				BindingsRoot: root, WorkspacesRoot: workspacesRoot,
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	serviceEndpoint = localComputerControlRPC(t, computerCore)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	computerCore.Reconcile(ctx, workspaceIDs)
	t.Cleanup(func() { computerCore.Reconcile(context.Background(), nil) })
	if err := computerCore.WaitReady(ctx, workspaceIDs); err != nil {
		t.Fatalf("wait for two real WorkspaceDaemons: %v", err)
	}
	recordA, pidA, okA := computerCore.Snapshot(workspaceIDs[0])
	recordB, pidB, okB := computerCore.Snapshot(workspaceIDs[1])
	if !okA || !okB || recordA.Status != computer.WorkspaceDaemonRunning || recordB.Status != computer.WorkspaceDaemonRunning {
		t.Fatalf("real WorkspaceDaemons not running: A=%+v/%t B=%+v/%t", recordA, okA, recordB, okB)
	}
	if pidA <= 0 || pidB <= 0 || pidA == pidB || pidA == os.Getpid() || pidB == os.Getpid() {
		t.Fatalf("WorkspaceDaemon PIDs = %d/%d, Computer PID = %d", pidA, pidB, os.Getpid())
	}
	ready := map[string]bool{}
	for len(ready) < len(workspaceIDs) {
		select {
		case workspaceID := <-readyFrames:
			ready[workspaceID] = true
		case <-ctx.Done():
			t.Fatalf("WorkspaceDaemon Ready set = %v", ready)
		}
	}
	if got := registerCalls.Load(); got != int32(len(workspaceIDs)) {
		t.Fatalf("Binding Runtime registrations = %d, want %d", got, len(workspaceIDs))
	}

	computerCore.Reconcile(ctx, []string{workspaceIDs[0]})
	waitForBindingLifecycle(t, ctx, computerCore, workspaceIDs[1], computer.WorkspaceDaemonStopped)
	afterA, afterPIDA, ok := computerCore.Snapshot(workspaceIDs[0])
	if !ok || afterA.Status != computer.WorkspaceDaemonRunning || afterA.DaemonInstanceID != recordA.DaemonInstanceID || afterPIDA != pidA {
		t.Fatalf("removing Binding B mutated A: before=%+v/%d after=%+v/%d", recordA, pidA, afterA, afterPIDA)
	}
	select {
	case workspaceID := <-disconnected:
		if workspaceID != workspaceIDs[1] {
			t.Fatalf("removing B disconnected sibling %q", workspaceID)
		}
	case <-ctx.Done():
		t.Fatal("removed WorkspaceDaemon B did not close its connection")
	}
}

func waitForBindingLifecycle(t *testing.T, ctx context.Context, computerCore *computer.ComputerCore, workspaceID string, want computer.WorkspaceDaemonStatus) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		record, _, ok := computerCore.Snapshot(workspaceID)
		if ok && record.Status == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("Binding %s lifecycle = %+v/%t, want %s", workspaceID, record, ok, want)
		case <-ticker.C:
		}
	}
}

func TestWorkspaceDaemonPublishesReadyWithoutAgentRuntimes(t *testing.T) {
	const (
		workspaceID  = "workspace-a"
		computerID   = "computer-a"
		controlToken = "computer-control-token"
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

	computerCore := newComputerControlTestHarness(t, controlToken, computer.ComputerControlCallbacks{})
	computerControlURL := localComputerControlRPC(t, computerCore.computerCore)
	t.Setenv("MULTICA_WORKSPACE_DAEMON_CONTROL_TOKEN", controlToken)
	t.Setenv("MULTICA_WORKSPACE_DAEMON_ZERO_RUNTIME", "1")
	bootstrap := computer.WorkspaceDaemonBootstrap{
		ProtocolVersion: computer.WorkspaceDaemonProtocolVersion, WorkspaceID: workspaceID,
		ComputerID:  computerID,
		Environment: "test", ServerBaseURL: server.URL, ServiceEndpoint: computerControlURL,
		BindingsRoot: root, WorkspacesRoot: workspacesRoot,
	}
	child, err := computer.StartWorkspaceDaemonProcess(os.Args[0], []string{"-test.run=TestRunWorkspaceDaemonProcessHelper"}, bootstrap)
	if err != nil {
		t.Fatalf("start WorkspaceDaemon process: %v", err)
	}
	t.Cleanup(func() { _ = child.Stop() })
	computerCore.state.mu.Lock()
	computerCore.state.pids[workspaceID] = child.PID()
	computerCore.state.mu.Unlock()
	installStartingWorkspaceDaemon(t, computerCore, workspaceID, child.PID())
	child.Activate()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ready, err := child.AwaitReady(ctx)
	if err != nil {
		t.Fatalf("zero-runtime DaemonCore must publish Ready after WorkspaceDaemon connect: %v", err)
	}
	if ready.PID != child.PID() || ready.WorkspaceID != workspaceID || ready.DaemonInstanceID == "" {
		t.Fatalf("WorkspaceDaemon Ready = %+v, pid=%d", ready, child.PID())
	}
	if got := registerCalls.Load(); got != 0 {
		t.Fatalf("zero-runtime WorkspaceDaemon posted register %d times", got)
	}
}

func TestRunWorkspaceDaemonProcessHelper(t *testing.T) {
	providerPath := os.Getenv(workspaceDaemonRuntimeHelperEnv)
	zeroRuntime := os.Getenv("MULTICA_WORKSPACE_DAEMON_ZERO_RUNTIME") == "1"
	if providerPath == "" && !zeroRuntime {
		return
	}
	bootstrap, err := computer.ReadWorkspaceDaemonBootstrap(os.Stdin)
	if err != nil {
		os.Exit(2)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	agents := map[string]AgentEntry{}
	if !zeroRuntime {
		agents["pi"] = AgentEntry{Path: providerPath}
	}
	err = RunWorkspaceDaemonProcess(context.Background(), WorkspaceDaemonProcessConfig{
		Daemon: Config{
			DaemonID:    bootstrap.ComputerID,
			Environment: bootstrap.Environment, ServerBaseURL: bootstrap.ServerBaseURL,
			BindingsRoot: bootstrap.BindingsRoot, WorkspacesRoot: bootstrap.WorkspacesRoot,
			LocalControlToken: os.Getenv("MULTICA_WORKSPACE_DAEMON_CONTROL_TOKEN"),
			Agents:            agents,
			PollInterval:      time.Hour, HeartbeatInterval: time.Hour,
		},
		Bootstrap: bootstrap,
		Logger:    logger,
		PublishReady: func(ready computer.WorkspaceDaemonReady) error {
			return computer.WriteWorkspaceDaemonReady(os.Stdout, ready)
		},
	})
	if err != nil {
		logger.Error("WorkspaceDaemon helper failed", "error", err)
		os.Exit(3)
	}
}

func TestWorkspaceDaemonCredentialProxyHasProcessOwnedListener(t *testing.T) {
	d := New(Config{HealthPort: DefaultHealthPort}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	listener, err := d.listenWorkspaceDaemonCredentialProxy()
	if err != nil {
		t.Fatalf("listenWorkspaceDaemonCredentialProxy: %v", err)
	}
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok || !addr.IP.IsLoopback() || addr.Port < 1 {
		t.Fatalf("WorkspaceDaemon Credential Proxy addr = %v, want loopback ephemeral port", listener.Addr())
	}
	if d.cfg.HealthPort != addr.Port {
		t.Fatalf("WorkspaceDaemon launch port = %d, want listener port %d", d.cfg.HealthPort, addr.Port)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.serveWorkspaceDaemonCredentialProxy(ctx, listener)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("WorkspaceDaemon Credential Proxy did not stop")
		}
	})

	response, err := http.Get("http://" + listener.Addr().String() + "/credential-proxy/messages/check")
	if err != nil {
		t.Fatalf("GET WorkspaceDaemon Credential Proxy: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET WorkspaceDaemon Credential Proxy status = %d, want 405", response.StatusCode)
	}
	if allow := response.Header.Get("Allow"); allow != http.MethodPost {
		t.Fatalf("GET WorkspaceDaemon Credential Proxy Allow = %q, want POST", allow)
	}

	health, err := http.Get("http://" + listener.Addr().String() + "/health")
	if err != nil {
		t.Fatalf("GET WorkspaceDaemon machine health route: %v", err)
	}
	_ = health.Body.Close()
	if health.StatusCode != http.StatusNotFound {
		t.Fatalf("WorkspaceDaemon exposed Computer /health with status %d", health.StatusCode)
	}
}

func TestExpiredBindingDoesNotInstallWorkspaceToken(t *testing.T) {
	d := newDaemonForRole(Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), daemonProcessWorkspaceDaemon)
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
	d := newDaemonForRole(Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), daemonProcessWorkspaceDaemon)
	err := d.prepareBindingExecutionCredential(computer.WorkspaceBinding{
		WorkspaceID: "workspace-a", ComputerID: "computer-a",
		Credential: "expired-binding-token", CredentialExpiresAt: time.Now().Add(-time.Hour), Active: true,
	})
	if err == nil || !strings.Contains(err.Error(), "no live execution credential") {
		t.Fatalf("current-package expired Binding error = %v", err)
	}
}

func TestWorkspaceDaemonMembershipRefreshStopsRevokedBinding(t *testing.T) {
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
		BindingsRoot: root, Environment: "test", DaemonID: "computer-a",
		ServerBaseURL: server.URL,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), daemonProcessWorkspaceDaemon)
	d.client.SetWorkspaceDaemonToken(workspaceID, "binding-token", expiresAt)

	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		done <- d.workspaceRefreshLoop(ctx, workspaceID, "binding-token", 5*time.Millisecond)
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
		t.Fatal("WorkspaceDaemon did not stop after its connection was revoked")
	}
}

func TestWorkspaceDaemonsUseIsolatedDurableExecutionState(t *testing.T) {
	root := t.TempDir()
	workspacesRoot := filepath.Join(root, "workspaces")
	bindingsRoot := filepath.Join(root, "bindings")
	firstRoot := filepath.Join(root, "execution", "workspace-a")
	secondRoot := filepath.Join(root, "execution", "workspace-b")
	first := newDaemonForRole(Config{WorkspacesRoot: workspacesRoot, BindingsRoot: bindingsRoot, MachineID: "machine-a", BindingStateRoot: firstRoot, WorkspaceID: "workspace-a"}, slog.New(slog.NewTextHandler(io.Discard, nil)), daemonProcessWorkspaceDaemon)
	second := newDaemonForRole(Config{WorkspacesRoot: workspacesRoot, BindingsRoot: bindingsRoot, MachineID: "machine-a", BindingStateRoot: secondRoot, WorkspaceID: "workspace-b"}, slog.New(slog.NewTextHandler(io.Discard, nil)), daemonProcessWorkspaceDaemon)

	for label, paths := range map[string]struct {
		pair          [2]string
		expectedRoots [2]string
	}{
		"Reminder cache": {
			pair: [2]string{first.reminderCache.storageRoot, second.reminderCache.storageRoot},
			expectedRoots: [2]string{
				filepath.Join(bindingsRoot, "app-storage", "v1", "machine-a", "workspace-a"),
				filepath.Join(bindingsRoot, "app-storage", "v1", "machine-a", "workspace-b"),
			},
		},
		"Activity outbox": {pair: [2]string{first.mixedRunActivityOutbox.path, second.mixedRunActivityOutbox.path}, expectedRoots: [2]string{firstRoot, secondRoot}},
	} {
		pair := paths.pair
		if pair[0] == "" || pair[1] == "" || pair[0] == pair[1] {
			t.Fatalf("%s paths are not isolated: %q / %q", label, pair[0], pair[1])
		}
		if !strings.HasPrefix(pair[0], paths.expectedRoots[0]) || !strings.HasPrefix(pair[1], paths.expectedRoots[1]) {
			t.Fatalf("%s escaped Binding state roots: %q / %q", label, pair[0], pair[1])
		}
	}
}
