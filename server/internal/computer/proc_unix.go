//go:build !windows

package computer

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// SysProcAttr returns the attributes used when spawning the background
// resident process. The withBreakaway argument exists only to share a
// signature with the Windows version (where it controls
// CREATE_BREAKAWAY_FROM_JOB); on Unix Setsid alone is sufficient to detach
// the child from its parent's session and process group.
func SysProcAttr(_ bool) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// IsAccessDeniedSpawnErr is always false on Unix. The Windows version looks
// for ERROR_ACCESS_DENIED to detect "parent Job Object disallowed breakaway"
// and trigger the breakaway-disabled retry; that retry is a no-op on Unix.
func IsAccessDeniedSpawnErr(_ error) bool { return false }

// TailLog tails the resident process log file, showing the last lines and
// optionally following new output, by delegating to the OS tail binary.
func TailLog(logPath string, lines int, follow bool) error {
	args := []string{"-n", strconv.Itoa(lines)}
	if follow {
		args = append(args, "-f")
	}
	args = append(args, logPath)

	tail := exec.Command("tail", args...)
	tail.Stdout = os.Stdout
	tail.Stderr = os.Stderr
	return tail.Run()
}
