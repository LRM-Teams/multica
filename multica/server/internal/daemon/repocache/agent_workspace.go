package repocache

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

// AgentRepoParams describes the dormant D2 repo materialization contract.
// D6 routes the agent-facing checkout command here after turn serialization.
type AgentRepoParams struct {
	WorkspacesRoot      string
	WorkspaceID         string
	AgentID             string
	TurnID              string
	RepoURL             string
	Ref                 string
	AgentName           string // display metadata only; never part of filesystem or branch identity
	CoAuthoredByEnabled bool
}

// AgentRepoResult keeps durable repo material separate from the disposable
// per-turn worktree where task changes belong.
type AgentRepoResult struct {
	DurablePath string `json:"durable_path"`
	Path        string `json:"path"`
	BranchName  string `json:"branch_name"`
}

// MaterializeAgentRepo ensures one durable detached checkout and one isolated
// worktree for the canonical turn. It is idempotent for the same turn and
// never resets, cleans, or removes existing agent material.
func (c *Cache) MaterializeAgentRepo(params AgentRepoParams) (*AgentRepoResult, error) {
	if strings.TrimSpace(params.RepoURL) == "" {
		return nil, fmt.Errorf("repo URL is required")
	}
	layout, err := execenv.ProvisionAgentWorkspace(
		params.WorkspacesRoot,
		params.WorkspaceID,
		params.AgentID,
		c.logger,
	)
	if err != nil {
		return nil, err
	}
	turn, err := execenv.ProvisionAgentTurn(*layout, params.TurnID)
	if err != nil {
		return nil, err
	}

	barePath := c.Lookup(params.WorkspaceID, params.RepoURL)
	if barePath == "" {
		return nil, fmt.Errorf("repo not found in cache: %s (workspace: %s)", params.RepoURL, params.WorkspaceID)
	}
	repoLock := c.lockForRepo(barePath)
	repoLock.Lock()
	defer repoLock.Unlock()

	if err := gitFetch(barePath); err != nil {
		c.logger.Warn("repo checkout: fetch failed, agent will see possibly stale code",
			"url", params.RepoURL,
			"error", err,
		)
	}
	baseRef, err := resolveBaseRef(barePath, params.Ref)
	if err != nil {
		return nil, err
	}
	if baseRef == "" {
		return nil, fmt.Errorf("cannot resolve default branch for %s: bare cache at %s has no usable refs", params.RepoURL, barePath)
	}

	repoDir := strings.TrimSuffix(bareDirName(params.RepoURL), ".git")
	durablePath := filepath.Join(layout.ReposDir, repoDir)
	worktreePath := filepath.Join(turn.WorktreesDir, repoDir)
	if !pathWithinRoot(layout.RootDir, durablePath) || !pathWithinRoot(turn.RootDir, worktreePath) {
		return nil, fmt.Errorf("repo materialization path escapes managed agent workspace")
	}
	for _, target := range []string{durablePath, worktreePath} {
		if err := execenv.ValidateAgentWorkspacePath(*layout, target); err != nil {
			return nil, fmt.Errorf("repo materialization path is unsafe: %w", err)
		}
	}

	if err := ensureDurableCheckout(barePath, durablePath, baseRef, c.logger); err != nil {
		return nil, err
	}
	branchName := fmt.Sprintf("agent/%s/%s", params.AgentID, params.TurnID)
	actualBranch, err := ensureTurnWorktree(barePath, worktreePath, branchName, baseRef)
	if err != nil {
		return nil, err
	}

	for _, pattern := range agentGitExcludePatterns {
		_ = excludeFromGit(worktreePath, pattern)
	}
	if params.CoAuthoredByEnabled {
		if err := installCoAuthoredByHook(worktreePath); err != nil {
			c.logger.Warn("repo checkout: install co-authored-by hook failed (non-fatal)", "error", err)
		}
	} else {
		if err := removeCoAuthoredByHook(worktreePath); err != nil {
			c.logger.Warn("repo checkout: remove co-authored-by hook failed (non-fatal)", "error", err)
		}
	}

	c.logger.Info("repo checkout: agent repo materialized",
		"url", params.RepoURL,
		"durable_path", durablePath,
		"turn_path", worktreePath,
		"branch", actualBranch,
		"base", baseRef,
		"turn_id", params.TurnID,
	)
	return &AgentRepoResult{
		DurablePath: durablePath,
		Path:        worktreePath,
		BranchName:  actualBranch,
	}, nil
}

func ensureDurableCheckout(barePath, durablePath, baseRef string, logger *slog.Logger) error {
	if isGitWorktree(durablePath) {
		if err := verifyWorktreeOwner(barePath, durablePath); err != nil {
			return err
		}
		clean, err := worktreeIsClean(durablePath)
		if err != nil {
			return err
		}
		if !clean {
			logger.Warn("repo checkout: durable checkout has local changes; preserving without refresh", "path", durablePath)
			return nil
		}
		cmd := exec.Command("git", "-C", durablePath, "checkout", "--detach", baseRef)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("refresh durable checkout: %s: %w", strings.TrimSpace(string(out)), err)
		}
		return nil
	}
	if _, err := os.Stat(durablePath); err == nil {
		return fmt.Errorf("durable checkout path already exists and is not a managed git worktree: %s", durablePath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat durable checkout path: %w", err)
	}

	cmd := exec.Command("git", "-C", barePath, "worktree", "add", "--detach", durablePath, baseRef)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("create durable checkout: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func ensureTurnWorktree(barePath, worktreePath, branchName, baseRef string) (string, error) {
	if isGitWorktree(worktreePath) {
		if err := verifyWorktreeOwner(barePath, worktreePath); err != nil {
			return "", err
		}
		actualBranch, err := currentWorktreeBranch(worktreePath)
		if err != nil {
			return "", err
		}
		if actualBranch != branchName {
			return "", fmt.Errorf("turn worktree %s is on unexpected branch %s (want %s)", worktreePath, actualBranch, branchName)
		}
		return actualBranch, nil
	}
	if _, err := os.Stat(worktreePath); err == nil {
		return "", fmt.Errorf("turn worktree path already exists and is not a managed git worktree: %s", worktreePath)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat turn worktree path: %w", err)
	}

	if gitRefExists(barePath, "refs/heads/"+branchName) {
		cmd := exec.Command("git", "-C", barePath, "worktree", "add", worktreePath, branchName)
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("reattach turn worktree: %s: %w", strings.TrimSpace(string(out)), err)
		}
		return branchName, nil
	}
	cmd := exec.Command("git", "-C", barePath, "worktree", "add", "-b", branchName, worktreePath, baseRef)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("create turn worktree: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return branchName, nil
}

func verifyWorktreeOwner(barePath, worktreePath string) error {
	cmd := exec.Command("git", "-C", worktreePath, "rev-parse", "--git-common-dir")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("resolve worktree owner for %s: %w", worktreePath, err)
	}
	commonDir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(worktreePath, commonDir)
	}
	want, err := filepath.Abs(barePath)
	if err != nil {
		return err
	}
	got, err := filepath.Abs(commonDir)
	if err != nil {
		return err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(want); resolveErr == nil {
		want = resolved
	}
	if resolved, resolveErr := filepath.EvalSymlinks(got); resolveErr == nil {
		got = resolved
	}
	if filepath.Clean(got) != filepath.Clean(want) {
		return fmt.Errorf("worktree %s belongs to %s, not managed repo %s", worktreePath, got, want)
	}
	return nil
}

func currentWorktreeBranch(worktreePath string) (string, error) {
	cmd := exec.Command("git", "-C", worktreePath, "branch", "--show-current")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("read turn worktree branch: %w", err)
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" {
		return "", fmt.Errorf("turn worktree is detached: %s", worktreePath)
	}
	return branch, nil
}

func worktreeIsClean(path string) (bool, error) {
	cmd := exec.Command("git", "-C", path, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("read durable checkout status: %w", err)
	}
	return strings.TrimSpace(string(out)) == "", nil
}

func pathWithinRoot(root, target string) bool {
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
