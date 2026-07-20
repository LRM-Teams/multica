//go:build !windows

package agent

import (
	"errors"
	"os"
	"syscall"
)

func processAlive(process *os.Process) (bool, bool) {
	if process == nil || process.Pid <= 0 {
		return false, false
	}
	err := syscall.Kill(process.Pid, 0)
	if err == nil || errors.Is(err, syscall.EPERM) {
		return true, true
	}
	if errors.Is(err, syscall.ESRCH) {
		return false, true
	}
	return false, false
}
