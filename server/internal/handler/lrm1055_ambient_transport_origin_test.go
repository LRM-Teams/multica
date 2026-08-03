package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// LRM-1055: ambient/GM-style inbox events may carry channel_id without a
// chat_session_id. Transport auth previously hard-rejected those events before
// origin resolution, so message/reminder APIs returned a misleading 403.
func TestLRM1055ChannelOnlyAmbientTransportOrigin(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTaskWithCapabilities(t, "group", []string{
		protocol.DaemonCapabilityChannelOutputActions,
		protocol.DaemonCapabilityReminderVersionedCache,
	})
	agentID := agentIDForTask(t, taskID)

	if _, err := testPool.Exec(ctx, `
		UPDATE agent_inbox_event
		SET chat_session_id = NULL,
		    channel_id = $2,
		    workspace_id = $3,
		    reason = 'ambient',
		    delivery_mode = 'observe',
		    response_mode = 'no_public_output',
		    status = 'draining'
		WHERE id = $1`, taskID, channelID, testWorkspaceID); err != nil {
		t.Fatalf("reshape inbox event to channel-only ambient: %v", err)
	}

	task, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(taskID))
	if err != nil {
		t.Fatalf("load ambient task: %v", err)
	}
	if task.ChatSessionID.Valid {
		t.Fatal("expected chat_session_id to be cleared")
	}
	if !task.ChannelID.Valid || uuidToString(task.ChannelID) != channelID {
		t.Fatalf("channel_id=%v, want %s", task.ChannelID, channelID)
	}
	if !agentInboxEventHasChatTransportOrigin(task) {
		t.Fatal("channel-only ambient event should be transport-eligible")
	}

	origin, ok := testHandler.chatOutputOriginForTask(ctx, task)
	if !ok {
		t.Fatal("chatOutputOriginForTask failed for channel-only ambient event")
	}
	if uuidToString(origin.channelID) != channelID ||
		uuidToString(origin.workspaceID) != testWorkspaceID ||
		uuidToString(origin.agentID) != agentID {
		t.Fatalf("origin=%+v, want channel=%s workspace=%s agent=%s",
			origin, channelID, testWorkspaceID, agentID)
	}

	listReq := agentTransportRequest(t, http.MethodPost, "/api/agent/reminders/list", taskID, agentID, map[string]any{})
	listRec := httptest.NewRecorder()
	testHandler.AgentTransportListReminders(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("reminder list status=%d body=%s", listRec.Code, listRec.Body.String())
	}

	target := "#" + channelNameForTransportTest(t, channelID)
	readReq := agentTransportRequest(t, http.MethodPost, "/api/agent/messages/read", taskID, agentID, map[string]any{
		"target": target,
		"limit":  1,
	})
	readRec := httptest.NewRecorder()
	testHandler.AgentTransportReadMessages(readRec, readReq)
	if readRec.Code != http.StatusOK {
		t.Fatalf("message read status=%d body=%s", readRec.Code, readRec.Body.String())
	}
	var readBody map[string]any
	if err := json.Unmarshal(readRec.Body.Bytes(), &readBody); err != nil {
		t.Fatalf("decode message read: %v", err)
	}
	if readBody["target"] != target {
		t.Fatalf("message read target=%v, want %s", readBody["target"], target)
	}
}

func TestLRM1055IssueOnlyInboxEventStillLacksTransportOrigin(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, _ := createChannelCompletionTask(t, "group")
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_inbox_event
		SET chat_session_id = NULL,
		    channel_id = NULL,
		    reason = 'issue',
		    status = 'draining'
		WHERE id = $1`, taskID); err != nil {
		t.Fatalf("reshape inbox event to issue-only: %v", err)
	}

	task, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(taskID))
	if err != nil {
		t.Fatalf("load issue task: %v", err)
	}
	if agentInboxEventHasChatTransportOrigin(task) {
		t.Fatal("issue-only event without channel/chat session must stay ineligible")
	}
	if _, ok := testHandler.chatOutputOriginForTask(ctx, task); ok {
		t.Fatal("issue-only event must not resolve a chat transport origin")
	}
}
