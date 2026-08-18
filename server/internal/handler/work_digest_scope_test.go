package handler

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestNormalizeGitRemoteURLEquivalence(t *testing.T) {
	want := "github.com/org/app"
	cases := []string{
		"https://github.com/org/app.git",
		"https://GitHub.com/Org/App",
		"http://github.com/org/app/",
		"git@github.com:org/app.git",
		"ssh://git@github.com/org/app.git",
		"ssh://git@github.com:22/org/app.git",
	}
	for _, raw := range cases {
		got, ok := normalizeGitRemoteURL(raw)
		if !ok || got != want {
			t.Fatalf("normalizeGitRemoteURL(%q) = %q ok=%v, want %q", raw, got, ok, want)
		}
	}
	if _, ok := normalizeGitRemoteURL("not-a-remote"); ok {
		t.Fatal("expected invalid remote to fail")
	}
}

func TestScopeWorkDigestReposWorkspaceVsUnscoped(t *testing.T) {
	repos := []protocol.WorkDigestRepo{
		{
			Root:    "/home/owner/code/app",
			Remotes: []string{"git@github.com:org/app.git"},
			Commits: []protocol.WorkDigestCommit{},
			Dirty:   []protocol.WorkDigestDirtyPath{},
		},
		{
			Root:    "/home/owner/code/side",
			Remotes: []string{"https://github.com/me/personal.git"},
			Commits: []protocol.WorkDigestCommit{},
			Dirty:   []protocol.WorkDigestDirtyPath{},
		},
		{
			Root:    "/home/owner/code/broken",
			Remotes: []string{"not-a-url"},
			Commits: []protocol.WorkDigestCommit{},
			Dirty:   []protocol.WorkDigestDirtyPath{},
		},
	}
	scoped := scopeWorkDigestRepos(repos, []string{
		"https://github.com/Org/App.git",
	})
	if len(scoped) != 3 {
		t.Fatalf("scoped len=%d, want 3 (never drop)", len(scoped))
	}
	if scoped[0].Scope != workDigestRepoScopeWorkspace || scoped[0].Root != "/home/owner/code/app" {
		t.Fatalf("first = %+v", scoped[0])
	}
	if scoped[1].Scope != workDigestRepoScopeUnscoped {
		t.Fatalf("personal repo scope = %q", scoped[1].Scope)
	}
	if scoped[2].Scope != workDigestRepoScopeUnscoped {
		t.Fatalf("unparseable remote must stay unscoped, got %q", scoped[2].Scope)
	}
}

func TestScopeWorkDigestForWorkspaceUsesProjectGithubRepos(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	projectID := createHandlerTestProject(t, "Digest scope "+uuid.NewString()[:8])
	if _, err := testPool.Exec(ctx, `
INSERT INTO project_resource (project_id, workspace_id, resource_type, resource_ref)
VALUES ($1, $2, 'github_repo', jsonb_build_object('url', $3::text))`,
		projectID, testWorkspaceID, "https://github.com/org/app.git"); err != nil {
		t.Fatalf("insert github_repo: %v", err)
	}

	digest := protocol.WorkDigest{
		ComputerID: "computer-1",
		Repos: []protocol.WorkDigestRepo{
			{Root: "/ws/app", Remotes: []string{"git@github.com:org/app.git"}},
			{Root: "/home/other", Remotes: []string{"https://github.com/me/toys.git"}},
		},
	}
	scoped, err := testHandler.scopeWorkDigestForWorkspace(ctx, parseUUID(testWorkspaceID), digest)
	if err != nil {
		t.Fatalf("scope: %v", err)
	}
	if len(scoped) != 2 {
		t.Fatalf("scoped=%+v", scoped)
	}
	if scoped[0].Scope != workDigestRepoScopeWorkspace || scoped[1].Scope != workDigestRepoScopeUnscoped {
		t.Fatalf("scopes = %q / %q", scoped[0].Scope, scoped[1].Scope)
	}
}

func createHandlerTestProject(t *testing.T, title string) string {
	t.Helper()
	var id string
	if err := testPool.QueryRow(context.Background(), `
INSERT INTO project (workspace_id, title, status)
VALUES ($1, $2, 'planned')
RETURNING id`, testWorkspaceID, title).Scan(&id); err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM project_resource WHERE project_id = $1`, id)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, id)
	})
	return id
}
