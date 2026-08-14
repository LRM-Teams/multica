package computer

import (
	"fmt"
	"os"
	"strings"

	"github.com/multica-ai/multica/server/internal/cli"
)

// LaunchBinaryEnv overrides the on-PATH Computer so a repo checkout can start
// its own `server/bin/multica` instead of ~/.local/bin/multica.
const LaunchBinaryEnv = "MULTICA_COMPUTER_LAUNCH_BIN"

// LaunchBinary returns the on-PATH Computer. A missing install path falls
// back to the running executable so repo checkouts can still start.
func LaunchBinary() (string, error) {
	if override := strings.TrimSpace(os.Getenv(LaunchBinaryEnv)); override != "" {
		info, err := os.Stat(override)
		if err != nil {
			return "", fmt.Errorf("%s: %w", LaunchBinaryEnv, err)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("%s is not a regular file: %s", LaunchBinaryEnv, override)
		}
		return override, nil
	}
	path, err := cli.InstallPath()
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		return path, nil
	}
	return os.Executable()
}
