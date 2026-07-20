//go:build !windows

package agent

import (
	"errors"
	"os"
	"syscall"
)

func processAlive(process *os.Process) bool {
	if process == nil || process.Pid <= 0 {
		return false
	}
	err := syscall.Kill(process.Pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
