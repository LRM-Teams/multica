package handler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/radar"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type radarIssueForeignWorkspaceFixture struct {
	workspaceID string
	userID      string
	agentID     string
	projectID   string
}

func createRadarIssueForeignWorkspaceFixture(t *testing.T) radarIssueForeignWorkspaceFixture {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()

	var fixture radarIssueForeignWorkspaceFixture
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ($1, $2)
		RETURNING id
	`, "Radar foreign user "+suffix, "radar-foreign-"+suffix+"@multica.test").Scan(&fixture.userID); err != nil {
		t.Fatalf("create foreign user: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, '', 'RFW')
		RETURNING id
	`, "Radar foreign workspace "+suffix, "radar-foreign-"+suffix).Scan(&fixture.workspaceID); err != nil {
		t.Fatalf("create foreign workspace: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'owner')
	`, fixture.workspaceID, fixture.userID); err != nil {
		t.Fatalf("create foreign member: %v", err)
	}

	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, last_seen_at, owner_id
		)
		VALUES ($1, NULL, $2, 'cloud', 'handler_test_runtime', 'online', $3, '{}'::jsonb, now(), $4)
		RETURNING id
	`, fixture.workspaceID, "Radar foreign runtime "+suffix, "Radar foreign runtime", fixture.userID).Scan(&runtimeID); err != nil {
		t.Fatalf("create foreign runtime: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id
		)
		VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 'workspace', 1, $4)
		RETURNING id
	`, fixture.workspaceID, "radar-foreign-agent-"+suffix, runtimeID, fixture.userID).Scan(&fixture.agentID); err != nil {
		t.Fatalf("create foreign agent: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title)
		VALUES ($1, $2)
		RETURNING id
	`, fixture.workspaceID, "Radar foreign project "+suffix).Scan(&fixture.projectID); err != nil {
		t.Fatalf("create foreign project: %v", err)
	}

	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, fixture.workspaceID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, fixture.userID)
	})
	return fixture
}

func executeRadarCreateIssueForTest(t *testing.T, agent db.Agent, payload radarIssuePayload) (map[string]any, error) {
	t.Helper()
	return executeRadarCreateIssueForRunTest(t, db.AgentRadarRun{
		WorkspaceID: parseUUID(testWorkspaceID),
		AgentID:     agent.ID,
	}, agent, payload)
}

func executeRadarCreateIssueForRunTest(t *testing.T, run db.AgentRadarRun, agent db.Agent, payload radarIssuePayload) (map[string]any, error) {
	t.Helper()
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal radar issue payload: %v", err)
	}
	return testHandler.executeRadarCreateIssue(context.Background(), run, agent, radar.RadarAction{
		Type:    radar.ActionCreateIssue,
		Payload: rawPayload,
	})
}

func cleanupRadarIssueTitle(t *testing.T, title string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `
			DELETE FROM issue WHERE workspace_id = $1 AND title = $2
		`, testWorkspaceID, title)
	})
}

func createRadarProjectForTest(t *testing.T, title string) string {
	t.Helper()
	var projectID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO project (workspace_id, title)
		VALUES ($1, $2)
		RETURNING id
	`, testWorkspaceID, title).Scan(&projectID); err != nil {
		t.Fatalf("create radar project: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID)
	})
	return projectID
}

func enqueueProjectScopedRadarRunForTest(t *testing.T, agent db.Agent, projectID string) db.AgentRadarRun {
	t.Helper()
	run, _, err := testHandler.TaskService.EnqueueAgentRadarRun(context.Background(), service.EnqueueAgentRadarRunParams{
		WorkspaceID:    agent.WorkspaceID,
		AgentID:        agent.ID,
		ProjectID:      parseUUID(projectID),
		TriggerKind:    "event",
		TriggerRef:     "project-scope-test-" + uuid.NewString(),
		CooldownKey:    "wendy_ambient:" + uuid.NewString(),
		ContextSummary: "project scope executor test",
		ScheduledFor:   time.Now(),
		Prompt:         "review project",
	})
	if err != nil {
		t.Fatalf("enqueue project-scoped radar run: %v", err)
	}
	return run
}

func TestExecuteRadarCreateIssueUsesPersistedProjectWhenPayloadOmitsIt(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	creatorID := createHandlerTestAgent(t, "Radar scoped project creator "+uuid.NewString(), nil)
	creator, err := testHandler.Queries.GetAgent(context.Background(), parseUUID(creatorID))
	if err != nil {
		t.Fatalf("load creator agent: %v", err)
	}
	projectID := createRadarProjectForTest(t, "Radar canonical project "+uuid.NewString())
	run := enqueueProjectScopedRadarRunForTest(t, creator, projectID)
	title := "Radar canonical project issue " + uuid.NewString()
	cleanupRadarIssueTitle(t, title)

	criteria := []string{"the implementation matches the approved specification"}
	result, err := executeRadarCreateIssueForRunTest(t, run, creator, radarIssuePayload{
		Title:              title,
		Description:        "Deliver the scoped project requirement",
		AcceptanceCriteria: criteria,
	})
	if err != nil {
		t.Fatalf("create project-scoped issue: %v", err)
	}
	issue, err := testHandler.Queries.GetIssueInWorkspace(context.Background(), db.GetIssueInWorkspaceParams{
		ID:          parseUUID(result["issue_id"].(string)),
		WorkspaceID: creator.WorkspaceID,
	})
	if err != nil {
		t.Fatalf("load project-scoped issue: %v", err)
	}
	if got := uuidToString(issue.ProjectID); got != projectID {
		t.Fatalf("created issue project_id = %s, want persisted scope %s", got, projectID)
	}
	if issue.AssigneeType.Valid || issue.AssigneeID.Valid {
		t.Fatalf("omitted assignee created manager-owned issue %q/%s", issue.AssigneeType.String, uuidToString(issue.AssigneeID))
	}
	var storedCriteria []string
	if err := json.Unmarshal(issue.AcceptanceCriteria, &storedCriteria); err != nil {
		t.Fatalf("decode acceptance criteria: %v", err)
	}
	if len(storedCriteria) != 1 || storedCriteria[0] != criteria[0] {
		t.Fatalf("acceptance criteria = %#v, want %#v", storedCriteria, criteria)
	}
	var managerTaskCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM agent_task_queue WHERE issue_id = $1 AND agent_id = $2
	`, issue.ID, creator.ID).Scan(&managerTaskCount); err != nil {
		t.Fatalf("count manager tasks: %v", err)
	}
	if managerTaskCount != 0 {
		t.Fatalf("omitted assignee queued %d concrete task(s) for the manager", managerTaskCount)
	}
}

func TestExecuteRadarCreateIssuePersistsSourceAndReferenceAttachment(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	creatorID := createHandlerTestAgent(t, "Radar evidence creator "+uuid.NewString(), nil)
	creator, err := testHandler.Queries.GetAgent(ctx, parseUUID(creatorID))
	if err != nil {
		t.Fatalf("load creator agent: %v", err)
	}
	projectID := createRadarProjectForTest(t, "Radar evidence project "+uuid.NewString())
	channelID := seedChannelForTest(t, "radar-evidence-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `UPDATE channel SET project_id = $2 WHERE id = $1`, channelID, projectID); err != nil {
		t.Fatalf("bind evidence channel: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
	`, channelID, testWorkspaceID, creatorID); err != nil {
		t.Fatalf("add evidence channel agent: %v", err)
	}
	var messageID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content)
		VALUES ($1, $2, 'user', $3, 'Evidence author', 'Use this reference image')
		RETURNING id
	`, channelID, testWorkspaceID, testUserID).Scan(&messageID); err != nil {
		t.Fatalf("create source message: %v", err)
	}
	var attachmentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO attachment (
			workspace_id, uploader_type, uploader_id, filename, url, content_type,
			size_bytes, channel_id, channel_message_id
		) VALUES ($1, 'member', $2, 'approved-reference.webp', '/reference.webp', 'image/webp', 42, $3, $4)
		RETURNING id
	`, testWorkspaceID, testUserID, channelID, messageID).Scan(&attachmentID); err != nil {
		t.Fatalf("create source attachment: %v", err)
	}

	run := enqueueProjectScopedRadarRunForTest(t, creator, projectID)
	task, err := testHandler.Queries.GetAgentTask(ctx, run.TaskID)
	if err != nil {
		t.Fatalf("load radar task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_task_queue
		SET context = jsonb_set(context, '{channel_id}', to_jsonb($2::text), true)
		WHERE id = $1
	`, task.ID, channelID); err != nil {
		t.Fatalf("pin radar channel context: %v", err)
	}
	title := "Radar evidence issue " + uuid.NewString()
	cleanupRadarIssueTitle(t, title)
	criteria := []string{"ship a WebP asset in the repository", "attach responsive screenshots"}
	createdEvents := make(chan events.Event, 1)
	testHandler.Bus.Subscribe(protocol.EventIssueCreated, func(event events.Event) {
		payload, _ := event.Payload.(map[string]any)
		issue, _ := payload["issue"].(IssueResponse)
		if issue.Title == title {
			createdEvents <- event
		}
	})
	result, err := executeRadarCreateIssueForRunTest(t, run, creator, radarIssuePayload{
		Title:              title,
		Description:        "Build the visual from the approved reference",
		AcceptanceCriteria: criteria,
		AttachmentIDs:      []string{attachmentID},
		SourceMessageID:    messageID,
	})
	if err != nil {
		t.Fatalf("create evidence-backed issue: %v", err)
	}
	issueID := result["issue_id"].(string)
	var sourceChannelID, sourceMessageID string
	if err := testPool.QueryRow(ctx, `
		SELECT channel_id::text, message_id::text
		FROM issue_source_message WHERE issue_id = $1
	`, issueID).Scan(&sourceChannelID, &sourceMessageID); err != nil {
		t.Fatalf("load source anchor: %v", err)
	}
	if sourceChannelID != channelID || sourceMessageID != messageID {
		t.Fatalf("source anchor = %s/%s, want %s/%s", sourceChannelID, sourceMessageID, channelID, messageID)
	}
	var linkedIssueID string
	if err := testPool.QueryRow(ctx, `SELECT issue_id::text FROM attachment WHERE id = $1`, attachmentID).Scan(&linkedIssueID); err != nil {
		t.Fatalf("load linked attachment: %v", err)
	}
	if linkedIssueID != issueID {
		t.Fatalf("attachment issue_id = %s, want %s", linkedIssueID, issueID)
	}
	select {
	case event := <-createdEvents:
		payload := event.Payload.(map[string]any)
		created := payload["issue"].(IssueResponse)
		if created.ID != issueID || len(created.AcceptanceCriteria) != len(criteria) || len(created.Attachments) != 1 {
			t.Fatalf("issue:created payload = %+v", created)
		}
	default:
		t.Fatal("radar issue creation did not publish the full issue payload")
	}
	var nodeCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM work_node WHERE linked_issue_id = $1`, issueID).Scan(&nodeCount); err != nil {
		t.Fatalf("count synced work nodes: %v", err)
	}
	if nodeCount != 1 {
		t.Fatalf("radar-created issue work node count = %d, want 1", nodeCount)
	}

	var deletedMessageID, deletedAttachmentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel_message (
			channel_id, workspace_id, author_type, author_id, author_name, content, deleted_at
		) VALUES ($1, $2, 'user', $3, 'Deleted evidence author', 'Deleted evidence', now())
		RETURNING id
	`, channelID, testWorkspaceID, testUserID).Scan(&deletedMessageID); err != nil {
		t.Fatalf("create deleted evidence message: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO attachment (
			workspace_id, uploader_type, uploader_id, filename, url, content_type,
			size_bytes, channel_id, channel_message_id
		) VALUES ($1, 'member', $2, 'deleted-reference.webp', '/deleted-reference.webp', 'image/webp', 42, $3, $4)
		RETURNING id
	`, testWorkspaceID, testUserID, channelID, deletedMessageID).Scan(&deletedAttachmentID); err != nil {
		t.Fatalf("create deleted evidence attachment: %v", err)
	}
	deletedTitle := "Radar deleted evidence issue " + uuid.NewString()
	cleanupRadarIssueTitle(t, deletedTitle)
	_, err = executeRadarCreateIssueForRunTest(t, run, creator, radarIssuePayload{
		Title:              deletedTitle,
		Description:        "This action must not reuse deleted evidence",
		AcceptanceCriteria: []string{"only live channel evidence is attached"},
		AttachmentIDs:      []string{deletedAttachmentID},
		SourceMessageID:    messageID,
	})
	if err == nil || err.Error() != "attachment message is deleted or outside the radar channel" {
		t.Fatalf("deleted attachment error = %v", err)
	}
	var deletedIssueCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM issue WHERE title = $1`, deletedTitle).Scan(&deletedIssueCount); err != nil {
		t.Fatalf("count deleted-evidence issues: %v", err)
	}
	if deletedIssueCount != 0 {
		t.Fatalf("deleted evidence created %d issue(s), want 0", deletedIssueCount)
	}
}

func TestExecuteRadarCreateIssueRejectsMissingAcceptanceCriteria(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	creatorID := createHandlerTestAgent(t, "Radar spec guard creator "+uuid.NewString(), nil)
	creator, err := testHandler.Queries.GetAgent(context.Background(), parseUUID(creatorID))
	if err != nil {
		t.Fatalf("load creator agent: %v", err)
	}
	title := "Radar missing criteria " + uuid.NewString()
	cleanupRadarIssueTitle(t, title)
	_, err = executeRadarCreateIssueForTest(t, creator, radarIssuePayload{
		Title:       title,
		Description: "A description without a verifiable definition of done",
	})
	if err == nil || err.Error() != "acceptance_criteria must contain at least one non-empty criterion" {
		t.Fatalf("error = %v, want acceptance criteria validation", err)
	}
	var count int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM issue WHERE title = $1`, title).Scan(&count); err != nil {
		t.Fatalf("count rejected issues: %v", err)
	}
	if count != 0 {
		t.Fatalf("missing-criteria action created %d issue(s), want 0", count)
	}
}

func TestExecuteRadarCreateIssueRejectsProjectConflictingWithPersistedScope(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	creatorID := createHandlerTestAgent(t, "Radar conflicting project creator "+uuid.NewString(), nil)
	creator, err := testHandler.Queries.GetAgent(context.Background(), parseUUID(creatorID))
	if err != nil {
		t.Fatalf("load creator agent: %v", err)
	}
	canonicalProjectID := createRadarProjectForTest(t, "Radar canonical project "+uuid.NewString())
	otherProjectID := createRadarProjectForTest(t, "Radar wrong project "+uuid.NewString())
	run := enqueueProjectScopedRadarRunForTest(t, creator, canonicalProjectID)
	title := "Radar conflicting project issue " + uuid.NewString()
	cleanupRadarIssueTitle(t, title)

	_, err = executeRadarCreateIssueForRunTest(t, run, creator, radarIssuePayload{
		Title:              title,
		Description:        "Reject a conflicting persisted project scope",
		AcceptanceCriteria: []string{"the issue stays in the persisted project"},
		ProjectID:          otherProjectID,
	})
	if err == nil {
		t.Fatal("expected conflicting project_id to be rejected")
	}
	if got, want := err.Error(), "project_id conflicts with the radar task project scope"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
	var count int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM issue WHERE workspace_id = $1 AND title = $2
	`, testWorkspaceID, title).Scan(&count); err != nil {
		t.Fatalf("count rejected issues: %v", err)
	}
	if count != 0 {
		t.Fatalf("conflicting project action created %d issues, want 0", count)
	}
}

func TestExecuteRadarCreateIssueRejectsForeignAgentAssignee(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	creatorID := createHandlerTestAgent(t, "Radar workspace guard creator "+uuid.NewString(), nil)
	creator, err := testHandler.Queries.GetAgent(context.Background(), parseUUID(creatorID))
	if err != nil {
		t.Fatalf("load creator agent: %v", err)
	}
	foreign := createRadarIssueForeignWorkspaceFixture(t)
	title := "Radar must reject foreign agent " + uuid.NewString()
	cleanupRadarIssueTitle(t, title)

	_, err = executeRadarCreateIssueForTest(t, creator, radarIssuePayload{
		Title:              title,
		Description:        "Reject a foreign agent assignee",
		AcceptanceCriteria: []string{"the assignee belongs to the run workspace"},
		AssigneeType:       "agent",
		AssigneeID:         foreign.agentID,
	})
	if err == nil {
		t.Fatal("expected foreign-workspace agent assignee to be rejected")
	}
}

func TestExecuteRadarCreateIssueRejectsForeignMemberAssignee(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	creatorID := createHandlerTestAgent(t, "Radar member guard creator "+uuid.NewString(), nil)
	creator, err := testHandler.Queries.GetAgent(context.Background(), parseUUID(creatorID))
	if err != nil {
		t.Fatalf("load creator agent: %v", err)
	}
	foreign := createRadarIssueForeignWorkspaceFixture(t)
	title := "Radar must reject foreign member " + uuid.NewString()
	cleanupRadarIssueTitle(t, title)

	_, err = executeRadarCreateIssueForTest(t, creator, radarIssuePayload{
		Title:              title,
		Description:        "Reject a foreign member assignee",
		AcceptanceCriteria: []string{"the assignee belongs to the run workspace"},
		AssigneeType:       "member",
		AssigneeID:         foreign.userID,
	})
	if err == nil {
		t.Fatal("expected foreign-workspace member assignee to be rejected")
	}
}

func TestExecuteRadarCreateIssueRejectsForeignProject(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	creatorID := createHandlerTestAgent(t, "Radar project guard creator "+uuid.NewString(), nil)
	creator, err := testHandler.Queries.GetAgent(context.Background(), parseUUID(creatorID))
	if err != nil {
		t.Fatalf("load creator agent: %v", err)
	}
	foreign := createRadarIssueForeignWorkspaceFixture(t)
	title := "Radar must reject foreign project " + uuid.NewString()
	cleanupRadarIssueTitle(t, title)

	_, err = executeRadarCreateIssueForTest(t, creator, radarIssuePayload{
		Title:              title,
		Description:        "Reject a foreign project",
		AcceptanceCriteria: []string{"the project belongs to the run workspace"},
		ProjectID:          foreign.projectID,
	})
	if err == nil {
		t.Fatal("expected foreign-workspace project to be rejected")
	}
	if got, want := err.Error(), "project_id does not refer to a project in the radar run workspace"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestExecuteRadarCreateIssueRejectsUnsupportedAssigneeType(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	creatorID := createHandlerTestAgent(t, "Radar assignee type guard creator "+uuid.NewString(), nil)
	creator, err := testHandler.Queries.GetAgent(context.Background(), parseUUID(creatorID))
	if err != nil {
		t.Fatalf("load creator agent: %v", err)
	}
	title := "Radar must reject unsupported assignee type " + uuid.NewString()
	cleanupRadarIssueTitle(t, title)

	_, err = executeRadarCreateIssueForTest(t, creator, radarIssuePayload{
		Title:              title,
		Description:        "Reject an unsupported assignee type",
		AcceptanceCriteria: []string{"the assignee type is supported"},
		AssigneeType:       "user",
		AssigneeID:         testUserID,
	})
	if err == nil {
		t.Fatal("expected unsupported assignee type to be rejected")
	}
	if got, want := err.Error(), "assignee_type must be 'member' or 'agent'"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestExecuteRadarCreateIssueLeavesOmittedAssigneeUnassigned(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	creatorID := createHandlerTestAgent(t, "Radar valid agent assignee "+uuid.NewString(), nil)
	creator, err := testHandler.Queries.GetAgent(context.Background(), parseUUID(creatorID))
	if err != nil {
		t.Fatalf("load creator agent: %v", err)
	}
	title := "Radar accepts workspace agent " + uuid.NewString()
	cleanupRadarIssueTitle(t, title)

	result, err := executeRadarCreateIssueForTest(t, creator, radarIssuePayload{
		Title:              title,
		Description:        "Keep this issue unassigned until a capable owner is selected",
		AcceptanceCriteria: []string{"a capable owner is explicitly selected"},
	})
	if err != nil {
		t.Fatalf("create issue for workspace agent: %v", err)
	}
	issueID, ok := result["issue_id"].(string)
	if !ok || issueID == "" {
		t.Fatalf("issue_id = %#v, want non-empty string", result["issue_id"])
	}
	issue, err := testHandler.Queries.GetIssueInWorkspace(context.Background(), db.GetIssueInWorkspaceParams{
		ID:          parseUUID(issueID),
		WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("load created issue: %v", err)
	}
	if issue.AssigneeType.Valid || issue.AssigneeID.Valid {
		t.Fatalf("assignee = %q/%s, want unassigned", issue.AssigneeType.String, uuidToString(issue.AssigneeID))
	}
}

func TestExecuteRadarCreateIssueAcceptsMemberInRunWorkspace(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	creatorID := createHandlerTestAgent(t, "Radar valid member creator "+uuid.NewString(), nil)
	creator, err := testHandler.Queries.GetAgent(context.Background(), parseUUID(creatorID))
	if err != nil {
		t.Fatalf("load creator agent: %v", err)
	}
	title := "Radar accepts workspace member " + uuid.NewString()
	cleanupRadarIssueTitle(t, title)

	result, err := executeRadarCreateIssueForTest(t, creator, radarIssuePayload{
		Title:              title,
		Description:        "Deliver the member-owned requirement",
		AcceptanceCriteria: []string{"the member verifies the delivered result"},
		AssigneeType:       "member",
		AssigneeID:         testUserID,
	})
	if err != nil {
		t.Fatalf("create issue for workspace member: %v", err)
	}
	issueID, ok := result["issue_id"].(string)
	if !ok || issueID == "" {
		t.Fatalf("issue_id = %#v, want non-empty string", result["issue_id"])
	}
	issue, err := testHandler.Queries.GetIssueInWorkspace(context.Background(), db.GetIssueInWorkspaceParams{
		ID:          parseUUID(issueID),
		WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("load created issue: %v", err)
	}
	if !issue.AssigneeType.Valid || issue.AssigneeType.String != "member" || uuidToString(issue.AssigneeID) != testUserID {
		t.Fatalf("assignee = %q/%s, want member/%s", issue.AssigneeType.String, uuidToString(issue.AssigneeID), testUserID)
	}
}

func TestExecuteRadarCreateIssueAmbientRejectsNonAgentAssignee(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	creatorID := createHandlerTestAgent(t, "Radar ambient assignee guard "+uuid.NewString(), nil)
	creator, err := testHandler.Queries.GetAgent(context.Background(), parseUUID(creatorID))
	if err != nil {
		t.Fatalf("load creator agent: %v", err)
	}
	projectID := createRadarProjectForTest(t, "Radar ambient assignee project "+uuid.NewString())
	run := enqueueProjectScopedRadarRunForTest(t, creator, projectID)
	title := "Radar ambient member assignment " + uuid.NewString()
	cleanupRadarIssueTitle(t, title)
	_, err = executeRadarCreateIssueForRunTest(t, run, creator, radarIssuePayload{
		Title:              title,
		Description:        "Ambient coordination must choose a qualified delivery agent",
		AcceptanceCriteria: []string{"a qualified channel agent owns implementation"},
		AssigneeType:       "member",
		AssigneeID:         testUserID,
	})
	if err == nil || err.Error() != "ambient issue creation may only assign a qualified channel agent" {
		t.Fatalf("ambient member assignment error = %v", err)
	}
}

func TestExecuteRadarCreateIssueRejectsCreatorFromAnotherWorkspace(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	foreign := createRadarIssueForeignWorkspaceFixture(t)
	creator, err := testHandler.Queries.GetAgent(context.Background(), parseUUID(foreign.agentID))
	if err != nil {
		t.Fatalf("load foreign creator agent: %v", err)
	}
	title := "Radar must reject foreign creator " + uuid.NewString()
	cleanupRadarIssueTitle(t, title)

	_, err = executeRadarCreateIssueForRunTest(t, db.AgentRadarRun{
		WorkspaceID: parseUUID(testWorkspaceID),
	}, creator, radarIssuePayload{
		Title:              title,
		Description:        "Reject a creator from another workspace",
		AcceptanceCriteria: []string{"the creator belongs to the run workspace"},
		AssigneeType:       "member",
		AssigneeID:         testUserID,
	})
	if err == nil {
		t.Fatal("expected creator from another workspace to be rejected")
	}
	if got, want := err.Error(), "radar agent does not belong to the run workspace"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestExecuteRadarCreateIssueRejectsCreatorThatDoesNotMatchRunAgent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	creatorID := createHandlerTestAgent(t, "Radar run creator "+uuid.NewString(), nil)
	creator, err := testHandler.Queries.GetAgent(context.Background(), parseUUID(creatorID))
	if err != nil {
		t.Fatalf("load creator agent: %v", err)
	}
	otherAgentID := createHandlerTestAgent(t, "Radar mismatched run agent "+uuid.NewString(), nil)
	title := "Radar must reject mismatched run agent " + uuid.NewString()
	cleanupRadarIssueTitle(t, title)

	_, err = executeRadarCreateIssueForRunTest(t, db.AgentRadarRun{
		WorkspaceID: parseUUID(testWorkspaceID),
		AgentID:     parseUUID(otherAgentID),
	}, creator, radarIssuePayload{
		Title:              title,
		Description:        "Reject a creator that differs from the run agent",
		AcceptanceCriteria: []string{"the creator matches the run agent"},
		AssigneeType:       "member",
		AssigneeID:         testUserID,
	})
	if err == nil {
		t.Fatal("expected creator that does not match run.agent_id to be rejected")
	}
	if got, want := err.Error(), "radar agent does not match the run"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}
