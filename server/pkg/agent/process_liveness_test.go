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
	alive, known := processAlive(process)
	if !known || !alive {
		t.Fatalf("current process liveness = (alive=%v, known=%v), want (true, true)", alive, known)
	}
}

func TestProcessAliveNil(t *testing.T) {
	alive, known := processAlive(nil)
	if alive || known {
		t.Fatalf("nil process liveness = (alive=%v, known=%v), want (false, false)", alive, known)
	}
}

func TestProcessAliveExitedProcess(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestProcessLivenessHelperProcess")
	cmd.Env = append(os.Environ(), processLivenessHelperEnv+"=1")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run helper process: %v", err)
	}
	alive, known := processAlive(cmd.Process)
	if !known || alive {
		t.Fatalf("exited process liveness = (alive=%v, known=%v), want (false, true)", alive, known)
	}
}

func TestProcessLivenessHelperProcess(t *testing.T) {
	if os.Getenv(processLivenessHelperEnv) == "1" {
		os.Exit(0)
	}
}
