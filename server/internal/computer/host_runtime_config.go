package computer

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

// ResolveHostMaxAgentProcesses reads the machine-wide absolute safety valve.
// Provider-specific scheduling remains inside each Binding child.
func ResolveHostMaxAgentProcesses() (int, error) {
	raw := strings.TrimSpace(os.Getenv("MULTICA_MAX_AGENT_PROCESSES"))
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("MULTICA_MAX_AGENT_PROCESSES: invalid non-negative integer %q", raw)
	}
	return value, nil
}
