// Package computer owns the single local Computer lifecycle: state layout,
// resident process coordination (start/stop/restart/status/logs/health), and
// the upgrade seam. Cobra and Desktop-facing adapters are thin shells over
// this module and do not own process, PID, log, singleton, or state-layout
// rules themselves.
package computer

import (
	"path/filepath"

	"github.com/multica-ai/multica/server/internal/cli"
)

// RootDir returns the one machine-wide Computer control-state directory.
// The profile argument is intentionally ignored while hidden legacy daemon
// adapters still pass it: profiles must never select a second resident.
func RootDir(_ string) string {
	dir, err := cli.ProfileDir("")
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "computer")
}

// PIDPath returns the resident process PID file path for the given profile.
func PIDPath(profile string) string {
	return servicePIDPath(RootDir(profile))
}

// LogPath returns the resident process log file path for the given profile.
func LogPath(profile string) string {
	return filepath.Join(RootDir(profile), "daemon.log")
}
