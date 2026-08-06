//go:build windows

package supervisor

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func configureWorkerProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP,
	}
}

func signalWorker(cmd *exec.Cmd) error {
	return windows.GenerateConsoleCtrlEvent(
		windows.CTRL_BREAK_EVENT,
		uint32(cmd.Process.Pid),
	)
}

func killWorker(cmd *exec.Cmd) error {
	return cmd.Process.Kill()
}
