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
	var openCommentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content)
		VALUES ($1, $2, 'member', $3, 'PR is ready for evidence review')
		RETURNING id
	`, openIssueID, testWorkspaceID, testUserID).Scan(&openCommentID); err != nil {
		t.Fatalf("create open issue progress: %v", err)
	}
	var issueAttachmentID, commentAttachmentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO attachment (workspace_id, issue_id, uploader_type, uploader_id, filename, url, content_type, size_bytes)
		VALUES ($1, $2, 'member', $3, 'approved-layout.webp', '/approved-layout.webp', 'image/webp', 42)
		RETURNING id
	`, testWorkspaceID, openIssueID, testUserID).Scan(&issueAttachmentID); err != nil {
		t.Fatalf("create open issue attachment: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO attachment (workspace_id, issue_id, comment_id, uploader_type, uploader_id, filename, url, content_type, size_bytes)
		VALUES ($1, $2, $3, 'member', $4, 'implementation-shot.png', '/implementation-shot.png', 'image/png', 84)
		RETURNING id
	`, testWorkspaceID, openIssueID, openCommentID, testUserID).Scan(&commentAttachmentID); err != nil {
		t.Fatalf("create open comment attachment: %v", err)
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

	workerID := createHandlerTestAgent(t, "visual-worker-"+uuid.NewString(), nil)
	worker, err := testHandler.Queries.GetAgent(ctx, parseUUID(workerID))
	if err != nil {
		t.Fatalf("load visual worker: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE agent SET description = 'Builds polished UI and repository-native visual assets' WHERE id = $1`, workerID); err != nil {
		t.Fatalf("configure visual worker: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)

ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, workerID); err != nil {
		t.Fatalf("add visual worker to channel: %v", err)
	}
	skillSuffix := uuid.NewString()
	assignedSkillName := "ui-production-" + skillSuffix
	runtimeSkillName := "imagegen-" + skillSuffix
	foreignSkillName := "foreign-runtime-imagegen-" + skillSuffix
	var assignedSkillID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO skill (workspace_id, name, description, content, created_by)
		VALUES ($1, $2, 'Implements responsive production UI', 'Use repository assets.', $3)
		RETURNING id
	`, testWorkspaceID, assignedSkillName, testUserID).Scan(&assignedSkillID); err != nil {
		t.Fatalf("create assigned visual skill: %v", err)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO agent_skill (agent_id, skill_id) VALUES ($1, $2)`, workerID, assignedSkillID); err != nil {
		t.Fatalf("assign visual skill: %v", err)
	}
	var runtimeSkillID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO skill (workspace_id, name, description, content, config, created_by)
		VALUES (
			$1, $2, 'Generates bitmap illustrations and textures', 'Use the configured image generator.',
			jsonb_build_object('origin', jsonb_build_object('type', 'runtime_shared', 'runtime_id', $3::text)), $4
		)
		RETURNING id
	`, testWorkspaceID, runtimeSkillName, uuidToString(worker.RuntimeID), testUserID).Scan(&runtimeSkillID); err != nil {
		t.Fatalf("create runtime visual skill: %v", err)
	}
	var foreignSkillID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO skill (workspace_id, name, description, content, config, created_by)
		VALUES (
			$1, $2, 'Must not be attributed to this worker', 'Unavailable here.',
			jsonb_build_object('origin', jsonb_build_object('type', 'runtime_shared', 'runtime_id', $3::text)), $4
		)
		RETURNING id
	`, testWorkspaceID, foreignSkillName, uuid.NewString(), testUserID).Scan(&foreignSkillID); err != nil {
		t.Fatalf("create foreign runtime skill: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM skill WHERE id = ANY($1::uuid[])`, []string{assignedSkillID, runtimeSkillID, foreignSkillID})
	})
	var messageID, messageAttachmentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content)
		VALUES ($1, $2, 'user', $3, 'Reference author', 'Match this visual reference')
		RETURNING id
	`, channelID, testWorkspaceID, testUserID).Scan(&messageID); err != nil {
		t.Fatalf("create reference message: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO attachment (
			workspace_id, uploader_type, uploader_id, filename, url, content_type,
			size_bytes, channel_id
		) VALUES ($1, 'member', $2, 'reference-board.png', '/reference-board.png', 'image/png', 128, $3)
		RETURNING id
	`, testWorkspaceID, testUserID, channelID).Scan(&messageAttachmentID); err != nil {
		t.Fatalf("create reference message attachment: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_message_attachment (workspace_id, channel_message_id, attachment_id)
		VALUES ($1, $2, $3)`, testWorkspaceID, messageID, messageAttachmentID); err != nil {
		t.Fatalf("create reference message attachment reference: %v", err)
	}
	var deletedMessageID, deletedAttachmentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, deleted_at)
		VALUES ($1, $2, 'user', $3, 'Deleted author', 'Deleted visual reference', now())
		RETURNING id
	`, channelID, testWorkspaceID, testUserID).Scan(&deletedMessageID); err != nil {
		t.Fatalf("create deleted reference message: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO attachment (
			workspace_id, uploader_type, uploader_id, filename, url, content_type,
			size_bytes, channel_id
		) VALUES ($1, 'member', $2, 'deleted-reference.png', '/deleted-reference.png', 'image/png', 64, $3)
		RETURNING id
	`, testWorkspaceID, testUserID, channelID).Scan(&deletedAttachmentID); err != nil {
		t.Fatalf("create deleted reference attachment: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_message_attachment (workspace_id, channel_message_id, attachment_id)
		VALUES ($1, $2, $3)`, testWorkspaceID, deletedMessageID, deletedAttachmentID); err != nil {
		t.Fatalf("create deleted reference attachment reference: %v", err)
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
		`"content": "PR is ready for evidence review"`,
		`"author_type": "member"`,
		`"attachment_id": "` + issueAttachmentID + `"`,
		`"filename": "approved-layout.webp"`,
		`"content_type": "image/webp"`,
		`"attachment_id": "` + commentAttachmentID + `"`,
		`"filename": "implementation-shot.png"`,
		`"content_type": "image/png"`,
		"## Recently Completed Project Issues",
		doneTitle,
		"completed_at=" + completedAt,
		"Verified on the delivered artifact",
		coordinationTitle,
		"agent_id=" + workerID,
		`description="Builds polished UI and repository-native visual assets"`,
		assignedSkillName,
		runtimeSkillName,
		"attachment_id=" + messageAttachmentID,
		`filename="reference-board.png" content_type=image/png`,
		"multica attachment view --id " + messageAttachmentID + " --output <path>",
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
	for _, forbidden := range []string{"Deleted visual reference", deletedAttachmentID, "deleted-reference.png", foreignSkillName} {
		if strings.Contains(md, forbidden) {
			t.Fatalf("project ambient markdown exposed unavailable evidence %q:\n%s", forbidden, md)
		}
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
