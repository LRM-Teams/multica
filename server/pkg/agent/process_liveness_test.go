package agent

import (
	"os"
	"os/exec"
	"testing"
)

const processLivenessHelperEnv = "MULTICA_PROCESS_LIVENESS_HELPER"

func TestProcessAliveCurrentProcess(t *testing.T) {
	process, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("find current process: %v", err)
	}
	if !processAlive(process) {
		t.Fatal("current process must be reported alive")
	}
}

func TestProcessAliveNil(t *testing.T) {
	if processAlive(nil) {
		t.Fatal("nil process must not be reported alive")
	}
}

func TestProcessAliveExitedProcess(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestProcessLivenessHelperProcess")
	cmd.Env = append(os.Environ(), processLivenessHelperEnv+"=1")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run helper process: %v", err)
	}
	if processAlive(cmd.Process) {
		t.Fatal("exited process must not be reported alive")
	}
}

func TestProcessLivenessHelperProcess(t *testing.T) {
	if os.Getenv(processLivenessHelperEnv) == "1" {
		os.Exit(0)
	}
}
