package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// CancelTaskByUser (POST /api/tasks/{taskId}/cancel) used to key cancellation
// off issue_id / chat_session_id alone, which 404'd every task whose only
// source link was autopilot_run_id or quick_create context (MUL-2827). These
// tests pin the new behavior: tenancy flows through the task's owning agent,
// with chat-creator privacy and the private-agent visibility gate layered on.

// taskStatus reads a task's current status straight from the DB so reject
// paths can assert "no side effect before the access check".
func taskStatus(t *testing.T, taskID string) string {
	t.Helper()
	var status string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM agent_task_queue WHERE id = $1`, taskID,
	).Scan(&status); err != nil {
		t.Fatalf("read task status: %v", err)
	}
	return status
}

// createAutopilotRunOnlyTask seeds the autopilot -> autopilot_run -> task chain
// that AutopilotService.dispatchRunOnly produces: a queued task with issue_id
// and chat_session_id NULL, linked only by autopilot_run_id. The autopilot is
// created in the agent's own workspace so the fixture works for foreign agents
// too.
func createAutopilotRunOnlyTask(t *testing.T, agentID string) string {
	t.Helper()
	ctx := context.Background()

	var workspaceID, runtimeID string
	if err := testPool.QueryRow(ctx,
		`SELECT workspace_id, runtime_id FROM agent WHERE id = $1`, agentID,
	).Scan(&workspaceID, &runtimeID); err != nil {
		t.Fatalf("load agent workspace/runtime: %v", err)
	}

	var autopilotID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO autopilot (workspace_id, title, assignee_id, execution_mode, created_by_type, created_by_id)
		VALUES ($1, 'cancel-runonly-ap', $2, 'run_only', 'member', $3)
		RETURNING id
	`, workspaceID, agentID, testUserID).Scan(&autopilotID); err != nil {
		t.Fatalf("create autopilot: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM autopilot WHERE id = $1`, autopilotID) })

	var runID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO autopilot_run (autopilot_id, source, status)
		VALUES ($1, 'manual', 'running')
		RETURNING id
	`, autopilotID).Scan(&runID); err != nil {
		t.Fatalf("create autopilot_run: %v", err)
	}

	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, autopilot_run_id)
		VALUES ($1, $2, 'queued', 0, $3)
		RETURNING id
	`, agentID, runtimeID, runID).Scan(&taskID); err != nil {
		t.Fatalf("create run_only task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })
	return taskID
}

// createForeignWorkspaceAgent stands up an isolated workspace + runtime + agent
// and returns the agent ID, for cross-tenant cancel tests.
func createForeignWorkspaceAgent(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	var workspaceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ('Foreign Cancel WS', 'foreign-cancel-ws', 'cross-tenant cancel test', 'FCW')
		RETURNING id
	`).Scan(&workspaceID); err != nil {
		t.Fatalf("create foreign workspace: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID) })

	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at)
		VALUES ($1, NULL, 'Foreign Cancel Runtime', 'cloud', 'foreign_runtime', 'online', 'Foreign runtime', '{}'::jsonb, now())
		RETURNING id
	`, workspaceID).Scan(&runtimeID); err != nil {
		t.Fatalf("create foreign runtime: %v", err)
	}

	var agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, description, runtime_mode, runtime_config, runtime_id, visibility, max_concurrent_tasks)
		VALUES ($1, 'Foreign Cancel Agent', '', 'cloud', '{}'::jsonb, $2, 'workspace', 1)
		RETURNING id
	`, workspaceID, runtimeID).Scan(&agentID); err != nil {
		t.Fatalf("create foreign agent: %v", err)
	}
	return agentID
}

// createWorkspaceMemberUser adds a plain (non-owner/admin) member to the test
// workspace and returns the user ID. The member row cascades when the user is
// deleted (member.user_id ON DELETE CASCADE).
func createWorkspaceMemberUser(t *testing.T, name, email string) string {
	t.Helper()
	ctx := context.Background()

	var userID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`, name, email,
	).Scan(&userID); err != nil {
		t.Fatalf("create user %s: %v", email, err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID) })

	if _, err := testPool.Exec(ctx,
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')`, testWorkspaceID, userID,
	); err != nil {
		t.Fatalf("add member %s: %v", email, err)
	}
	return userID
}

func cancelTaskByUserRequest(t *testing.T, userID, taskID string) *http.Request {
	t.Helper()
	req := newRequestAs(userID, "POST", "/api/tasks/"+taskID+"/cancel", nil)
	req = withURLParam(req, "taskId", taskID)
	return withChatTestWorkspaceCtx(t, req)
}

func TestCancelTaskByUser_AgentInboxEvent_ChannelBound_ReturnsConflict(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "CancelInboxEventAgent", []byte("[]"))
	channelID := seedChannelForTest(t, "cancel-inbox-event-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	root := dispatchThreadMentionForTest(t, channelID, agentID, "cancel-inbox-event-"+uuid.NewString())
	eventID := latestChannelAgentInboxEventForRootForTest(t, root.ID, agentID)

	w := httptest.NewRecorder()
	testHandler.CancelTaskByUser(w, cancelTaskByUserRequest(t, testUserID, eventID))
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for channel inbox via /api/tasks cancel, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "agent-inbox") {
		t.Fatalf("expected explicit inbox cancel contract error, got %s", w.Body.String())
	}

	var eventStatus, terminalOutcome string
	if err := testPool.QueryRow(ctx, `
		SELECT status, COALESCE(terminal_outcome, '')
		FROM agent_inbox_event
		WHERE id = $1`, eventID).Scan(&eventStatus, &terminalOutcome); err != nil {
		t.Fatalf("load inbox event: %v", err)
	}
	if terminalOutcome != "" || eventStatus == "suppressed" {
		t.Fatalf("channel inbox event was mutated via deprecated path: status=%q outcome=%q", eventStatus, terminalOutcome)
	}
}

func TestCancelChannelAgentInboxEvent_CancelsActiveWake(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "CancelChannelInboxAgent", []byte("[]"))
	channelID := seedChannelForTest(t, "cancel-channel-inbox-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	root := dispatchThreadMentionForTest(t, channelID, agentID, "cancel-channel-inbox-"+uuid.NewString())
	eventID := latestChannelAgentInboxEventForRootForTest(t, root.ID, agentID)

	var deliveryID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_event_delivery (
			workspace_id,
			agent_session_id,
			inbox_event_id,
			runtime_id,
			status
		)
		SELECT workspace_id,
		       agent_session_id,
		       id,
		       $2,
		       'leased'
		FROM agent_inbox_event
		WHERE id = $1
		RETURNING id`, eventID, handlerTestRuntimeID(t)).Scan(&deliveryID); err != nil {
		t.Fatalf("seed inbox delivery: %v", err)
	}

	req := newRequestAs(testUserID, http.MethodPost, "/api/channels/"+channelID+"/agent-inbox/events/"+eventID+"/cancel", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParams(req, "channelId", channelID, "eventId", eventID)
	w := httptest.NewRecorder()
	testHandler.CancelChannelAgentInboxEvent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp ChannelAgentInboxCancelResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode cancel response: %v", err)
	}
	if resp.InboxEventID != eventID || resp.Status != "cancelled" || resp.Kind != "chat" || resp.CompletedAt == nil {
		t.Fatalf("cancel inbox response = %+v, want cancelled chat inbox event", resp)
	}

	var eventStatus, terminalOutcome, terminalDeliveryID string
	var retryable bool
	if err := testPool.QueryRow(ctx, `
		SELECT status,
		       COALESCE(terminal_outcome, ''),
		       COALESCE(terminal_delivery_id::text, ''),
		       retryable
		FROM agent_inbox_event
		WHERE id = $1`, eventID).Scan(&eventStatus, &terminalOutcome, &terminalDeliveryID, &retryable); err != nil {
		t.Fatalf("load cancelled inbox event: %v", err)
	}
	if eventStatus != "suppressed" || terminalOutcome != "no_reply" || terminalDeliveryID != deliveryID || retryable {
		t.Fatalf("cancelled inbox event status=%q outcome=%q delivery=%q retryable=%v", eventStatus, terminalOutcome, terminalDeliveryID, retryable)
	}
	var deliveryStatus string
	if err := testPool.QueryRow(ctx, `SELECT status FROM agent_event_delivery WHERE id = $1`, deliveryID).Scan(&deliveryStatus); err != nil {
		t.Fatalf("load cancelled delivery: %v", err)
	}
	if deliveryStatus != "failed" {
		t.Fatalf("delivery status = %q, want failed", deliveryStatus)
	}

	activeReq := withURLParam(newRequest(http.MethodGet, "/api/channels/"+channelID+"/active-tasks", nil), "channelId", channelID)
	activeReq = withChannelTestWorkspaceCtx(t, activeReq, testUserID)
	activeRec := httptest.NewRecorder()
	testHandler.ListChannelActiveTasks(activeRec, activeReq)
	if activeRec.Code != http.StatusOK {
		t.Fatalf("list active tasks after cancel: status=%d body=%s", activeRec.Code, activeRec.Body.String())
	}
	var activeResp ChannelActiveTasksResponse
	if err := json.NewDecoder(activeRec.Body).Decode(&activeResp); err != nil {
		t.Fatalf("decode active tasks after cancel: %v", err)
	}
	for _, task := range activeResp.Tasks {
		if task.TaskID == eventID {
			t.Fatalf("cancelled inbox event still appears in active strip: %+v", activeResp.Tasks)
		}
	}
}

func TestCancelChannelActiveAgentInboxEvents_BulkStopsAllActive(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	channelID := seedChannelForTest(t, "cancel-active-bulk-"+uuid.NewString(), testUserID)
	agentA := createHandlerTestAgent(t, "CancelActiveAgentA", []byte("[]"))
	agentB := createHandlerTestAgent(t, "CancelActiveAgentB", []byte("[]"))
	for _, agentID := range []string{agentA, agentB} {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
			VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
			t.Fatalf("seed agent member: %v", err)
		}
	}

	rootA := dispatchThreadMentionForTest(t, channelID, agentA, "cancel-active-a-"+uuid.NewString())
	rootB := dispatchThreadMentionForTest(t, channelID, agentB, "cancel-active-b-"+uuid.NewString())
	eventA := latestChannelAgentInboxEventForRootForTest(t, rootA.ID, agentA)
	eventB := latestChannelAgentInboxEventForRootForTest(t, rootB.ID, agentB)

	req := withURLParam(newRequestAs(testUserID, http.MethodPost, "/api/channels/"+channelID+"/agent-inbox/cancel-active", nil), "channelId", channelID)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	w := httptest.NewRecorder()
	testHandler.CancelChannelActiveAgentInboxEvents(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp ChannelAgentInboxCancelActiveResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode cancel-active response: %v", err)
	}
	if resp.CancelledCount != 2 || len(resp.Cancelled) != 2 {
		t.Fatalf("cancel-active response = %+v, want 2 cancelled", resp)
	}
	got := map[string]bool{}
	for _, item := range resp.Cancelled {
		got[item.InboxEventID] = true
		if item.Status != "cancelled" {
			t.Fatalf("item status = %q, want cancelled", item.Status)
		}
	}
	if !got[eventA] || !got[eventB] {
		t.Fatalf("cancelled ids = %v, want %s and %s", got, eventA, eventB)
	}

	for _, eventID := range []string{eventA, eventB} {
		var terminalOutcome string
		if err := testPool.QueryRow(ctx, `
			SELECT COALESCE(terminal_outcome, '')
			FROM agent_inbox_event WHERE id = $1`, eventID).Scan(&terminalOutcome); err != nil {
			t.Fatalf("load event %s: %v", eventID, err)
		}
		if terminalOutcome != "no_reply" {
			t.Fatalf("event %s outcome=%q, want no_reply", eventID, terminalOutcome)
		}
	}

	// Idempotent second Stop All: nothing left to cancel.
	w2 := httptest.NewRecorder()
	testHandler.CancelChannelActiveAgentInboxEvents(w2, req)
	if w2.Code != http.StatusOK {
		t.Fatalf("second cancel-active expected 200, got %d: %s", w2.Code, w2.Body.String())
	}
	var resp2 ChannelAgentInboxCancelActiveResponse
	if err := json.Unmarshal(w2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("decode second cancel-active: %v", err)
	}
	if resp2.CancelledCount != 0 || len(resp2.Cancelled) != 0 {
		t.Fatalf("second cancel-active = %+v, want empty", resp2)
	}
}

// TestCancelTaskByUser_RunOnlyAutopilot_Succeeds is the core MUL-2827 fix: a
// run_only autopilot task (issue_id + chat_session_id NULL, only
// autopilot_run_id set) is cancellable by a member of its agent's workspace.
func TestCancelTaskByUser_RunOnlyAutopilot_Succeeds(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "CancelRunOnlyAgent", []byte("[]"))
	taskID := createAutopilotRunOnlyTask(t, agentID)

	w := httptest.NewRecorder()
	testHandler.CancelTaskByUser(w, cancelTaskByUserRequest(t, testUserID, taskID))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := taskStatus(t, taskID); got != "cancelled" {
		t.Fatalf("task not cancelled: status = %q", got)
	}
}

// TestCancelTaskByUser_RunOnlyAutopilot_CrossWorkspace_Returns404 verifies the
// tenant guard: a member of workspace A cannot cancel a run_only task whose
// agent lives in workspace B, and the task is not mutated before the check.
func TestCancelTaskByUser_RunOnlyAutopilot_CrossWorkspace_Returns404(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	foreignAgentID := createForeignWorkspaceAgent(t)
	taskID := createAutopilotRunOnlyTask(t, foreignAgentID)

	w := httptest.NewRecorder()
	testHandler.CancelTaskByUser(w, cancelTaskByUserRequest(t, testUserID, taskID))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
	if got := taskStatus(t, taskID); got != "queued" {
		t.Fatalf("foreign task was mutated: status = %q", got)
	}
}

// TestCancelTaskByUser_QuickCreate_Succeeds verifies a quick_create task — no
// issue yet, no chat session, only context JSONB — is cancellable during its
// active window (the pre-issue-creation phase, i.e. whenever the user clicks X).
func TestCancelTaskByUser_QuickCreate_Succeeds(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "CancelQuickCreateAgent", []byte("[]"))

	var taskID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, issue_id, context)
		VALUES ($1, (SELECT runtime_id FROM agent WHERE id = $1), 'running', 0, NULL,
		        '{"type":"quick_create","workspace_id":"ws","prompt":"do a thing"}'::jsonb)
		RETURNING id
	`, agentID).Scan(&taskID); err != nil {
		t.Fatalf("create quick_create task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	w := httptest.NewRecorder()
	testHandler.CancelTaskByUser(w, cancelTaskByUserRequest(t, testUserID, taskID))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := taskStatus(t, taskID); got != "cancelled" {
		t.Fatalf("task not cancelled: status = %q", got)
	}
}

// TestCancelTaskByUser_RetryClone_Autopilot_Succeeds verifies a retry clone of
// an autopilot task — which copies parent_task_id + autopilot_run_id verbatim,
// inheriting the NULL issue/chat links — is still cancellable.
func TestCancelTaskByUser_RetryClone_Autopilot_Succeeds(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "CancelRetryCloneAgent", []byte("[]"))
	parentID := createAutopilotRunOnlyTask(t, agentID)

	var cloneID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, autopilot_run_id, parent_task_id, attempt)
		SELECT agent_id, runtime_id, 'queued', priority, autopilot_run_id, id, 1
		FROM agent_task_queue WHERE id = $1
		RETURNING id
	`, parentID).Scan(&cloneID); err != nil {
		t.Fatalf("create retry clone: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, cloneID) })

	w := httptest.NewRecorder()
	testHandler.CancelTaskByUser(w, cancelTaskByUserRequest(t, testUserID, cloneID))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := taskStatus(t, cloneID); got != "cancelled" {
		t.Fatalf("clone not cancelled: status = %q", got)
	}
}

// TestCancelTaskByUser_IssueTask_Succeeds is a regression guard: issue-bound
// tasks (the original supported case) stay cancellable after the rewrite.
func TestCancelTaskByUser_IssueTask_Succeeds(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "CancelIssueTaskAgent", []byte("[]"))

	var issueID, taskID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position)
		VALUES ($1, 'cancel-byid-issue', 'todo', 'medium', $2, 'member', 92001, 0)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })

	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, issue_id)
		VALUES ($1, (SELECT runtime_id FROM agent WHERE id = $1), 'queued', 0, $2)
		RETURNING id
	`, agentID, issueID).Scan(&taskID); err != nil {
		t.Fatalf("create issue task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	w := httptest.NewRecorder()
	testHandler.CancelTaskByUser(w, cancelTaskByUserRequest(t, testUserID, taskID))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := taskStatus(t, taskID); got != "cancelled" {
		t.Fatalf("task not cancelled: status = %q", got)
	}
}

// TestCancelTaskByUser_ChatTask_NonCreator_Returns403 preserves chat privacy:
// a workspace member who did not start the conversation cannot cancel its task.
func TestCancelTaskByUser_ChatTask_NonCreator_Returns403(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "CancelChatAgent", []byte("[]"))
	sessionID := createHandlerTestChatSession(t, agentID) // creator = testUserID
	otherUserID := createWorkspaceMemberUser(t, "Chat Bystander", "cancel-chat-bystander@multica.test")

	var taskID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, issue_id, chat_session_id)
		VALUES ($1, (SELECT runtime_id FROM agent WHERE id = $1), 'running', 0, NULL, $2)
		RETURNING id
	`, agentID, sessionID).Scan(&taskID); err != nil {
		t.Fatalf("create chat task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	w := httptest.NewRecorder()
	testHandler.CancelTaskByUser(w, cancelTaskByUserRequest(t, otherUserID, taskID))
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	if got := taskStatus(t, taskID); got != "running" {
		t.Fatalf("chat task was mutated: status = %q", got)
	}
}

func TestCancelTaskByUser_ChatTaskWithTranscript_PersistsAssistantSnapshot(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "CancelChatTranscriptAgent", []byte("[]"))
	sessionID := createHandlerTestChatSession(t, agentID)

	var taskID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, issue_id, chat_session_id, created_at)
		VALUES ($1, (SELECT runtime_id FROM agent WHERE id = $1), 'running', 0, NULL, $2, now() - interval '5 seconds')
		RETURNING id
	`, agentID, sessionID).Scan(&taskID); err != nil {
		t.Fatalf("create chat task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO chat_message (chat_session_id, role, content, task_id)
		VALUES ($1, 'user', 'please answer', $2)
	`, sessionID, taskID); err != nil {
		t.Fatalf("create linked user chat message: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO task_message (task_id, seq, type, content)
		VALUES ($1, 1, 'text', 'partial answer')
	`, taskID); err != nil {
		t.Fatalf("create task message: %v", err)
	}

	w := httptest.NewRecorder()
	testHandler.CancelTaskByUser(w, cancelTaskByUserRequest(t, testUserID, taskID))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp CancelTaskByUserResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode cancel response: %v", err)
	}
	if resp.CancelledChatMessage != nil {
		t.Fatalf("expected no restore payload when transcript exists, got %#v", resp.CancelledChatMessage)
	}
	if got := taskStatus(t, taskID); got != "cancelled" {
		t.Fatalf("task not cancelled: status = %q", got)
	}

	var role, content, messageTaskID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT role, content, COALESCE(task_id::text, '')
		FROM chat_message
		WHERE chat_session_id = $1 AND role = 'assistant'
	`, sessionID).Scan(&role, &content, &messageTaskID); err != nil {
		t.Fatalf("read cancelled assistant chat message: %v", err)
	}
	if role != "assistant" || content != "Stopped." || messageTaskID != taskID {
		t.Fatalf("assistant snapshot mismatch: role=%q content=%q task_id=%q", role, content, messageTaskID)
	}
}

func TestCancelTaskByUser_ChatTaskWithoutTranscript_RestoresUserDraft(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "CancelChatNoTranscriptAgent", []byte("[]"))
	sessionID := createHandlerTestChatSession(t, agentID)

	var taskID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, issue_id, chat_session_id)
		VALUES ($1, (SELECT runtime_id FROM agent WHERE id = $1), 'running', 0, NULL, $2)
		RETURNING id
	`, agentID, sessionID).Scan(&taskID); err != nil {
		t.Fatalf("create chat task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	var userMessageID string
	const userContent = "keep this prompt"
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO chat_message (chat_session_id, role, content, task_id)
		VALUES ($1, 'user', $2, $3)
		RETURNING id
	`, sessionID, userContent, taskID).Scan(&userMessageID); err != nil {
		t.Fatalf("create linked user chat message: %v", err)
	}

	w := httptest.NewRecorder()
	testHandler.CancelTaskByUser(w, cancelTaskByUserRequest(t, testUserID, taskID))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp CancelTaskByUserResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode cancel response: %v", err)
	}
	if resp.CancelledChatMessage == nil {
		t.Fatal("expected restore payload for empty transcript cancel")
	}
	if resp.CancelledChatMessage.MessageID != userMessageID ||
		resp.CancelledChatMessage.Content != userContent ||
		!resp.CancelledChatMessage.RestoreToInput {
		t.Fatalf("restore payload mismatch: %#v", resp.CancelledChatMessage)
	}

	var count int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM chat_message
		WHERE chat_session_id = $1 AND role = 'assistant'
	`, sessionID).Scan(&count); err != nil {
		t.Fatalf("count assistant chat messages: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no assistant snapshot for empty transcript, got %d", count)
	}
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM chat_message
		WHERE id = $1
	`, userMessageID).Scan(&count); err != nil {
		t.Fatalf("count deleted user chat message: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected linked user message to be deleted, got %d", count)
	}
}

func TestCancelTaskByUser_ChannelBoundChatTask_ChannelMemberSucceeds(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "CancelChannelBoundAgent", []byte("[]"))
	sessionID := createHandlerTestChatSession(t, agentID) // creator = testUserID
	otherUserID := createWorkspaceMemberUser(t, "Channel Stopper", "cancel-channel-stopper-"+randomID()+"@multica.test")
	channelName := "cancel-channel-bound-" + randomID()

	var channelID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO channel (workspace_id, name, created_by, kind)
		VALUES ($1, $2, $3, 'group')
		RETURNING id
	`, testWorkspaceID, channelName, testUserID).Scan(&channelID); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID) })
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'user', $3), ($1, $2, 'user', $4), ($1, $2, 'agent', $5)
	`, channelID, testWorkspaceID, testUserID, otherUserID, agentID); err != nil {
		t.Fatalf("seed channel members: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO channel_agent_session (channel_id, agent_id, chat_session_id)
		VALUES ($1, $2, $3)
	`, channelID, agentID, sessionID); err != nil {
		t.Fatalf("bind channel agent session: %v", err)
	}

	var taskID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, issue_id, chat_session_id)
		VALUES ($1, (SELECT runtime_id FROM agent WHERE id = $1), 'running', 0, NULL, $2)
		RETURNING id
	`, agentID, sessionID).Scan(&taskID); err != nil {
		t.Fatalf("create channel-bound chat task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	w := httptest.NewRecorder()
	testHandler.CancelTaskByUser(w, cancelTaskByUserRequest(t, otherUserID, taskID))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := taskStatus(t, taskID); got != "cancelled" {
		t.Fatalf("task not cancelled: status = %q", got)
	}
}

func TestCancelTaskByUser_ChannelBoundChatTask_NonMemberReturns403(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "CancelChannelNonMemberAgent", []byte("[]"))
	sessionID := createHandlerTestChatSession(t, agentID)
	outsiderID := createWorkspaceMemberUser(t, "Channel Outsider", "cancel-channel-outsider-"+randomID()+"@multica.test")
	channelName := "cancel-channel-non-member-" + randomID()

	var channelID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO channel (workspace_id, name, created_by, kind)
		VALUES ($1, $2, $3, 'group')
		RETURNING id
	`, testWorkspaceID, channelName, testUserID).Scan(&channelID); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID) })
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'user', $3), ($1, $2, 'agent', $4)
	`, channelID, testWorkspaceID, testUserID, agentID); err != nil {
		t.Fatalf("seed channel members: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO channel_agent_session (channel_id, agent_id, chat_session_id)
		VALUES ($1, $2, $3)
	`, channelID, agentID, sessionID); err != nil {
		t.Fatalf("bind channel agent session: %v", err)
	}

	var taskID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, issue_id, chat_session_id)
		VALUES ($1, (SELECT runtime_id FROM agent WHERE id = $1), 'running', 0, NULL, $2)
		RETURNING id
	`, agentID, sessionID).Scan(&taskID); err != nil {
		t.Fatalf("create channel-bound chat task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	w := httptest.NewRecorder()
	testHandler.CancelTaskByUser(w, cancelTaskByUserRequest(t, outsiderID, taskID))
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	if got := taskStatus(t, taskID); got != "running" {
		t.Fatalf("task was mutated: status = %q", got)
	}
}

// TestCancelTaskByUser_PrivateAgent_PlainMember_Returns403 verifies the cancel
// endpoint mirrors the agent Activity / snapshot visibility gate: a plain
// member who cannot see a private agent's tasks cannot cancel them either.
func TestCancelTaskByUser_PrivateAgent_PlainMember_Returns403(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID, _, memberID := privateAgentTestFixture(t)
	taskID := createAutopilotRunOnlyTask(t, agentID)

	w := httptest.NewRecorder()
	testHandler.CancelTaskByUser(w, cancelTaskByUserRequest(t, memberID, taskID))
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	if got := taskStatus(t, taskID); got != "queued" {
		t.Fatalf("task was mutated: status = %q", got)
	}
}
