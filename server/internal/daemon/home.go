package daemon

import (
	"os"
	"strings"
)

func userHomeDir() (string, error) {
	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		return home, nil
	}
	return os.UserHomeDir()
}
