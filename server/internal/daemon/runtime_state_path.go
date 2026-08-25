package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func validateStatePathPart(label, value string) error {
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." || filepath.Base(value) != value || filepath.IsAbs(value) || strings.ContainsAny(value, `/\\`) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("invalid %s path component", label)
	}
	return nil
}

func validateWorkspaceStatePath(workspacesRoot, workspaceID string) error {
	if err := validateStatePathPart("workspace", workspaceID); err != nil {
		return err
	}
	path := workspaceStateRoot(workspacesRoot, workspaceID)
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("workspace state path contains symlink: %s", current)
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("inspect workspace state path: %w", err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	return nil
}

// workspaceStateRoot is the workspace-scoped state home containing profiles
// and daemon-managed runtime transport. The default layout is
// ~/.multica/workspaces/<workspace-id>/{profiles,agent-proxy-tokens,cli-transport}.
func workspaceStateRoot(workspacesRoot, workspaceID string) string {
	return filepath.Join(filepath.Clean(workspacesRoot), workspaceID)
}
