package computer

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	// ResidentRunnerArg is the hidden argv for one Binding's supervised child.
	ResidentRunnerArg = "__run"
)

// BindingChild is one supervised Binding execution process.
type BindingChild interface {
	PID() int
	Wait() RunnerExitClass
	Stop() error
}

// ReadyBindingChild exposes one Binding execution's real Workspace Runner
// readiness through the generation-fenced supervision seam.
type ReadyBindingChild interface {
	BindingChild
	AwaitReady(context.Context) (BindingChildReady, error)
}

// BindingRunnerArgs is the Computer-owned argv for one Binding child.
func BindingRunnerArgs(workspaceID string) []string {
	return []string{ResidentCommand, ResidentRunnerArg, "--workspace-id", strings.TrimSpace(workspaceID)}
}

// BindingRunner is the production executable child adapter.
type BindingRunner struct {
	cmd          *exec.Cmd
	stopping     atomic.Bool
	bootstrap    BindingChildBootstrap
	bootstrapIn  io.WriteCloser
	readyOut     io.ReadCloser
	activateOnce sync.Once
	readyDone    chan struct{}
	readyMu      sync.Mutex
	ready        BindingChildReady
	readyErr     error
}

// StartBindingRunner launches the Computer binary as `computer __run`.
func StartBindingRunner(exe string, bootstrap BindingChildBootstrap) (*BindingRunner, error) {
	return StartBindingProcess(exe, BindingRunnerArgs(bootstrap.WorkspaceID), bootstrap)
}

// StartBindingProcess launches one fallback process, which waits for Activate
// before receiving its inherited bootstrap over stdin.
func StartBindingProcess(exe string, args []string, bootstrap BindingChildBootstrap) (*BindingRunner, error) {
	bootstrap, err := bootstrap.validated()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(exe) == "" {
		return nil, fmt.Errorf("binding runner executable is required")
	}
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = SysProcAttr(true)
	configureChildParentDeath(cmd)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open Binding child bootstrap pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open Binding child ready pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}
	runner := &BindingRunner{
		cmd:         cmd,
		bootstrap:   bootstrap,
		bootstrapIn: stdin,
		readyOut:    stdout,
		readyDone:   make(chan struct{}),
	}
	return runner, nil
}

// Activate releases the fallback process only after the supervisor has stored
// its generation-fenced identity.
func (c *BindingRunner) Activate() {
	if c == nil || c.bootstrapIn == nil || c.readyOut == nil {
		return
	}
	c.activateOnce.Do(func() {
		if err := writeBindingChildBootstrap(c.bootstrapIn, c.bootstrap); err != nil {
			_ = c.bootstrapIn.Close()
			_ = c.cmd.Process.Kill()
			c.readyMu.Lock()
			c.readyErr = err
			c.readyMu.Unlock()
			close(c.readyDone)
			return
		}
		if err := c.bootstrapIn.Close(); err != nil {
			_ = c.cmd.Process.Kill()
			c.readyMu.Lock()
			c.readyErr = fmt.Errorf("close Binding child bootstrap pipe: %w", err)
			c.readyMu.Unlock()
			close(c.readyDone)
			return
		}
		go c.observeReady(c.readyOut)
	})
}

// StartBindingCommand launches a generic supervised process without the
// Binding bootstrap/Ready protocol.
func StartBindingCommand(exe string, args []string) (*BindingRunner, error) {
	if strings.TrimSpace(exe) == "" {
		return nil, fmt.Errorf("binding runner executable is required")
	}
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = SysProcAttr(true)
	configureChildParentDeath(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &BindingRunner{cmd: cmd}, nil
}

func (c *BindingRunner) observeReady(stdout io.ReadCloser) {
	defer stdout.Close()
	ready, err := readBindingChildReady(stdout)
	if err == nil {
		switch {
		case ready.ProtocolVersion != c.bootstrap.ProtocolVersion:
			err = fmt.Errorf("Binding child ready protocol version %d does not match bootstrap %d", ready.ProtocolVersion, c.bootstrap.ProtocolVersion)
		case strings.TrimSpace(ready.WorkspaceID) != c.bootstrap.WorkspaceID:
			err = fmt.Errorf("Binding child ready workspace %q does not match bootstrap %q", ready.WorkspaceID, c.bootstrap.WorkspaceID)
		case ready.RunnerGeneration != c.bootstrap.RunnerGeneration:
			err = fmt.Errorf("Binding child ready runner generation %d does not match bootstrap %d", ready.RunnerGeneration, c.bootstrap.RunnerGeneration)
		case c.cmd == nil || c.cmd.Process == nil || ready.PID != c.cmd.Process.Pid:
			err = fmt.Errorf("Binding child ready pid %d does not match process", ready.PID)
		case !validLocalControlEndpoint(ready.RunnerEndpoint):
			err = fmt.Errorf("Binding runner Ready endpoint is invalid")
		}
	}
	c.readyMu.Lock()
	c.ready = ready
	c.readyErr = err
	c.readyMu.Unlock()
	close(c.readyDone)
}

func (c *BindingRunner) AwaitReady(ctx context.Context) (BindingChildReady, error) {
	if c == nil || c.readyDone == nil {
		return BindingChildReady{}, fmt.Errorf("Binding child has no readiness seam")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return BindingChildReady{}, ctx.Err()
	case <-c.readyDone:
		c.readyMu.Lock()
		defer c.readyMu.Unlock()
		return c.ready, c.readyErr
	}
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
