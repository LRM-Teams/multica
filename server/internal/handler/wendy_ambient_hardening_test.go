package handler

import (
	"context"
	"encoding/json"
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
	const completedAt = "2026-07-20T03:04:05Z"
	if _, err := testPool.Exec(ctx, `
		INSERT INTO activity_log (workspace_id, issue_id, actor_type, actor_id, action, details, created_at)
		VALUES ($1, $2, 'member', $3, 'status_changed', '{"from":"in_review","to":"done"}'::jsonb, $4::timestamptz)
	`, testWorkspaceID, doneIssueID, testUserID, completedAt); err != nil {
		t.Fatalf("record completed issue transition: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE issue SET updated_at = '2026-07-21T06:07:08Z'::timestamptz WHERE id = $1`, doneIssueID); err != nil {
		t.Fatalf("edit completed issue after completion: %v", err)
	}

	unrelatedTitle := "Unrelated Workspace Issue " + uuid.NewString()
	unrelatedIssueID := createIssueForTimeline(t, unrelatedTitle)
	unrelatedIssue, err := testHandler.Queries.GetIssue(ctx, parseUUID(unrelatedIssueID))
	if err != nil {
		t.Fatalf("load unrelated issue: %v", err)
	}
	unrelatedNode, err := testHandler.WorkGraph.SyncIssueNode(ctx, unrelatedIssue)
	if err != nil {
		t.Fatalf("sync unrelated issue work node: %v", err)
	}
	if err := testHandler.WorkGraph.SetPrimaryChannel(ctx, unrelatedNode.ID, parseUUID(channelID)); err != nil {
		t.Fatalf("attach unrelated issue work node to project channel: %v", err)
	}
	coordinationTitle := "Channel-only coordination " + uuid.NewString()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO work_node (
		  workspace_id, kind, title, owner_type, owner_id, status, primary_channel_id, description
		) VALUES ($1, 'chat_commitment', $2, 'unassigned', NULL, 'active', $3, 'test coordination node')
	`, testWorkspaceID, coordinationTitle, channelID); err != nil {
		t.Fatalf("create channel coordination node: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `
			DELETE FROM work_node WHERE workspace_id = $1 AND title = $2
		`, testWorkspaceID, coordinationTitle)
	})

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
		"type=comment author_type=member author_id=" + testUserID,
		"## Recently Completed Project Issues",
		doneTitle,
		"completed_at=" + completedAt,
		"Verified on the delivered artifact",
		coordinationTitle,
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("project ambient markdown missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, unrelatedTitle) {
		t.Fatalf("project ambient markdown leaked unrelated workspace issue %q:\n%s", unrelatedTitle, md)
	}
	if strings.Contains(md, "completed_at=2026-07-21T06:07:08Z") {
		t.Fatalf("project ambient markdown mislabeled issue.updated_at as completion time:\n%s", md)
	}
}

func TestTrimAmbientJSONKeepsTruncatedValuesValid(t *testing.T) {
	raw := []byte(`{"items":["` + strings.Repeat("long-value-", 80) + `"]}`)
	got := trimAmbientJSON(raw)
	if !json.Valid([]byte(got)) {
		t.Fatalf("trimmed structured value is invalid JSON: %s", got)
	}
	var envelope struct {
		Truncated bool   `json:"truncated"`
		Preview   string `json:"preview"`
	}
	if err := json.Unmarshal([]byte(got), &envelope); err != nil {
		t.Fatalf("decode truncation envelope: %v", err)
	}
	if !envelope.Truncated || envelope.Preview == "" {
		t.Fatalf("truncation envelope = %+v, want explicit non-empty preview", envelope)
	}
}
