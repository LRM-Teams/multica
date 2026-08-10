package computer

import (
	"os"

	"github.com/multica-ai/multica/server/internal/cli"
)

// LaunchBinary returns the stable installation entrypoint. Active release
// binaries stay behind that launcher and are never persisted as lifecycle or
// service paths.
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
	if _, active, err := store.ActiveBinaryPath(); err == nil && active {
		if launcher, ok, err := store.LauncherPath(); err == nil && ok {
			return launcher, nil
		}
	}
	return exePath, nil
}
