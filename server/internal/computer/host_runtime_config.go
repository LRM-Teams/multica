package computer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/multica-ai/multica/server/internal/agentworkspace"
)

// ResolveHostWorkspacesRoot resolves the machine-wide checkout root without
// loading daemon execution configuration.
func ResolveHostWorkspacesRoot() (string, error) {
	root := strings.TrimSpace(os.Getenv("MULTICA_WORKSPACES_ROOT"))
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w (set MULTICA_WORKSPACES_ROOT to override)", err)
		}
		root = agentworkspace.DefaultWorkspacesRoot(home)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve absolute Computer workspaces root: %w", err)
	}
	return abs, nil
}
