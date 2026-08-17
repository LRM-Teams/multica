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

func TestBindingRunnerLauncherSpawnsInProcessChild(t *testing.T) {
	started := make(chan BindingChildBootstrap, 1)
	launcher := BindingRunnerLauncher{
		ComputerID: "computer-a", ComputerGeneration: 3, Environment: "test",
		ServerBaseURL: "https://test.example.com", HostControlURL: "http://127.0.0.1:19514",
		BindingsRoot: t.TempDir(), WorkspacesRoot: t.TempDir(),
		Run: func(_ context.Context, bootstrap BindingChildBootstrap, publishReady func(BindingChildReady) error) error {
			started <- bootstrap
			return publishReady(BindingChildReady{
				ProtocolVersion: BindingChildProtocolVersion,
				WorkspaceID:     bootstrap.WorkspaceID, RunnerGeneration: bootstrap.RunnerGeneration,
				PID: os.Getpid(), ControlURL: "http://127.0.0.1:9",
			})
		},
	}
	child, err := launcher.Spawn("workspace-a", 7)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer child.Stop()
	if _, isProcess := child.(*BindingRunner); isProcess {
		t.Fatal("Computer Binding was spawned as an OS child")
	}
	if child.PID() != os.Getpid() {
		t.Fatalf("in-process Binding pid = %d, want host pid %d", child.PID(), os.Getpid())
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ready, err := child.(ReadyBindingChild).AwaitReady(ctx)
	if err != nil {
		t.Fatalf("AwaitReady: %v", err)
	}
	if ready.WorkspaceID != "workspace-a" || ready.RunnerGeneration != 7 || ready.PID != os.Getpid() {
		t.Fatalf("ready = %+v", ready)
	}
	select {
	case bootstrap := <-started:
		if bootstrap.WorkspaceID != "workspace-a" || bootstrap.RunnerGeneration != 7 || bootstrap.ComputerID != "computer-a" {
			t.Fatalf("in-process bootstrap = %+v", bootstrap)
		}
	case <-time.After(time.Second):
		t.Fatal("in-process Binding did not start")
	}
}

func TestPreviousPackageBindingBootstrapEndsWithLauncherProcess(t *testing.T) {
	launcher := BindingRunnerLauncher{
		PreviousPackageUpgradeBootstrap: true,
		PreviousPackageUpgradeSourcePID: 42,
		sourceProcessAlive: func(pid int) (bool, bool) {
			return pid == 42, true
		},
	}
	if !launcher.previousPackageBootstrapActive() {
		t.Fatal("live previous-package launcher did not enable the bounded Binding bootstrap")
	}
	launcher.sourceProcessAlive = func(int) (bool, bool) { return false, true }
	if launcher.previousPackageBootstrapActive() {
		t.Fatal("previous-package Binding bootstrap remained enabled after launcher exit")
	}
}

func TestBindingChildBootstrapRoundTripPublishesExactReadyGeneration(t *testing.T) {
	t.Setenv("MULTICA_BINDING_CHILD_HELPER", "ready")
	bootstrap := BindingChildBootstrap{
		ProtocolVersion:                 BindingChildProtocolVersion,
		WorkspaceID:                     "workspace-a",
		ComputerID:                      "computer-a",
		ComputerGeneration:              11,
		RunnerGeneration:                7,
		Environment:                     "test",
		ServerBaseURL:                   "https://test.example.com",
		HostControlURL:                  "http://127.0.0.1:19514",
		BindingsRoot:                    "/tmp/computer-a",
		WorkspacesRoot:                  "/tmp/workspaces-a",
		PreviousPackageUpgradeBootstrap: true,
	}
	raw, err := json.Marshal(bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"computer_id":"computer-a"`) || strings.Contains(string(raw), `"daemon_id"`) {
		t.Fatalf("Binding bootstrap identity wire = %s", raw)
	}
	if !strings.Contains(string(raw), `"previous_package_upgrade_bootstrap":true`) {
		t.Fatalf("previous-package bootstrap marker was not forwarded to the Binding child: %s", raw)
	}
	child, err := StartBindingProcess(os.Args[0], []string{"-test.run=TestBindingChildProtocolHelper"}, bootstrap)
	if err != nil {
		t.Fatalf("StartBindingProcess: %v", err)
	}
	defer child.Stop()

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
		HostControlURL:     "http://127.0.0.1:19514",
		BindingsRoot:       "/tmp/computer-a",
		WorkspacesRoot:     "/tmp/workspaces-a",
	}
	child, err := StartBindingProcess(os.Args[0], []string{"-test.run=TestBindingChildProtocolHelper"}, bootstrap)
	if err != nil {
		t.Fatalf("StartBindingProcess: %v", err)
	}
	defer child.Stop()

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
		ControlURL:       "http://127.0.0.1:19515",
	}
	if mode == "stale" {
		ready.RunnerGeneration--
	}
	if err := WriteBindingChildReady(os.Stdout, ready); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}
