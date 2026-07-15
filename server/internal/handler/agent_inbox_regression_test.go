package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestAgentInboxDrainSerializesSameAgentAndKeepsDifferentAgentsConcurrent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	runtimeID := handlerTestRuntimeID(t)
	firstAgentName := "Inbox Serial Agent A " + uuid.NewString()[:8]
	firstAgentID := createHandlerTestAgent(t, firstAgentName, nil)
	firstChannelID := seedChannelForTest(t, "inbox-serial-a-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, firstChannelID, testWorkspaceID, firstAgentID); err != nil {
		t.Fatalf("seed first agent member: %v", err)
	}
	firstChannel, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(firstChannelID))
	if !found {
		t.Fatal("first channel not found after seed")
	}
	for i := 0; i < 2; i++ {
		trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(firstChannelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "[@"+firstAgentName+"](mention://agent/"+firstAgentID+") serial prompt", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("inbox-serial-a-"+uuid.NewString()), 0)
		if err != nil {
			t.Fatalf("insert first-agent trigger %d: %v", i, err)
		}
		testHandler.dispatchChannelMessageToAgents(ctx, firstChannel, trigger, parseUUID(testUserID))
	}

	drain := func(daemonID string) (DrainAgentInboxResponse, error) {
		req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, daemonID)
		req = withURLParam(req, "runtimeId", runtimeID)
		rec := httptest.NewRecorder()
		testHandler.DrainAgentInboxByRuntime(rec, req)
		if rec.Code != http.StatusOK {
			return DrainAgentInboxResponse{}, fmt.Errorf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var resp DrainAgentInboxResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			return DrainAgentInboxResponse{}, err
		}
		return resp, nil
	}

	start := make(chan struct{})
	results := make(chan DrainAgentInboxResponse, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			<-start
			resp, err := drain(fmt.Sprintf("inbox-serial-racer-%d", i))
			if err != nil {
				errs <- err
				return
			}
			results <- resp
		}(i)
	}
	close(start)

	var firstDelivery AgentInboxEventResponse
	leased := 0
	for i := 0; i < 2; i++ {
		select {
		case err := <-errs:
			t.Fatalf("concurrent drain failed: %v", err)
		case resp := <-results:
			if len(resp.Events) > 1 {
				t.Fatalf("drain returned %d events, want at most 1", len(resp.Events))
			}
			if len(resp.Events) == 1 {
				leased++
				firstDelivery = resp.Events[0]
			}
		}
	}
	if leased != 1 || firstDelivery.AgentID != firstAgentID {
		t.Fatalf("same-agent concurrent drains leased %d events, want exactly 1 for %s", leased, firstAgentID)
	}

	var draining, pending, activeDeliveries int
	if err := testPool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE e.status = 'draining'),
			count(*) FILTER (WHERE e.status = 'pending'),
			count(*) FILTER (
				WHERE d.status IN ('leased', 'processing') AND d.lease_expires_at > now()
			)
		FROM agent_inbox_event e
		LEFT JOIN agent_event_delivery d ON d.inbox_event_id = e.id
		WHERE e.agent_id = $1`, firstAgentID).Scan(&draining, &pending, &activeDeliveries); err != nil {
		t.Fatalf("load first-agent lease states: %v", err)
	}
	if draining != 1 || pending != 1 || activeDeliveries != 1 {
		t.Fatalf("first-agent states draining=%d pending=%d active=%d, want 1/1/1", draining, pending, activeDeliveries)
	}

	secondAgentName := "Inbox Serial Agent B " + uuid.NewString()[:8]
	secondAgentID := createHandlerTestAgent(t, secondAgentName, nil)
	secondChannelID := seedChannelForTest(t, "inbox-serial-b-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, secondChannelID, testWorkspaceID, secondAgentID); err != nil {
		t.Fatalf("seed second agent member: %v", err)
	}
	secondChannel, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(secondChannelID))
	if !found {
		t.Fatal("second channel not found after seed")
	}
	secondTrigger, err := testHandler.insertChannelMessage(ctx, parseUUID(secondChannelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "[@"+secondAgentName+"](mention://agent/"+secondAgentID+") concurrent other agent", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("inbox-serial-b-"+uuid.NewString()), 0)
	if err != nil {
		t.Fatalf("insert second-agent trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, secondChannel, secondTrigger, parseUUID(testUserID))

	secondResp, err := drain("inbox-serial-other-agent")
	if err != nil {
		t.Fatalf("drain different agent: %v", err)
	}
	if len(secondResp.Events) != 1 || secondResp.Events[0].AgentID != secondAgentID {
		t.Fatalf("different-agent drain = %+v, want one event for %s while first agent is active", secondResp.Events, secondAgentID)
	}

	ack := func(event AgentInboxEventResponse) {
		req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+event.ID+"/ack", AckAgentInboxEventRequest{
			DeliveryID:  event.DeliveryID,
			LeaseToken:  event.LeaseToken,
			SeenUpToSeq: event.SeqTo,
		}, testWorkspaceID, "inbox-serial-ack")
		req = withURLParam(req, "eventId", event.ID)
		rec := httptest.NewRecorder()
		testHandler.AckAgentInboxEvent(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("ack event %s: status=%d body=%s", event.ID, rec.Code, rec.Body.String())
		}
	}
	ack(secondResp.Events[0])
	ack(firstDelivery)

	nextResp, err := drain("inbox-serial-next")
	if err != nil {
		t.Fatalf("drain queued first-agent event: %v", err)
	}
	if len(nextResp.Events) != 1 || nextResp.Events[0].AgentID != firstAgentID || nextResp.Events[0].ID == firstDelivery.ID {
		t.Fatalf("queued first-agent drain = %+v, want the second event after release", nextResp.Events)
	}
}

func TestRetryAgentInboxEventReplaysOriginalPromptAfterNewerCompletion(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	runtimeID := handlerTestRuntimeID(t)
	agentName := "Inbox Retry Prompt Agent " + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentName, nil)
	channelID := seedChannelForTest(t, "inbox-retry-prompt-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	channel, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}

	const promptA = "PROMPT_A_ORIGINAL_RETRY_TARGET"
	triggerA, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "[@"+agentName+"](mention://agent/"+agentID+") "+promptA, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("retry-a-"+uuid.NewString()), 0)
	if err != nil {
		t.Fatalf("insert prompt A: %v", err)
	}
	var promptAAttachmentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO attachment (
			workspace_id, channel_id, channel_message_id, uploader_type, uploader_id,
			filename, url, content_type, size_bytes
		)
		VALUES ($1, $2, $3, 'member', $4, 'prompt-a.txt', 's3://prompt-a.txt', 'text/plain', 8)
		RETURNING id`, testWorkspaceID, channelID, triggerA.ID, testUserID).Scan(&promptAAttachmentID); err != nil {
		t.Fatalf("seed prompt A attachment: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, channel, triggerA, parseUUID(testUserID))

	drainAReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "retry-prompt-daemon")
	drainAReq = withURLParam(drainAReq, "runtimeId", runtimeID)
	drainARec := httptest.NewRecorder()
	testHandler.DrainAgentInboxByRuntime(drainARec, drainAReq)
	if drainARec.Code != http.StatusOK {
		t.Fatalf("drain prompt A: status=%d body=%s", drainARec.Code, drainARec.Body.String())
	}
	var drainA DrainAgentInboxResponse
	if err := json.Unmarshal(drainARec.Body.Bytes(), &drainA); err != nil {
		t.Fatalf("decode prompt A drain: %v", err)
	}
	if len(drainA.Events) != 1 || drainA.Events[0].Task == nil {
		t.Fatalf("prompt A drain missing runnable event: %s", drainARec.Body.String())
	}
	eventA := drainA.Events[0]

	failAReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+eventA.ID+"/fail", FailAgentInboxEventRequest{
		DeliveryID:    eventA.DeliveryID,
		LeaseToken:    eventA.LeaseToken,
		Error:         "provider rejected prompt A",
		FailureReason: "agent_error.provider_auth_or_access",
	}, testWorkspaceID, "retry-prompt-daemon")
	failAReq = withURLParam(failAReq, "eventId", eventA.ID)
	failARec := httptest.NewRecorder()
	testHandler.FailAgentInboxEvent(failARec, failAReq)
	if failARec.Code != http.StatusOK {
		t.Fatalf("fail prompt A: status=%d body=%s", failARec.Code, failARec.Body.String())
	}

	const promptB = "PROMPT_B_NEWER_COMPLETED"
	triggerB, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "[@"+agentName+"](mention://agent/"+agentID+") "+promptB, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("retry-b-"+uuid.NewString()), 0)
	if err != nil {
		t.Fatalf("insert prompt B: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, channel, triggerB, parseUUID(testUserID))

	drainBReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "retry-prompt-daemon")
	drainBReq = withURLParam(drainBReq, "runtimeId", runtimeID)
	drainBRec := httptest.NewRecorder()
	testHandler.DrainAgentInboxByRuntime(drainBRec, drainBReq)
	if drainBRec.Code != http.StatusOK {
		t.Fatalf("drain prompt B: status=%d body=%s", drainBRec.Code, drainBRec.Body.String())
	}
	var drainB DrainAgentInboxResponse
	if err := json.Unmarshal(drainBRec.Body.Bytes(), &drainB); err != nil {
		t.Fatalf("decode prompt B drain: %v", err)
	}
	if len(drainB.Events) != 1 || drainB.Events[0].Task == nil {
		t.Fatalf("prompt B drain missing runnable event: %s", drainBRec.Body.String())
	}
	eventB := drainB.Events[0]
	completeBReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+eventB.ID+"/complete", CompleteAgentInboxEventRequest{
		DeliveryID: eventB.DeliveryID,
		LeaseToken: eventB.LeaseToken,
		TaskCompleteRequest: TaskCompleteRequest{
			Output: "prompt B completed",
		},
	}, testWorkspaceID, "retry-prompt-daemon")
	completeBReq = withURLParam(completeBReq, "eventId", eventB.ID)
	completeBRec := httptest.NewRecorder()
	testHandler.CompleteAgentInboxEvent(completeBRec, completeBReq)
	if completeBRec.Code != http.StatusOK {
		t.Fatalf("complete prompt B: status=%d body=%s", completeBRec.Code, completeBRec.Body.String())
	}

	retryReq := newRequestAs(testUserID, http.MethodPost, "/api/channels/"+channelID+"/agent-inbox/events/"+eventA.ID+"/retry", nil)
	retryReq = withChannelTestWorkspaceCtx(t, retryReq, testUserID)
	retryReq = withURLParams(retryReq, "channelId", channelID, "eventId", eventA.ID)
	retryRec := httptest.NewRecorder()
	testHandler.RetryChannelAgentInboxEvent(retryRec, retryReq)
	if retryRec.Code != http.StatusCreated {
		t.Fatalf("retry prompt A: status=%d body=%s", retryRec.Code, retryRec.Body.String())
	}

	drainRetryReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "retry-prompt-daemon")
	drainRetryReq = withURLParam(drainRetryReq, "runtimeId", runtimeID)
	drainRetryRec := httptest.NewRecorder()
	testHandler.DrainAgentInboxByRuntime(drainRetryRec, drainRetryReq)
	if drainRetryRec.Code != http.StatusOK {
		t.Fatalf("drain retry A: status=%d body=%s", drainRetryRec.Code, drainRetryRec.Body.String())
	}
	var drainRetry DrainAgentInboxResponse
	if err := json.Unmarshal(drainRetryRec.Body.Bytes(), &drainRetry); err != nil {
		t.Fatalf("decode retry A drain: %v", err)
	}
	if len(drainRetry.Events) != 1 || drainRetry.Events[0].Task == nil {
		t.Fatalf("retry A drain missing runnable event: %s", drainRetryRec.Body.String())
	}
	gotPrompt := drainRetry.Events[0].Task.ChatMessage
	if strings.TrimSpace(gotPrompt) == "" || !strings.Contains(gotPrompt, promptA) || strings.Contains(gotPrompt, promptB) {
		t.Fatalf("retried prompt = %q, want original A only", gotPrompt)
	}
	attachments := drainRetry.Events[0].Task.ChatMessageAttachments
	if len(attachments) != 1 || attachments[0].ID != promptAAttachmentID || attachments[0].Filename != "prompt-a.txt" {
		t.Fatalf("retried attachments = %+v, want prompt A attachment %s", attachments, promptAAttachmentID)
	}
}

func TestAgentInboxDrainRejectsChatSessionFromDifferentRuntime(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	runtimeID := handlerTestRuntimeID(t)
	agentName := "Inbox Runtime Pointer Agent " + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentName, nil)
	channelID := seedChannelForTest(t, "inbox-runtime-pointer-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	channel, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "[@"+agentName+"](mention://agent/"+agentID+") inspect runtime pointer", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("runtime-pointer-"+uuid.NewString()), 0)
	if err != nil {
		t.Fatalf("insert trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, channel, trigger, parseUUID(testUserID))

	var chatSessionID string
	if err := testPool.QueryRow(ctx, `
		SELECT chat_session_id
		FROM agent_inbox_event
		WHERE source_message_id = $1 AND agent_id = $2
		ORDER BY created_at DESC
		LIMIT 1`, trigger.ID, agentID).Scan(&chatSessionID); err != nil {
		t.Fatalf("load chat session: %v", err)
	}
	var foreignRuntimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, owner_id, last_seen_at
		)
		VALUES ($1, $2, 'Foreign Inbox Runtime', 'local', 'pi', 'online', 'test runtime', '{}'::jsonb, $3, now())
		RETURNING id`, testWorkspaceID, "foreign-inbox-runtime-"+uuid.NewString(), testUserID).Scan(&foreignRuntimeID); err != nil {
		t.Fatalf("create foreign runtime: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `
			UPDATE chat_session
			SET session_id = NULL, work_dir = NULL, runtime_id = NULL
			WHERE id = $1`, chatSessionID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, foreignRuntimeID)
	})
	if _, err := testPool.Exec(ctx, `
		UPDATE chat_session
		SET session_id = 'foreign-session',
		    work_dir = '/tmp/foreign-inbox-workdir',
		    runtime_id = $2
		WHERE id = $1`, chatSessionID, foreignRuntimeID); err != nil {
		t.Fatalf("seed foreign chat session pointer: %v", err)
	}

	drainReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "runtime-pointer-daemon")
	drainReq = withURLParam(drainReq, "runtimeId", runtimeID)
	drainRec := httptest.NewRecorder()
	testHandler.DrainAgentInboxByRuntime(drainRec, drainReq)
	if drainRec.Code != http.StatusOK {
		t.Fatalf("drain inbox: status=%d body=%s", drainRec.Code, drainRec.Body.String())
	}
	var drainResp DrainAgentInboxResponse
	if err := json.Unmarshal(drainRec.Body.Bytes(), &drainResp); err != nil {
		t.Fatalf("decode drain response: %v", err)
	}
	if len(drainResp.Events) != 1 || drainResp.Events[0].Task == nil {
		t.Fatalf("drain missing runnable task: %s", drainRec.Body.String())
	}
	task := drainResp.Events[0].Task
	if task.RuntimeID != runtimeID {
		t.Fatalf("task runtime = %q, want %q", task.RuntimeID, runtimeID)
	}
	if task.PriorSessionID != "" || task.PriorWorkDir != "" {
		t.Fatalf("foreign runtime pointer leaked into task: session=%q workdir=%q", task.PriorSessionID, task.PriorWorkDir)
	}
}

func TestAgentInboxDrainRejectsFallbackSessionFromDifferentRuntime(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	runtimeID := handlerTestRuntimeID(t)
	agentName := "Inbox Runtime Fallback Agent " + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentName, nil)
	channelID := seedChannelForTest(t, "inbox-runtime-fallback-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	channel, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "[@"+agentName+"](mention://agent/"+agentID+") inspect fallback runtime", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("runtime-fallback-"+uuid.NewString()), 0)
	if err != nil {
		t.Fatalf("insert trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, channel, trigger, parseUUID(testUserID))

	var chatSessionID string
	if err := testPool.QueryRow(ctx, `
		SELECT chat_session_id
		FROM agent_inbox_event
		WHERE source_message_id = $1 AND agent_id = $2
		ORDER BY created_at DESC
		LIMIT 1`, trigger.ID, agentID).Scan(&chatSessionID); err != nil {
		t.Fatalf("load chat session: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE chat_session
		SET session_id = NULL, work_dir = NULL, runtime_id = NULL
		WHERE id = $1`, chatSessionID); err != nil {
		t.Fatalf("clear chat session pointer: %v", err)
	}

	var foreignRuntimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, owner_id, last_seen_at
		)
		VALUES ($1, $2, 'Foreign Fallback Runtime', 'local', 'pi', 'online', 'test runtime', '{}'::jsonb, $3, now())
		RETURNING id`, testWorkspaceID, "foreign-fallback-runtime-"+uuid.NewString(), testUserID).Scan(&foreignRuntimeID); err != nil {
		t.Fatalf("create foreign runtime: %v", err)
	}
	var priorTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority, chat_session_id,
			session_id, work_dir, completed_at
		)
		VALUES ($1, $2, NULL, 'completed', 10, $3, 'foreign-fallback-session', '/tmp/foreign-fallback-workdir', now())
		RETURNING id`, agentID, foreignRuntimeID, chatSessionID).Scan(&priorTaskID); err != nil {
		t.Fatalf("seed foreign fallback task: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, priorTaskID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, foreignRuntimeID)
	})

	drainReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "runtime-fallback-daemon")
	drainReq = withURLParam(drainReq, "runtimeId", runtimeID)
	drainRec := httptest.NewRecorder()
	testHandler.DrainAgentInboxByRuntime(drainRec, drainReq)
	if drainRec.Code != http.StatusOK {
		t.Fatalf("drain inbox: status=%d body=%s", drainRec.Code, drainRec.Body.String())
	}
	var drainResp DrainAgentInboxResponse
	if err := json.Unmarshal(drainRec.Body.Bytes(), &drainResp); err != nil {
		t.Fatalf("decode drain response: %v", err)
	}
	if len(drainResp.Events) != 1 || drainResp.Events[0].Task == nil {
		t.Fatalf("drain missing runnable task: %s", drainRec.Body.String())
	}
	task := drainResp.Events[0].Task
	if task.PriorSessionID != "" || task.PriorWorkDir != "" {
		t.Fatalf("foreign fallback leaked into task: session=%q workdir=%q", task.PriorSessionID, task.PriorWorkDir)
	}
}

func TestAgentInboxDrainIncludesSourceChannelMessageAttachment(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	runtimeID := handlerTestRuntimeID(t)
	agentName := "Inbox Source Attachment Agent " + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentName, nil)
	channelID := seedChannelForTest(t, "inbox-source-attachment-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	var attachmentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO attachment (
			workspace_id, channel_id, uploader_type, uploader_id,
			filename, url, content_type, size_bytes
		)
		VALUES ($1, $2, 'member', $3, 'source-brief.pdf', 's3://source-brief.pdf', 'application/pdf', 42)
		RETURNING id`, testWorkspaceID, channelID, testUserID).Scan(&attachmentID); err != nil {
		t.Fatalf("seed source attachment: %v", err)
	}

	content := "@" + agentName + " inspect the attached brief"
	sendRec := sendChannelMessageForTest(t, channelID, testUserID, map[string]any{
		"content": content,
		"parts": []map[string]any{
			{
				"type": "text",
				"text": content,
			},
			{"type": "attachment", "attachment_id": attachmentID},
		},
	})
	if sendRec.Code != http.StatusCreated {
		t.Fatalf("send attachment mention: status=%d body=%s", sendRec.Code, sendRec.Body.String())
	}

	drainReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "source-attachment-daemon")
	drainReq = withURLParam(drainReq, "runtimeId", runtimeID)
	drainRec := httptest.NewRecorder()
	testHandler.DrainAgentInboxByRuntime(drainRec, drainReq)
	if drainRec.Code != http.StatusOK {
		t.Fatalf("drain inbox: status=%d body=%s", drainRec.Code, drainRec.Body.String())
	}
	var drainResp DrainAgentInboxResponse
	if err := json.Unmarshal(drainRec.Body.Bytes(), &drainResp); err != nil {
		t.Fatalf("decode drain response: %v", err)
	}
	if len(drainResp.Events) != 1 || drainResp.Events[0].Task == nil {
		t.Fatalf("drain missing runnable task: %s", drainRec.Body.String())
	}
	attachments := drainResp.Events[0].Task.ChatMessageAttachments
	if len(attachments) != 1 || attachments[0].ID != attachmentID || attachments[0].Filename != "source-brief.pdf" || attachments[0].ContentType != "application/pdf" {
		t.Fatalf("task attachments = %+v, want source channel attachment %s", attachments, attachmentID)
	}
}

func TestAgentInboxDrainDeduplicatesSourceAndPromptAttachment(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	runtimeID := handlerTestRuntimeID(t)
	agentName := "Inbox Attachment Dedupe Agent " + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentName, nil)
	channelID := seedChannelForTest(t, "inbox-attachment-dedupe-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	var attachmentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO attachment (
			workspace_id, channel_id, uploader_type, uploader_id,
			filename, url, content_type, size_bytes
		)
		VALUES ($1, $2, 'member', $3, 'dedupe.txt', 's3://dedupe.txt', 'text/plain', 7)
		RETURNING id`, testWorkspaceID, channelID, testUserID).Scan(&attachmentID); err != nil {
		t.Fatalf("seed attachment: %v", err)
	}

	content := "@" + agentName + " inspect the dedupe file"
	sendRec := sendChannelMessageForTest(t, channelID, testUserID, map[string]any{
		"content": content,
		"parts": []map[string]any{
			{
				"type": "text",
				"text": content,
			},
			{"type": "attachment", "attachment_id": attachmentID},
		},
	})
	if sendRec.Code != http.StatusCreated {
		t.Fatalf("send attachment mention: status=%d body=%s", sendRec.Code, sendRec.Body.String())
	}
	var sourceMessage ChannelMessageResponse
	if err := json.Unmarshal(sendRec.Body.Bytes(), &sourceMessage); err != nil {
		t.Fatalf("decode source message: %v", err)
	}
	var promptMessageID, chatSessionID string
	if err := testPool.QueryRow(ctx, `
		SELECT prompt.id, prompt.chat_session_id
		FROM agent_inbox_event event
		JOIN chat_message prompt ON prompt.task_id = event.id AND prompt.role = 'user'
		WHERE event.source_message_id = $1 AND event.agent_id = $2
		ORDER BY event.created_at DESC
		LIMIT 1`, sourceMessage.ID, agentID).Scan(&promptMessageID, &chatSessionID); err != nil {
		t.Fatalf("load synthetic prompt: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE attachment
		SET chat_session_id = $2, chat_message_id = $3
		WHERE id = $1`, attachmentID, chatSessionID, promptMessageID); err != nil {
		t.Fatalf("also bind attachment to synthetic prompt: %v", err)
	}

	drainReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "attachment-dedupe-daemon")
	drainReq = withURLParam(drainReq, "runtimeId", runtimeID)
	drainRec := httptest.NewRecorder()
	testHandler.DrainAgentInboxByRuntime(drainRec, drainReq)
	if drainRec.Code != http.StatusOK {
		t.Fatalf("drain inbox: status=%d body=%s", drainRec.Code, drainRec.Body.String())
	}
	var drainResp DrainAgentInboxResponse
	if err := json.Unmarshal(drainRec.Body.Bytes(), &drainResp); err != nil {
		t.Fatalf("decode drain response: %v", err)
	}
	if len(drainResp.Events) != 1 || drainResp.Events[0].Task == nil {
		t.Fatalf("drain missing runnable task: %s", drainRec.Body.String())
	}
	attachments := drainResp.Events[0].Task.ChatMessageAttachments
	if len(attachments) != 1 || attachments[0].ID != attachmentID {
		t.Fatalf("task attachments = %+v, want attachment %s exactly once", attachments, attachmentID)
	}
}

func TestAgentInboxDrainRejectsEventWithoutExactPrompt(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	runtimeID := handlerTestRuntimeID(t)
	agentName := "Inbox Exact Prompt Agent " + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentName, nil)
	channelID := seedChannelForTest(t, "inbox-exact-prompt-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	channel, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "[@"+agentName+"](mention://agent/"+agentID+") exact prompt target", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("exact-prompt-"+uuid.NewString()), 0)
	if err != nil {
		t.Fatalf("insert trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, channel, trigger, parseUUID(testUserID))

	var eventID, chatSessionID string
	if err := testPool.QueryRow(ctx, `
		SELECT id, chat_session_id
		FROM agent_inbox_event
		WHERE source_message_id = $1 AND agent_id = $2
		ORDER BY created_at DESC
		LIMIT 1`, trigger.ID, agentID).Scan(&eventID, &chatSessionID); err != nil {
		t.Fatalf("load inbox event: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE chat_message
		SET task_id = NULL
		WHERE task_id = $1 AND role = 'user'`, eventID); err != nil {
		t.Fatalf("remove exact event prompt link: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO chat_message (chat_session_id, role, content, task_id)
		VALUES ($1, 'user', 'WRONG_LATEST_PROMPT_MUST_NOT_RUN', $2)`, chatSessionID, uuid.NewString()); err != nil {
		t.Fatalf("seed unrelated latest prompt: %v", err)
	}

	drainReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "exact-prompt-daemon")
	drainReq = withURLParam(drainReq, "runtimeId", runtimeID)
	drainRec := httptest.NewRecorder()
	testHandler.DrainAgentInboxByRuntime(drainRec, drainReq)
	if drainRec.Code != http.StatusOK {
		t.Fatalf("drain inbox: status=%d body=%s", drainRec.Code, drainRec.Body.String())
	}
	var drainResp DrainAgentInboxResponse
	if err := json.Unmarshal(drainRec.Body.Bytes(), &drainResp); err != nil {
		t.Fatalf("decode drain response: %v", err)
	}
	if len(drainResp.Events) != 1 {
		t.Fatalf("drain events = %d, want leased event metadata", len(drainResp.Events))
	}
	if drainResp.Events[0].Task != nil {
		t.Fatalf("event without exact prompt became runnable: %+v", drainResp.Events[0].Task)
	}
}
