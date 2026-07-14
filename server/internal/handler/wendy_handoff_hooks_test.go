package handler

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/workgraph"
)

func TestWendyHandoffHookUnlocksAfterProductionIssueUpdates(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	fixture := seedWendyHandoffHookFixture(t)
	updateIssueStatusForWendyHook(t, fixture.issueA.ID.String(), "done")
	fixture.setPrimaryChannel(t, fixture.issueC)
	updateIssueStatusForWendyHook(t, fixture.issueB.ID.String(), "done")

	assertWendyUnlockHandoffChannel(t, fixture.channelID)
	fixture.makeUnlockDue(t)
	if got := dispatchWendyUnlockHandoffsForTest(t); got != 1 {
		t.Fatalf("dispatched handoffs = %d, want 1", got)
	}
	assertWendyUnlockMessageCount(t, fixture.channelID, fixture.wendyID, 1)
	assertWendyUnlockMessageMentions(t, fixture.channelID, fixture.cID)
}

func TestWendyCreateIssueEnqueuesStartWorkHandoff(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	supervisor := createRadarSupervisorForExecutorTest(t)
	target := createHandlerTestAgent(t, "Wendy Create Target "+uuid.NewString(), nil)
	channelID := seedChannelForTest(t, "wendy-create-"+uuid.NewString(), testUserID)
	addRadarAgentMembersForExecutorTest(t, channelID, supervisor.ID.String(), target)
	bindWendySupervisorForHandoffTest(t, supervisor.ID.String())

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues", map[string]any{
		"title":         "Continue development",
		"status":        "todo",
		"assignee_type": "agent",
		"assignee_id":   target,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != 201 {
		t.Fatalf("create issue: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var count int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM pending_handoff
		WHERE workspace_id = $1
		  AND reason_code = 'start_work'
		  AND status = 'pending'
		  AND target_actor_id = $2
	`, testWorkspaceID, target).Scan(&count); err != nil {
		t.Fatalf("count start_work handoffs: %v", err)
	}
	if count != 1 {
		t.Fatalf("start_work handoffs = %d, want 1", count)
	}

	var nodeCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM work_node
		WHERE workspace_id = $1 AND kind = 'issue' AND title = 'Continue development'
	`, testWorkspaceID).Scan(&nodeCount); err != nil {
		t.Fatalf("count work nodes: %v", err)
	}
	if nodeCount != 1 {
		t.Fatalf("work nodes = %d, want 1", nodeCount)
	}
}

func TestWendyGroupMessageTouchesAmbientWatch(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	supervisor := createRadarSupervisorForExecutorTest(t)
	channelID := seedChannelForTest(t, "wendy-ambient-"+uuid.NewString(), testUserID)
	addRadarAgentMembersForExecutorTest(t, channelID, supervisor.ID.String())
	bindWendySupervisorForHandoffTest(t, supervisor.ID.String())

	prev := workgraph.AmbientDebounce
	workgraph.AmbientDebounce = time.Minute
	t.Cleanup(func() { workgraph.AmbientDebounce = prev })

	testHandler.ingestWendyHumanGroupMessage(context.Background(), ChannelResponse{
		ID:          channelID,
		WorkspaceID: testWorkspaceID,
		Kind:        "group",
		Name:        "ambient",
	}, ChannelMessageResponse{
		Content: "我们继续讨论一下进度",
	})

	var dirty bool
	var status string
	err := testPool.QueryRow(context.Background(), `
		SELECT dirty, status
		FROM wendy_channel_ambient
		WHERE channel_id = $1 AND wendy_agent_id = $2
	`, channelID, supervisor.ID).Scan(&dirty, &status)
	if err != nil {
		t.Fatalf("load ambient watch: %v", err)
	}
	if !dirty || status != "idle" {
		t.Fatalf("ambient watch dirty=%v status=%q, want dirty idle", dirty, status)
	}
}

func TestWendyHandoffHookUnlocksAfterBatchIssueUpdate(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	fixture := seedWendyHandoffHookFixture(t)
	updateIssueStatusForWendyHook(t, fixture.issueA.ID.String(), "done")
	fixture.setPrimaryChannel(t, fixture.issueC)
	batchUpdateIssueStatusForWendyHook(t, fixture.issueB.ID.String(), "done")

	assertWendyUnlockHandoffChannel(t, fixture.channelID)
}

func seedWendyHandoffHookFixture(t *testing.T) wendyUnlockDispatchFixture {
	t.Helper()
	ctx := context.Background()
	supervisor := createRadarSupervisorForExecutorTest(t)
	target := createHandlerTestAgent(t, "Wendy Hook Target "+uuid.NewString(), nil)
	channelID := seedChannelForTest(t, "wendy-hook-"+uuid.NewString(), testUserID)
	addRadarAgentMembersForExecutorTest(t, channelID, supervisor.ID.String(), target)

	bindWendySupervisorForHandoffTest(t, supervisor.ID.String())

	fixture := wendyUnlockDispatchFixture{
		wendyID:   supervisor.ID.String(),
		cID:       target,
		channelID: channelID,
	}
	fixture.issueA = fixture.createIssue(t, "Hook prerequisite A", target)
	fixture.issueB = fixture.createIssue(t, "Hook prerequisite B", target)
	fixture.issueC = fixture.createIssue(t, "Hook waiting C", target)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO issue_dependency (issue_id, depends_on_issue_id, type)
		VALUES ($1, $2, 'blocked_by'), ($1, $3, 'blocked_by')
	`, fixture.issueC.ID, fixture.issueA.ID, fixture.issueB.ID); err != nil {
		t.Fatalf("create C dependencies: %v", err)
	}
	return fixture
}

func updateIssueStatusForWendyHook(t *testing.T, issueID, status string) {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/issues/"+issueID, map[string]any{"status": status})
	req = withURLParam(req, "id", issueID)
	testHandler.UpdateIssue(w, req)
	if w.Code != 200 {
		t.Fatalf("update issue status=%q: expected 200, got %d: %s", status, w.Code, w.Body.String())
	}
}

func batchUpdateIssueStatusForWendyHook(t *testing.T, issueID, status string) {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/batch-update", map[string]any{
		"issue_ids": []string{issueID},
		"updates":   map[string]any{"status": status},
	})
	testHandler.BatchUpdateIssues(w, req)
	if w.Code != 200 {
		t.Fatalf("batch update issue status=%q: expected 200, got %d: %s", status, w.Code, w.Body.String())
	}
}
