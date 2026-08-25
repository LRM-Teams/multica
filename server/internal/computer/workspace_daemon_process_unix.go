//go:build !windows

package computer

import (
	"os"
	"syscall"
)

func stopWorkspaceDaemonProcess(proc *os.Process) error {
	if proc == nil {
		return nil
	}
	return proc.Signal(syscall.SIGTERM)
}
