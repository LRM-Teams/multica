package computer

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestWorkspaceDaemonArgsAreComputerOwned(t *testing.T) {
	args := WorkspaceDaemonArgs("workspace-a")
	if got, want := args[0], ResidentCommand; got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
	if got, want := args[1], WorkspaceDaemonArg; got != want {
		t.Fatalf("service = %q, want %q", got, want)
	}
	joined := args[0] + " " + args[1]
	if joined == "daemon start" || args[1] == ResidentServiceArg {
		t.Fatalf("WorkspaceDaemon reused Computer argv %q", joined)
	}
}

func TestWorkspaceDaemonExitLeavesComputerProcessAlive(t *testing.T) {
	computerPID := os.Getpid()
	helper, err := lookPathSleep()
	if err != nil {
		t.Fatalf("sleep helper: %v", err)
	}
	child, err := startWorkspaceDaemonCommand(helper.path, helper.args)
	if err != nil {
		t.Fatalf("startWorkspaceDaemonCommand: %v", err)
	}
	if child.PID() <= 0 || child.PID() == computerPID {
		t.Fatalf("WorkspaceDaemon pid %d Computer pid %d", child.PID(), computerPID)
	}
	if err := child.cmd.Process.Kill(); err != nil {
		t.Fatalf("kill child: %v", err)
	}
	if class := child.Wait(); class != WorkspaceDaemonExitCrash {
		t.Fatalf("killed child class = %s, want crash", class)
	}
	if os.Getpid() != computerPID {
		t.Fatal("Computer process did not survive WorkspaceDaemon exit")
	}
}

func TestWorkspaceDaemonWaitReportsCrashThenGracefulStop(t *testing.T) {
	falsePath, err := exec.LookPath("false")
	if err != nil {
		t.Skip("false not on PATH")
	}
	crash, err := startWorkspaceDaemonCommand(falsePath, nil)
	if err != nil {
		t.Fatalf("start false: %v", err)
	}
	if class := crash.Wait(); class != WorkspaceDaemonExitCrash {
		t.Fatalf("false exit class = %s, want crash", class)
	}

	helper, err := lookPathSleep()
	if err != nil {
		t.Fatalf("sleep helper: %v", err)
	}
	child, err := startWorkspaceDaemonCommand(helper.path, helper.args)
	if err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	if err := child.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if class := child.Wait(); class != WorkspaceDaemonExitGraceful {
		t.Fatalf("stopped child class = %s, want graceful", class)
	}
}

type sleepHelper struct {
	path string
	args []string
}

func lookPathSleep() (sleepHelper, error) {
	if path, err := exec.LookPath("sleep"); err == nil {
		return sleepHelper{path: path, args: []string{"30"}}, nil
	}
	if path, err := exec.LookPath("timeout"); err == nil {
		return sleepHelper{path: path, args: []string{"/t", "30", "/nobreak"}}, nil
	}
	return sleepHelper{}, os.ErrNotExist
}

func TestStartWorkspaceDaemonRequiresWorkspace(t *testing.T) {
	if _, err := StartWorkspaceDaemon("/bin/true", WorkspaceDaemonBootstrap{}); err == nil {
		t.Fatal("empty workspace-id must fail")
	}
}

func TestWorkspaceDaemonBootstrapRoundTripPublishesInstance(t *testing.T) {
	t.Setenv("MULTICA_WORKSPACE_DAEMON_HELPER", "ready")
	bootstrap := WorkspaceDaemonBootstrap{
		ProtocolVersion: WorkspaceDaemonProtocolVersion, WorkspaceID: "workspace-a",
		ComputerID: "computer-a", Environment: "test",
		ServerBaseURL: "https://test.example.com", ServiceEndpoint: "unix:///tmp/multica-test-service.sock",
		BindingsRoot: "/tmp/computer-a", WorkspacesRoot: "/tmp/workspaces-a",
	}
	raw, err := json.Marshal(bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"computerId":"computer-a"`) || strings.Contains(string(raw), `"computer_id"`) {
		t.Fatalf("WorkspaceDaemon bootstrap identity wire = %s", raw)
	}
	child, err := StartWorkspaceDaemonProcess(os.Args[0], []string{"-test.run=TestWorkspaceDaemonProtocolHelper"}, bootstrap)
	if err != nil {
		t.Fatalf("StartWorkspaceDaemonProcess: %v", err)
	}
	defer child.Stop()
	child.Activate()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ready, err := child.AwaitReady(ctx)
	if err != nil {
		t.Fatalf("AwaitReady: %v", err)
	}
	if ready.WorkspaceID != bootstrap.WorkspaceID || ready.DaemonInstanceID == "" {
		t.Fatalf("ready identity = %#v, want workspace %q and a child daemon instance", ready, bootstrap.WorkspaceID)
	}
	if ready.PID != child.PID() {
		t.Fatalf("ready pid = %d, want child pid %d", ready.PID, child.PID())
	}
	if class := child.Wait(); class != WorkspaceDaemonExitGraceful {
		t.Fatalf("helper exit class = %s, want graceful", class)
	}
}

func TestWorkspaceDaemonReadyRejectsMissingDaemonInstance(t *testing.T) {
	t.Setenv("MULTICA_WORKSPACE_DAEMON_HELPER", "stale")
	bootstrap := WorkspaceDaemonBootstrap{
		ProtocolVersion: WorkspaceDaemonProtocolVersion, WorkspaceID: "workspace-a",
		ComputerID: "computer-a", Environment: "production",
		ServerBaseURL: "https://api.leagent.me", ServiceEndpoint: "unix:///tmp/multica-test-service.sock",
		BindingsRoot: "/tmp/computer-a", WorkspacesRoot: "/tmp/workspaces-a",
	}
	child, err := StartWorkspaceDaemonProcess(os.Args[0], []string{"-test.run=TestWorkspaceDaemonProtocolHelper"}, bootstrap)
	if err != nil {
		t.Fatalf("StartWorkspaceDaemonProcess: %v", err)
	}
	defer child.Stop()
	child.Activate()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := child.AwaitReady(ctx); err == nil || !strings.Contains(err.Error(), "daemon instance") {
		t.Fatalf("AwaitReady error = %v, want daemon instance rejection", err)
	}
	if class := child.Wait(); class != WorkspaceDaemonExitGraceful {
		t.Fatalf("helper exit class = %s, want graceful", class)
	}
}

func TestWorkspaceDaemonProtocolHelper(t *testing.T) {
	mode := os.Getenv("MULTICA_WORKSPACE_DAEMON_HELPER")
	if mode == "" {
		return
	}
	bootstrap, err := ReadWorkspaceDaemonBootstrap(os.Stdin)
	if err != nil {
		os.Exit(2)
	}
	ready := WorkspaceDaemonReady{
		ProtocolVersion: WorkspaceDaemonProtocolVersion, WorkspaceID: bootstrap.WorkspaceID,
		DaemonInstanceID: "child-instance-1", PID: os.Getpid(),
		RunnerEndpoint: "unix:///tmp/multica-test-runner.sock",
	}
	if mode == "stale" {
		ready.DaemonInstanceID = ""
		if err := json.NewEncoder(os.Stdout).Encode(ready); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
	if err := WriteWorkspaceDaemonReady(os.Stdout, ready); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}
