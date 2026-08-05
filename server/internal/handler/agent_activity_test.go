package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// promoteToWorkspaceAdmin upgrades an existing plain member to admin role in
// testWorkspaceID, so the caller can be used to exercise admin|owner-gated
// surfaces (task #908) while keeping any other test fixtures (channel
// membership, ownership) already set up for that user id unchanged.
func promoteToWorkspaceAdmin(t *testing.T, userID string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		UPDATE member SET role = 'admin' WHERE workspace_id = $1 AND user_id = $2`,
		testWorkspaceID, userID); err != nil {
		t.Fatalf("promote %s to admin: %v", userID, err)
	}
}

func TestAgentActivity_RoleGatesStepAndDiagnosticPayloads(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	memberID := createWorkspaceMemberUser(t, "Activity Member", "activity-member-"+randomID()+"@multica.test")
	agentID := createWorkspaceVisibleActivityAgent(t, "activity-role-agent")
	taskID := createActivityRunTask(t, agentID, "", "completed", "safe summary")
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_inbox_event
		SET result = '{"action":"no_reply","trigger_kind":"time_trigger","output_suppressed_reason":"legacy_protocol_output"}'::jsonb,
		    error = 'raw stack /Users/frank/secret sk_agent_should_not_leak',
		    started_at = now() - interval '2 minutes',
		    completed_at = now() - interval '1 minute'
		WHERE id = $1`, taskID); err != nil {
		t.Fatalf("update activity task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_usage (execution_id, provider, model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens)
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

	// Task #908: Activity is an internal/history surface gated to admin|owner
	// — a plain member can no longer reach the list at all (superseding the
	// old "summary for everyone, detail for owner" two-tier model; the tier
	// distinction is now moot since only owner/admin ever pass the gate).
	memberActivity := httptest.NewRecorder()
	testHandler.ListAgentActivity(memberActivity, withURLParam(newRequestAs(memberID, http.MethodGet, "/api/agents/"+agentID+"/activity", nil), "id", agentID))
	if memberActivity.Code != http.StatusForbidden {
		t.Fatalf("member ListAgentActivity: expected 403, got %d: %s", memberActivity.Code, memberActivity.Body.String())
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

	ownerList := listAgentActivityForUser(t, testUserID, agentID, "")
	item := requireActivityItem(t, ownerList, taskID)
	if item.VisibleLevel != agentActivityVisibleDetail {
		t.Fatalf("owner visible_level = %q, want detail", item.VisibleLevel)
	}
	if item.Run == nil || item.Run.StepCount != 2 {
		t.Fatalf("owner run summary missing step count: %+v", item.Run)
	}
	if item.Run.ResultState == nil || *item.Run.ResultState != "no_reply" || item.Run.Status != "no_reply" {
		t.Fatalf("no_reply result not surfaced: status=%q result=%v", item.Run.Status, item.Run.ResultState)
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
	if strings.Contains(ownerList.raw, "sk_agent_step_secret") || strings.Contains(ownerList.raw, "raw output secret") {
		t.Fatalf("owner list leaked raw step payload (list is summary-level, steps have their own endpoint): %s", ownerList.raw)
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

	// Task #908: Activity is admin|owner-gated now, so the per-conversation
	// target-visibility checks below (which are an orthogonal, still-live
	// boundary: even an admin/owner shouldn't see another user's private DM
	// with the agent, or a channel run for a channel they aren't in) must be
	// exercised by admin actors — a plain member can no longer reach
	// ListAgentActivity at all, tested separately in
	// TestAgentActivity_RoleGatesStepAndDiagnosticPayloads.
	memberID := createWorkspaceMemberUser(t, "Activity Channel Member", "activity-channel-"+randomID()+"@multica.test")
	outsiderID := createWorkspaceMemberUser(t, "Activity Channel Outsider", "activity-outsider-"+randomID()+"@multica.test")
	promoteToWorkspaceAdmin(t, memberID)
	promoteToWorkspaceAdmin(t, outsiderID)
	agentID := createWorkspaceVisibleActivityAgent(t, "activity-target-agent")
	dmSessionID := createActivityChatSession(t, agentID, testUserID, "owner dm")
	dmTaskID := createActivityRunTask(t, agentID, dmSessionID, "running", "dm work")
	dmMessageID := createActivityChatMessage(t, dmSessionID, dmTaskID, "dm answer")
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_inbox_event SET result = '{"action":"message_send"}'::jsonb WHERE id = $1
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

	// Task #908: promoted to admin so this actor can reach Activity at all —
	// see the comment in TestAgentActivity_TargetVisibilityForDMAndChannelRuns.
	memberID := createWorkspaceMemberUser(t, "Activity Event Member", "activity-event-"+randomID()+"@multica.test")
	promoteToWorkspaceAdmin(t, memberID)
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

func TestAgentActivity_ListKeepsRecentEventsVisibleWithLegacyRunHistory(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createWorkspaceVisibleActivityAgent(t, "activity-feed-history-agent")
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_inbox_event (
			agent_id, runtime_id, status, priority, trigger_summary,
			created_at, started_at, completed_at
		)
		SELECT
			$1, $2, 'acked', 0, 'legacy run ' || g,
			now() - interval '2 hours' - (g || ' seconds')::interval,
			now() - interval '2 hours' - (g || ' seconds')::interval,
			now() - interval '2 hours' - (g || ' seconds')::interval
		FROM generate_series(1, 120) AS g
	`, agentID, handlerTestRuntimeID(t)); err != nil {
		t.Fatalf("seed legacy activity runs: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_inbox_event WHERE agent_id = $1`, agentID) })

	eventID := createActivityEvent(t, agentID, "agent", agentID, "agent_inbox_failed")
	list := listAgentActivityForUser(t, testUserID, agentID, "?limit=1")
	if len(list.resp.Activities) != 1 {
		t.Fatalf("activity page size = %d, want 1: %+v", len(list.resp.Activities), list.resp.Activities)
	}
	if list.resp.Activities[0].ID != eventID {
		t.Fatalf("first activity = %s, want recent event %s", list.resp.Activities[0].ID, eventID)
	}
	if list.resp.Activities[0].Event == nil || list.resp.Activities[0].Event.EventType != "agent_inbox_failed" {
		t.Fatalf("first activity event summary wrong: %+v", list.resp.Activities[0])
	}
}

func TestAgentActivityEvents_UsesRaftKindsAndTaskMessageRows(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	// Task #908: promoted to admin so this actor can reach the events feed at
	// all — see the comment in TestAgentActivity_TargetVisibilityForDMAndChannelRuns.
	outsiderID := createWorkspaceMemberUser(t, "Activity Events Outsider", "activity-events-outsider-"+randomID()+"@multica.test")
	promoteToWorkspaceAdmin(t, outsiderID)
	agentID := createWorkspaceVisibleActivityAgent(t, "activity-events-agent")
	dmSessionID := createActivityChatSession(t, agentID, testUserID, "activity events dm")
	taskID := createActivityRunTask(t, agentID, dmSessionID, "running", "dm work")

	ctx := context.Background()
	var thinkingID, toolID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO task_message (task_id, seq, type, content, visibility)
		VALUES ($1, 1, 'thinking', 'thinking aggregate text', 'user_facing')
		RETURNING id
	`, taskID).Scan(&thinkingID); err != nil {
		t.Fatalf("insert thinking task message: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO task_message (task_id, seq, type, tool, input, content, visibility)
		VALUES ($1, 2, 'tool_use', 'exec_command',
		        '{"cmd":"pnpm --filter @multica/web build --token sk-proj-abc123def456ghi789jkl012mno345"}',
		        'tool input is not the public narrative', 'user_facing')
		RETURNING id
	`, taskID).Scan(&toolID); err != nil {
		t.Fatalf("insert tool task message: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM task_message WHERE id IN ($1, $2)`, thinkingID, toolID)
	})

	ownerEvents := listAgentActivityEventsForUser(t, testUserID, agentID, "")
	if strings.Contains(ownerEvents.raw, `"run_id"`) {
		t.Fatalf("activity events must not expose run_id: %s", ownerEvents.raw)
	}
	for _, removedField := range []string{`"label"`, `"subtext"`, `"tone"`, `"reason_label"`, `"path"`, `"query"`, `"pattern"`} {
		if strings.Contains(ownerEvents.raw, removedField) {
			t.Fatalf("activity events must not expose presentation field %s: %s", removedField, ownerEvents.raw)
		}
	}
	if thinking := findActivityTimelineEvent(ownerEvents, thinkingID); thinking != nil {
		t.Fatalf("thinking must not enter the main Activity timeline: %+v", *thinking)
	}
	tool := requireActivityTimelineEvent(t, ownerEvents, toolID)
	if tool.ActivityKind != activityKindToolCall || tool.DetailKind != "tool_use" {
		t.Fatalf("tool event activity/detail kind = %q/%q", tool.ActivityKind, tool.DetailKind)
	}
	if tool.Tool == nil || *tool.Tool != "bash" {
		t.Fatalf("tool event canonical tool = %+v, want bash", tool.Tool)
	}
	if tool.ToolTarget == nil || !strings.HasPrefix(*tool.ToolTarget, "pnpm --filter @multica/web build") {
		t.Fatalf("tool event tool_target = %+v, want redacted command summary", tool.ToolTarget)
	}
	if tool.Status == nil || *tool.Status != "running" {
		t.Fatalf("tool event status = %+v, want running", tool.Status)
	}
	if tool.DisplayLabel != "Running command" || tool.LabelKey != "running_command" {
		t.Fatalf("tool display label/key = %q/%q, want Running command/running_command", tool.DisplayLabel, tool.LabelKey)
	}
	if len(tool.Entries) != 1 || tool.Entries[0].Tool == nil || *tool.Entries[0].Tool != "bash" || tool.Entries[0].Command == nil {
		t.Fatalf("tool entries missing canonical tool + full redacted command: %+v", tool.Entries)
	}
	if !strings.Contains(*tool.Entries[0].Command, "pnpm --filter @multica/web build") {
		t.Fatalf("tool full command missing from full activity entry: %+v", tool.Entries[0].Command)
	}
	if strings.Contains(ownerEvents.raw, `"activity_kind"`) == false || strings.Contains(ownerEvents.raw, `"detail_kind"`) == false {
		t.Fatalf("activity events missing raft field names: %s", ownerEvents.raw)
	}
	assertActivityEventsOmitTopLevelLegacyFields(t, ownerEvents.raw)
	for _, leak := range []string{"sk-proj-abc123", "tool input is not the public narrative"} {
		if strings.Contains(ownerEvents.raw, leak) {
			t.Fatalf("activity event leaked raw tool content %q: %s", leak, ownerEvents.raw)
		}
	}

	outsiderEvents := listAgentActivityEventsForUser(t, outsiderID, agentID, "")
	if got := findActivityTimelineEvent(outsiderEvents, thinkingID); got != nil {
		t.Fatalf("thinking must not enter a non-owner Activity timeline: %+v", *got)
	}
}

func TestAgentActivityEvents_FileToolUsesTopLevelPathFacts(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID := createWorkspaceVisibleActivityAgent(t, "activity-file-tool-agent")
	const filePath = "/Users/frank/.multica/workspaces/ws/agents/agent/hello_world_2.txt"
	ctx := context.Background()

	var eventID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_activity_event (
			workspace_id, agent_id, event_kind, event_type, severity,
			target_kind, target_id, message, details
		)
		VALUES (
			$1, $2, 'tool_call', 'tool_use', 'info',
			'agent', $2, '', $3::jsonb
		)
		RETURNING id
	`, testWorkspaceID, agentID, fmt.Sprintf(`{
		"tool": "write_file",
		"command": "create",
		"path": %q,
		"tool_target": %q,
		"summary_kind": "file_path"
	}`, filePath, filePath)).Scan(&eventID); err != nil {
		t.Fatalf("insert file tool activity event: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_activity_event WHERE id = $1`, eventID) })

	events := listAgentActivityEventsForUser(t, testUserID, agentID, "")
	event := requireActivityTimelineEvent(t, events, eventID)
	if event.Tool == nil || *event.Tool != "write_file" {
		t.Fatalf("file tool canonical tool = %+v, want write_file", event.Tool)
	}
	if event.ToolTarget == nil || *event.ToolTarget != filePath {
		t.Fatalf("file tool target = %+v, want %q", event.ToolTarget, filePath)
	}
	if event.Details != nil {
		t.Fatalf("file tool timeline details = %+v, want omitted outside held-freshness contract", event.Details)
	}
	if len(event.Entries) != 1 {
		t.Fatalf("file tool entries = %+v, want one entry", event.Entries)
	}
	entry := event.Entries[0]
	if entry.Tool == nil || *entry.Tool != "write_file" {
		t.Fatalf("file tool entry tool = %+v, want write_file", entry.Tool)
	}
	if entry.ToolTarget == nil || *entry.ToolTarget != filePath {
		t.Fatalf("file tool entry target = %+v, want %q", entry.ToolTarget, filePath)
	}
	if entry.SummaryKind == nil || *entry.SummaryKind != "file_path" {
		t.Fatalf("file tool entry summary kind = %+v, want file_path", entry.SummaryKind)
	}
	if entry.Command != nil {
		t.Fatalf("file tool entry command = %+v, want omitted native operation verb", entry.Command)
	}
	if strings.Contains(events.raw, `"command":"create"`) {
		t.Fatalf("native file tool create verb must not be exposed as a command: %s", events.raw)
	}
}

func TestAgentActivityEvents_ExposesHeldFreshnessDetails(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID := createWorkspaceVisibleActivityAgent(t, "activity-freshness-details-agent")
	channelID, _ := createActivityChannelSession(t, agentID, testUserID)
	ctx := context.Background()
	details := `{
		"reason": "newer_messages_available",
		"decision": "local_hold",
		"producer_fact_id": "freshness:producer:1",
		"transport_id": "transport-1",
		"seen_up_to_seq": 9,
		"latest_seq": 12,
		"new_message_count": 3,
		"shown_message_count": 2,
		"omitted_message_count": 1,
		"target": "#multica:test",
		"recommended_action": "review_newer_messages",
		"input": {"secret": "sk_agent_should_not_leak"}
	}`
	var statusID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_activity_event (
			workspace_id, agent_id, event_kind, event_type, severity,
			target_kind, target_id, target_slug, message, details, visibility, created_at
		)
		VALUES (
			$1, $2, 'blocked', 'send_freshness_hold', 'info',
			'channel', $3, '#multica:test', 'Send held by freshness check', $4::jsonb, 'user_facing', now() - interval '2 seconds'
		)
		RETURNING id
	`, testWorkspaceID, agentID, channelID, details).Scan(&statusID); err != nil {
		t.Fatalf("insert freshness hold status event: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_activity_event WHERE id = $1`, statusID)
	})

	events := listAgentActivityEventsForUser(t, testUserID, agentID, "")
	event := requireActivityTimelineEvent(t, events, statusID)
	if event.Details == nil {
		t.Fatalf("%s details missing from timeline event: %+v", event.DetailKind, event)
	}
	assertActivityTimelineDetail(t, event, "reason", "newer_messages_available")
	assertActivityTimelineDetail(t, event, "decision", "local_hold")
	assertActivityTimelineDetail(t, event, "recommended_action", "review_newer_messages")
	assertActivityTimelineDetail(t, event, "producer_fact_id", "freshness:producer:1")
	assertActivityTimelineDetail(t, event, "transport_id", "transport-1")
	assertActivityTimelineDetail(t, event, "seen_up_to_seq", float64(9))
	assertActivityTimelineDetail(t, event, "latest_seq", float64(12))
	assertActivityTimelineDetail(t, event, "new_message_count", float64(3))
	assertActivityTimelineDetail(t, event, "shown_message_count", float64(2))
	assertActivityTimelineDetail(t, event, "omitted_message_count", float64(1))
	assertActivityTimelineDetail(t, event, "target", "#multica:test")
	if _, ok := event.Details["input"]; ok {
		t.Fatalf("%s leaked raw input details: %+v", event.DetailKind, event.Details)
	}
	if strings.Contains(events.raw, "sk_agent_should_not_leak") {
		t.Fatalf("activity details leaked raw input secret: %s", events.raw)
	}
}

func TestAgentActivityEvents_ExposesFreshnessResolutionDetails(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID := createWorkspaceVisibleActivityAgent(t, "activity-freshness-resolution-agent")
	channelID, _ := createActivityChannelSession(t, agentID, testUserID)
	ctx := context.Background()
	details := `{
		"producer_fact_id": "freshness:producer:resolved",
		"outcome": "revised_send",
		"freshness_hold_resolution_seconds": 1.25,
		"resolution_ms": 1250,
		"transport_id": "transport-resolved",
		"message_id": "message-resolved",
		"target": "#multica:test",
		"input": {"secret": "sk_agent_should_not_leak"}
	}`
	var eventID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_activity_event (
			workspace_id, agent_id, event_kind, event_type, severity,
			target_kind, target_id, target_slug, message, details, visibility
		)
		VALUES (
			$1, $2, 'text', 'send_freshness_resolved', 'info',
			'channel', $3, '#multica:test', 'Freshness hold resolved', $4::jsonb, 'user_facing'
		)
		RETURNING id
	`, testWorkspaceID, agentID, channelID, details).Scan(&eventID); err != nil {
		t.Fatalf("insert freshness resolution event: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_activity_event WHERE id = $1`, eventID)
	})

	events := listAgentActivityEventsForUser(t, testUserID, agentID, "")
	event := requireActivityTimelineEvent(t, events, eventID)
	for key, want := range map[string]any{
		"producer_fact_id":                  "freshness:producer:resolved",
		"outcome":                           "revised_send",
		"freshness_hold_resolution_seconds": float64(1.25),
		"resolution_ms":                     float64(1250),
		"transport_id":                      "transport-resolved",
		"message_id":                        "message-resolved",
		"target":                            "#multica:test",
	} {
		assertActivityTimelineDetail(t, event, key, want)
	}
	if _, ok := event.Details["input"]; ok || strings.Contains(events.raw, "sk_agent_should_not_leak") {
		t.Fatalf("freshness resolution leaked raw input details: %+v raw=%s", event.Details, events.raw)
	}
}

func TestAgentActivityEvents_DefaultPageSkipsDiagnosticNoise(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createWorkspaceVisibleActivityAgent(t, "activity-events-noise-agent")
	dmSessionID := createActivityChatSession(t, agentID, testUserID, "activity events noise dm")
	var thinkingID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_activity_event (
			workspace_id, agent_id, event_kind, event_type, severity,
			target_kind, target_id, message, details, visibility, created_at
		)
		VALUES ($1, $2, 'thinking', 'thinking', 'info', 'dm', $3, 'old visible thinking', '{}'::jsonb, 'user_facing', now() - interval '10 minutes')
		RETURNING id
	`, testWorkspaceID, agentID, dmSessionID).Scan(&thinkingID); err != nil {
		t.Fatalf("insert narrative thinking event: %v", err)
	}
	executedID, ok := insertAgentActivityEvent(
		ctx, testPool, parseUUID(testWorkspaceID), parseUUID(agentID), pgtype.UUID{}, pgtype.UUID{},
		activityKindCustom, "radar_action_executed", "info",
		"agent", parseUUID(agentID), "", "", "Radar executed: create issue", nil,
	)
	if !ok {
		t.Fatal("insert executed Radar activity event")
	}
	failedID, ok := insertAgentActivityEvent(
		ctx, testPool, parseUUID(testWorkspaceID), parseUUID(agentID), pgtype.UUID{}, pgtype.UUID{},
		activityKindCustom, "radar_action_failed", "warning",
		"agent", parseUUID(agentID), "", "create_issue", "Radar failed: create issue", nil,
	)
	if !ok {
		t.Fatal("insert failed Radar activity event")
	}
	statusID, ok := insertAgentActivityEvent(
		ctx, testPool, parseUUID(testWorkspaceID), parseUUID(agentID), pgtype.UUID{}, pgtype.UUID{},
		activityKindCustom, agentInboxStatusChangedEventType, "info",
		"agent", parseUUID(agentID), "", "", "Idle", map[string]any{"status": agentInboxStatusActivityIdle},
	)
	if !ok {
		t.Fatal("insert agent status activity event")
	}
	noActionID, ok := insertAgentActivityEvent(
		ctx, testPool, parseUUID(testWorkspaceID), parseUUID(agentID), pgtype.UUID{}, pgtype.UUID{},
		activityKindCustom, "radar_action_executed", "info",
		"agent", parseUUID(agentID), "", "no_action", "Radar executed: no_action", nil,
	)
	if !ok {
		t.Fatal("insert no_action Radar activity event")
	}
	reminderEvents := []struct {
		eventType string
		message   string
	}{
		{eventType: "reminder_scheduled", message: "Agent scheduled a future self-wake"},
		{eventType: "reminder_snoozed", message: "Agent snoozed a reminder"},
		{eventType: "reminder_updated", message: "Agent updated a reminder"},
		{eventType: "reminder_cancelled", message: "Agent cancelled a reminder"},
		{eventType: "reminder_fired", message: "Reminder fired and woke the agent"},
	}
	reminderEventIDs := make(map[string]pgtype.UUID, len(reminderEvents))
	for _, reminderEvent := range reminderEvents {
		id, inserted := insertAgentActivityEvent(
			ctx, testPool, parseUUID(testWorkspaceID), parseUUID(agentID), pgtype.UUID{}, pgtype.UUID{},
			activityKindCustom, reminderEvent.eventType, "info",
			"agent", parseUUID(agentID), "", "", reminderEvent.message,
			map[string]any{"reminder_id": "test-reminder"},
		)
		if !inserted {
			t.Fatalf("insert %s activity event", reminderEvent.eventType)
		}
		reminderEventIDs[reminderEvent.eventType] = id
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_activity_event (
			workspace_id, agent_id, event_kind, event_type, severity,
			target_kind, target_id, message, details, visibility, created_at
		)
		SELECT $1, $2, 'transport', 'runtime_progress', 'info', 'dm', $3, 'transport noise ' || g, '{}'::jsonb, 'diagnostic_only', now() - (g || ' seconds')::interval
		FROM generate_series(1, 80) AS g
	`, testWorkspaceID, agentID, dmSessionID); err != nil {
		t.Fatalf("insert diagnostic noise: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_activity_event WHERE agent_id = $1`, agentID) })

	events := listAgentActivityEventsForUser(t, testUserID, agentID, "")
	if events.resp.Limit != agentActivityEventDefaultLimit {
		t.Fatalf("default events limit = %d, want %d", events.resp.Limit, agentActivityEventDefaultLimit)
	}
	if got := findActivityTimelineEvent(events, thinkingID); got != nil {
		t.Fatalf("default events page included thinking row: %+v", *got)
	}
	requireActivityTimelineEvent(t, events, uuidToString(executedID))
	requireActivityTimelineEvent(t, events, uuidToString(failedID))
	statusEvent := requireActivityTimelineEvent(t, events, uuidToString(statusID))
	if statusEvent.Status == nil || *statusEvent.Status != agentInboxStatusActivityIdle {
		t.Fatalf("status event status = %+v, want %q", statusEvent.Status, agentInboxStatusActivityIdle)
	}
	if len(statusEvent.Entries) != 1 || statusEvent.Entries[0].Status == nil || *statusEvent.Entries[0].Status != agentInboxStatusActivityIdle {
		t.Fatalf("status event entries = %+v, want idle status entry", statusEvent.Entries)
	}
	if got := findActivityTimelineEvent(events, uuidToString(noActionID)); got != nil {
		t.Fatalf("no_action Radar event leaked into user narrative: %+v", *got)
	}
	for _, reminderEvent := range reminderEvents {
		event := requireActivityTimelineEvent(t, events, uuidToString(reminderEventIDs[reminderEvent.eventType]))
		if event.ActivityKind != activityKindCustom || event.DetailKind != reminderEvent.eventType {
			t.Fatalf("%s activity/detail kind = %q/%q", reminderEvent.eventType, event.ActivityKind, event.DetailKind)
		}
		if event.Text == nil || *event.Text != reminderEvent.message {
			t.Fatalf("%s text = %+v, want %q", reminderEvent.eventType, event.Text, reminderEvent.message)
		}
	}
	for _, event := range events.resp.Events {
		if event.ActivityKind == activityKindTransport {
			t.Fatalf("default events page included diagnostic transport row: %+v", event)
		}
	}
}

func TestAgentActivityEvents_CursorUsesOccurredAt(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createWorkspaceVisibleActivityAgent(t, "activity-events-cursor-agent")
	dmSessionID := createActivityChatSession(t, agentID, testUserID, "activity events cursor dm")
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_activity_event (
			workspace_id, agent_id, event_kind, event_type, severity,
			target_kind, target_id, message, details, visibility, created_at
		)
		SELECT $1, $2, 'text', 'text', 'info', 'dm', $3, 'cursor event ' || g, '{}'::jsonb, 'user_facing', now() - (g || ' seconds')::interval
		FROM generate_series(1, 2) AS g
	`, testWorkspaceID, agentID, dmSessionID); err != nil {
		t.Fatalf("insert cursor events: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_activity_event WHERE agent_id = $1`, agentID) })

	events := listAgentActivityEventsForUser(t, testUserID, agentID, "?limit=1")
	if len(events.resp.Events) != 1 || !events.resp.HasMore || events.resp.NextCursor == nil {
		t.Fatalf("events page = %+v, want one row with next cursor", events.resp)
	}
	cursor, err := decodeAgentActivityEventCursor(*events.resp.NextCursor)
	if err != nil {
		t.Fatalf("decode next cursor: %v", err)
	}
	if cursor.OccurredAt == "" {
		t.Fatalf("next cursor missing occurred_at: %+v", cursor)
	}
	if cursor.ID != events.resp.Events[0].ID {
		t.Fatalf("next cursor must point at last returned event: cursor=%+v event=%+v", cursor, events.resp.Events[0])
	}
	nextPage := listAgentActivityEventsForUser(t, testUserID, agentID, fmt.Sprintf("?limit=1&before=%s", url.QueryEscape(*events.resp.NextCursor)))
	if len(nextPage.resp.Events) != 1 || nextPage.resp.Events[0].ID == events.resp.Events[0].ID {
		t.Fatalf("next page did not advance by occurred_at/id cursor: first=%+v next=%+v", events.resp.Events, nextPage.resp.Events)
	}
}

func TestActivityVisibilityFor_SourceBackedLifecycle(t *testing.T) {
	if got := activityVisibilityFor(activityKindCompactionStarted, "compaction_started", "info", ""); got != "user_facing" {
		t.Fatalf("compaction_started visibility = %q, want user_facing", got)
	}
	if got := activityVisibilityFor(activityKindCustom, "subagent_started", "info", "auto_retry"); got != "user_facing" {
		t.Fatalf("subagent custom visibility = %q, want user_facing", got)
	}
	if got := activityVisibilityFor(activityKindCustom, agentInboxStatusChangedEventType, "info", ""); got != "user_facing" {
		t.Fatalf("agent status visibility = %q, want user_facing", got)
	}
	statusRow := agentActivityRawRow{
		Kind:      activityKindCustom,
		EventType: pgtype.Text{String: agentInboxStatusChangedEventType, Valid: true},
	}
	if !agentActivityTimelineRowIsNarrative(statusRow) {
		t.Fatalf("agent status changes must be included in the activity narrative")
	}
	for _, eventType := range []string{"radar_action_executed", "radar_action_failed"} {
		if got := activityVisibilityFor(activityKindCustom, eventType, "info", "create_issue"); got != "user_facing" {
			t.Fatalf("%s visibility = %q, want user_facing", eventType, got)
		}
		row := agentActivityRawRow{
			Kind:      activityKindCustom,
			EventType: pgtype.Text{String: eventType, Valid: true},
		}
		if !agentActivityTimelineRowIsNarrative(row) {
			t.Fatalf("%s must be included in the activity narrative", eventType)
		}
	}
	for _, eventType := range []string{
		"reminder_scheduled",
		"reminder_snoozed",
		"reminder_updated",
		"reminder_cancelled",
		"reminder_fired",
	} {
		if got := activityVisibilityFor(activityKindCustom, eventType, "info", ""); got != "user_facing" {
			t.Fatalf("%s visibility = %q, want user_facing", eventType, got)
		}
		row := agentActivityRawRow{
			Kind:      activityKindCustom,
			EventType: pgtype.Text{String: eventType, Valid: true},
		}
		if !agentActivityTimelineRowIsNarrative(row) {
			t.Fatalf("%s must be included in the activity narrative", eventType)
		}
	}
	if got := activityVisibilityFor(activityKindCustom, "radar_action_executed", "info", "no_action"); got != "diagnostic_only" {
		t.Fatalf("no_action Radar visibility = %q, want diagnostic_only", got)
	}
	if got := activityVisibilityFor(activityKindTransport, "runtime_progress", "info", ""); got != "diagnostic_only" {
		t.Fatalf("transport visibility = %q, want diagnostic_only", got)
	}
}

func TestAgentActivityDetailKindDefaultsToOther(t *testing.T) {
	if got := agentActivityDetailKind(pgtype.Text{}); got != "other" {
		t.Fatalf("missing detail kind = %q, want other", got)
	}
	if got := agentActivityDetailKind(pgtype.Text{String: "tool_use", Valid: true}); got != "tool_use" {
		t.Fatalf("detail kind = %q, want tool_use", got)
	}
}

func TestAgentActivityCanonicalToolName_UsesRaftAliases(t *testing.T) {
	tests := map[string]string{
		"Bash":                      "bash",
		"command_execution":         "bash",
		"run_terminal_command":      "bash",
		"run_shell_command":         "bash",
		"ReadFile":                  "read_file",
		"file_read":                 "read_file",
		"Write":                     "write_file",
		"create_file":               "write_file",
		"StrReplaceFile":            "edit_file",
		"mcp__chat__send_message":   "send_message",
		"mcp_chat_search_messages":  "search_messages",
		"read_messages":             "read_history",
		"add_channel_member":        "add_channel_member",
		"list_issues":               "list_issues",
		"comment_issue":             "comment_issue",
		"mcp__filesystem__ReadFile": "read_file",
		"SearchWeb":                 "web_search",
		"FetchURL":                  "web_fetch",
		"SetTodoList":               "todo_write",
		"unknown_provider_tool":     "unknown_provider_tool",
	}

	for raw, want := range tests {
		if got := agentActivityCanonicalToolName(raw); got != want {
			t.Fatalf("agentActivityCanonicalToolName(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestAgentActivityCanonicalToolName_CoversNarrativeActionMatrix(t *testing.T) {
	tests := map[string]string{
		"send_message":         "send_message",
		"message_send":         "send_message",
		"check_messages":       "check_messages",
		"wait_for_message":     "wait_for_message",
		"receive_message":      "receive_message",
		"read_messages":        "read_history",
		"read_history":         "read_history",
		"search_messages":      "search_messages",
		"list_server":          "list_server",
		"list_tasks":           "list_tasks",
		"create_tasks":         "create_tasks",
		"claim_tasks":          "claim_tasks",
		"unclaim_task":         "unclaim_task",
		"update_task_status":   "update_task_status",
		"add_channel_member":   "add_channel_member",
		"join_channel":         "join_channel",
		"leave_channel":        "leave_channel",
		"upload_file":          "upload_file",
		"view_file":            "view_file",
		"web_fetch":            "web_fetch",
		"schedule_reminder":    "schedule_reminder",
		"list_reminders":       "list_reminders",
		"cancel_reminder":      "cancel_reminder",
		"todo_write":           "todo_write",
		"collab_tool_call":     "collab_tool_call",
		"list_issues":          "list_issues",
		"get_issue":            "get_issue",
		"search_issues":        "search_issues",
		"list_issue_comments":  "list_issue_comments",
		"comment_issue":        "comment_issue",
		"delete_issue_comment": "delete_issue_comment",
	}

	for raw, want := range tests {
		got, known := agentActivityCanonicalToolNameKnown(raw)
		if !known || got != want {
			t.Fatalf("agentActivityCanonicalToolNameKnown(%q) = %q/%v, want %q/true", raw, got, known, want)
		}
	}
}

func TestAgentActivityTimelineEvent_AliasProducesSingleCanonicalEntry(t *testing.T) {
	tests := map[string]string{
		"message_send":  "send_message",
		"read_messages": "read_history",
		"FetchURL":      "web_fetch",
		"SetTodoList":   "todo_write",
	}

	for raw, want := range tests {
		t.Run(raw, func(t *testing.T) {
			details, err := json.Marshal(map[string]any{"tool": raw, "input": map[string]any{}})
			if err != nil {
				t.Fatalf("marshal details: %v", err)
			}
			event := agentActivityTimelineEvent(agentActivityRawRow{
				ID:      parseUUID("11111111-1111-1111-1111-111111111111"),
				AgentID: parseUUID("22222222-2222-2222-2222-222222222222"),
				Kind:    activityKindToolCall,
				Details: details,
			}, AgentActivityTargetRef{})
			if event.Tool == nil || *event.Tool != want {
				t.Fatalf("event tool = %+v, want %q", event.Tool, want)
			}
			if len(event.Entries) != 1 || event.Entries[0].Tool == nil || *event.Entries[0].Tool != want {
				t.Fatalf("entries = %+v, want one canonical %q entry", event.Entries, want)
			}
		})
	}
}

func TestTaskMessageCanonicalToolName_StatusLikeCommandIsBash(t *testing.T) {
	canonical, known := taskMessageCanonicalToolName("running", map[string]any{
		"command": "multica message send --target #multica --message-file hello_world.txt",
	})
	if !known {
		t.Fatalf("running command tool should be classified as a known shell command")
	}
	if canonical != "bash" {
		t.Fatalf("canonical tool = %q, want bash", canonical)
	}
}

func TestTaskMessageCanonicalToolName_UnknownToolStaysUnmapped(t *testing.T) {
	canonical, known := taskMessageCanonicalToolName("some_future_tool", map[string]any{
		"path": "hello_world.txt",
	})
	if known {
		t.Fatalf("unknown tool should remain unmapped, got %q", canonical)
	}
	if canonical != "some_future_tool" {
		t.Fatalf("canonical unknown tool = %q, want normalized raw slug", canonical)
	}
}

func TestResolveRaftCLIInvocation_MapsMessageCommands(t *testing.T) {
	invocation, ok := resolveRaftCLIInvocation("bash", map[string]any{
		"command": `API_KEY=secret sh -c 'multica message send --target #multica:98d5197a --message-file /tmp/reply.md'`,
	})
	if !ok {
		t.Fatalf("expected multica message send to resolve as raft cli invocation")
	}
	if invocation.Tool != "send_message" {
		t.Fatalf("tool = %q, want send_message", invocation.Tool)
	}
	if invocation.ToolTarget != "#multica:98d5197a" || invocation.SummaryKind != "message_target" {
		t.Fatalf("target/kind = %q/%q, want #multica:98d5197a/message_target", invocation.ToolTarget, invocation.SummaryKind)
	}
	if got := invocation.Details["command"]; got == "" {
		t.Fatalf("full redacted command must be retained for full activity detail: %+v", invocation.Details)
	}

	check, ok := resolveRaftCLIInvocation("bash", map[string]any{"command": "multica message check"})
	if !ok || check.Tool != "check_messages" || check.ToolTarget != "" || check.SummaryKind != "none" {
		t.Fatalf("message check invocation = %+v ok=%v, want check_messages without target", check, ok)
	}
}

func TestResolveRaftCLIInvocation_MapsStableActivityActions(t *testing.T) {
	tests := []struct {
		command     string
		tool        string
		toolTarget  string
		summaryKind string
	}{
		{command: "raft task create --target '#multica' --title test", tool: "create_tasks", toolTarget: "#multica", summaryKind: "message_target"},
		{command: "raft task unclaim --target '#multica' --number 601", tool: "unclaim_task", toolTarget: "#multica", summaryKind: "message_target"},
		{command: "raft channel add-member --target '#multica' --member @Barry", tool: "add_channel_member", toolTarget: "#multica", summaryKind: "message_target"},
		{command: "raft reminder list", tool: "list_reminders", summaryKind: "none"},
		{command: "raft reminder snooze --id abc123 --delay-seconds 60", tool: "snooze_reminder", summaryKind: "none"},
		{command: "raft reminder update --id abc123 --cadence every:2h", tool: "update_reminder", summaryKind: "none"},
		{command: "raft reminder cancel --id abc123", tool: "cancel_reminder", summaryKind: "none"},
		{command: "raft reminder log --id abc123", tool: "log_reminder", summaryKind: "none"},
		{command: "multica issue list --mine --output json", tool: "list_issues", summaryKind: "none"},
		{command: "multica issue get MUL-601 --output json", tool: "get_issue", toolTarget: "MUL-601", summaryKind: "issue"},
		{command: `multica issue search "activity fallback" --output json`, tool: "search_issues", toolTarget: "activity fallback", summaryKind: "query"},
		{command: "multica issue comment list MUL-601 --recent 20", tool: "list_issue_comments", toolTarget: "MUL-601", summaryKind: "issue"},
		{command: "multica issue comment add MUL-601 --content-stdin", tool: "comment_issue", toolTarget: "MUL-601", summaryKind: "issue"},
		{command: "multica issue comment delete deadbeef", tool: "delete_issue_comment", toolTarget: "deadbeef", summaryKind: "comment"},
	}

	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			got, ok := resolveRaftCLIInvocation("bash", map[string]any{"command": tt.command})
			if !ok {
				t.Fatalf("resolveRaftCLIInvocation(%q) did not resolve", tt.command)
			}
			if got.Tool != tt.tool || got.ToolTarget != tt.toolTarget || got.SummaryKind != tt.summaryKind {
				t.Fatalf("invocation = %+v, want tool=%q target=%q kind=%q", got, tt.tool, tt.toolTarget, tt.summaryKind)
			}
		})
	}
}

func TestAgentActivityTimelineRowIsNarrative_UnknownToolUsesSafeFallback(t *testing.T) {
	for _, details := range []string{
		`{"tool":"future_provider_action","input":{"secret":"must-not-be-rendered"}}`,
		`{"unmapped_tool_name":"future_provider_action"}`,
	} {
		row := agentActivityRawRow{
			Kind:    activityKindToolCall,
			Details: []byte(details),
		}
		if !agentActivityTimelineRowIsNarrative(row) {
			t.Fatalf("unknown tool call must reach the presentation fallback: %s", details)
		}
	}

	if agentActivityTimelineRowIsNarrative(agentActivityRawRow{Kind: activityKindToolCall, Details: []byte(`{}`)}) {
		t.Fatal("empty tool row must not become a generic narrative event")
	}
}

func TestAgentActivityTimelineEvent_UnknownToolDoesNotLeakRawDiagnostics(t *testing.T) {
	row := agentActivityRawRow{
		ID:      parseUUID("11111111-1111-1111-1111-111111111111"),
		AgentID: parseUUID("22222222-2222-2222-2222-222222222222"),
		Kind:    activityKindToolCall,
		Status:  pgtype.Text{String: "running", Valid: true},
		Message: pgtype.Text{String: "raw provider status", Valid: true},
		ReasonCode: pgtype.Text{
			String: "unmapped_tool_name",
			Valid:  true,
		},
		Details: []byte(`{
			"tool":"future_provider_action",
			"tool_target":"private-target",
			"summary_kind":"command",
			"command":"precomputed private command",
			"input":{
				"secret":"must-not-be-rendered",
				"command":"curl https://private.example/?token=sk_agent_should_not_leak"
			}
		}`),
	}
	event := agentActivityTimelineEvent(row, AgentActivityTargetRef{})
	if event.Tool != nil {
		t.Fatalf("unknown provider tool leaked into narrative tool field: %q", *event.Tool)
	}
	if event.ToolTarget != nil || event.Status != nil || event.Text != nil || event.ReasonCode != "" || len(event.Entries) != 0 {
		t.Fatalf("unknown provider diagnostics leaked into narrative fields: target=%v status=%v text=%v reason=%q entries=%+v", event.ToolTarget, event.Status, event.Text, event.ReasonCode, event.Entries)
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	for _, forbidden := range []string{
		"future_provider_action",
		"must-not-be-rendered",
		"private-target",
		"summary_kind",
		"precomputed private command",
		"raw provider status",
		"unmapped_tool_name",
		"curl https://private.example",
		"sk_agent_",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("unknown diagnostic %q leaked into narrative event: %s", forbidden, encoded)
		}
	}
}

func TestTaskMessageActivityTimelineEvent_UnmappedRealtimeToolUsesOnlyGenericNarrative(t *testing.T) {
	h := &Handler{}
	taskID := parseUUID("11111111-1111-1111-1111-111111111111")
	agentID := parseUUID("22222222-2222-2222-2222-222222222222")
	task := db.AgentInboxEvent{
		ID:      taskID,
		AgentID: agentID,
		Status:  "running",
	}
	message := db.TaskMessage{
		ID:     parseUUID("33333333-3333-3333-3333-333333333333"),
		TaskID: taskID,
		Seq:    7,
		Type:   "tool_use",
		Tool:   pgtype.Text{String: "future_provider_action", Valid: true},
		Input: []byte(`{
			"command":"curl https://private.example/?token=sk_agent_should_not_leak",
			"path":"/private/realtime-target"
		}`),
		CreatedAt:  pgtype.Timestamptz{Time: time.Unix(1_700_000_000, 0), Valid: true},
		Visibility: "user_facing",
	}

	event := h.taskMessageActivityTimelineEvent(context.Background(), "", task, message)
	if event == nil {
		t.Fatal("realtime builder returned nil event")
	}
	if event.Tool != nil || event.ToolTarget != nil || event.Status != nil || event.Text != nil || event.ReasonCode != "" || len(event.Entries) != 0 {
		t.Fatalf("unmapped realtime tool must serialize as generic narrative only: %+v", event)
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal realtime event: %v", err)
	}
	for _, forbidden := range []string{
		"future_provider_action",
		"unmapped_tool_name",
		"private/realtime-target",
		"curl https://private.example",
		"sk_agent_",
		"running",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("unmapped realtime diagnostic %q leaked into narrative event: %s", forbidden, encoded)
		}
	}
}

func TestTaskMessageActivityTimelineEvent_MappedRealtimeCommandKeepsSemanticEntry(t *testing.T) {
	h := &Handler{}
	taskID := parseUUID("44444444-4444-4444-4444-444444444444")
	task := db.AgentInboxEvent{
		ID:      taskID,
		AgentID: parseUUID("55555555-5555-5555-5555-555555555555"),
		Status:  "running",
	}
	message := db.TaskMessage{
		ID:         parseUUID("66666666-6666-6666-6666-666666666666"),
		TaskID:     taskID,
		Seq:        8,
		Type:       "tool_use",
		Tool:       pgtype.Text{String: "bash", Valid: true},
		Input:      []byte(`{"command":"multica issue list --mine --output json"}`),
		CreatedAt:  pgtype.Timestamptz{Time: time.Unix(1_700_000_001, 0), Valid: true},
		Visibility: "user_facing",
	}

	event := h.taskMessageActivityTimelineEvent(context.Background(), "", task, message)
	if event == nil || event.Tool == nil || *event.Tool != "list_issues" {
		t.Fatalf("mapped realtime CLI command lost semantic tool: %+v", event)
	}
	if event.Status == nil || *event.Status != "running" || len(event.Entries) != 1 || event.Entries[0].Tool == nil || *event.Entries[0].Tool != "list_issues" {
		t.Fatalf("mapped realtime CLI command lost status/entry projection: %+v", event)
	}
}

func TestTaskMessageActivityTimelineEvent_CheckMessagesHidesOnlyTransportCommand(t *testing.T) {
	h := &Handler{}
	taskID := parseUUID("77777777-7777-7777-7777-777777777777")
	task := db.AgentInboxEvent{
		ID:      taskID,
		AgentID: parseUUID("88888888-8888-8888-8888-888888888888"),
		Status:  "running",
	}
	tests := []struct {
		name        string
		messageID   string
		rawTool     string
		command     string
		wantTool    string
		wantCommand bool
	}{
		{
			name:      "inbox polling is semantic only",
			messageID: "90000000-0000-0000-0000-000000000001",
			command:   "raft message check",
			wantTool:  "check_messages",
		},
		{
			name:      "receive alias is semantic only",
			messageID: "90000000-0000-0000-0000-000000000002",
			rawTool:   "receive_message",
			command:   "raft message receive",
			wantTool:  "receive_message",
		},
		{
			name:      "wait alias is semantic only",
			messageID: "90000000-0000-0000-0000-000000000003",
			rawTool:   "wait_for_message",
			command:   "raft message wait",
			wantTool:  "wait_for_message",
		},
		{
			name:        "issue list keeps its real command",
			messageID:   "90000000-0000-0000-0000-000000000004",
			command:     "multica issue list --mine --output json",
			wantTool:    "list_issues",
			wantCommand: true,
		},
		{
			name:        "ordinary shell keeps its real command",
			messageID:   "90000000-0000-0000-0000-000000000005",
			command:     "printf 'TASK601_LIVE'",
			wantTool:    "bash",
			wantCommand: true,
		},
		{
			name:        "message send keeps its real command",
			messageID:   "90000000-0000-0000-0000-000000000006",
			command:     "multica message send --target dm:@andong3 --message-stdin",
			wantTool:    "send_message",
			wantCommand: true,
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rawTool := tt.rawTool
			if rawTool == "" {
				rawTool = "shell"
			}
			message := db.TaskMessage{
				ID:         parseUUID(tt.messageID),
				TaskID:     taskID,
				Seq:        int32(i + 1),
				Type:       "tool_use",
				Tool:       pgtype.Text{String: rawTool, Valid: true},
				Input:      []byte(fmt.Sprintf(`{"command":%q}`, tt.command)),
				CreatedAt:  pgtype.Timestamptz{Time: time.Unix(1_700_000_100+int64(i), 0), Valid: true},
				Visibility: "user_facing",
			}

			event := h.taskMessageActivityTimelineEvent(context.Background(), "", task, message)
			if event == nil || event.Tool == nil || *event.Tool != tt.wantTool || len(event.Entries) != 1 {
				t.Fatalf("event = %+v, want one %q entry", event, tt.wantTool)
			}
			gotCommand := event.Entries[0].Command
			if tt.wantCommand {
				if gotCommand == nil || *gotCommand != tt.command {
					t.Fatalf("command = %v, want %q", gotCommand, tt.command)
				}
			} else if gotCommand != nil {
				t.Fatalf("transport command leaked into narrative entry: %q", *gotCommand)
			}
		})
	}
}

func TestAgentActivitySafeToolTargetForTool_FileToolsUseSourceBackedPath(t *testing.T) {
	tests := []struct {
		name string
		tool string
		key  string
		path string
		want string
	}{
		{
			name: "write file absolute path",
			tool: "write_file",
			key:  "path",
			path: "/Users/frank/Code/multica/server/internal/handler/activity.go",
			want: "/Users/frank/Code/multica/server/internal/handler/activity.go",
		},
		{
			name: "edit file relative path",
			tool: "edit_file",
			key:  "path",
			path: "server/internal/handler/activity.go",
			want: "server/internal/handler/activity.go",
		},
		{
			name: "read file path from alternate key",
			tool: "read_file",
			key:  "file_path",
			path: "/tmp/activity-event.ts",
			want: "/tmp/activity-event.ts",
		},
		{
			name: "read file path from runtime camel case key",
			tool: "read_file",
			key:  "filePath",
			path: "/tmp/activity-event.ts",
			want: "/tmp/activity-event.ts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, kind := agentActivitySafeToolTargetForTool(tt.tool, map[string]any{tt.key: tt.path})
			if got != tt.want || kind != "file_path" {
				t.Fatalf("target=(%q,%q), want (%q,file_path)", got, kind, tt.want)
			}
		})
	}
}

func TestAgentActivitySafeToolTargetForTool_NonFileToolsKeepSafeSummary(t *testing.T) {
	shellTarget, shellKind := agentActivitySafeToolTargetForTool("bash", map[string]any{
		"command": "cat /tmp/secret.txt",
		"path":    "/Users/frank/Code/multica/private/secret.txt",
	})
	if shellTarget != "cat /tmp/secret.txt" || shellKind != "command" {
		t.Fatalf("shell target=(%q,%q), want raft command summary", shellTarget, shellKind)
	}
	if strings.Contains(shellTarget, "/Users/frank/Code") {
		t.Fatalf("shell target leaked raw path/command: %q", shellTarget)
	}

	unknownTarget, unknownKind := agentActivitySafeToolTargetForTool("", map[string]any{
		"path": "/Users/frank/Code/multica/private/future.txt",
	})
	if unknownTarget != "" || unknownKind != "" {
		t.Fatalf("unknown target=(%q,%q), want no invented target", unknownTarget, unknownKind)
	}

	argvTarget, argvKind := agentActivitySafeToolTargetForTool("", map[string]any{
		"command": "git status --short",
	})
	if argvTarget != "" || argvKind != "" {
		t.Fatalf("unknown command target=(%q,%q), want no argv0-derived target", argvTarget, argvKind)
	}
}

func TestAgentActivityToolInputSummaryForTool_UsesRaftLikeToolInputWithoutInventingTarget(t *testing.T) {
	globSummary := agentActivityToolInputSummaryForTool("glob", map[string]any{
		"path": "multica",
	})
	if globSummary.ToolTarget != "" || globSummary.SummaryKind != "" {
		t.Fatalf("plain glob path target=(%q,%q), want no invented target", globSummary.ToolTarget, globSummary.SummaryKind)
	}

	patternSummary := agentActivityToolInputSummaryForTool("glob", map[string]any{
		"pattern": "server/internal/**/*.go",
		"cwd":     "/Users/frank/Code/multica",
	})
	if patternSummary.ToolTarget != "server/internal/**/*.go" || patternSummary.SummaryKind != "pattern" {
		t.Fatalf("glob pattern summary = %+v, want pattern target", patternSummary)
	}

	grepSummary := agentActivityToolInputSummaryForTool("grep", map[string]any{
		"query": "agentActivityToolInputSummary",
		"path":  "server/internal/handler",
	})
	if grepSummary.ToolTarget != "agentActivityToolInputSummary" || grepSummary.SummaryKind != "query" {
		t.Fatalf("grep query summary = %+v, want query target", grepSummary)
	}

	readSummary := agentActivityToolInputSummaryForTool("read_file", map[string]any{
		"path": "/Users/frank/Code/multica/server/internal/handler/agent_activity.go",
	})
	if readSummary.ToolTarget != "/Users/frank/Code/multica/server/internal/handler/agent_activity.go" || readSummary.SummaryKind != "file_path" {
		t.Fatalf("read_file summary = %+v, want full path target", readSummary)
	}

	bashSummary := agentActivityToolInputSummaryForTool("bash", map[string]any{
		"command": "cat /tmp/secret.txt",
		"path":    "/Users/frank/Code/multica/private/secret.txt",
	})
	if bashSummary.ToolTarget != "cat /tmp/secret.txt" || bashSummary.SummaryKind != "command" {
		t.Fatalf("bash summary = %+v, want command target", bashSummary)
	}
}

func TestAgentActivityApplyToolSourceFacts_PreservesSourceFactsWithoutInventingCommands(t *testing.T) {
	readDetails := map[string]any{}
	agentActivityApplyToolSourceFacts(readDetails, "read", "read_file", map[string]any{
		"filePath": "/tmp/test.go",
		"basePath": "/repo",
	})
	if readDetails["command"] != nil || readDetails["path"] != "/tmp/test.go" || readDetails["scope"] != "/repo" {
		t.Fatalf("read source facts = %+v, want path/scope without invented command", readDetails)
	}

	globDetails := map[string]any{}
	agentActivityApplyToolSourceFacts(globDetails, "glob", "glob", map[string]any{
		"pattern": "server/internal/**/*.go",
		"cwd":     "/Users/frank/Code/multica",
	})
	if globDetails["command"] != nil || globDetails["pattern"] != "server/internal/**/*.go" || globDetails["scope"] != "/Users/frank/Code/multica" {
		t.Fatalf("glob source facts = %+v, want pattern/scope without invented command", globDetails)
	}

	bashDetails := map[string]any{}
	agentActivityApplyToolSourceFacts(bashDetails, "terminal", "bash", map[string]any{
		"command": "cat /tmp/secret.txt",
		"path":    "/tmp/secret.txt",
	})
	if bashDetails["command"] != "cat /tmp/secret.txt" || bashDetails["path"] != nil {
		t.Fatalf("bash source facts = %+v, want explicit command without path-backed shell target", bashDetails)
	}
}

func TestReportTaskMessagesPublishesHydratedScopedActivityEvent(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	memberID := createWorkspaceMemberUser(t, "Activity Realtime Member", "activity-realtime-"+randomID()+"@multica.test")
	agentID := createWorkspaceVisibleActivityAgent(t, "activity-realtime-agent")
	_, channelSessionID := createActivityChannelSession(t, agentID, memberID)
	taskID := createActivityRunTask(t, agentID, channelSessionID, "running", "channel realtime work")

	var captured *events.Event
	testHandler.Bus.Subscribe(protocol.EventAgentActivityEvent, func(e events.Event) {
		payload, ok := e.Payload.(AgentActivityEventRealtimePayload)
		if !ok || payload.AgentID != agentID || payload.Event == nil || payload.Event.EventType != "thinking" {
			return
		}
		copy := e
		captured = &copy
	})

	req := newRequest(http.MethodPost, "/api/daemon/tasks/"+taskID+"/messages", map[string]any{
		"messages": []map[string]any{{
			"seq":     7,
			"type":    "thinking",
			"content": "Realtime aggregate thinking",
		}},
	})
	req = withChatTestWorkspaceCtx(t, req)
	req = withURLParam(req, "taskId", taskID)
	w := httptest.NewRecorder()
	testHandler.ReportTaskMessages(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ReportTaskMessages: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if captured != nil {
		t.Fatalf("thinking must not publish an Activity realtime event: %+v", captured.Payload)
	}
}

func TestReportTaskMessagesRecordsCompactionAsActivityOnly(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	memberID := createWorkspaceMemberUser(t, "Compaction Activity Member", "compaction-activity-"+randomID()+"@multica.test")
	agentID := createWorkspaceVisibleActivityAgent(t, "compaction-activity-agent")
	_, channelSessionID := createActivityChannelSession(t, agentID, memberID)
	taskID := createActivityRunTask(t, agentID, channelSessionID, "running", "compaction lifecycle work")

	var captured *events.Event
	testHandler.Bus.Subscribe(protocol.EventAgentActivityEvent, func(e events.Event) {
		payload, ok := e.Payload.(AgentActivityEventRealtimePayload)
		if !ok || payload.AgentID != agentID || payload.Event == nil || payload.Event.EventType != "compaction_started" {
			return
		}
		copy := e
		captured = &copy
	})

	req := newRequest(http.MethodPost, "/api/daemon/tasks/"+taskID+"/messages", map[string]any{
		"messages": []map[string]any{{"seq": 8, "type": "compaction_started"}},
	})
	req = withChatTestWorkspaceCtx(t, req)
	req = withURLParam(req, "taskId", taskID)
	w := httptest.NewRecorder()
	testHandler.ReportTaskMessages(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ReportTaskMessages: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if captured == nil || captured.Payload.(AgentActivityEventRealtimePayload).Event.Text == nil || *captured.Payload.(AgentActivityEventRealtimePayload).Event.Text != "Compacting context" {
		t.Fatalf("compaction realtime Activity = %+v, want canonical lifecycle event", captured)
	}

	var kind, eventType, message, visibility string
	if err := testPool.QueryRow(context.Background(), `
		SELECT event_kind, event_type, message, visibility
		FROM agent_activity_event
		WHERE task_id = $1 AND event_type = 'compaction_started'
		ORDER BY created_at DESC
		LIMIT 1`, taskID).Scan(&kind, &eventType, &message, &visibility); err != nil {
		t.Fatalf("load compaction Activity event: %v", err)
	}
	if kind != activityKindCompactionStarted || eventType != "compaction_started" || message != "Compacting context" || visibility != "user_facing" {
		t.Fatalf("compaction Activity row = kind=%q type=%q message=%q visibility=%q", kind, eventType, message, visibility)
	}
}

func TestReportTaskMessagesUnmappedToolIsDiagnosticAndEmitsGap(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	memberID := createWorkspaceMemberUser(t, "Activity Unmapped Member", "activity-unmapped-"+randomID()+"@multica.test")
	agentID := createWorkspaceVisibleActivityAgent(t, "activity-unmapped-agent")
	_, channelSessionID := createActivityChannelSession(t, agentID, memberID)
	taskID := createActivityRunTask(t, agentID, channelSessionID, "running", "channel unmapped tool work")

	var captured *events.Event
	testHandler.Bus.Subscribe(protocol.EventAgentActivityEvent, func(e events.Event) {
		payload, ok := e.Payload.(AgentActivityEventRealtimePayload)
		if !ok || payload.AgentID != agentID || payload.Event == nil || payload.Event.EventType != "unmapped_tool_name" {
			return
		}
		copy := e
		captured = &copy
	})

	req := newRequest(http.MethodPost, "/api/daemon/tasks/"+taskID+"/messages", map[string]any{
		"messages": []map[string]any{{
			"seq":  9,
			"type": "tool_use",
			"tool": "some_future_tool",
			"input": map[string]any{
				"path": "hello_world.txt",
			},
		}},
	})
	req = withChatTestWorkspaceCtx(t, req)
	req = withURLParam(req, "taskId", taskID)
	w := httptest.NewRecorder()
	testHandler.ReportTaskMessages(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ReportTaskMessages: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var visibility string
	if err := testPool.QueryRow(context.Background(), `
		SELECT visibility
		FROM task_message
		WHERE task_id = $1 AND seq = 9
	`, parseUUID(taskID)).Scan(&visibility); err != nil {
		t.Fatalf("lookup task message visibility: %v", err)
	}
	if visibility != "diagnostic_only" {
		t.Fatalf("unmapped tool task message visibility = %q, want diagnostic_only", visibility)
	}

	if captured == nil {
		t.Fatal("unmapped_tool_name gap event was not published")
	}
	payload := captured.Payload.(AgentActivityEventRealtimePayload)
	if payload.Event.Kind != activityKindCustom || payload.Event.Visibility != "diagnostic_only" || payload.Event.ReasonCode != "unmapped_tool_name" {
		t.Fatalf("unmapped gap event contract wrong: %+v", payload.Event)
	}
	if payload.Event.Tool != nil {
		t.Fatalf("unmapped gap event must not expose a user-facing tool label: %+v", payload.Event.Tool)
	}
	if !hasSourceSeq(payload.Event.SourceRefs, "seq", 9) {
		t.Fatalf("unmapped gap source refs missing seq: %+v", payload.Event.SourceRefs)
	}
}

func TestRecordAgentActivityEventPublishesHydratedRealtimeEvent(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID := createWorkspaceVisibleActivityAgent(t, "activity-realtime-record-agent")
	var captured *events.Event
	testHandler.Bus.Subscribe(protocol.EventAgentActivityEvent, func(e events.Event) {
		payload, ok := e.Payload.(AgentActivityEventRealtimePayload)
		if !ok || payload.AgentID != agentID || payload.Event == nil || payload.Event.EventType != "agent_inbox_failed" {
			return
		}
		copy := e
		captured = &copy
	})

	testHandler.recordAgentActivityEvent(
		context.Background(),
		testHandler.DB,
		parseUUID(testWorkspaceID),
		parseUUID(agentID),
		pgtype.UUID{},
		pgtype.UUID{},
		activityKindError,
		"agent_inbox_failed",
		"error",
		"agent",
		parseUUID(agentID),
		"",
		"grok_first_turn_no_progress",
		"Runtime stopped before first output.",
		map[string]any{"agent_session_id": "session-1"},
	)

	if captured == nil {
		t.Fatal("agent_activity:event was not published with a hydrated agent_activity_event row")
	}
	payload := captured.Payload.(AgentActivityEventRealtimePayload)
	if payload.EventID == "" || payload.Event.ID != payload.EventID {
		t.Fatalf("payload event id mismatch: %+v", payload)
	}
	if payload.Event.Kind != activityKindError || payload.Event.ReasonCode != "grok_first_turn_no_progress" {
		t.Fatalf("hydrated event reason contract wrong: %+v", payload.Event)
	}
	if payload.Event.TargetRef.Kind != "agent" || payload.Event.TargetRef.ID == nil || *payload.Event.TargetRef.ID != agentID {
		t.Fatalf("hydrated event target ref = %+v, want agent %s", payload.Event.TargetRef, agentID)
	}
	if !hasSourceID(payload.Event.SourceRefs, "agent_session", "session-1") {
		t.Fatalf("hydrated event source refs missing agent_session: %+v", payload.Event.SourceRefs)
	}
	if captured.RecipientUserIDs != nil {
		t.Fatalf("agent-scope activity event should keep workspace fanout, got recipients %+v", captured.RecipientUserIDs)
	}
}

type agentActivityListResult struct {
	resp AgentActivityPageResponse
	raw  string
}

type agentActivityEventsResult struct {
	resp AgentActivityEventsPageResponse
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
	raw := w.Body.String()
	var resp AgentActivityPageResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("decode activity response: %v", err)
	}
	return agentActivityListResult{resp: resp, raw: raw}
}

func listAgentActivityEventsForUser(t *testing.T, userID, agentID, query string) agentActivityEventsResult {
	t.Helper()
	w := httptest.NewRecorder()
	path := "/api/agents/" + agentID + "/activity/events" + query
	req := withURLParam(newRequestAs(userID, http.MethodGet, path, nil), "id", agentID)
	testHandler.ListAgentActivityEvents(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListAgentActivityEvents(%s): expected 200, got %d: %s", userID, w.Code, w.Body.String())
	}
	raw := w.Body.String()
	var resp AgentActivityEventsPageResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("decode activity events response: %v", err)
	}
	return agentActivityEventsResult{resp: resp, raw: raw}
}

func assertActivityEventsOmitTopLevelLegacyFields(t *testing.T, raw string) {
	t.Helper()
	var body struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decode activity events as map: %v", err)
	}
	for _, event := range body.Events {
		for _, legacyField := range []string{"kind", "event_type", "visibility"} {
			if _, ok := event[legacyField]; ok {
				t.Fatalf("activity events must not serialize top-level legacy field %s: %s", legacyField, raw)
			}
		}
	}
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

func requireActivityTimelineEvent(t *testing.T, list agentActivityEventsResult, id string) AgentActivityTimelineEvent {
	t.Helper()
	item := findActivityTimelineEvent(list, id)
	if item == nil {
		t.Fatalf("activity event %s not found in %+v", id, list.resp.Events)
	}
	return *item
}

func findActivityTimelineEvent(list agentActivityEventsResult, id string) *AgentActivityTimelineEvent {
	for i := range list.resp.Events {
		if list.resp.Events[i].ID == id {
			return &list.resp.Events[i]
		}
	}
	return nil
}

func assertActivityTimelineDetail(t *testing.T, event AgentActivityTimelineEvent, key string, want any) {
	t.Helper()
	got, ok := event.Details[key]
	if !ok {
		t.Fatalf("%s details missing %q: %+v", event.DetailKind, key, event.Details)
	}
	if got != want {
		t.Fatalf("%s details[%q] = %#v, want %#v; all details=%+v", event.DetailKind, key, got, want, event.Details)
	}
}

func hasSourceID(refs []AgentActivitySourceRef, kind, id string) bool {
	for _, ref := range refs {
		if ref.Kind == kind && ref.ID == id {
			return true
		}
	}
	return false
}

func hasSourceSeq(refs []AgentActivitySourceRef, kind string, seq int64) bool {
	for _, ref := range refs {
		if ref.Kind == kind && ref.Seq != nil && *ref.Seq == seq {
			return true
		}
	}
	return false
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
			workspace_id, name, description, runtime_mode, runtime_config, runtime_id, max_concurrent_tasks, owner_id, instructions, custom_env, custom_args, mcp_config
		, model) VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 1, $4, '', '{}'::jsonb, '[]'::jsonb, '{}'::jsonb, 'composer-1.5')
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
		INSERT INTO agent_inbox_event (
			agent_id, runtime_id, chat_session_id, status, priority, trigger_summary,
			created_at, started_at
		)
		VALUES ($1, $2, $3, $4, 0, $5, now(), now())
		RETURNING id
	`, agentID, handlerTestRuntimeID(t), chatArg, status, summary).Scan(&taskID); err != nil {
		t.Fatalf("create activity task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, taskID) })
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
	`, testWorkspaceID, "activity-"+randomID(), memberID).Scan(&channelID); err != nil {
		t.Fatalf("create activity channel: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID) })
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'user', $3)

ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, memberID); err != nil {
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
		VALUES ($1, $2, 'custom', $3, 'info', $4, $5, 'test_reason', 'safe event', $6::jsonb)
		RETURNING id
	`, testWorkspaceID, agentID, eventType, targetKind, targetID, details).Scan(&eventID); err != nil {
		t.Fatalf("create activity event: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_activity_event WHERE id = $1`, eventID) })
	return eventID
}

func TestActivityVisibilityReminderOverdueIsUserFacing(t *testing.T) {
	if got := activityVisibilityFor(activityKindCustom, "reminder_overdue", "warning", "reminder_overdue"); got != "user_facing" {
		t.Fatalf("reminder_overdue visibility = %q, want user_facing", got)
	}
}
