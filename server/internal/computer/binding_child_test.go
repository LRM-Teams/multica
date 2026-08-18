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

func TestBindingRunnerArgsAreComputerOwned(t *testing.T) {
	args := BindingRunnerArgs("workspace-a")
	if got, want := args[0], ResidentCommand; got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
	if got, want := args[1], ResidentRunnerArg; got != want {
		t.Fatalf("service = %q, want %q", got, want)
	}
	joined := args[0] + " " + args[1]
	if joined == "daemon start" || args[1] == ResidentServiceArg {
		t.Fatalf("binding child reused resident argv %q", joined)
	}
}

func TestStartBindingCommandChildExitLeavesHostAlive(t *testing.T) {
	host := os.Getpid()
	helper, err := lookPathSleep()
	if err != nil {
		t.Fatalf("sleep helper: %v", err)
	}
	child, err := StartBindingCommand(helper.path, helper.args)
	if err != nil {
		t.Fatalf("StartBindingCommand: %v", err)
	}
	if child.PID() <= 0 || child.PID() == host {
		t.Fatalf("child pid %d host %d", child.PID(), host)
	}
	if err := child.cmd.Process.Kill(); err != nil {
		t.Fatalf("kill child: %v", err)
	}
	if class := child.Wait(); class != RunnerExitCrash {
		t.Fatalf("killed child class = %s, want crash", class)
	}
	if os.Getpid() != host {
		t.Fatal("host process did not survive child death")
	}
}

func TestWaitBindingRunnerCrashThenGracefulStop(t *testing.T) {
	falsePath, err := exec.LookPath("false")
	if err != nil {
		t.Skip("false not on PATH")
	}
	crash, err := StartBindingCommand(falsePath, nil)
	if err != nil {
		t.Fatalf("start false: %v", err)
	}
	if class := crash.Wait(); class != RunnerExitCrash {
		t.Fatalf("false exit class = %s, want crash", class)
	}

	helper, err := lookPathSleep()
	if err != nil {
		t.Fatalf("sleep helper: %v", err)
	}
	child, err := StartBindingCommand(helper.path, helper.args)
	if err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	if err := child.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if class := child.Wait(); class != RunnerExitGraceful {
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

func TestStartBindingRunnerRequiresWorkspace(t *testing.T) {
	if _, err := StartBindingRunner("/bin/true", BindingChildBootstrap{}); err == nil {
		t.Fatal("empty workspace-id must fail")
	}
}

func TestBindingChildBootstrapRoundTripPublishesExactReadyGeneration(t *testing.T) {
	t.Setenv("MULTICA_BINDING_CHILD_HELPER", "ready")
	bootstrap := BindingChildBootstrap{
		ProtocolVersion:    BindingChildProtocolVersion,
		WorkspaceID:        "workspace-a",
		ComputerID:         "computer-a",
		ComputerGeneration: 11,
		RunnerGeneration:   7,
		Environment:        "test",
		ServerBaseURL:      "https://test.example.com",
		ServiceEndpoint:    "unix:///tmp/multica-test-service.sock",
		BindingsRoot:       "/tmp/computer-a",
		WorkspacesRoot:     "/tmp/workspaces-a",
	}
	raw, err := json.Marshal(bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"computer_id":"computer-a"`) || strings.Contains(string(raw), `"daemon_id"`) {
		t.Fatalf("Binding bootstrap identity wire = %s", raw)
	}
	child, err := StartBindingProcess(os.Args[0], []string{"-test.run=TestBindingChildProtocolHelper"}, bootstrap)
	if err != nil {
		t.Fatalf("StartBindingProcess: %v", err)
	}
	defer child.Stop()
	child.Activate()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ready, err := child.AwaitReady(ctx)
	if err != nil {
		t.Fatalf("AwaitReady: %v", err)
	}
	if ready.WorkspaceID != bootstrap.WorkspaceID || ready.RunnerGeneration != bootstrap.RunnerGeneration {
		t.Fatalf("ready identity = %#v, want workspace %q generation %d", ready, bootstrap.WorkspaceID, bootstrap.RunnerGeneration)
	}
	if ready.PID != child.PID() {
		t.Fatalf("ready pid = %d, want child pid %d", ready.PID, child.PID())
	}
	if class := child.Wait(); class != RunnerExitGraceful {
		t.Fatalf("helper exit class = %s, want graceful", class)
	}
}

func TestBindingChildReadyRejectsStaleGeneration(t *testing.T) {
	t.Setenv("MULTICA_BINDING_CHILD_HELPER", "stale")
	bootstrap := BindingChildBootstrap{
		ProtocolVersion:    BindingChildProtocolVersion,
		WorkspaceID:        "workspace-a",
		ComputerID:         "computer-a",
		ComputerGeneration: 11,
		RunnerGeneration:   7,
		Environment:        "production",
		ServerBaseURL:      "https://api.leagent.me",
		ServiceEndpoint:    "unix:///tmp/multica-test-service.sock",
		BindingsRoot:       "/tmp/computer-a",
		WorkspacesRoot:     "/tmp/workspaces-a",
	}
	child, err := StartBindingProcess(os.Args[0], []string{"-test.run=TestBindingChildProtocolHelper"}, bootstrap)
	if err != nil {
		t.Fatalf("StartBindingProcess: %v", err)
	}
	defer child.Stop()
	child.Activate()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := child.AwaitReady(ctx); err == nil || !strings.Contains(err.Error(), "runner generation") {
		t.Fatalf("AwaitReady error = %v, want runner generation rejection", err)
	}
	if class := child.Wait(); class != RunnerExitGraceful {
		t.Fatalf("helper exit class = %s, want graceful", class)
	}
}

func TestBindingChildProtocolHelper(t *testing.T) {
	mode := os.Getenv("MULTICA_BINDING_CHILD_HELPER")
	if mode == "" {
		return
	}
	bootstrap, err := ReadBindingChildBootstrap(os.Stdin)
	if err != nil {
		os.Exit(2)
	}
	ready := BindingChildReady{
		ProtocolVersion:  BindingChildProtocolVersion,
		WorkspaceID:      bootstrap.WorkspaceID,
		RunnerGeneration: bootstrap.RunnerGeneration,
		PID:              os.Getpid(),
		RunnerEndpoint:   "unix:///tmp/multica-test-runner.sock",
	}
	if mode == "stale" {
		ready.RunnerGeneration--
	}
	if err := WriteBindingChildReady(os.Stdout, ready); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}
