// Package computer owns the single local Computer lifecycle: state layout,
// resident process coordination (start/stop/restart/status/logs/health), and
// the upgrade seam. Cobra and Desktop-facing adapters are thin shells over
// this module and do not own process, PID, log, singleton, or state-layout
// rules themselves.
package computer

import (
	"path/filepath"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/daemon"
)

// RootDir returns the machine-wide Computer state directory for the given
// profile. Empty profile → ~/.multica/, named profile →
// ~/.multica/profiles/<name>/.
func RootDir(profile string) string {
	dir, err := cli.ProfileDir(profile)
	if err != nil {
		return ""
	}
	return dir
}

// PIDPath returns the resident process PID file path for the given profile.
func PIDPath(profile string) string {
	return filepath.Join(RootDir(profile), "daemon.pid")
}

// LogPath returns the resident process log file path for the given profile.
func LogPath(profile string) string {
	return filepath.Join(RootDir(profile), "daemon.log")
}

// HealthPort returns the loopback health-check port for the given profile.
// The default profile uses the standard port (19514); named profiles get a
// deterministic offset derived from the profile name.
func HealthPort(profile string) int {
	if profile == "" {
		return daemon.DefaultHealthPort
	}
	// Simple hash: sum of bytes mod 1000, offset from base+1.
	var h int
	for _, b := range []byte(profile) {
		h += int(b)
	}
	return daemon.DefaultHealthPort + 1 + (h % 1000)
}
