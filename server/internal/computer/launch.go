package computer

import (
	"os"

	"github.com/multica-ai/multica/server/internal/cli"
)

// LaunchBinary picks the binary to exec for a fresh resident process. It
// prefers a VersionStore Active version staged by `multica update` (task #41:
// `restart` previously always re-exec'd whatever binary invoked the command,
// silently ignoring anything staged by a prior `update` run) and falls back
// to the invoking binary's own path when there is no Active version — the
// normal case for an install that has never run `multica update`. Brew
// installs manage their own binary outside the VersionStore and are left
// untouched.
func LaunchBinary() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	if cli.IsBrewInstall() {
		return exePath, nil
	}
	store, err := cli.OpenVersionStore("")
	if err != nil {
		return exePath, nil
	}
	if activePath, ok, err := store.ActiveBinaryPath(); err == nil && ok {
		return activePath, nil
	}
	return exePath, nil
}
