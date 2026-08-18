package handler

import (
	"context"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	workDigestRepoScopeWorkspace = "workspace"
	workDigestRepoScopeUnscoped  = "unscoped"
)

// scopedWorkDigestRepo is a Work Digest repo labeled for Period Work Synthesis.
// Scope never drops a repo: match failure means unscoped, not omission.
type scopedWorkDigestRepo struct {
	protocol.WorkDigestRepo
	Scope string `json:"scope"` // workspace | unscoped
}

// normalizeGitRemoteURL maps http(s)/ssh/scp-like remotes to host/owner/repo
// (lowercase, no .git). Returns ok=false when the string is not a usable remote.
func normalizeGitRemoteURL(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", false
	}
	var host, pathPart string
	switch {
	case strings.Contains(s, "://"):
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return "", false
		}
		host = u.Hostname()
		pathPart = u.Path
	case strings.HasPrefix(s, "git@") || (strings.Count(s, ":") == 1 && strings.Contains(s, "@")):
		// scp-like: git@host:path
		at := strings.Index(s, "@")
		colon := strings.LastIndex(s, ":")
		if at < 0 || colon <= at {
			return "", false
		}
		host = s[at+1 : colon]
		pathPart = s[colon+1:]
	default:
		return "", false
	}
	host = strings.ToLower(strings.TrimSpace(host))
	pathPart = strings.Trim(pathPart, "/")
	pathPart = strings.TrimSuffix(pathPart, ".git")
	pathPart = strings.TrimSuffix(pathPart, "/")
	if host == "" || pathPart == "" {
		return "", false
	}
	parts := strings.Split(pathPart, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	// Keep owner/repo (and nested groups) lowercased for case-insensitive match.
	for i := range parts {
		parts[i] = strings.ToLower(parts[i])
	}
	return host + "/" + strings.Join(parts, "/"), true
}

// scopeWorkDigestRepos labels each digest repo workspace vs unscoped by
// comparing normalized remotes to Workspace-bound github_repo URLs.
func scopeWorkDigestRepos(repos []protocol.WorkDigestRepo, workspaceRemotes []string) []scopedWorkDigestRepo {
	workspaceKeys := make(map[string]struct{}, len(workspaceRemotes))
	for _, remote := range workspaceRemotes {
		if key, ok := normalizeGitRemoteURL(remote); ok {
			workspaceKeys[key] = struct{}{}
		}
	}
	out := make([]scopedWorkDigestRepo, 0, len(repos))
	for _, repo := range repos {
		scope := workDigestRepoScopeUnscoped
		for _, remote := range repo.Remotes {
			key, ok := normalizeGitRemoteURL(remote)
			if !ok {
				continue
			}
			if _, match := workspaceKeys[key]; match {
				scope = workDigestRepoScopeWorkspace
				break
			}
		}
		out = append(out, scopedWorkDigestRepo{WorkDigestRepo: repo, Scope: scope})
	}
	return out
}

// listWorkspaceGitRepoRemotes returns github_repo resource URLs bound to
// projects in the Workspace. Used only for Digest scoping.
func (h *Handler) listWorkspaceGitRepoRemotes(ctx context.Context, workspaceID pgtype.UUID) ([]string, error) {
	if h == nil || h.DB == nil {
		return nil, nil
	}
	rows, err := h.DB.Query(ctx, `
SELECT COALESCE(pr.resource_ref->>'url', '')
FROM project_resource pr
JOIN project p ON p.id = pr.project_id
WHERE p.workspace_id = $1
  AND pr.resource_type = 'github_repo'`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var remote string
		if err := rows.Scan(&remote); err != nil {
			return nil, err
		}
		remote = strings.TrimSpace(remote)
		if remote != "" {
			out = append(out, remote)
		}
	}
	return out, rows.Err()
}

// scopeWorkDigestForWorkspace loads Workspace github_repo remotes and labels
// every digest repo. Matching failure yields unscoped; repos are never dropped.
func (h *Handler) scopeWorkDigestForWorkspace(
	ctx context.Context,
	workspaceID pgtype.UUID,
	digest protocol.WorkDigest,
) ([]scopedWorkDigestRepo, error) {
	remotes, err := h.listWorkspaceGitRepoRemotes(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	return scopeWorkDigestRepos(digest.Repos, remotes), nil
}
