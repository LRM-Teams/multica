package handler

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
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
