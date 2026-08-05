package execenv

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/agentworkspace"
)

// AgentWorkspaceLayout is the filesystem contract for one Agent.
type AgentWorkspaceLayout struct {
	AgentRoot string
}

// AgentWorkspaceRemovalReason is the small, explicit set of lifecycle events
// allowed to remove a durable agent workspace.
type AgentWorkspaceRemovalReason string

const (
	AgentWorkspaceRemovalFullReset AgentWorkspaceRemovalReason = "full_reset"
)

type RemoveAgentWorkspaceParams struct {
	WorkspacesRoot string
	WorkspaceID    string
	AgentID        string
	AgentRoot      string
	Reason         AgentWorkspaceRemovalReason
}

// PredictAgentRootDir returns the canonical full-ID agent root without doing
// I/O. Invalid or non-canonical UUIDs fail closed with an empty path.
func PredictAgentRootDir(workspacesRoot, workspaceID, agentID string) string {
	if strings.TrimSpace(workspacesRoot) == "" ||
		!isCanonicalFullUUID(workspaceID) ||
		!isCanonicalFullUUID(agentID) {
		return ""
	}
	return agentworkspace.Root(workspacesRoot, workspaceID, agentID)
}

// ResolveAgentWorkspaceLayout returns the canonical stable workspace layout.
func ResolveAgentWorkspaceLayout(workspacesRoot, workspaceID, agentID string) (AgentWorkspaceLayout, error) {
	root := PredictAgentRootDir(workspacesRoot, workspaceID, agentID)
	if root == "" {
		return AgentWorkspaceLayout{}, errors.New("execenv: workspaces root and canonical full workspace/agent UUIDs are required")
	}
	return AgentWorkspaceLayout{
		AgentRoot: root,
	}, nil
}

// ProvisionAgentWorkspace idempotently creates the stable root directory. It
// never removes, resets, or pre-populates Agent content.
func ProvisionAgentWorkspace(workspacesRoot, workspaceID, agentID string, logger *slog.Logger) (*AgentWorkspaceLayout, error) {
	layout, err := ResolveAgentWorkspaceLayout(workspacesRoot, workspaceID, agentID)
	if err != nil {
		return nil, err
	}
	if err := mkdirAllWithoutSymlink(paramsRoot(workspacesRoot), layout.AgentRoot, 0o755); err != nil {
		return nil, fmt.Errorf("execenv: create agent workspace directory %s: %w", layout.AgentRoot, err)
	}
	if logger != nil {
		logger.Debug("execenv: provisioned agent workspace", "root", layout.AgentRoot)
	}
	return &layout, nil
}

// RemoveAgentWorkspace is intentionally separate from provision and turn GC.
// Callers must supply the exact canonical full-ID root before the durable
// workspace can be removed. Full reset deliberately has hard-cut semantics.
func RemoveAgentWorkspace(params RemoveAgentWorkspaceParams) error {
	if params.Reason != AgentWorkspaceRemovalFullReset {
		return fmt.Errorf("execenv: unsupported agent workspace removal reason %q", params.Reason)
	}
	expected := PredictAgentRootDir(params.WorkspacesRoot, params.WorkspaceID, params.AgentID)
	if expected == "" || filepath.Clean(params.AgentRoot) != filepath.Clean(expected) {
		return errors.New("execenv: refusing agent workspace removal outside exact canonical root")
	}
	if err := validateNoSymlinkDescendants(paramsRoot(params.WorkspacesRoot), expected); err != nil {
		return fmt.Errorf("execenv: refusing unsafe agent workspace removal: %w", err)
	}
	if err := os.RemoveAll(expected); err != nil {
		return fmt.Errorf("execenv: remove agent workspace: %w", err)
	}
	return nil
}

// ValidateAgentWorkspacePath rejects lexical escapes and symlinked managed
// ancestors.
func ValidateAgentWorkspacePath(layout AgentWorkspaceLayout, target string) error {
	if !pathWithin(layout.AgentRoot, target) {
		return errors.New("execenv: path escapes agent workspace")
	}
	if err := validateManagedBase(layout.AgentRoot); err != nil {
		return err
	}
	return validateNoSymlinkDescendants(layout.AgentRoot, target)
}

func isCanonicalFullUUID(value string) bool {
	if value == "" || value != strings.ToLower(strings.TrimSpace(value)) {
		return false
	}
	parsed, err := uuid.Parse(value)
	return err == nil && parsed != uuid.Nil && parsed.String() == value
}

func paramsRoot(root string) string {
	return filepath.Clean(strings.TrimSpace(root))
}

func mkdirAllWithoutSymlink(base, target string, mode os.FileMode) error {
	absBase, err := filepath.Abs(base)
	if err != nil {
		return err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(absBase, absTarget)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return errors.New("target escapes managed base")
	}
	if err := os.MkdirAll(absBase, mode); err != nil {
		return err
	}
	current := absBase
	for _, component := range strings.Split(rel, string(os.PathSeparator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		switch {
		case err == nil:
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("managed path component is a symlink: %s", current)
			}
			if !info.IsDir() {
				return fmt.Errorf("managed path component is not a directory: %s", current)
			}
		case os.IsNotExist(err):
			if err := os.Mkdir(current, mode); err != nil && !os.IsExist(err) {
				return err
			}
		default:
			return err
		}
	}
	return nil
}

func validateNoSymlinkDescendants(base, target string) error {
	absBase, err := filepath.Abs(base)
	if err != nil {
		return err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(absBase, absTarget)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return errors.New("target escapes managed base")
	}
	current := absBase
	for _, component := range strings.Split(rel, string(os.PathSeparator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("managed path component is a symlink: %s", current)
		}
	}
	return nil
}

func validateManagedBase(base string) error {
	info, err := os.Lstat(base)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("managed base is a symlink: %s", base)
	}
	if !info.IsDir() {
		return fmt.Errorf("managed base is not a directory: %s", base)
	}
	return nil
}

func pathWithin(root, target string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absTarget)
	return err == nil && rel != "." && rel != "" && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
