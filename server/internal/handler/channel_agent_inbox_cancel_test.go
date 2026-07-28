package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func seedChannelInboxWakeWithDelivery(t *testing.T, channelID, agentID, suffix string) (eventID, deliveryID string) {
	t.Helper()
	ctx := context.Background()
	root := dispatchThreadMentionForTest(t, channelID, agentID, "cancel-wake-"+suffix+"-"+uuid.NewString())
	eventID = latestChannelAgentInboxEventForRootForTest(t, root.ID, agentID)
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
	return eventID, deliveryID
}

func cancelChannelInboxEventRequest(t *testing.T, userID, channelID, eventID string) *http.Request {
	t.Helper()
	req := newRequestAs(userID, http.MethodPost, "/api/channels/"+channelID+"/agent-inbox/events/"+eventID+"/cancel", nil)
	req = withChannelTestWorkspaceCtx(t, req, userID)
	return withURLParams(req, "channelId", channelID, "eventId", eventID)
}

func cancelChannelActiveInboxRequest(t *testing.T, userID, channelID string) *http.Request {
	t.Helper()
	req := newRequestAs(userID, http.MethodPost, "/api/channels/"+channelID+"/agent-inbox/cancel-active", nil)
	req = withChannelTestWorkspaceCtx(t, req, userID)
	return withURLParams(req, "channelId", channelID)
}

func TestCancelChannelAgentInboxEvent_StopsWakeAndClearsActiveStrip(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "CancelChannelInboxSingle", []byte("[]"))
	channelID := seedChannelForTest(t, "cancel-channel-inbox-single-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	eventID, deliveryID := seedChannelInboxWakeWithDelivery(t, channelID, agentID, "single")

	w := httptest.NewRecorder()
	testHandler.CancelChannelAgentInboxEvent(w, cancelChannelInboxEventRequest(t, testUserID, channelID, eventID))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp ChannelCancelAgentInboxEventResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode cancel response: %v", err)
	}
	if !resp.OK || resp.InboxEventID != eventID || resp.AgentID != agentID || resp.Status != "cancelled" {
		t.Fatalf("cancel response = %+v", resp)
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
		if task.TaskID == eventID || (task.InboxEventID != nil && *task.InboxEventID == eventID) {
			t.Fatalf("cancelled inbox event still appears in active strip: %+v", activeResp.Tasks)
		}
	}
}

func TestCancelChannelActiveAgentInboxEvents_StopAllInOneRequest(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentA := createHandlerTestAgent(t, "CancelChannelInboxBulkA", []byte("[]"))
	agentB := createHandlerTestAgent(t, "CancelChannelInboxBulkB", []byte("[]"))
	channelID := seedChannelForTest(t, "cancel-channel-inbox-bulk-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3), ($1, $2, 'agent', $4)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentA, agentB); err != nil {
		t.Fatalf("seed agent members: %v", err)
	}
	eventA, _ := seedChannelInboxWakeWithDelivery(t, channelID, agentA, "bulk-a")
	eventB, _ := seedChannelInboxWakeWithDelivery(t, channelID, agentB, "bulk-b")

	w := httptest.NewRecorder()
	testHandler.CancelChannelActiveAgentInboxEvents(w, cancelChannelActiveInboxRequest(t, testUserID, channelID))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp ChannelCancelActiveAgentInboxResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode cancel-active response: %v", err)
	}
	if !resp.OK || resp.CancelledCount < 2 {
		t.Fatalf("cancel-active response = %+v, want >=2 cancelled", resp)
	}
	got := map[string]string{}
	for _, item := range resp.Cancelled {
		got[item.InboxEventID] = item.AgentID
		if item.Status != "cancelled" || !item.OK {
			t.Fatalf("unexpected cancelled item: %+v", item)
		}
	}
	if got[eventA] != agentA || got[eventB] != agentB {
		t.Fatalf("cancelled map = %+v, want events %s/%s", got, eventA, eventB)
	}

	for _, eventID := range []string{eventA, eventB} {
		var status, outcome string
		if err := testPool.QueryRow(ctx, `
			SELECT status, COALESCE(terminal_outcome, '')
			FROM agent_inbox_event WHERE id = $1`, eventID).Scan(&status, &outcome); err != nil {
			t.Fatalf("load event %s: %v", eventID, err)
		}
		if status != "suppressed" || outcome != "no_reply" {
			t.Fatalf("event %s status=%q outcome=%q", eventID, status, outcome)
		}
	}

	activeReq := withURLParam(newRequest(http.MethodGet, "/api/channels/"+channelID+"/active-tasks", nil), "channelId", channelID)
	activeReq = withChannelTestWorkspaceCtx(t, activeReq, testUserID)
	activeRec := httptest.NewRecorder()
	testHandler.ListChannelActiveTasks(activeRec, activeReq)
	if activeRec.Code != http.StatusOK {
		t.Fatalf("list active tasks after stop-all: status=%d body=%s", activeRec.Code, activeRec.Body.String())
	}
	var activeResp ChannelActiveTasksResponse
	if err := json.NewDecoder(activeRec.Body).Decode(&activeResp); err != nil {
		t.Fatalf("decode active tasks after stop-all: %v", err)
	}
	for _, task := range activeResp.Tasks {
		if task.Status == "queued" || task.Status == "running" {
			t.Fatalf("stop-all left active strip row: %+v", task)
		}
	}
}

func TestCancelChannelActiveAgentInboxEvents_EmptyChannelReturnsZero(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	channelID := seedChannelForTest(t, "cancel-channel-inbox-empty-"+uuid.NewString(), testUserID)
	w := httptest.NewRecorder()
	testHandler.CancelChannelActiveAgentInboxEvents(w, cancelChannelActiveInboxRequest(t, testUserID, channelID))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp ChannelCancelActiveAgentInboxResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode cancel-active response: %v", err)
	}
	if !resp.OK || resp.CancelledCount != 0 || len(resp.Cancelled) != 0 {
		t.Fatalf("empty cancel-active = %+v", resp)
	}
}
