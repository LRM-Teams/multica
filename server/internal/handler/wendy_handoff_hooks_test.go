package handler

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
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
	bindChannelGroupManagerForTest(t, channelID, supervisor.ID.String())

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

func TestWendyGroupMessageTouchesAmbientForPersonalWendyWithoutSupervisor(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	personalWendy := createHandlerTestAgent(t, "Wendy", nil)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent SET display_name = 'Wendy' WHERE id = $1
	`, personalWendy); err != nil {
		t.Fatalf("set Wendy display name: %v", err)
	}
	channelID := seedChannelForTest(t, "wendy-personal-ambient-"+uuid.NewString(), testUserID)
	addRadarAgentMembersForExecutorTest(t, channelID, personalWendy)
	bindChannelGroupManagerForTest(t, channelID, personalWendy)

	// Ensure workspace supervisor is someone else not in this channel.
	otherSupervisor := createRadarSupervisorForExecutorTest(t)
	bindWendySupervisorForHandoffTest(t, otherSupervisor.ID.String())

	prev := workgraph.AmbientDebounce
	workgraph.AmbientDebounce = time.Minute
	t.Cleanup(func() { workgraph.AmbientDebounce = prev })

	testHandler.ingestWendyHumanGroupMessage(context.Background(), ChannelResponse{
		ID:          channelID,
		WorkspaceID: testWorkspaceID,
		Kind:        "group",
		Name:        "personal-ambient",
	}, ChannelMessageResponse{
		Content: "群里随便聊两句",
	})

	var wendyAgentID string
	err := testPool.QueryRow(context.Background(), `
		SELECT wendy_agent_id::text
		FROM wendy_channel_ambient
		WHERE channel_id = $1 AND dirty = TRUE
	`, channelID).Scan(&wendyAgentID)
	if err != nil {
		t.Fatalf("load personal Wendy ambient watch: %v", err)
	}
	if wendyAgentID != personalWendy {
		t.Fatalf("ambient wendy_agent_id = %s, want personal Wendy %s", wendyAgentID, personalWendy)
	}
}

func TestWendyAgentGroupMessageTouchesAmbientWatch(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	supervisor := createRadarSupervisorForExecutorTest(t)
	worker := createHandlerTestAgent(t, "Ambient Worker "+uuid.NewString(), nil)
	channelID := seedChannelForTest(t, "wendy-agent-ambient-"+uuid.NewString(), testUserID)
	addRadarAgentMembersForExecutorTest(t, channelID, supervisor.ID.String(), worker)
	bindWendySupervisorForHandoffTest(t, supervisor.ID.String())
	bindChannelGroupManagerForTest(t, channelID, supervisor.ID.String())

	prev := workgraph.AmbientDebounce
	workgraph.AmbientDebounce = time.Minute
	t.Cleanup(func() { workgraph.AmbientDebounce = prev })

	ch := ChannelResponse{
		ID:          channelID,
		WorkspaceID: testWorkspaceID,
		Kind:        "group",
		Name:        "agent-ambient",
	}
	testHandler.ingestWendyAgentGroupMessage(context.Background(), ch, ChannelMessageResponse{
		Content: "我这边做完了，下一棒可以开始",
	}, parseUUID(worker))

	var dirty bool
	var status string
	err := testPool.QueryRow(context.Background(), `
		SELECT dirty, status
		FROM wendy_channel_ambient
		WHERE channel_id = $1 AND wendy_agent_id = $2
	`, channelID, supervisor.ID).Scan(&dirty, &status)
	if err != nil {
		t.Fatalf("load ambient watch after agent message: %v", err)
	}
	if !dirty || status != "idle" {
		t.Fatalf("ambient watch dirty=%v status=%q, want dirty idle", dirty, status)
	}
}

func TestWendyOwnGroupMessageDoesNotTouchAmbientWatch(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	supervisor := createRadarSupervisorForExecutorTest(t)
	channelID := seedChannelForTest(t, "wendy-self-ambient-"+uuid.NewString(), testUserID)
	addRadarAgentMembersForExecutorTest(t, channelID, supervisor.ID.String())
	bindWendySupervisorForHandoffTest(t, supervisor.ID.String())
	bindChannelGroupManagerForTest(t, channelID, supervisor.ID.String())

	prev := workgraph.AmbientDebounce
	workgraph.AmbientDebounce = time.Minute
	t.Cleanup(func() { workgraph.AmbientDebounce = prev })

	ch := ChannelResponse{
		ID:          channelID,
		WorkspaceID: testWorkspaceID,
		Kind:        "group",
		Name:        "self-ambient",
	}
	testHandler.ingestWendyHumanGroupMessage(context.Background(), ch, ChannelMessageResponse{
		Content: "先聊一句把 watch 建起来",
	})

	var beforeNotBefore time.Time
	var beforeMessageAt time.Time
	if err := testPool.QueryRow(context.Background(), `
		SELECT review_not_before, last_human_message_at
		FROM wendy_channel_ambient
		WHERE channel_id = $1
	`, channelID).Scan(&beforeNotBefore, &beforeMessageAt); err != nil {
		t.Fatalf("load ambient before Wendy reply: %v", err)
	}

	testHandler.ingestWendyAgentGroupMessage(context.Background(), ch, ChannelMessageResponse{
		Content: "@someone 请继续下一棒",
		CreatedAt: time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339Nano),
	}, supervisor.ID)

	var afterNotBefore time.Time
	var afterMessageAt time.Time
	if err := testPool.QueryRow(context.Background(), `
		SELECT review_not_before, last_human_message_at
		FROM wendy_channel_ambient
		WHERE channel_id = $1
	`, channelID).Scan(&afterNotBefore, &afterMessageAt); err != nil {
		t.Fatalf("load ambient after Wendy reply: %v", err)
	}
	if !afterNotBefore.Equal(beforeNotBefore) || !afterMessageAt.Equal(beforeMessageAt) {
		t.Fatalf("Wendy self-message re-armed ambient: before=(%v,%v) after=(%v,%v)",
			beforeNotBefore, beforeMessageAt, afterNotBefore, afterMessageAt)
	}
}

func TestWendyAmbientDispatchEnqueuesEventRadarRun(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	supervisor := createRadarSupervisorForExecutorTest(t)
	channelID := seedChannelForTest(t, "wendy-ambient-dispatch-"+uuid.NewString(), testUserID)
	addRadarAgentMembersForExecutorTest(t, channelID, supervisor.ID.String())
	bindWendySupervisorForHandoffTest(t, supervisor.ID.String())
	bindChannelGroupManagerForTest(t, channelID, supervisor.ID.String())

	prev := workgraph.AmbientDebounce
	workgraph.AmbientDebounce = time.Second
	t.Cleanup(func() { workgraph.AmbientDebounce = prev })

	ch := ChannelResponse{
		ID:          channelID,
		WorkspaceID: testWorkspaceID,
		Kind:        "group",
		Name:        "ambient-dispatch",
	}
	testHandler.ingestWendyHumanGroupMessage(context.Background(), ch, ChannelMessageResponse{
		Content:   "enough chatter to need a review",
		CreatedAt: time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339Nano),
	})
	if _, err := testPool.Exec(context.Background(), `
		UPDATE wendy_channel_ambient
		SET review_not_before = now() - interval '1 minute',
		    dirty = TRUE,
		    status = 'idle',
		    claim_token = NULL,
		    claimed_at = NULL
		WHERE channel_id = $1
	`, channelID); err != nil {
		t.Fatalf("make ambient due: %v", err)
	}

	enqueued, err := testHandler.DispatchDueWendyAmbientReviews(context.Background(), 5)
	if err != nil {
		t.Fatalf("dispatch ambient: %v", err)
	}
	if enqueued != 1 {
		t.Fatalf("enqueued ambient reviews = %d, want 1", enqueued)
	}

	var runID pgtype.UUID
	var triggerKind, cooldownKey, status string
	err = testPool.QueryRow(context.Background(), `
		SELECT id, trigger_kind, cooldown_key, status
		FROM agent_radar_run
		WHERE workspace_id = $1
		  AND agent_id = $2
		  AND cooldown_key = $3
		ORDER BY created_at DESC
		LIMIT 1
	`, testWorkspaceID, supervisor.ID, "wendy_ambient:"+channelID).Scan(&runID, &triggerKind, &cooldownKey, &status)
	if err != nil {
		t.Fatalf("load ambient radar run: %v", err)
	}
	if triggerKind != "event" || status != "queued" {
		t.Fatalf("ambient radar run trigger=%q status=%q, want event queued", triggerKind, status)
	}

	// #2: the review is in flight after enqueue — dirty stays set and the row is
	// 'running' so it is not re-claimed and is not lost if the run fails.
	var dirty bool
	var ambientStatus string
	if err := testPool.QueryRow(context.Background(), `
		SELECT dirty, status FROM wendy_channel_ambient WHERE channel_id = $1
	`, channelID).Scan(&dirty, &ambientStatus); err != nil {
		t.Fatalf("load ambient after dispatch: %v", err)
	}
	if !dirty || ambientStatus != "running" {
		t.Fatalf("ambient after dispatch dirty=%v status=%q, want dirty running", dirty, ambientStatus)
	}

	// A successful run completion settles the review: dirty cleared, back to idle.
	if err := testHandler.WorkGraph.ReconcileChannelAmbientRun(context.Background(), runID, true); err != nil {
		t.Fatalf("reconcile ambient run: %v", err)
	}
	if err := testPool.QueryRow(context.Background(), `
		SELECT dirty, status FROM wendy_channel_ambient WHERE channel_id = $1
	`, channelID).Scan(&dirty, &ambientStatus); err != nil {
		t.Fatalf("load ambient after reconcile: %v", err)
	}
	if dirty || ambientStatus != "idle" {
		t.Fatalf("ambient after reconcile dirty=%v status=%q, want clean idle", dirty, ambientStatus)
	}

	var authorized bool
	if err := testPool.QueryRow(context.Background(), `
		SELECT workspace_radar_task_is_authorized(rr.task_id)
		FROM agent_radar_run rr
		WHERE rr.cooldown_key = $1
		ORDER BY rr.created_at DESC
		LIMIT 1
	`, "wendy_ambient:"+channelID).Scan(&authorized); err != nil {
		t.Fatalf("check ambient claim auth: %v", err)
	}
	if !authorized {
		t.Fatal("ambient radar task must be claim-authorized via workspace_radar_task_is_authorized")
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
	bindChannelGroupManagerForTest(t, channelID, supervisor.ID.String())

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
