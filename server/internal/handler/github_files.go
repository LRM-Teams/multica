package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Channel project-files, GitHub-backed source.
//
// When a project is bound to a `github_repo` resource, the channel Files panel
// reads the repo's file tree and contents **read-only from the GitHub API**,
// using the workspace's GitHub App installation token. This is decoupled from
// any runtime and from where agents execute — agents never touch the canonical
// source — and it works automatically for every GitHub-backed project without
// per-project configuration. Projects without a github_repo resource fall
// through to the managed-workdir daemon path (see channel_project.go).

var (
	errNoGitHubInstallation   = errors.New("workspace has no github installation")
	errGitHubAppNotConfigured = errors.New("github app not configured")
)

// githubFileMaxBytes caps a single previewed file. The GitHub contents API
// only inlines content up to 1MB; above that we report too_large rather than
// chasing the blobs API.
const githubFileMaxBytes = 1024 * 1024

// mediaMimeByExtGH maps extensions the preview renders directly to their MIME
// type (mirrors the daemon's media handling). Anything else is treated as text.
var mediaMimeByExtGH = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp", ".bmp": "image/bmp",
	".ico": "image/x-icon", ".svg": "image/svg+xml", ".avif": "image/avif",
	".mp4": "video/mp4", ".webm": "video/webm", ".mov": "video/quicktime",
	".mp3": "audio/mpeg", ".wav": "audio/wav", ".ogg": "audio/ogg",
	".m4a": "audio/mp4", ".flac": "audio/flac",
	".pdf": "application/pdf",
}

func mediaMimeGH(p string) string {
	return mediaMimeByExtGH[strings.ToLower(path.Ext(p))]
}

// githubTreeIgnore are path segments skipped in the tree (deps/build/VCS noise)
// so a committed node_modules or build dir doesn't bury the source.
var githubTreeIgnore = map[string]struct{}{
	".git": {}, "node_modules": {}, "dist": {}, "build": {}, "out": {},
	".next": {}, ".turbo": {}, ".cache": {}, "vendor": {}, "target": {},
}

const githubTreeMaxEntries = 5000

// parseGitHubRepoURL extracts owner/repo from the forms we store on a
// github_repo resource: https://github.com/owner/repo(.git), with or without a
// trailing slash, and the git@github.com:owner/repo.git SSH form.
func parseGitHubRepoURL(raw string) (owner, repo string, ok bool) {
	s := strings.TrimSpace(raw)
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	if i := strings.Index(s, "github.com"); i >= 0 {
		s = s[i+len("github.com"):]
	}
	s = strings.TrimLeft(s, "/:")
	parts := strings.Split(s, "/")
	if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
		return parts[0], parts[1], true
	}
	return "", "", false
}

// projectGitHubRepo returns the owner/repo of a project's bound github_repo
// resource, if any.
func (h *Handler) projectGitHubRepo(ctx context.Context, projectID string) (owner, repo string, ok bool) {
	var raw string
	_ = h.DB.QueryRow(ctx, `
		SELECT resource_ref->>'url'
		FROM project_resource
		WHERE project_id = $1 AND resource_type = 'github_repo'
		  AND resource_ref->>'url' IS NOT NULL
		ORDER BY created_at DESC
		LIMIT 1`, parseUUID(projectID)).Scan(&raw)
	if raw == "" {
		return "", "", false
	}
	return parseGitHubRepoURL(raw)
}

// installationTokenForWorkspace mints a short-lived GitHub App installation
// access token for the workspace's connected installation. Returns
// errNoGitHubInstallation / errGitHubAppNotConfigured so callers can surface a
// clear "connect GitHub" state instead of a generic error.
func (h *Handler) installationTokenForWorkspace(ctx context.Context, workspaceID pgtype.UUID) (string, error) {
	insts, err := h.Queries.ListGitHubInstallationsByWorkspace(ctx, workspaceID)
	if err != nil {
		return "", err
	}
	if len(insts) == 0 {
		return "", errNoGitHubInstallation
	}
	jwtTok, err := signGitHubAppJWT(time.Now())
	if err != nil {
		return "", err
	}
	if jwtTok == "" {
		return "", errGitHubAppNotConfigured
	}
	endpoint := fmt.Sprintf("%s/app/installations/%d/access_tokens",
		strings.TrimRight(githubAPIBase, "/"), insts[0].InstallationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+jwtTok)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		io.Copy(io.Discard, resp.Body)
		return "", fmt.Errorf("github access_tokens: status %d", resp.StatusCode)
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.Token == "" {
		return "", errors.New("empty installation token")
	}
	return body.Token, nil
}

// githubAPIGetJSON does an installation-authenticated GET and decodes JSON.
// Returns the HTTP status so callers can distinguish 404 from other failures.
func githubAPIGetJSON(ctx context.Context, token, apiPath string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(githubAPIBase, "/")+apiPath, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, nil
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

// githubProjectFiles returns the repo's file tree mapped to the panel's node
// shape, plus a status: ok | github_unlinked (App not connected) | error.
func (h *Handler) githubProjectFiles(ctx context.Context, workspaceID pgtype.UUID, owner, repo string) ([]protocol.WorkdirFileNode, bool, string) {
	token, err := h.installationTokenForWorkspace(ctx, workspaceID)
	if err != nil {
		if errors.Is(err, errNoGitHubInstallation) || errors.Is(err, errGitHubAppNotConfigured) {
			return nil, false, "github_unlinked"
		}
		return nil, false, "error"
	}

	var repoInfo struct {
		DefaultBranch string `json:"default_branch"`
	}
	if status, err := githubAPIGetJSON(ctx, token, fmt.Sprintf("/repos/%s/%s", owner, repo), &repoInfo); err != nil || status != http.StatusOK {
		return nil, false, "error"
	}
	branch := repoInfo.DefaultBranch
	if branch == "" {
		branch = "main"
	}

	var tree struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
			Size int64  `json:"size"`
		} `json:"tree"`
		Truncated bool `json:"truncated"`
	}
	if status, err := githubAPIGetJSON(ctx, token,
		fmt.Sprintf("/repos/%s/%s/git/trees/%s?recursive=1", owner, repo, url.PathEscape(branch)), &tree); err != nil || status != http.StatusOK {
		return nil, false, "error"
	}

	nodes := make([]protocol.WorkdirFileNode, 0, len(tree.Tree))
	truncated := tree.Truncated
	for _, e := range tree.Tree {
		if ignoredTreePath(e.Path) {
			continue
		}
		if len(nodes) >= githubTreeMaxEntries {
			truncated = true
			break
		}
		nodes = append(nodes, protocol.WorkdirFileNode{
			Path:  e.Path,
			IsDir: e.Type == "tree",
			Size:  e.Size,
		})
	}
	return nodes, truncated, "ok"
}

// ignoredTreePath reports whether any segment of the slash-path is in the
// ignore set.
func ignoredTreePath(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if _, ok := githubTreeIgnore[seg]; ok {
			return true
		}
	}
	return false
}

// githubProjectFileContent fetches a single file's content from the repo. Text
// is returned UTF-8; recognized media is base64 with a MIME type; oversize or
// binary files are flagged. Returns ok=false to signal "not available" (the
// caller maps that to an HTTP error).
func (h *Handler) githubProjectFileContent(ctx context.Context, workspaceID pgtype.UUID, owner, repo, filePath string) (ChannelProjectFileContentResponse, bool) {
	var out ChannelProjectFileContentResponse
	token, err := h.installationTokenForWorkspace(ctx, workspaceID)
	if err != nil {
		return out, false
	}

	var repoInfo struct {
		DefaultBranch string `json:"default_branch"`
	}
	if status, err := githubAPIGetJSON(ctx, token, fmt.Sprintf("/repos/%s/%s", owner, repo), &repoInfo); err != nil || status != http.StatusOK {
		return out, false
	}
	branch := repoInfo.DefaultBranch
	if branch == "" {
		branch = "main"
	}

	// Encode each path segment so spaces / unicode in the path survive.
	segs := strings.Split(strings.TrimLeft(filePath, "/"), "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	apiPath := fmt.Sprintf("/repos/%s/%s/contents/%s?ref=%s", owner, repo, strings.Join(segs, "/"), url.QueryEscape(branch))

	var body struct {
		Type     string `json:"type"`
		Encoding string `json:"encoding"`
		Size     int64  `json:"size"`
		Content  string `json:"content"`
	}
	status, err := githubAPIGetJSON(ctx, token, apiPath, &body)
	if err != nil || status != http.StatusOK || body.Type != "file" {
		return out, false
	}

	// Files over the contents-API inline limit come back with empty content.
	if body.Size > githubFileMaxBytes || (body.Content == "" && body.Size > 0) {
		out.TooLarge = true
		return out, true
	}
	raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(body.Content, "\n", ""))
	if err != nil {
		return out, false
	}
	if mime := mediaMimeGH(filePath); mime != "" {
		out.MimeType = mime
		out.Encoding = "base64"
		out.Content = base64.StdEncoding.EncodeToString(raw)
		return out, true
	}
	if len(raw) > 0 && (!utf8.Valid(raw) || strings.IndexByte(string(raw), 0) >= 0) {
		out.Binary = true
		return out, true
	}
	out.Content = string(raw)
	return out, true
}
