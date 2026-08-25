//go:build windows

package computer

import "os"

func stopWorkspaceDaemonProcess(proc *os.Process) error {
	if proc == nil {
		return nil
	}
	return proc.Kill()
}
