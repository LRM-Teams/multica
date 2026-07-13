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
	"github.com/multica-ai/multica/server/pkg/protocol"
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

func TestAgentActivity_ListKeepsRecentEventsVisibleWithLegacyRunHistory(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createWorkspaceVisibleActivityAgent(t, "activity-feed-history-agent")
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, status, priority, trigger_summary,
			created_at, started_at, completed_at
		)
		SELECT
			$1, $2, 'completed', 0, 'legacy run ' || g,
			now() - interval '2 hours' - (g || ' seconds')::interval,
			now() - interval '2 hours' - (g || ' seconds')::interval,
			now() - interval '2 hours' - (g || ' seconds')::interval
		FROM generate_series(1, 120) AS g
	`, agentID, handlerTestRuntimeID(t)); err != nil {
		t.Fatalf("seed legacy activity runs: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE agent_id = $1`, agentID) })

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

	outsiderID := createWorkspaceMemberUser(t, "Activity Events Outsider", "activity-events-outsider-"+randomID()+"@multica.test")
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
	thinking := requireActivityTimelineEvent(t, ownerEvents, thinkingID)
	if thinking.ActivityKind != activityKindThinking || thinking.DetailKind != "thinking" {
		t.Fatalf("thinking event activity/detail kind = %q/%q", thinking.ActivityKind, thinking.DetailKind)
	}
	if thinking.Text == nil || *thinking.Text != "thinking aggregate text" {
		t.Fatalf("thinking text not surfaced as ordered aggregate text: %+v", thinking)
	}
	if len(thinking.Entries) != 1 || thinking.Entries[0].Kind != activityKindThinking || thinking.Entries[0].Text == nil || *thinking.Entries[0].Text != "thinking aggregate text" {
		t.Fatalf("thinking entries missing raft-style text entry: %+v", thinking.Entries)
	}
	if thinking.TaskID != nil {
		t.Fatalf("chat task must not expose issue task_id deep link: %+v", thinking.TaskID)
	}
	if !hasSourceSeq(thinking.SourceRefs, "seq", 1) || !hasSourceID(thinking.SourceRefs, "task_message", thinkingID) {
		t.Fatalf("thinking source refs missing task message/seq: %+v", thinking.SourceRefs)
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
		t.Fatalf("non-creator must not see owner DM task-message event: %+v", *got)
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
	noActionID, ok := insertAgentActivityEvent(
		ctx, testPool, parseUUID(testWorkspaceID), parseUUID(agentID), pgtype.UUID{}, pgtype.UUID{},
		activityKindCustom, "radar_action_executed", "info",
		"agent", parseUUID(agentID), "", "no_action", "Radar executed: no_action", nil,
	)
	if !ok {
		t.Fatal("insert no_action Radar activity event")
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
	requireActivityTimelineEvent(t, events, thinkingID)
	requireActivityTimelineEvent(t, events, uuidToString(executedID))
	requireActivityTimelineEvent(t, events, uuidToString(failedID))
	if got := findActivityTimelineEvent(events, uuidToString(noActionID)); got != nil {
		t.Fatalf("no_action Radar event leaked into user narrative: %+v", *got)
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
		SELECT $1, $2, 'thinking', 'thinking', 'info', 'dm', $3, 'cursor event ' || g, '{}'::jsonb, 'user_facing', now() - (g || ' seconds')::interval
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
		"StrReplaceFile":            "edit_file",
		"mcp__chat__send_message":   "send_message",
		"mcp_chat_search_messages":  "search_messages",
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

func TestTaskMessageCanonicalToolName_StatusLikeCommandIsBash(t *testing.T) {
	canonical, known := taskMessageCanonicalToolName("running", map[string]any{
		"command": "multica message send --message-file hello_world.txt",
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

	check, ok := resolveRaftCLIInvocation("bash", map[string]any{"command": "raft message check"})
	if !ok || check.Tool != "check_messages" || check.ToolTarget != "" || check.SummaryKind != "none" {
		t.Fatalf("message check invocation = %+v ok=%v, want check_messages without target", check, ok)
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
		"command": "multica repo checkout https://github.com/LRM-Teams/multica.git",
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
	channelID, channelSessionID := createActivityChannelSession(t, agentID, memberID)
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
	if captured == nil {
		t.Fatal("agent_activity:event was not published with a hydrated thinking event")
	}
	payload := captured.Payload.(AgentActivityEventRealtimePayload)
	if payload.EventID == "" || payload.Event.ID != payload.EventID {
		t.Fatalf("payload event id mismatch: %+v", payload)
	}
	if payload.Event.Kind != activityKindThinking || payload.Event.Text == nil || *payload.Event.Text != "Realtime aggregate thinking" {
		t.Fatalf("hydrated event missing thinking payload: %+v", payload.Event)
	}
	if payload.Event.TargetRef.Kind != "channel" || payload.Event.TargetRef.ID == nil || *payload.Event.TargetRef.ID != channelID {
		t.Fatalf("hydrated event target ref = %+v, want channel %s", payload.Event.TargetRef, channelID)
	}
	if len(captured.RecipientUserIDs) != 1 || captured.RecipientUserIDs[0] != memberID {
		t.Fatalf("channel activity realtime must be recipient-scoped to channel users, got %+v want [%s]", captured.RecipientUserIDs, memberID)
	}
	if payload.Event.TaskID != nil {
		t.Fatalf("chat/channel task-message event must not expose issue task_id deep link: %+v", payload.Event.TaskID)
	}
	if !hasSourceSeq(payload.Event.SourceRefs, "seq", 7) || !hasSourceID(payload.Event.SourceRefs, "task_message", payload.EventID) {
		t.Fatalf("hydrated event source refs missing task_message/seq: %+v", payload.Event.SourceRefs)
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
		VALUES ($1, $2, 'custom', $3, 'info', $4, $5, 'test_reason', 'safe event', $6::jsonb)
		RETURNING id
	`, testWorkspaceID, agentID, eventType, targetKind, targetID, details).Scan(&eventID); err != nil {
		t.Fatalf("create activity event: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_activity_event WHERE id = $1`, eventID) })
	return eventID
}
