package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestCancelChannelAgentInboxEvent_CancelsSingleWake(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "CancelChannelInboxSingle", []byte("[]"))
	channelID := seedChannelForTest(t, "cancel-channel-inbox-single-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	root := dispatchThreadMentionForTest(t, channelID, agentID, "cancel-channel-inbox-single-"+uuid.NewString())
	eventID := latestChannelAgentInboxEventForRootForTest(t, root.ID, agentID)

	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_event_delivery (
			workspace_id, agent_session_id, inbox_event_id, runtime_id, status
		)
		SELECT workspace_id, agent_session_id, id, $2, 'leased'
		FROM agent_inbox_event
		WHERE id = $1`, eventID, handlerTestRuntimeID(t)); err != nil {
		t.Fatalf("seed inbox delivery: %v", err)
	}

	req := newRequestAs(testUserID, http.MethodPost,
		"/api/channels/"+channelID+"/agent-inbox/events/"+eventID+"/cancel", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParams(req, "channelId", channelID, "eventId", eventID)
	rec := httptest.NewRecorder()
	testHandler.CancelChannelAgentInboxEvent(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel single: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp cancelChannelInboxEventResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode cancel single: %v", err)
	}
	if !resp.OK || resp.InboxEventID != eventID || resp.AgentID != agentID || resp.Status != "cancelled" {
		t.Fatalf("cancel single response = %+v", resp)
	}

	var eventStatus, terminalOutcome string
	if err := testPool.QueryRow(ctx, `
		SELECT status, COALESCE(terminal_outcome, '')
		FROM agent_inbox_event WHERE id = $1`, eventID).Scan(&eventStatus, &terminalOutcome); err != nil {
		t.Fatalf("load cancelled event: %v", err)
	}
	if eventStatus != "suppressed" || terminalOutcome != "no_reply" {
		t.Fatalf("event status=%q outcome=%q", eventStatus, terminalOutcome)
	}
}

func TestCancelChannelActiveAgentInboxEvents_BulkStopsAll(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentA := createHandlerTestAgent(t, "CancelChannelInboxBulkA", []byte("[]"))
	agentB := createHandlerTestAgent(t, "CancelChannelInboxBulkB", []byte("[]"))
	channelID := seedChannelForTest(t, "cancel-channel-inbox-bulk-"+uuid.NewString(), testUserID)
	for _, agentID := range []string{agentA, agentB} {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
			VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
			t.Fatalf("seed agent member: %v", err)
		}
	}

	rootA := dispatchThreadMentionForTest(t, channelID, agentA, "cancel-bulk-a-"+uuid.NewString())
	rootB := dispatchThreadMentionForTest(t, channelID, agentB, "cancel-bulk-b-"+uuid.NewString())
	eventA := latestChannelAgentInboxEventForRootForTest(t, rootA.ID, agentA)
	eventB := latestChannelAgentInboxEventForRootForTest(t, rootB.ID, agentB)

	req := newRequestAs(testUserID, http.MethodPost,
		"/api/channels/"+channelID+"/agent-inbox/cancel-active", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParams(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.CancelChannelActiveAgentInboxEvents(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel-active: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp cancelChannelActiveInboxResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode cancel-active: %v", err)
	}
	if !resp.OK || resp.CancelledCount < 2 {
		t.Fatalf("cancel-active response = %+v, want >=2 cancelled", resp)
	}
	got := map[string]bool{}
	for _, id := range resp.CancelledInboxEventIDs {
		got[id] = true
	}
	if !got[eventA] || !got[eventB] {
		t.Fatalf("cancelled ids=%v, want %s and %s", resp.CancelledInboxEventIDs, eventA, eventB)
	}

	activeReq := withURLParam(newRequest(http.MethodGet, "/api/channels/"+channelID+"/active-tasks", nil), "channelId", channelID)
	activeReq = withChannelTestWorkspaceCtx(t, activeReq, testUserID)
	activeRec := httptest.NewRecorder()
	testHandler.ListChannelActiveTasks(activeRec, activeReq)
	if activeRec.Code != http.StatusOK {
		t.Fatalf("list active after bulk: status=%d body=%s", activeRec.Code, activeRec.Body.String())
	}
	var activeResp ChannelActiveTasksResponse
	if err := json.NewDecoder(activeRec.Body).Decode(&activeResp); err != nil {
		t.Fatalf("decode active tasks: %v", err)
	}
	for _, task := range activeResp.Tasks {
		if task.TaskID == eventA || task.TaskID == eventB {
			t.Fatalf("cancelled events still in active-tasks: %+v", activeResp.Tasks)
		}
	}

	// Idempotent second call — nothing left to cancel.
	req2 := newRequestAs(testUserID, http.MethodPost,
		"/api/channels/"+channelID+"/agent-inbox/cancel-active", nil)
	req2 = withChannelTestWorkspaceCtx(t, req2, testUserID)
	req2 = withURLParams(req2, "channelId", channelID)
	rec2 := httptest.NewRecorder()
	testHandler.CancelChannelActiveAgentInboxEvents(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("cancel-active empty: status=%d body=%s", rec2.Code, rec2.Body.String())
	}
	var resp2 cancelChannelActiveInboxResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("decode cancel-active empty: %v", err)
	}
	if !resp2.OK || resp2.CancelledCount != 0 {
		t.Fatalf("second cancel-active = %+v, want zero cancelled", resp2)
	}
}

func TestCancelChannelAgentInboxEvent_WrongChannelReturns404(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "CancelChannelInboxWrongCh", []byte("[]"))
	channelA := seedChannelForTest(t, "cancel-wrong-a-"+uuid.NewString(), testUserID)
	channelB := seedChannelForTest(t, "cancel-wrong-b-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelA, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	root := dispatchThreadMentionForTest(t, channelA, agentID, "cancel-wrong-channel-"+uuid.NewString())
	eventID := latestChannelAgentInboxEventForRootForTest(t, root.ID, agentID)

	req := newRequestAs(testUserID, http.MethodPost,
		"/api/channels/"+channelB+"/agent-inbox/events/"+eventID+"/cancel", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParams(req, "channelId", channelB, "eventId", eventID)
	rec := httptest.NewRecorder()
	testHandler.CancelChannelAgentInboxEvent(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("wrong channel cancel: status=%d body=%s, want 404", rec.Code, rec.Body.String())
	}
}
