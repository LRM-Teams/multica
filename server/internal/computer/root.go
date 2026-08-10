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

// DefaultHealthPort is the loopback health-check port for the default
// Computer. It is intentionally the same value the legacy daemon used so the
// two control surfaces stay on one port during the transition.
const DefaultHealthPort = 19514

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
	return filepath.Join(RootDir(profile), "daemon.pid")
}

// LogPath returns the resident process log file path for the given profile.
func LogPath(profile string) string {
	return filepath.Join(RootDir(profile), "daemon.log")
}

// HealthPort returns the one loopback control port for the machine-wide
// Computer. The profile argument is ignored for compatibility.
func HealthPort(_ string) int {
	return DefaultHealthPort
}
