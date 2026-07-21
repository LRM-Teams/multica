package handler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/radar"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
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

	result, err := executeRadarCreateIssueForRunTest(t, run, creator, radarIssuePayload{Title: title})
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
		Title:     title,
		ProjectID: otherProjectID,
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
		Title:        title,
		AssigneeType: "agent",
		AssigneeID:   foreign.agentID,
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
		Title:        title,
		AssigneeType: "member",
		AssigneeID:   foreign.userID,
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
		Title:     title,
		ProjectID: foreign.projectID,
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
		Title:        title,
		AssigneeType: "user",
		AssigneeID:   testUserID,
	})
	if err == nil {
		t.Fatal("expected unsupported assignee type to be rejected")
	}
	if got, want := err.Error(), "assignee_type must be 'member', 'agent', or 'squad'"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestExecuteRadarCreateIssueAcceptsDefaultAgentInRunWorkspace(t *testing.T) {
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
		Title: title,
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
	if !issue.AssigneeType.Valid || issue.AssigneeType.String != "agent" || uuidToString(issue.AssigneeID) != creatorID {
		t.Fatalf("assignee = %q/%s, want agent/%s", issue.AssigneeType.String, uuidToString(issue.AssigneeID), creatorID)
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
		Title:        title,
		AssigneeType: "member",
		AssigneeID:   testUserID,
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
		Title:        title,
		AssigneeType: "member",
		AssigneeID:   testUserID,
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
		Title:        title,
		AssigneeType: "member",
		AssigneeID:   testUserID,
	})
	if err == nil {
		t.Fatal("expected creator that does not match run.agent_id to be rejected")
	}
	if got, want := err.Error(), "radar agent does not match the run"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}
