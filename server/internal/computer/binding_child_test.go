package computer

import (
	"os"
	"os/exec"
	"testing"
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
	if _, err := StartBindingRunner("/bin/true", ""); err == nil {
		t.Fatal("empty workspace-id must fail")
	}
}
