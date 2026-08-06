// Package agentworkspace owns the canonical filesystem layout for durable
// Multica Agent workspaces. Callers must use these helpers instead of joining
// the "agents" segment themselves.
package agentworkspace

import (
	"path"
	"path/filepath"
	"strings"
)

const (
	MulticaDirName    = ".multica"
	WorkspacesDirName = "workspaces"
	AgentsDirName     = "agents"
)

// DefaultWorkspacesRoot returns the single default root used by every daemon
// profile and CLI command.
func DefaultWorkspacesRoot(home string) string {
	return filepath.Join(home, MulticaDirName, WorkspacesDirName)
}

// WorkspaceDir returns one workspace directory under WorkspacesRoot.
func WorkspaceDir(workspacesRoot, workspaceID string) string {
	return filepath.Join(workspacesRoot, workspaceID)
}

// AgentsDir returns the directory containing all durable Agent roots for one
// workspace.
func AgentsDir(workspacesRoot, workspaceID string) string {
	return filepath.Join(WorkspaceDir(workspacesRoot, workspaceID), AgentsDirName)
}

// Root returns the durable root and working directory for one Agent:
// <workspaces_root>/<workspace_id>/agents/<agent_id>.
func Root(workspacesRoot, workspaceID, agentID string) string {
	return filepath.Join(AgentsDir(workspacesRoot, workspaceID), agentID)
}

// AgentsRelPath returns the slash-separated path relative to WorkspacesRoot.
func AgentsRelPath(workspaceID string) string {
	return path.Join(path.Clean(workspaceID), AgentsDirName)
}

// RootRelPath returns one Agent root relative to WorkspacesRoot.
func RootRelPath(workspaceID, agentID string) string {
	return path.Join(AgentsRelPath(workspaceID), agentID)
}

// IDsFromRelPath recognizes a path rooted at
// <workspace_id>/agents/<agent_id>. It also accepts descendants because file
// APIs receive paths inside an Agent root. Callers remain responsible for ID
// validation and filesystem confinement.
func IDsFromRelPath(relPath string) (workspaceID, agentID string, ok bool) {
	parts := strings.Split(strings.Trim(filepath.ToSlash(relPath), "/"), "/")
	if len(parts) < 3 || parts[0] == "" || parts[1] != AgentsDirName || parts[2] == "" {
		return "", "", false
	}
	return parts[0], parts[2], true
}
