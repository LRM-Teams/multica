package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/workgraph"
)

// An unbound channel keeps a workspace-wide issue fallback so legacy/general
// channels still have work visibility.
func TestBuildWendyAmbientMarkdownIncludesWorkspaceFallbackIssues(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	title := "Ambient Visible Issue " + uuid.NewString()
	createIssueForTimeline(t, title)
	channelID := seedChannelForTest(t, "ambient-md-"+uuid.NewString(), testUserID)

	watch := workgraph.ChannelAmbientWatch{
		WorkspaceID: parseUUID(testWorkspaceID),
		ChannelID:   parseUUID(channelID),
	}
	ch := ChannelResponse{ID: channelID, WorkspaceID: testWorkspaceID, Name: "ambient-md", Kind: "group"}

	md, err := testHandler.buildWendyAmbientChannelMarkdown(ctx, watch, ch)
	if err != nil {
		t.Fatalf("build ambient markdown: %v", err)
	}
	if !strings.Contains(md, "## Open Workspace Fallback Issues") {
		t.Fatalf("markdown missing workspace fallback issues section:\n%s", md)
	}
	if !strings.Contains(md, title) {
		t.Fatalf("markdown missing open issue %q:\n%s", title, md)
	}
}

func TestBuildWendyAmbientMarkdownUsesBoundProjectContext(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	projectTitle := "Ambient Project " + uuid.NewString()
	projectDescription := "Build and review the actual project product"
	var projectID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title, description, status)
		VALUES ($1, $2, $3, 'in_progress')
		RETURNING id
	`, testWorkspaceID, projectTitle, projectDescription).Scan(&projectID); err != nil {
		t.Fatalf("create ambient project: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID)
	})

	const repoURL = "https://github.com/example/ambient-project"
	if _, err := testPool.Exec(ctx, `
		INSERT INTO project_resource (project_id, workspace_id, resource_type, resource_ref, label)
		VALUES ($1, $2, 'github_repo', $3::jsonb, 'Product repository')
	`, projectID, testWorkspaceID, `{"url":"`+repoURL+`","default_branch_hint":"dev"}`); err != nil {
		t.Fatalf("create ambient project resource: %v", err)
	}

	channelID := seedChannelForTest(t, "ambient-project-md-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `UPDATE channel SET project_id = $1 WHERE id = $2`, projectID, channelID); err != nil {
		t.Fatalf("bind ambient channel project: %v", err)
	}

	openTitle := "Project Open Issue " + uuid.NewString()
	openIssueID := createIssueForTimeline(t, openTitle)
	if _, err := testPool.Exec(ctx, `
		UPDATE issue
		SET project_id = $1,
		    description = 'Inspect the deployed behavior',
		    acceptance_criteria = '["shows project evidence","uses the issue UUID"]'::jsonb
		WHERE id = $2
	`, projectID, openIssueID); err != nil {
		t.Fatalf("attach open issue to ambient project: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content)
		VALUES ($1, $2, 'member', $3, 'PR is ready for evidence review')
	`, openIssueID, testWorkspaceID, testUserID); err != nil {
		t.Fatalf("create open issue progress: %v", err)
	}

	doneTitle := "Project Completed Issue " + uuid.NewString()
	doneIssueID := createIssueForTimeline(t, doneTitle)
	if _, err := testPool.Exec(ctx, `
		UPDATE issue
		SET project_id = $1,
		    status = 'done',
		    acceptance_criteria = '["verified completion"]'::jsonb,
		    updated_at = now()
		WHERE id = $2
	`, projectID, doneIssueID); err != nil {
		t.Fatalf("attach completed issue to ambient project: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content)
		VALUES ($1, $2, 'member', $3, 'Verified on the delivered artifact')
	`, doneIssueID, testWorkspaceID, testUserID); err != nil {
		t.Fatalf("create completed issue evidence: %v", err)
	}

	unrelatedTitle := "Unrelated Workspace Issue " + uuid.NewString()
	createIssueForTimeline(t, unrelatedTitle)

	channel, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("load project-bound ambient channel")
	}
	watch := workgraph.ChannelAmbientWatch{
		WorkspaceID: parseUUID(testWorkspaceID),
		ChannelID:   parseUUID(channelID),
	}
	md, err := testHandler.buildWendyAmbientChannelMarkdown(ctx, watch, channel)
	if err != nil {
		t.Fatalf("build project ambient markdown: %v", err)
	}

	for _, want := range []string{
		"context_mode=coordination",
		"project_id=" + projectID,
		projectTitle,
		projectDescription,
		"## Project Resources",
		repoURL,
		"## Open Project Issues",
		"issue_id=" + openIssueID,
		openTitle,
		`acceptance_criteria=["shows project evidence", "uses the issue UUID"]`,
		"PR is ready for evidence review",
		"## Recently Completed Project Issues",
		doneTitle,
		"Verified on the delivered artifact",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("project ambient markdown missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, unrelatedTitle) {
		t.Fatalf("project ambient markdown leaked unrelated workspace issue %q:\n%s", unrelatedTitle, md)
	}
}
