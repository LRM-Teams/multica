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
	// WorkspaceDaemonArg is the hidden argv for one WorkspaceDaemon process.
	WorkspaceDaemonArg = "__run"
)

// WorkspaceDaemonProcess is one supervised WorkspaceDaemon OS process.
type WorkspaceDaemonProcess interface {
	PID() int
	Wait() WorkspaceDaemonExitClass
	Stop() error
}

// ReadyWorkspaceDaemonProcess exposes the process Ready handshake.
type ReadyWorkspaceDaemonProcess interface {
	WorkspaceDaemonProcess
	AwaitReady(context.Context) (WorkspaceDaemonReady, error)
}

// WorkspaceDaemonArgs is the Computer-owned argv for one WorkspaceDaemon.
func WorkspaceDaemonArgs(workspaceID string) []string {
	return []string{ResidentCommand, WorkspaceDaemonArg, "--workspace-id", strings.TrimSpace(workspaceID)}
}

type workspaceDaemonProcess struct {
	cmd          *exec.Cmd
	stopping     atomic.Bool
	bootstrap    WorkspaceDaemonBootstrap
	bootstrapIn  io.WriteCloser
	readyOut     io.ReadCloser
	activateOnce sync.Once
	readyDone    chan struct{}
	readyMu      sync.Mutex
	ready        WorkspaceDaemonReady
	readyErr     error
}

// StartWorkspaceDaemon launches the Computer binary as `computer __run`.
func StartWorkspaceDaemon(exe string, bootstrap WorkspaceDaemonBootstrap) (*workspaceDaemonProcess, error) {
	return StartWorkspaceDaemonProcess(exe, WorkspaceDaemonArgs(bootstrap.WorkspaceID), bootstrap)
}

// StartWorkspaceDaemonProcess launches one process, which waits for Activate
// before receiving its inherited bootstrap over stdin.
func StartWorkspaceDaemonProcess(exe string, args []string, bootstrap WorkspaceDaemonBootstrap) (*workspaceDaemonProcess, error) {
	bootstrap, err := bootstrap.validated()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(exe) == "" {
		return nil, fmt.Errorf("WorkspaceDaemon executable is required")
	}
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = SysProcAttr(true)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open WorkspaceDaemon bootstrap pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open WorkspaceDaemon ready pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}
	runner := &workspaceDaemonProcess{
		cmd:         cmd,
		bootstrap:   bootstrap,
		bootstrapIn: stdin,
		readyOut:    stdout,
		readyDone:   make(chan struct{}),
	}
	return runner, nil
}

// Activate releases the process only after DaemonCore has stored
// its generation-fenced identity.
func (c *workspaceDaemonProcess) Activate() {
	if c == nil || c.bootstrapIn == nil || c.readyOut == nil {
		return
	}
	c.activateOnce.Do(func() {
		if err := writeWorkspaceDaemonBootstrap(c.bootstrapIn, c.bootstrap); err != nil {
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
			c.readyErr = fmt.Errorf("close WorkspaceDaemon bootstrap pipe: %w", err)
			c.readyMu.Unlock()
			close(c.readyDone)
			return
		}
		go c.observeReady(c.readyOut)
	})
}

// startWorkspaceDaemonCommand launches a test process without the
// WorkspaceDaemon bootstrap/Ready protocol.
func startWorkspaceDaemonCommand(exe string, args []string) (*workspaceDaemonProcess, error) {
	if strings.TrimSpace(exe) == "" {
		return nil, fmt.Errorf("WorkspaceDaemon executable is required")
	}
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = SysProcAttr(true)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &workspaceDaemonProcess{cmd: cmd}, nil
}

func (c *workspaceDaemonProcess) observeReady(stdout io.ReadCloser) {
	defer stdout.Close()
	ready, err := readWorkspaceDaemonReady(stdout)
	if err == nil {
		switch {
		case ready.ProtocolVersion != c.bootstrap.ProtocolVersion:
			err = fmt.Errorf("WorkspaceDaemon ready protocol version %d does not match bootstrap %d", ready.ProtocolVersion, c.bootstrap.ProtocolVersion)
		case strings.TrimSpace(ready.WorkspaceID) != c.bootstrap.WorkspaceID:
			err = fmt.Errorf("WorkspaceDaemon ready workspace %q does not match bootstrap %q", ready.WorkspaceID, c.bootstrap.WorkspaceID)
		case strings.TrimSpace(ready.DaemonInstanceID) == "":
			err = fmt.Errorf("WorkspaceDaemon ready daemon instance is missing")
		case c.cmd == nil || c.cmd.Process == nil || ready.PID != c.cmd.Process.Pid:
			err = fmt.Errorf("WorkspaceDaemon ready pid %d does not match process", ready.PID)
		case !validLocalControlEndpoint(ready.RunnerEndpoint):
			err = fmt.Errorf("WorkspaceDaemon Ready endpoint is invalid")
		}
	}
	c.readyMu.Lock()
	c.ready = ready
	c.readyErr = err
	c.readyMu.Unlock()
	close(c.readyDone)
}

func (c *workspaceDaemonProcess) AwaitReady(ctx context.Context) (WorkspaceDaemonReady, error) {
	if c == nil || c.readyDone == nil {
		return WorkspaceDaemonReady{}, fmt.Errorf("WorkspaceDaemon process has no readiness seam")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return WorkspaceDaemonReady{}, ctx.Err()
	case <-c.readyDone:
		c.readyMu.Lock()
		defer c.readyMu.Unlock()
		return c.ready, c.readyErr
	}
}

func (c *workspaceDaemonProcess) PID() int {
	if c == nil || c.cmd == nil || c.cmd.Process == nil {
		return 0
	}
	return c.cmd.Process.Pid
}

func (c *workspaceDaemonProcess) Wait() WorkspaceDaemonExitClass {
	if c == nil || c.cmd == nil {
		return WorkspaceDaemonExitCrash
	}
	err := c.cmd.Wait()
	if c.stopping.Load() {
		return WorkspaceDaemonExitGraceful
	}
	if err == nil {
		return WorkspaceDaemonExitGraceful
	}
	return WorkspaceDaemonExitCrash
}

func (c *workspaceDaemonProcess) Stop() error {
	if c == nil || c.cmd == nil || c.cmd.Process == nil {
		return nil
	}
	c.stopping.Store(true)
	return stopWorkspaceDaemonProcess(c.cmd.Process)
}
