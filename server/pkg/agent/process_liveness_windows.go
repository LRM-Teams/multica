//go:build windows

package agent

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func processAlive(process *os.Process) (bool, bool) {
	if process == nil || process.Pid <= 0 {
		return false, false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(process.Pid))
	if err != nil {
		switch {
		case errors.Is(err, windows.ERROR_ACCESS_DENIED):
			return windowsProcessLivenessDecision(windowsProcessOpenAccessDenied, 0, false)
		case errors.Is(err, windows.ERROR_INVALID_PARAMETER):
			return windowsProcessLivenessDecision(windowsProcessOpenNotFound, 0, false)
		default:
			return windowsProcessLivenessDecision(windowsProcessOpenUnknown, 0, false)
		}
	}
	defer windows.CloseHandle(handle)
	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return windowsProcessLivenessDecision(windowsProcessOpenSucceeded, 0, false)
	}
	return windowsProcessLivenessDecision(windowsProcessOpenSucceeded, exitCode, true)
}
