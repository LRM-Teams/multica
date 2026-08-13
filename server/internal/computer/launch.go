package computer

import (
	"os"

	"github.com/multica-ai/multica/server/internal/cli"
)

// LaunchBinary returns the on-PATH Computer. A missing install path falls
// back to the running executable so repo checkouts can still start.
func LaunchBinary() (string, error) {
	path, err := cli.InstallPath()
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		return path, nil
	}
	return os.Executable()
}
