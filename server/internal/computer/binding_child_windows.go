//go:build windows

package computer

import "os"

func stopBindingProcess(proc *os.Process) error {
	if proc == nil {
		return nil
	}
	return proc.Kill()
}
