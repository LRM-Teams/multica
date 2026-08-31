package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestUniversalDAGAgentInboxMessageBoundary(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Task3 Inbox "+uuid.NewString()[:8], nil)
	runtimeID := handlerTestRuntimeID(t)
	channelID := seedChannelForTest(t, "task3-inbox-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
		ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent channel membership: %v", err)
	}
	eventID := createProductInboxEventForRuntime(t, runtimeID, agentID, channelID)
	cleanupUniversalDAGTask(t, eventID)

	drainReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "task3-inbox-daemon")
	drainReq = withURLParam(drainReq, "runtimeId", runtimeID)
	drainRec := httptest.NewRecorder()
	testHandler.DrainAgentInboxByRuntime(drainRec, drainReq)
	if drainRec.Code != http.StatusOK {
		t.Fatalf("drain inbox: status=%d body=%s", drainRec.Code, drainRec.Body.String())
	}
	var drained DrainAgentInboxResponse
	if err := json.Unmarshal(drainRec.Body.Bytes(), &drained); err != nil {
		t.Fatalf("decode drain: %v", err)
	}
	if len(drained.Events) != 1 || drained.Events[0].ID != eventID {
		t.Fatalf("drained events=%+v, want event %s", drained.Events, eventID)
	}
	event := drained.Events[0]

	reportReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+event.ID+"/messages", ReportAgentInboxMessagesRequest{
		DeliveryID: event.DeliveryID,
		LeaseToken: event.LeaseToken,
		Messages: []TaskMessageRequest{
			{Seq: 1, Type: "thinking", Content: "synthetic diagnostic"},
			{Seq: 2, Type: "tool_use", Tool: "read", Input: map[string]any{"filePath": "/tmp/synthetic.txt"}},
		},
	}, testWorkspaceID, "task3-inbox-daemon")
	reportReq = withURLParam(reportReq, "eventId", event.ID)
	reportRec := httptest.NewRecorder()
	testHandler.ReportAgentInboxMessages(reportRec, reportReq)
	if reportRec.Code != http.StatusOK {
		t.Fatalf("report inbox messages: status=%d body=%s", reportRec.Code, reportRec.Body.String())
	}

	var messageCount, segmentCount int
	var openEnd int32
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM task_message WHERE task_id = $1`, event.ID).Scan(&messageCount); err != nil {
		t.Fatalf("count inbox task messages: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT open_end_seq
		FROM interaction_dag_task_cursor
		WHERE workspace_id = $1 AND agent_run_id = $2`, testWorkspaceID, event.ID).Scan(&openEnd); err != nil {
		t.Fatalf("load inbox universal DAG cursor: %v", err)
	}
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM interaction_dag_segment WHERE workspace_id = $1 AND agent_run_id = $2`, testWorkspaceID, event.ID).Scan(&segmentCount); err != nil {
		t.Fatalf("count inbox universal DAG segments: %v", err)
	}
	if messageCount != 2 || openEnd != 2 || segmentCount != 0 {
		t.Fatalf("inbox canonical messages/open_end/segments=%d/%d/%d, want 2/2/0", messageCount, openEnd, segmentCount)
	}
}

func TestUniversalDAGAgentInboxCompletionOutcomesAndRollback(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := handlerTestRuntimeID(t)
	agentID := createHandlerTestAgent(t, "Task3 Inbox Completion "+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "task3-inbox-complete-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3) ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed inbox completion membership: %v", err)
	}

	createAndDrain := func(label string) AgentInboxEventResponse {
		var chatSessionID, eventID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO chat_session (workspace_id, agent_id, creator_id, title)
			VALUES ($1, $2, $3, $4) RETURNING id`, testWorkspaceID, agentID, testUserID, label).Scan(&chatSessionID); err != nil {
			t.Fatalf("create inbox completion chat session: %v", err)
		}
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent_inbox_event (
				workspace_id, agent_session_id, channel_id, chat_session_id, agent_id, runtime_id,
				reason, delivery_mode, response_mode, requires_wake, status, priority, execution_config
			) VALUES (
				$1, ensure_agent_wake_session($2), NULL, $3, $2, $4,
				'chat_session', 'execute', 'public_response', true, 'pending', 0, '{}'::jsonb
			) RETURNING id`, testWorkspaceID, agentID, chatSessionID, runtimeID).Scan(&eventID); err != nil {
			t.Fatalf("create inbox completion event: %v", err)
		}
		if _, err := testPool.Exec(ctx, `
			INSERT INTO chat_message (chat_session_id, role, content, task_id)
			VALUES ($1, 'user', $2, $3)`, chatSessionID, "synthetic prompt "+label, eventID); err != nil {
			t.Fatalf("create inbox completion prompt: %v", err)
		}
		cleanupUniversalDAGTask(t, eventID)
		req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "task3-inbox-complete-daemon")
		req = withURLParam(req, "runtimeId", runtimeID)
		rec := httptest.NewRecorder()
		testHandler.DrainAgentInboxByRuntime(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("drain inbox completion event: status=%d body=%s", rec.Code, rec.Body.String())
		}
		var response DrainAgentInboxResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || len(response.Events) != 1 {
			t.Fatalf("decode inbox completion drain: err=%v events=%+v", err, response.Events)
		}
		return response.Events[0]
	}
	complete := func(event AgentInboxEventResponse, body TaskCompleteRequest) *httptest.ResponseRecorder {
		req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+event.ID+"/complete", CompleteAgentInboxEventRequest{
			DeliveryID: event.DeliveryID, LeaseToken: event.LeaseToken, TaskCompleteRequest: body,
		}, testWorkspaceID, "task3-inbox-complete-daemon")
		req = withURLParam(req, "eventId", event.ID)
		rec := httptest.NewRecorder()
		testHandler.CompleteAgentInboxEvent(rec, req)
		return rec
	}

	messageEvent := createAndDrain("task3-message")
	messageRec := complete(messageEvent, TaskCompleteRequest{Type: "message", Output: "synthetic inbox assistant output"})
	if messageRec.Code != http.StatusOK {
		t.Fatalf("complete inbox message: status=%d body=%s", messageRec.Code, messageRec.Body.String())
	}
	var messageID string
	if err := testPool.QueryRow(ctx, `SELECT id FROM chat_message WHERE task_id=$1 AND role='assistant'`, messageEvent.ID).Scan(&messageID); err != nil {
		t.Fatalf("load inbox assistant message: %v", err)
	}
	var messageSegments, terminalSegments, outboxCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM interaction_dag_segment WHERE workspace_id=$1 AND agent_run_id=$2 AND close_action_kind='message' AND canonical_action_id=$3`, testWorkspaceID, messageEvent.ID, messageID).Scan(&messageSegments); err != nil {
		t.Fatalf("count inbox message segments: %v", err)
	}
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM interaction_dag_segment WHERE workspace_id=$1 AND agent_run_id=$2 AND close_action_kind='metadata_only'`, testWorkspaceID, messageEvent.ID).Scan(&terminalSegments); err != nil {
		t.Fatalf("count inbox terminal segments: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM interaction_dag_publish_outbox o
		JOIN interaction_dag_segment s ON s.workspace_id=o.workspace_id AND s.segment_id=o.segment_id
		WHERE s.workspace_id=$1 AND s.agent_run_id=$2`, testWorkspaceID, messageEvent.ID).Scan(&outboxCount); err != nil {
		t.Fatalf("count inbox completion outbox: %v", err)
	}
	if messageSegments != 1 || terminalSegments != 0 || outboxCount != 1 {
		t.Fatalf("inbox message/extra-terminal/outbox=%d/%d/%d, want 1/0/1", messageSegments, terminalSegments, outboxCount)
	}

	noReplyEvent := createAndDrain("task3-no-reply")
	noReplyRec := complete(noReplyEvent, TaskCompleteRequest{Type: "no_reply", OutputSuppressedReason: "synthetic"})
	if noReplyRec.Code != http.StatusOK {
		t.Fatalf("complete inbox no-reply: status=%d body=%s", noReplyRec.Code, noReplyRec.Body.String())
	}
	var noReplyMessages, noReplySegments int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM chat_message WHERE task_id=$1 AND role='assistant'`, noReplyEvent.ID).Scan(&noReplyMessages); err != nil {
		t.Fatalf("count no-reply messages: %v", err)
	}
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM interaction_dag_segment WHERE workspace_id=$1 AND agent_run_id=$2 AND close_action_kind='metadata_only'`, testWorkspaceID, noReplyEvent.ID).Scan(&noReplySegments); err != nil {
		t.Fatalf("count no-reply terminal segment: %v", err)
	}
	if noReplyMessages != 0 || noReplySegments != 1 {
		t.Fatalf("no-reply messages/segments=%d/%d, want 0/1", noReplyMessages, noReplySegments)
	}

	rollbackEvent := createAndDrain("task3-rollback")
	originalDAG := testHandler.TaskService.UniversalDAG
	testHandler.TaskService.UniversalDAG = nil
	rollbackRec := complete(rollbackEvent, TaskCompleteRequest{Type: "message", Output: "synthetic rolled-back inbox output"})
	testHandler.TaskService.UniversalDAG = originalDAG
	if rollbackRec.Code != http.StatusInternalServerError {
		t.Fatalf("rollback inbox completion: status=%d body=%s, want 500", rollbackRec.Code, rollbackRec.Body.String())
	}
	var eventStatus, deliveryStatus string
	var rollbackMessages int
	if err := testPool.QueryRow(ctx, `
		SELECT event.status, delivery.status
		FROM agent_inbox_event event JOIN agent_event_delivery delivery ON delivery.inbox_event_id=event.id
		WHERE event.id=$1 ORDER BY delivery.created_at DESC LIMIT 1`, rollbackEvent.ID).Scan(&eventStatus, &deliveryStatus); err != nil {
		t.Fatalf("load rolled-back inbox completion: %v", err)
	}
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM chat_message WHERE task_id=$1 AND role='assistant'`, rollbackEvent.ID).Scan(&rollbackMessages); err != nil {
		t.Fatalf("count rolled-back inbox messages: %v", err)
	}
	if eventStatus != "draining" || deliveryStatus != "leased" || rollbackMessages != 0 {
		t.Fatalf("rolled-back inbox event/delivery/messages=%q/%q/%d, want draining/leased/0", eventStatus, deliveryStatus, rollbackMessages)
	}
}
