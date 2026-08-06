package execenv

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

const (
	agentWorkspaceMetaFile      = ".workspace_meta.json"
	agentWorkspaceSchemaVersion = 1
)

// AgentWorkspaceLayout is the dormant D2 filesystem contract for one agent.
// D6 activates WorkDir as the provider CWD after per-agent serialization and
// current-turn transport binding are live.
type AgentWorkspaceLayout struct {
	RootDir  string
	WorkDir  string
	ReposDir string
	TurnsDir string
}

// AgentTurnLayout isolates the disposable material for exactly one canonical
// turn. Durable workspace and repo material never lives below this subtree.
type AgentTurnLayout struct {
	RootDir      string
	WorktreesDir string
	ArtifactsDir string
}

type agentWorkspaceMeta struct {
	SchemaVersion int    `json:"schema_version"`
	WorkspaceID   string `json:"workspace_id"`
	AgentID       string `json:"agent_id"`
}

// AgentWorkspaceRemovalReason is the small, explicit set of lifecycle events
// allowed to remove a durable agent workspace.
type AgentWorkspaceRemovalReason string

const (
	AgentWorkspaceRemovalFullReset       AgentWorkspaceRemovalReason = "full_reset"
	AgentWorkspaceRemovalAgentDeleted    AgentWorkspaceRemovalReason = "agent_deleted"
	AgentWorkspaceRemovalWorkspaceRevoke AgentWorkspaceRemovalReason = "workspace_revoked"
)

// AgentWorkspaceRemovalProof is supplied by the lifecycle owner after it has
// checked server/runtime state. Filesystem code cannot infer leases safely.
type AgentWorkspaceRemovalProof struct {
	NoActiveTurn          bool
	NoActiveProviderLease bool
}

type RemoveAgentWorkspaceParams struct {
	WorkspacesRoot string
	WorkspaceID    string
	AgentID        string
	RootDir        string
	Reason         AgentWorkspaceRemovalReason
	Proof          AgentWorkspaceRemovalProof
}

// PredictAgentRootDir returns the canonical full-ID agent root without doing
// I/O. Invalid or non-canonical UUIDs fail closed with an empty path.
func PredictAgentRootDir(workspacesRoot, workspaceID, agentID string) string {
	if strings.TrimSpace(workspacesRoot) == "" ||
		!isCanonicalFullUUID(workspaceID) ||
		!isCanonicalFullUUID(agentID) {
		return ""
	}
	return filepath.Join(workspacesRoot, workspaceID, ".multica", "agents", agentID)
}

// ResolveAgentWorkspaceLayout returns the canonical stable workspace layout.
func ResolveAgentWorkspaceLayout(workspacesRoot, workspaceID, agentID string) (AgentWorkspaceLayout, error) {
	root := PredictAgentRootDir(workspacesRoot, workspaceID, agentID)
	if root == "" {
		return AgentWorkspaceLayout{}, errors.New("execenv: workspaces root and canonical full workspace/agent UUIDs are required")
	}
	return AgentWorkspaceLayout{
		RootDir:  root,
		WorkDir:  filepath.Join(root, "workspace"),
		ReposDir: filepath.Join(root, "repos"),
		TurnsDir: filepath.Join(root, "turns"),
	}, nil
}

// Turn returns the disposable subtree for one canonical full turn UUID.
func (layout AgentWorkspaceLayout) Turn(turnID string) (AgentTurnLayout, error) {
	if !isCanonicalFullUUID(turnID) {
		return AgentTurnLayout{}, errors.New("execenv: canonical full turn UUID is required")
	}
	if strings.TrimSpace(layout.RootDir) == "" || strings.TrimSpace(layout.TurnsDir) == "" {
		return AgentTurnLayout{}, errors.New("execenv: agent workspace layout is incomplete")
	}
	root := filepath.Join(layout.TurnsDir, turnID)
	if !pathWithin(layout.RootDir, root) {
		return AgentTurnLayout{}, errors.New("execenv: turn root escapes agent workspace")
	}
	return AgentTurnLayout{
		RootDir:      root,
		WorktreesDir: filepath.Join(root, "worktree"),
		ArtifactsDir: filepath.Join(root, "artifacts"),
	}, nil
}

// ProvisionAgentWorkspace idempotently creates the stable D2 layout and an
// atomic managed marker. It never removes or resets existing content.
func ProvisionAgentWorkspace(workspacesRoot, workspaceID, agentID string, logger *slog.Logger) (*AgentWorkspaceLayout, error) {
	layout, err := ResolveAgentWorkspaceLayout(workspacesRoot, workspaceID, agentID)
	if err != nil {
		return nil, err
	}
	for _, dir := range []string{layout.RootDir, layout.WorkDir, layout.ReposDir, layout.TurnsDir} {
		if err := mkdirAllWithoutSymlink(paramsRoot(workspacesRoot), dir, 0o755); err != nil {
			return nil, fmt.Errorf("execenv: create agent workspace directory %s: %w", dir, err)
		}
	}
	meta := agentWorkspaceMeta{
		SchemaVersion: agentWorkspaceSchemaVersion,
		WorkspaceID:   workspaceID,
		AgentID:       agentID,
	}
	if err := writeAgentWorkspaceMetaAtomic(layout.RootDir, meta); err != nil {
		return nil, err
	}
	if logger != nil {
		logger.Debug("execenv: provisioned agent workspace", "root", layout.RootDir)
	}
	return &layout, nil
}

// ProvisionAgentTurn idempotently creates only the disposable subtree for one
// turn. It never changes durable workspace or repo material.
func ProvisionAgentTurn(layout AgentWorkspaceLayout, turnID string) (*AgentTurnLayout, error) {
	turn, err := layout.Turn(turnID)
	if err != nil {
		return nil, err
	}
	if err := validateManagedBase(layout.RootDir); err != nil {
		return nil, fmt.Errorf("execenv: invalid agent workspace root: %w", err)
	}
	for _, dir := range []string{turn.WorktreesDir, turn.ArtifactsDir} {
		if err := mkdirAllWithoutSymlink(layout.RootDir, dir, 0o755); err != nil {
			return nil, fmt.Errorf("execenv: create agent turn directory %s: %w", dir, err)
		}
	}
	return &turn, nil
}

// CleanupAgentTurn removes exactly one canonical turn subtree. Ordinary
// terminal/orphan cleanup must use this boundary, never the agent root.
func CleanupAgentTurn(layout AgentWorkspaceLayout, turnID string) error {
	turn, err := layout.Turn(turnID)
	if err != nil {
		return err
	}
	if !pathWithin(layout.TurnsDir, turn.RootDir) {
		return errors.New("execenv: refusing to clean turn outside managed turns root")
	}
	if err := ValidateAgentWorkspacePath(layout, turn.RootDir); err != nil {
		return fmt.Errorf("execenv: refusing unsafe agent turn cleanup: %w", err)
	}
	if err := os.RemoveAll(turn.RootDir); err != nil {
		return fmt.Errorf("execenv: clean agent turn: %w", err)
	}
	return nil
}

// RemoveAgentWorkspace is intentionally separate from provision and turn GC.
// Callers must prove both runtime quiescence conditions and an exact canonical
// full-ID root before the durable workspace can be removed.
func RemoveAgentWorkspace(params RemoveAgentWorkspaceParams) error {
	if !params.Proof.NoActiveTurn || !params.Proof.NoActiveProviderLease {
		return errors.New("execenv: refusing agent workspace removal without quiescence proof")
	}
	switch params.Reason {
	case AgentWorkspaceRemovalFullReset,
		AgentWorkspaceRemovalAgentDeleted,
		AgentWorkspaceRemovalWorkspaceRevoke:
	default:
		return fmt.Errorf("execenv: unsupported agent workspace removal reason %q", params.Reason)
	}
	expected := PredictAgentRootDir(params.WorkspacesRoot, params.WorkspaceID, params.AgentID)
	if expected == "" || filepath.Clean(params.RootDir) != filepath.Clean(expected) {
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
// ancestors. Repo materialization calls it immediately before git writes.
func ValidateAgentWorkspacePath(layout AgentWorkspaceLayout, target string) error {
	if !pathWithin(layout.RootDir, target) {
		return errors.New("execenv: path escapes agent workspace")
	}
	if err := validateManagedBase(layout.RootDir); err != nil {
		return err
	}
	return validateNoSymlinkDescendants(layout.RootDir, target)
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

func writeAgentWorkspaceMetaAtomic(root string, meta agentWorkspaceMeta) error {
	path := filepath.Join(root, agentWorkspaceMetaFile)
	data, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("execenv: marshal agent workspace metadata: %w", err)
	}
	data = append(data, '\n')
	if existing, err := os.ReadFile(path); err == nil && string(existing) == string(data) {
		return nil
	}

	tmp, err := os.CreateTemp(root, "."+agentWorkspaceMetaFile+".tmp-*")
	if err != nil {
		return fmt.Errorf("execenv: create agent workspace metadata temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("execenv: chmod agent workspace metadata: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("execenv: write agent workspace metadata: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("execenv: close agent workspace metadata: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		if existing, readErr := os.ReadFile(path); readErr == nil && string(existing) == string(data) {
			return nil
		}
		return fmt.Errorf("execenv: publish agent workspace metadata: %w", err)
	}
	return nil
}
