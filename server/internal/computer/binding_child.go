package computer

import (
	"fmt"
	"os/exec"
	"strings"
	"sync/atomic"
)

const (
	// ResidentRunnerArg is the hidden argv for one Binding's supervised child.
	ResidentRunnerArg = "__runner"
)

// BindingChild is one OS process the Computer host supervises for a Binding.
type BindingChild interface {
	PID() int
	Wait() RunnerExitClass
	Stop() error
}

// BindingRunnerArgs is the Computer-owned argv for one Binding child.
func BindingRunnerArgs(workspaceID string) []string {
	return []string{ResidentCommand, ResidentRunnerArg, "--workspace-id", strings.TrimSpace(workspaceID)}
}

// BindingRunner is the shipped OS-child adapter. Tests and production both
// start it through StartBindingCommand / StartBindingRunner.
type BindingRunner struct {
	cmd      *exec.Cmd
	stopping atomic.Bool
}

// StartBindingRunner launches the Computer binary as `computer __runner`.
func StartBindingRunner(exe, workspaceID string) (*BindingRunner, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return nil, fmt.Errorf("workspace-id is required")
	}
	return StartBindingCommand(exe, BindingRunnerArgs(workspaceID))
}

// StartBindingCommand is the spawn primitive reconcile uses. Production
// passes the Computer binary plus BindingRunnerArgs; tests pass a short-lived
// helper command.
func StartBindingCommand(exe string, args []string) (*BindingRunner, error) {
	if strings.TrimSpace(exe) == "" {
		return nil, fmt.Errorf("binding runner executable is required")
	}
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = SysProcAttr(true)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &BindingRunner{cmd: cmd}, nil
}

func (c *BindingRunner) PID() int {
	if c == nil || c.cmd == nil || c.cmd.Process == nil {
		return 0
	}
	return c.cmd.Process.Pid
}

func (c *BindingRunner) Wait() RunnerExitClass {
	if c == nil || c.cmd == nil {
		return RunnerExitCrash
	}
	err := c.cmd.Wait()
	if c.stopping.Load() {
		return RunnerExitGraceful
	}
	if err == nil {
		return RunnerExitGraceful
	}
	return RunnerExitCrash
}

func (c *BindingRunner) Stop() error {
	if c == nil || c.cmd == nil || c.cmd.Process == nil {
		return nil
	}
	c.stopping.Store(true)
	return stopBindingProcess(c.cmd.Process)
}
