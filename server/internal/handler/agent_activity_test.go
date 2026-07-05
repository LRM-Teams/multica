package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAgentActivity_RoleGatesStepAndDiagnosticPayloads(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	memberID := createWorkspaceMemberUser(t, "Activity Member", "activity-member-"+randomID()+"@multica.test")
	agentID := createWorkspaceVisibleActivityAgent(t, "activity-role-agent")
	taskID := createActivityRunTask(t, agentID, "", "completed", "safe summary")
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_task_queue
		SET result = '{"action":"no_reply","trigger_kind":"time_trigger","output_suppressed_reason":"legacy_protocol_output"}'::jsonb,
		    error = 'raw stack /Users/frank/secret sk_agent_should_not_leak',
		    started_at = now() - interval '2 minutes',
		    completed_at = now() - interval '1 minute'
		WHERE id = $1`, taskID); err != nil {
		t.Fatalf("update activity task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO task_usage (task_id, provider, model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens)
		VALUES ($1, 'openai', 'gpt-test', 11, 7, 3, 2)
	`, taskID); err != nil {
		t.Fatalf("insert task usage: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO task_message (task_id, seq, type, tool, content, input, output)
		VALUES
		  ($1, 1, 'tool_use', 'exec_command', 'command step', '{"cmd":"echo sk_agent_step_secret"}', 'raw output secret'),
		  ($1, 2, 'thinking', NULL, 'private chain of thought', NULL, NULL)
	`, taskID); err != nil {
		t.Fatalf("insert task messages: %v", err)
	}

	memberList := listAgentActivityForUser(t, memberID, agentID, "")
	item := requireActivityItem(t, memberList, taskID)
	if item.VisibleLevel != agentActivityVisibleSummary {
		t.Fatalf("member visible_level = %q, want summary", item.VisibleLevel)
	}
	if item.Realtime != nil {
		t.Fatalf("member summary must not include realtime detail contract: %+v", item.Realtime)
	}
	if item.Run == nil || item.Run.StepCount != 2 {
		t.Fatalf("member run summary missing step count: %+v", item.Run)
	}
	if item.Run.ResultState == nil || *item.Run.ResultState != "no_reply" || item.Run.Status != "no_reply" {
		t.Fatalf("no_reply result not surfaced in summary: status=%q result=%v", item.Run.Status, item.Run.ResultState)
	}
	if item.Run.Result.Action == nil || *item.Run.Result.Action != "no_reply" || item.Run.Result.MessageRef != nil {
		t.Fatalf("no_reply result contract wrong: %+v", item.Run.Result)
	}
	if item.Run.Result.OutputSuppressedReason == nil || *item.Run.Result.OutputSuppressedReason != "legacy_protocol_output" {
		t.Fatalf("suppressed reason not surfaced in run result: %+v", item.Run.Result)
	}
	if item.Run.Trigger.Kind != agentActivityTriggerTimeTrigger {
		t.Fatalf("reserved time_trigger not surfaced: %+v", item.Run.Trigger)
	}
	if _, err := time.Parse(time.RFC3339, item.Run.CreatedAt); err != nil {
		t.Fatalf("created_at is not RFC3339: %q (%v)", item.Run.CreatedAt, err)
	}
	if got := len(item.Run.Tokens); got != 1 {
		t.Fatalf("token usage rows = %d, want 1", got)
	}
	if strings.Contains(memberList.raw, "sk_agent_step_secret") || strings.Contains(memberList.raw, "raw output secret") {
		t.Fatalf("member summary leaked step payload: %s", memberList.raw)
	}

	memberSteps := httptest.NewRecorder()
	testHandler.ListAgentActivitySteps(memberSteps, agentActivityRequest(memberID, agentID, taskID, "/steps"))
	if memberSteps.Code != http.StatusForbidden {
		t.Fatalf("member steps: expected 403, got %d: %s", memberSteps.Code, memberSteps.Body.String())
	}
	memberDiag := httptest.NewRecorder()
	testHandler.GetAgentActivityDiagnostic(memberDiag, agentActivityRequest(memberID, agentID, taskID, "/diagnostic"))
	if memberDiag.Code != http.StatusForbidden {
		t.Fatalf("member diagnostic: expected 403, got %d: %s", memberDiag.Code, memberDiag.Body.String())
	}

	ownerSteps := httptest.NewRecorder()
	testHandler.ListAgentActivitySteps(ownerSteps, agentActivityRequest(testUserID, agentID, taskID, "/steps?limit=1"))
	if ownerSteps.Code != http.StatusOK {
		t.Fatalf("owner steps: expected 200, got %d: %s", ownerSteps.Code, ownerSteps.Body.String())
	}
	var steps AgentActivityStepPageResponse
	if err := json.NewDecoder(ownerSteps.Body).Decode(&steps); err != nil {
		t.Fatalf("decode owner steps: %v", err)
	}
	if len(steps.Steps) != 1 || !steps.HasMore || steps.NextCursor == nil {
		t.Fatalf("owner step page metadata wrong: %+v", steps)
	}
	if steps.NextCursor.Seq != steps.Steps[0].Seq {
		t.Fatalf("next step cursor must point at last included row, got cursor=%+v step=%+v", steps.NextCursor, steps.Steps[0])
	}
	if steps.Realtime.EventType != "agent_activity:step" {
		t.Fatalf("realtime event type = %q", steps.Realtime.EventType)
	}
	if steps.Realtime.Payload != agentActivityRealtimeStepPayload {
		t.Fatalf("realtime payload = %q, want %q", steps.Realtime.Payload, agentActivityRealtimeStepPayload)
	}

	ownerDiag := httptest.NewRecorder()
	testHandler.GetAgentActivityDiagnostic(ownerDiag, agentActivityRequest(testUserID, agentID, taskID, "/diagnostic"))
	if ownerDiag.Code != http.StatusOK {
		t.Fatalf("owner diagnostic: expected 200, got %d: %s", ownerDiag.Code, ownerDiag.Body.String())
	}
	if strings.Contains(ownerDiag.Body.String(), "sk_agent_should_not_leak") || strings.Contains(ownerDiag.Body.String(), "/Users/frank/secret") {
		t.Fatalf("diagnostic leaked raw error: %s", ownerDiag.Body.String())
	}
	if !strings.Contains(ownerDiag.Body.String(), `"output_suppressed_reason":"legacy_protocol_output"`) {
		t.Fatalf("diagnostic missing output_suppressed_reason: %s", ownerDiag.Body.String())
	}
}

func TestAgentActivity_TargetVisibilityForDMAndChannelRuns(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	memberID := createWorkspaceMemberUser(t, "Activity Channel Member", "activity-channel-"+randomID()+"@multica.test")
	outsiderID := createWorkspaceMemberUser(t, "Activity Channel Outsider", "activity-outsider-"+randomID()+"@multica.test")
	agentID := createWorkspaceVisibleActivityAgent(t, "activity-target-agent")
	dmSessionID := createActivityChatSession(t, agentID, testUserID, "owner dm")
	dmTaskID := createActivityRunTask(t, agentID, dmSessionID, "running", "dm work")
	dmMessageID := createActivityChatMessage(t, dmSessionID, dmTaskID, "dm answer")
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_task_queue SET result = '{"action":"message_send"}'::jsonb WHERE id = $1
	`, dmTaskID); err != nil {
		t.Fatalf("update dm task result: %v", err)
	}
	channelID, channelSessionID := createActivityChannelSession(t, agentID, memberID)
	channelTaskID := createActivityRunTask(t, agentID, channelSessionID, "running", "channel work")

	memberList := listAgentActivityForUser(t, memberID, agentID, "")
	if got := findActivityItem(memberList, dmTaskID); got != nil {
		t.Fatalf("channel member must not see owner DM task: %+v", *got)
	}
	channelItem := requireActivityItem(t, memberList, channelTaskID)
	if channelItem.TargetRef.Kind != "channel" || channelItem.TargetRef.ID == nil || *channelItem.TargetRef.ID != channelID {
		t.Fatalf("channel target ref = %+v, want channel %s", channelItem.TargetRef, channelID)
	}

	outsiderList := listAgentActivityForUser(t, outsiderID, agentID, "")
	if got := findActivityItem(outsiderList, channelTaskID); got != nil {
		t.Fatalf("non-channel member must not see channel task: %+v", *got)
	}
	if got := findActivityItem(outsiderList, dmTaskID); got != nil {
		t.Fatalf("non-creator must not see DM task: %+v", *got)
	}

	ownerList := listAgentActivityForUser(t, testUserID, agentID, "")
	ownerDM := requireActivityItem(t, ownerList, dmTaskID)
	if ownerDM.Run == nil || ownerDM.Run.Result.MessageRef == nil {
		t.Fatalf("send-result run missing message_ref: %+v", ownerDM.Run)
	}
	if ownerDM.Run.Result.MessageRef.Kind != "chat_message" || ownerDM.Run.Result.MessageRef.ID != dmMessageID {
		t.Fatalf("send-result message_ref = %+v, want chat_message %s", ownerDM.Run.Result.MessageRef, dmMessageID)
	}
	if got := findActivityItem(ownerList, channelTaskID); got != nil {
		t.Fatalf("workspace owner who is not in channel must still respect channel membership on target rows: %+v", *got)
	}
}

func TestAgentActivity_EventRowsUseTargetVisibility(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	memberID := createWorkspaceMemberUser(t, "Activity Event Member", "activity-event-"+randomID()+"@multica.test")
	agentID := createWorkspaceVisibleActivityAgent(t, "activity-event-agent")
	dmSessionID := createActivityChatSession(t, agentID, testUserID, "event dm")
	channelID, _ := createActivityChannelSession(t, agentID, memberID)

	channelEventID := createActivityEventWithDetails(t, agentID, "channel", channelID, "restart", `{"replay_count":3,"raw_error":"must_not_surface"}`)
	dmEventID := createActivityEvent(t, agentID, "dm", dmSessionID, "dm_decision")

	memberList := listAgentActivityForUser(t, memberID, agentID, "")
	channelEvent := requireActivityItem(t, memberList, channelEventID)
	if channelEvent.Event == nil {
		t.Fatalf("restart event missing event summary: %+v", channelEvent)
	}
	replayCount, replayOK := int64FromJSONValue(channelEvent.Event.Details["replay_count"])
	if !replayOK || replayCount != 3 {
		t.Fatalf("restart event replay_count not surfaced safely: %+v", channelEvent.Event)
	}
	if strings.Contains(memberList.raw, "must_not_surface") {
		t.Fatalf("event summary leaked raw detail: %s", memberList.raw)
	}
	if got := findActivityItem(memberList, dmEventID); got != nil {
		t.Fatalf("member must not see event targeted at another user's DM: %+v", *got)
	}

	ownerList := listAgentActivityForUser(t, testUserID, agentID, "")
	requireActivityItem(t, ownerList, dmEventID)
	if got := findActivityItem(ownerList, channelEventID); got != nil {
		t.Fatalf("workspace owner who is not in channel must still respect channel event visibility: %+v", *got)
	}
}

type agentActivityListResult struct {
	resp AgentActivityPageResponse
	raw  string
}

func listAgentActivityForUser(t *testing.T, userID, agentID, query string) agentActivityListResult {
	t.Helper()
	w := httptest.NewRecorder()
	path := "/api/agents/" + agentID + "/activity" + query
	req := withURLParam(newRequestAs(userID, http.MethodGet, path, nil), "id", agentID)
	testHandler.ListAgentActivity(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListAgentActivity(%s): expected 200, got %d: %s", userID, w.Code, w.Body.String())
	}
	var resp AgentActivityPageResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode activity response: %v", err)
	}
	return agentActivityListResult{resp: resp, raw: w.Body.String()}
}

func requireActivityItem(t *testing.T, list agentActivityListResult, id string) AgentActivityItem {
	t.Helper()
	item := findActivityItem(list, id)
	if item == nil {
		t.Fatalf("activity %s not found in %+v", id, list.resp.Activities)
	}
	return *item
}

func findActivityItem(list agentActivityListResult, id string) *AgentActivityItem {
	for i := range list.resp.Activities {
		if list.resp.Activities[i].ID == id {
			return &list.resp.Activities[i]
		}
	}
	return nil
}

func agentActivityRequest(userID, agentID, activityID, suffix string) *http.Request {
	req := newRequestAs(userID, http.MethodGet, "/api/agents/"+agentID+"/activity/"+activityID+suffix, nil)
	return withRouteParams(req, "id", agentID, "activityId", activityID)
}

func createWorkspaceVisibleActivityAgent(t *testing.T, name string) string {
	t.Helper()
	var agentID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id,
			instructions, custom_env, custom_args, mcp_config
		)
		VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 'workspace', 1, $4, '', '{}'::jsonb, '[]'::jsonb, '{}'::jsonb)
		RETURNING id
	`, testWorkspaceID, name, handlerTestRuntimeID(t), testUserID).Scan(&agentID); err != nil {
		t.Fatalf("create activity agent: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID) })
	return agentID
}

func createActivityRunTask(t *testing.T, agentID, chatSessionID, status, summary string) string {
	t.Helper()
	var chatArg any
	if chatSessionID != "" {
		chatArg = chatSessionID
	}
	var taskID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, chat_session_id, status, priority, trigger_summary,
			created_at, started_at
		)
		VALUES ($1, $2, $3, $4, 0, $5, now(), now())
		RETURNING id
	`, agentID, handlerTestRuntimeID(t), chatArg, status, summary).Scan(&taskID); err != nil {
		t.Fatalf("create activity task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })
	return taskID
}

func createActivityChatSession(t *testing.T, agentID, creatorID, title string) string {
	t.Helper()
	var sessionID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, testWorkspaceID, agentID, creatorID, title).Scan(&sessionID); err != nil {
		t.Fatalf("create activity chat session: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM chat_session WHERE id = $1`, sessionID) })
	return sessionID
}

func createActivityChatMessage(t *testing.T, chatSessionID, taskID, content string) string {
	t.Helper()
	var messageID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO chat_message (chat_session_id, role, content, task_id)
		VALUES ($1, 'assistant', $2, $3)
		RETURNING id
	`, chatSessionID, content, taskID).Scan(&messageID); err != nil {
		t.Fatalf("create activity chat message: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM chat_message WHERE id = $1`, messageID) })
	return messageID
}

func createActivityChannelSession(t *testing.T, agentID, memberID string) (string, string) {
	t.Helper()
	var channelID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO channel (workspace_id, name, created_by, kind)
		VALUES ($1, $2, $3, 'group')
		RETURNING id
	`, testWorkspaceID, "activity-"+randomID(), testUserID).Scan(&channelID); err != nil {
		t.Fatalf("create activity channel: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID) })
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'user', $3)
	`, channelID, testWorkspaceID, memberID); err != nil {
		t.Fatalf("add activity channel member: %v", err)
	}
	sessionID := createActivityChatSession(t, agentID, testUserID, "channel")
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO channel_agent_session (channel_id, agent_id, chat_session_id)
		VALUES ($1, $2, $3)
	`, channelID, agentID, sessionID); err != nil {
		t.Fatalf("create channel agent session: %v", err)
	}
	return channelID, sessionID
}

func createActivityEvent(t *testing.T, agentID, targetKind, targetID, eventType string) string {
	t.Helper()
	return createActivityEventWithDetails(t, agentID, targetKind, targetID, eventType, `{}`)
}

func createActivityEventWithDetails(t *testing.T, agentID, targetKind, targetID, eventType, details string) string {
	t.Helper()
	var eventID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_activity_event (
			workspace_id, agent_id, event_kind, event_type, severity,
			target_kind, target_id, reason_code, message, details
		)
		VALUES ($1, $2, 'platform_decision', $3, 'info', $4, $5, 'test_reason', 'safe event', $6::jsonb)
		RETURNING id
	`, testWorkspaceID, agentID, eventType, targetKind, targetID, details).Scan(&eventID); err != nil {
		t.Fatalf("create activity event: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_activity_event WHERE id = $1`, eventID) })
	return eventID
}
