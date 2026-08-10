package computer

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAlive(t *testing.T) {
	t.Parallel()

	cases := []struct {
		status any
		want   bool
	}{
		{"running", true},
		{"starting", true},
		{"stopped", false},
		{"", false},
		{nil, false},
		{"bogus", false},
	}
	for _, c := range cases {
		if got := Alive(map[string]any{"status": c.status}); got != c.want {
			t.Errorf("Alive(status=%v) = %v, want %v", c.status, got, c.want)
		}
	}
	if Alive(map[string]any{}) {
		t.Errorf("Alive(no status) = true, want false")
	}
}

// --- remove PID if matches ---

func TestRemovePIDIfMatchesNeverDeletesSuccessorPID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lc := &Lifecycle{}

	path := PIDPath("upgrade")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("2002"), 0o600); err != nil {
		t.Fatal(err)
	}
	lc.RemovePIDIfMatches(1001)
	if data, err := os.ReadFile(path); err != nil || string(data) != "2002" {
		t.Fatalf("successor pid after incumbent cleanup = %q, %v", data, err)
	}
	lc.RemovePIDIfMatches(2002)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("matching pid file remains: %v", err)
	}
}

func TestPublishPIDWritesCurrentPIDAndCleanupRespectsMatches(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lc := &Lifecycle{}

	cleanup, err := lc.PublishPID()
	if err != nil {
		t.Fatalf("PublishPID: %v", err)
	}
	data, err := os.ReadFile(PIDPath("merge"))
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		t.Fatalf("pid file should contain os.Getpid, got %q", data)
	}
	// Simulate a successor overwriting the PID, then clean up: it must not
	// delete the successor's fresh value.
	if err := os.WriteFile(PIDPath("merge"), []byte("999"), 0o644); err != nil {
		t.Fatal(err)
	}
	cleanup()
	if data, err := os.ReadFile(PIDPath("merge")); err != nil || string(data) != "999" {
		t.Fatalf("cleanup deleted successor pid: %q, %v", data, err)
	}
}

// --- status through the Lifecycle with a replaceable probe ---

func TestStatusReportsStoppedWhenNoResident(t *testing.T) {
	lc := &Lifecycle{}
	lc.Probe = func(context.Context, int) map[string]any {
		return map[string]any{"status": "stopped"}
	}
	health := lc.Status()
	if health["status"] != "stopped" {
		t.Fatalf("status = %v, want stopped", health["status"])
	}
}

func TestStatusReportsRunning(t *testing.T) {
	lc := &Lifecycle{}
	lc.Probe = func(context.Context, int) map[string]any {
		return map[string]any{"status": "running", "pid": float64(1234)}
	}
	health := lc.Status()
	if health["status"] != "running" {
		t.Fatalf("status = %v, want running", health["status"])
	}
}

func TestStatusIsRedactedReadOnlyComputerProjection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := RootDir("")
	if err := NewBindingsStore(root).AddOrRepair(WorkspaceBinding{
		WorkspaceID: "workspace-1", WorkspaceSlug: "team", ComputerID: "computer-1",
		Credential: "workspace-secret", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, ".multica", "config.json")
	if err := os.WriteFile(configPath, []byte(`{"server_url":"https://leagent.me","token":"user-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	lc := &Lifecycle{Probe: func(context.Context, int) map[string]any {
		return map[string]any{
			"status": "running", "connected": true, "pid": float64(42),
			"agents": []any{"must-not-leak"}, "workspaces": []any{"must-not-drive-status"},
		}
	}}
	status := lc.Status()
	if status["session_present"] != true || status["service_origin"] != CanonicalCloudOrigin {
		t.Fatalf("session projection = %+v", status)
	}
	if _, leaked := status["agents"]; leaked {
		t.Fatalf("Computer status leaked aggregate Agent state: %+v", status)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"user-secret", "workspace-secret", "must-not-leak"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("Computer status leaked %q: %s", secret, encoded)
		}
	}
	if got := status["workspace_connections"].([]map[string]any); len(got) != 1 || got[0]["workspace_id"] != "workspace-1" {
		t.Fatalf("safe Workspace connection projection = %+v", got)
	}
}

// --- stop through the Lifecycle ---

func TestStopWhenNotRunning(t *testing.T) {
	lc := &Lifecycle{}
	lc.Probe = func(context.Context, int) map[string]any {
		return map[string]any{"status": "stopped"}
	}
	res := lc.Stop()
	if res.Running {
		t.Fatalf("Stop reported Running=true for a stopped Computer")
	}
}

func TestStopGracefullyThenConfirmsStopped(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lc := &Lifecycle{}

	if err := os.MkdirAll(filepath.Dir(PIDPath("")), 0o755); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	lc.Probe = func(_ context.Context, _ int) map[string]any {
		if calls.Add(1) == 1 {
			return map[string]any{"status": "running", "pid": float64(4242)}
		}
		return map[string]any{"status": "stopped"}
	}
	lc.Sleep = func(time.Duration) {}

	// Create a PID file that Stop should clear.
	if err := os.WriteFile(PIDPath(""), []byte("4242"), 0o644); err != nil {
		t.Fatal(err)
	}

	restore := setRequestShutdown(func(int) error { return nil })
	defer restore()

	res := lc.Stop()
	if !res.Running {
		t.Fatalf("Stop reported Running=false for a live Computer")
	}
	if res.GracefulFailed {
		t.Fatalf("graceful shutdown unexpectedly failed")
	}
	if !res.Stopped {
		t.Fatalf("Stop did not reach Stopped")
	}
	if res.Err != nil {
		t.Fatalf("Stop returned error: %v", res.Err)
	}
	if _, err := os.Stat(PIDPath("")); !os.IsNotExist(err) {
		t.Fatalf("Stop did not clear the PID file: %v", err)
	}
}

func TestStopFallsBackToKillWhenShutdownFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(PIDPath("")), 0o755); err != nil {
		t.Fatal(err)
	}

	// Spawn a real short-lived process so the kill fallback targets a live
	// PID (Killing a nonexistent PID errors and would mask the path we want
	// to exercise).
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		t.Skipf("cannot spawn child process: %v", err)
	}
	pid := child.Process.Pid
	defer func() {
		_ = child.Process.Kill()
		_, _ = child.Process.Wait()
	}()

	lc := &Lifecycle{}
	var calls atomic.Int32
	lc.Probe = func(_ context.Context, _ int) map[string]any {
		if calls.Add(1) == 1 {
			return map[string]any{"status": "running", "pid": float64(pid)}
		}
		return map[string]any{"status": "stopped"}
	}
	lc.Sleep = func(time.Duration) {}

	restore := setRequestShutdown(func(int) error { return os.ErrClosed })
	defer restore()

	res := lc.Stop()
	if !res.GracefulFailed {
		t.Fatalf("expected GracefulFailed when shutdown delivery fails")
	}
	if !res.Stopped {
		t.Fatalf("Stop did not reach Stopped after kill fallback: %+v", res)
	}
}

// --- start background already-running guard ---

func TestStartBackgroundRefusesWhenAlreadyRunning(t *testing.T) {
	lc := &Lifecycle{}
	lc.Probe = func(_ context.Context, _ int) map[string]any {
		return map[string]any{"status": "running", "pid": float64(4321)}
	}
	_, err := lc.StartBackground(StartOptions{})
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("StartBackground = %v, want already-running error", err)
	}
}

// setRequestShutdown temporarily replaces the graceful-shutdown transport.
func setRequestShutdown(fn func(int) error) func() {
	old := requestShutdown
	requestShutdown = fn
	return func() { requestShutdown = old }
}

// --- start background success path with a fake spawned process ---

type fakeProc struct {
	pid int
}

func (f *fakeProc) Start() error   { return nil }
func (f *fakeProc) Pid() int       { return f.pid }
func (f *fakeProc) Release() error { return nil }

func setSpawnResident(fn func(string, []string, *os.File) (procHandle, error)) func() {
	old := spawnResident
	spawnResident = fn
	return func() { spawnResident = old }
}

func TestStartBackgroundLaunchesWritesPIDAndConfirmsReady(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(PIDPath("")), 0o755); err != nil {
		t.Fatal(err)
	}

	lc := &Lifecycle{}

	// Probe: first call (already-running guard) reports stopped; readiness
	// polls report running.
	var calls atomic.Int32
	lc.Probe = func(_ context.Context, _ int) map[string]any {
		if calls.Add(1) == 1 {
			return map[string]any{"status": "stopped"}
		}
		return map[string]any{"status": "running", "pid": float64(4242)}
	}
	lc.Sleep = func(time.Duration) {}

	restore := setSpawnResident(func(exe string, args []string, log *os.File) (procHandle, error) {
		return &fakeProc{pid: 7777}, nil
	})
	defer restore()

	res, err := lc.StartBackground(StartOptions{})
	if err != nil {
		t.Fatalf("StartBackground: %v", err)
	}
	if !res.Started {
		t.Fatalf("StartBackground did not report Started=true: %+v", res)
	}
	if res.Pid != 7777 {
		t.Fatalf("StartBackground pid = %d, want 7777", res.Pid)
	}
	// The PID file should have been published with the fake child's PID.
	data, err := os.ReadFile(PIDPath(""))
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	if strings.TrimSpace(string(data)) != "7777" {
		t.Fatalf("pid file = %q, want 7777", data)
	}
}

func TestResidentArgsOwnedByComputerAndNeverSelectProfileOrOrigin(t *testing.T) {
	args := ResidentArgs(StartOptions{
		DaemonID: "computer-1", DeviceName: "Laptop", RuntimeName: "Local",
		PollInterval: time.Second, HeartbeatInterval: 2 * time.Second,
		AgentTimeoutSet: true, AgentTimeout: 0,
	})
	joined := strings.Join(args, " ")
	if !strings.HasPrefix(joined, "daemon start --foreground") {
		t.Fatalf("resident compatibility contract = %q", joined)
	}
	for _, forbidden := range []string{"--profile", "--server-url", "supervise", "install-service"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("resident args expose retired process selection %q: %s", forbidden, joined)
		}
	}
	for _, required := range []string{"--daemon-id computer-1", "--device-name Laptop", "--agent-timeout 0s"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("resident args %q missing %q", joined, required)
		}
	}
}
