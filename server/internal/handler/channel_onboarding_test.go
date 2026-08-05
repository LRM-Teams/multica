package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func addChannelAgentForOnboardingTest(t *testing.T, channelID, agentID string) {
	t.Helper()
	req := newRequest(http.MethodPost, "/api/channels/"+channelID+"/members", AddChannelMemberRequest{
		MemberType: "agent",
		MemberID:   agentID,
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.AddChannelMember(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add channel agent: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func removeChannelAgentForOnboardingTest(t *testing.T, channelID, agentID string) {
	t.Helper()
	req := newRequest(http.MethodDelete, "/api/channels/"+channelID+"/members/agent/"+agentID+"?expected_remove_effect=none", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withRouteParams(req, "channelId", channelID, "memberType", "agent", "memberId", agentID)
	rec := httptest.NewRecorder()
	testHandler.RemoveChannelMember(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove channel agent: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func drainChannelOnboardingForTest(t *testing.T, runtimeID string) DrainAgentInboxResponse {
	t.Helper()
	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "channel-onboarding-test-daemon")
	req = withURLParam(req, "runtimeId", runtimeID)
	rec := httptest.NewRecorder()
	testHandler.DrainAgentInboxByRuntime(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("drain channel onboarding: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response DrainAgentInboxResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode channel onboarding drain: %v", err)
	}
	return response
}

func completeChannelOnboardingForTest(t *testing.T, event AgentInboxEventResponse, output, decision string, wantStatus int) {
	t.Helper()
	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+event.ID+"/complete", CompleteAgentInboxEventRequest{
		DeliveryID: event.DeliveryID,
		LeaseToken: event.LeaseToken,
		TaskCompleteRequest: TaskCompleteRequest{
			Output:                    output,
			ChannelOnboardingDecision: decision,
		},
	}, testWorkspaceID, "channel-onboarding-test-daemon")
	req = withURLParam(req, "eventId", event.ID)
	rec := httptest.NewRecorder()
	testHandler.CompleteAgentInboxEvent(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("complete channel onboarding: status=%d body=%s, want %d", rec.Code, rec.Body.String(), wantStatus)
	}
}

func sendChannelOnboardingMessageForTest(t *testing.T, event AgentInboxEventResponse, agentID, target, content string) AgentTransportSendResponse {
	t.Helper()
	req := newRequest(http.MethodPost, "/api/agent/messages/send", map[string]any{
		"target":  target,
		"content": content,
	})
	req = withChatTestWorkspaceCtx(t, req)
	req.Header.Set("X-Actor-Source", "agent_inbox_token")
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Agent-Inbox-Event-ID", event.ID)
	req.Header.Set("X-Agent-Inbox-Delivery-ID", event.DeliveryID)
	req.Header.Set("X-Task-ID", event.ID)
	rec := httptest.NewRecorder()
	testHandler.AgentTransportSendMessage(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("send channel onboarding message: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response AgentTransportSendResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode channel onboarding send: %v", err)
	}
	return response
}

func TestChannelOnboardingAgentAddPublishesBeforeLeaseAndSendsOnce(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID, runtimeID := createHandlerTestAgentWithIsolatedRuntime(t)
	channelID := seedChannelForTest(t, "onboarding-send-"+uuid.NewString(), testUserID)
	channelName := channelNameForTransportTest(t, channelID)

	var eventMu sync.Mutex
	var publishedEvents []events.Event
	testHandler.Bus.Subscribe(protocol.EventChannelMessage, func(event events.Event) {
		message, ok := event.Payload.(ChannelMessageResponse)
		if !ok || message.ChannelID != channelID || event.RealtimeEventID == "" {
			return
		}
		eventMu.Lock()
		publishedEvents = append(publishedEvents, event)
		eventMu.Unlock()
	})

	addChannelAgentForOnboardingTest(t, channelID, agentID)

	var onboardingID, generationID, systemMessageID, status, publicationStatus, sourceType, sourceActorID, eventKind string
	var systemSeq int64
	if err := testPool.QueryRow(ctx, `
		SELECT onboarding.id::text,
		       onboarding.membership_generation_id::text,
		       onboarding.system_message_id::text,
		       onboarding.status,
		       onboarding.publication_status,
		       onboarding.source_type,
		       COALESCE(onboarding.source_actor_id::text, ''),
		       message.seq,
		       message.parts->0->>'event'
		FROM channel_agent_onboarding onboarding
		JOIN channel_message message ON message.id = onboarding.system_message_id
		WHERE onboarding.channel_id = $1 AND onboarding.agent_id = $2`, channelID, agentID).Scan(
		&onboardingID, &generationID, &systemMessageID, &status, &publicationStatus,
		&sourceType, &sourceActorID, &systemSeq, &eventKind,
	); err != nil {
		t.Fatalf("load canonical onboarding row: %v", err)
	}
	if onboardingID == "" || generationID == "" || systemMessageID == "" || systemSeq <= 0 {
		t.Fatalf("canonical onboarding identity incomplete: onboarding=%q generation=%q message=%q seq=%d", onboardingID, generationID, systemMessageID, systemSeq)
	}
	if status != "pending" || publicationStatus != "published" || sourceType != "manual" || sourceActorID != testUserID || eventKind != "channel_member_added" {
		t.Fatalf("canonical onboarding = status:%q publication:%q source:%q actor:%q event:%q", status, publicationStatus, sourceType, sourceActorID, eventKind)
	}
	eventMu.Lock()
	if len(publishedEvents) != 1 || publishedEvents[0].RealtimeEventID != systemMessageID {
		t.Fatalf("published realtime events = %+v, want one stable id %s", publishedEvents, systemMessageID)
	}
	eventMu.Unlock()
	if err := testHandler.publishChannelOnboardingSystemMessageForGeneration(ctx, parseUUID(generationID)); err != nil {
		t.Fatalf("replay already published onboarding generation: %v", err)
	}
	eventMu.Lock()
	if len(publishedEvents) != 1 {
		t.Fatalf("already published generation replayed %d realtime events", len(publishedEvents))
	}
	eventMu.Unlock()

	drain := drainChannelOnboardingForTest(t, runtimeID)
	if len(drain.Events) != 1 {
		t.Fatalf("drained onboarding events = %d, want 1", len(drain.Events))
	}
	event := drain.Events[0]
	if event.AgentID != agentID || event.ChannelID != channelID || event.Reason != protocol.ChannelOnboardingReason || !event.RequiresWake || event.Task == nil {
		t.Fatalf("drained onboarding event = %+v", event)
	}
	if event.SourceMessageID != systemMessageID || event.SeqFrom != systemSeq || event.SeqTo != systemSeq {
		t.Fatalf("drained onboarding source = message:%q seq:%d..%d, want %s/%d", event.SourceMessageID, event.SeqFrom, event.SeqTo, systemMessageID, systemSeq)
	}
	if !strings.Contains(event.Task.ChatMessage, "Exact message target: #"+channelName) || !strings.Contains(event.Task.ChatMessage, protocol.ChannelOnboardingSkipReceipt) {
		t.Fatalf("onboarding prompt does not bind exact target and typed skip: %q", event.Task.ChatMessage)
	}

	for _, invalid := range []map[string]any{
		{"target": "#" + channelName + ":" + systemMessageID, "content": "must not reply in a thread"},
	} {
		req := newRequest(http.MethodPost, "/api/agent/messages/send", invalid)
		req = withChatTestWorkspaceCtx(t, req)
		req.Header.Set("X-Actor-Source", "agent_inbox_token")
		req.Header.Set("X-Agent-ID", agentID)
		req.Header.Set("X-Agent-Inbox-Event-ID", event.ID)
		req.Header.Set("X-Agent-Inbox-Delivery-ID", event.DeliveryID)
		req.Header.Set("X-Task-ID", event.ID)
		rec := httptest.NewRecorder()
		testHandler.AgentTransportSendMessage(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid onboarding transport request %+v: status=%d body=%s, want 400", invalid, rec.Code, rec.Body.String())
		}
	}

	first := sendChannelOnboardingMessageForTest(t, event, agentID, "#"+channelName, "Hello from the joined agent")
	if !first.Created {
		t.Fatalf("first onboarding send created=false: %+v", first)
	}
	second := sendChannelOnboardingMessageForTest(t, event, agentID, "#"+channelName, "different retry body must not duplicate")
	if second.Created || second.Message.ID != first.Message.ID {
		t.Fatalf("retry onboarding send = created:%v message:%s, want existing %s", second.Created, second.Message.ID, first.Message.ID)
	}

	var visibleAgentMessages, sendAudits, otherAgentInboxes int
	var clientMessageID string
	if err := testPool.QueryRow(ctx, `
		SELECT count(*), COALESCE(min(client_message_id), '')
		FROM channel_message
		WHERE channel_id = $1 AND author_type = 'agent' AND author_id = $2`, channelID, agentID).Scan(&visibleAgentMessages, &clientMessageID); err != nil {
		t.Fatalf("count onboarding agent messages: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_task_transport_audit
		WHERE inbox_event_id = $1 AND action = 'message_send'`, event.ID).Scan(&sendAudits); err != nil {
		t.Fatalf("count onboarding send audits: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_inbox_event inbox
		JOIN channel_agent_onboarding onboarding ON onboarding.id = inbox.channel_onboarding_id
		WHERE onboarding.channel_id = $1 AND inbox.agent_id <> $2`, channelID, agentID).Scan(&otherAgentInboxes); err != nil {
		t.Fatalf("count non-target onboarding inboxes: %v", err)
	}
	if visibleAgentMessages != 1 || sendAudits != 1 || otherAgentInboxes != 0 || clientMessageID != "channel-onboarding:"+event.ID {
		t.Fatalf("send ledger = messages:%d audits:%d other_inboxes:%d client_id:%q", visibleAgentMessages, sendAudits, otherAgentInboxes, clientMessageID)
	}

	finalProse := "ordinary final text must not become a second greeting"
	completeChannelOnboardingForTest(t, event, finalProse, "", http.StatusOK)
	var onboardingStatus, inboxOutcome string
	if err := testPool.QueryRow(ctx, `
		SELECT onboarding.status, COALESCE(inbox.terminal_outcome, '')
		FROM channel_agent_onboarding onboarding
		JOIN agent_inbox_event inbox ON inbox.channel_onboarding_id = onboarding.id
		WHERE onboarding.id = $1`, onboardingID).Scan(&onboardingStatus, &inboxOutcome); err != nil {
		t.Fatalf("load sent onboarding terminal state: %v", err)
	}
	if onboardingStatus != "sent" || inboxOutcome != "sent" {
		t.Fatalf("sent onboarding terminal state = onboarding:%q inbox:%q", onboardingStatus, inboxOutcome)
	}
	assertChannelMessageContentCount(t, channelID, finalProse, 0)
}

func TestChannelOnboardingBatchAgentAddUsesCanonicalGeneration(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Onboarding Batch "+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "onboarding-batch-"+uuid.NewString(), testUserID)
	request := func() *http.Request {
		req := newRequest(http.MethodPost, "/api/channels/"+channelID+"/members/batch", AddChannelMembersRequest{Members: []AddChannelMemberRequest{{
			MemberType: "agent",
			MemberID:   agentID,
		}}})
		req = withChannelTestWorkspaceCtx(t, req, testUserID)
		return withURLParam(req, "channelId", channelID)
	}

	for attempt := 0; attempt < 2; attempt++ {
		rec := httptest.NewRecorder()
		testHandler.AddChannelMembers(rec, request())
		wantStatus := http.StatusCreated
		if attempt > 0 {
			// Re-adding the same member is an authorized idempotent no-op:
			// the first request creates the membership, later requests do not.
			wantStatus = http.StatusOK
		}
		if rec.Code != wantStatus {
			t.Fatalf("batch add agent attempt %d: status=%d body=%s", attempt+1, rec.Code, rec.Body.String())
		}
	}

	var onboardings, systemRows int
	var sourceType, publicationStatus string
	if err := testPool.QueryRow(ctx, `
		SELECT count(*), COALESCE(min(source_type), ''), COALESCE(min(publication_status), '')
		FROM channel_agent_onboarding
		WHERE channel_id = $1 AND agent_id = $2`, channelID, agentID).Scan(&onboardings, &sourceType, &publicationStatus); err != nil {
		t.Fatalf("load batch onboarding ledger: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1
		  AND membership_generation_id IS NOT NULL
		  AND parts->0->>'event' = 'channel_member_added'`, channelID).Scan(&systemRows); err != nil {
		t.Fatalf("count batch onboarding system rows: %v", err)
	}
	if onboardings != 1 || systemRows != 1 || sourceType != "manual" || publicationStatus != "published" {
		t.Fatalf("batch onboarding ledger = onboardings:%d system_rows:%d source:%q publication:%q", onboardings, systemRows, sourceType, publicationStatus)
	}

	drain := drainChannelOnboardingForTest(t, handlerTestRuntimeID(t))
	if len(drain.Events) != 1 || drain.Events[0].AgentID != agentID || drain.Events[0].ChannelID != channelID {
		t.Fatalf("batch onboarding drain = %+v", drain.Events)
	}
	completeChannelOnboardingForTest(t, drain.Events[0], "", protocol.ChannelOnboardingDecisionSkipped, http.StatusOK)
}

func TestChannelOnboardingRequiresTypedSkipReceipt(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Onboarding Skip "+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "onboarding-skip-"+uuid.NewString(), testUserID)
	addChannelAgentForOnboardingTest(t, channelID, agentID)
	drain := drainChannelOnboardingForTest(t, handlerTestRuntimeID(t))
	if len(drain.Events) != 1 {
		t.Fatalf("drained onboarding events = %d, want 1", len(drain.Events))
	}
	event := drain.Events[0]

	completeChannelOnboardingForTest(t, event, "I think no introduction is needed", "", http.StatusConflict)
	var eventStatus, deliveryStatus, onboardingStatus string
	if err := testPool.QueryRow(ctx, `
		SELECT inbox.status, delivery.status, onboarding.status
		FROM agent_inbox_event inbox
		JOIN agent_event_delivery delivery ON delivery.inbox_event_id = inbox.id
		JOIN channel_agent_onboarding onboarding ON onboarding.id = inbox.channel_onboarding_id
		WHERE inbox.id = $1 AND delivery.id = $2`, event.ID, event.DeliveryID).Scan(&eventStatus, &deliveryStatus, &onboardingStatus); err != nil {
		t.Fatalf("load retryable onboarding decision state: %v", err)
	}
	if eventStatus != "draining" || deliveryStatus != "leased" || onboardingStatus != "claimed" {
		t.Fatalf("untyped skip mutated state = inbox:%q delivery:%q onboarding:%q", eventStatus, deliveryStatus, onboardingStatus)
	}

	completeChannelOnboardingForTest(t, event, "", protocol.ChannelOnboardingDecisionSkipped, http.StatusOK)
	var terminalOutcome string
	var agentMessages int
	if err := testPool.QueryRow(ctx, `
		SELECT onboarding.status, COALESCE(inbox.terminal_outcome, '')
		FROM channel_agent_onboarding onboarding
		JOIN agent_inbox_event inbox ON inbox.channel_onboarding_id = onboarding.id
		WHERE inbox.id = $1`, event.ID).Scan(&onboardingStatus, &terminalOutcome); err != nil {
		t.Fatalf("load skipped onboarding terminal state: %v", err)
	}
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM channel_message WHERE channel_id = $1 AND author_type = 'agent'`, channelID).Scan(&agentMessages); err != nil {
		t.Fatalf("count skipped onboarding messages: %v", err)
	}
	if onboardingStatus != "skipped" || terminalOutcome != "skipped" || agentMessages != 0 {
		t.Fatalf("typed skip state = onboarding:%q inbox:%q agent_messages:%d", onboardingStatus, terminalOutcome, agentMessages)
	}
}

func TestChannelOnboardingRemoveReaddExpiresOldGenerationAndSend(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Onboarding Readd "+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "onboarding-readd-"+uuid.NewString(), testUserID)
	channelName := channelNameForTransportTest(t, channelID)
	addChannelAgentForOnboardingTest(t, channelID, agentID)
	var oldGeneration string
	if err := testPool.QueryRow(ctx, `SELECT membership_generation_id FROM channel_agent_onboarding WHERE channel_id = $1 AND agent_id = $2`, channelID, agentID).Scan(&oldGeneration); err != nil {
		t.Fatalf("load old onboarding generation: %v", err)
	}
	removeChannelAgentForOnboardingTest(t, channelID, agentID)
	addChannelAgentForOnboardingTest(t, channelID, agentID)

	var newGeneration, oldStatus string
	if err := testPool.QueryRow(ctx, `
		SELECT active.membership_generation_id::text, old.status
		FROM channel_agent_onboarding active
		JOIN channel_agent_onboarding old
		  ON old.channel_id = active.channel_id
		 AND old.agent_id = active.agent_id
		 AND old.membership_generation_id = $3
		WHERE active.channel_id = $1 AND active.agent_id = $2
		  AND active.status = 'pending'`, channelID, agentID, oldGeneration).Scan(&newGeneration, &oldStatus); err != nil {
		t.Fatalf("load readded onboarding generations: %v", err)
	}
	if oldStatus != "expired" || newGeneration == oldGeneration {
		t.Fatalf("readd generations = old:%s/%s new:%s", oldGeneration, oldStatus, newGeneration)
	}

	drain := drainChannelOnboardingForTest(t, handlerTestRuntimeID(t))
	if len(drain.Events) != 1 {
		t.Fatalf("drained readd onboarding events = %d, want 1", len(drain.Events))
	}
	event := drain.Events[0]
	removeChannelAgentForOnboardingTest(t, channelID, agentID)

	req := newRequest(http.MethodPost, "/api/agent/messages/send", map[string]any{
		"target":  "#" + channelName,
		"content": "must not send after removal",
	})
	req = withChatTestWorkspaceCtx(t, req)
	req.Header.Set("X-Actor-Source", "agent_inbox_token")
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Agent-Inbox-Event-ID", event.ID)
	req.Header.Set("X-Agent-Inbox-Delivery-ID", event.DeliveryID)
	req.Header.Set("X-Task-ID", event.ID)
	rec := httptest.NewRecorder()
	testHandler.AgentTransportSendMessage(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("send after onboarding removal: status=%d body=%s, want 409", rec.Code, rec.Body.String())
	}
	var visible int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM channel_message WHERE channel_id = $1 AND author_type = 'agent' AND author_id = $2`, channelID, agentID).Scan(&visible); err != nil {
		t.Fatalf("count post-removal onboarding messages: %v", err)
	}
	if visible != 0 {
		t.Fatalf("post-removal onboarding visible messages = %d, want 0", visible)
	}
}

func TestChannelOnboardingArchiveAfterDrainExpiresWithoutVisibleSend(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Onboarding Archive "+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "onboarding-archive-"+uuid.NewString(), testUserID)
	channelName := channelNameForTransportTest(t, channelID)
	addChannelAgentForOnboardingTest(t, channelID, agentID)
	drain := drainChannelOnboardingForTest(t, handlerTestRuntimeID(t))
	if len(drain.Events) != 1 {
		t.Fatalf("drained archive onboarding events = %d, want 1", len(drain.Events))
	}
	event := drain.Events[0]

	if _, err := testPool.Exec(ctx, `UPDATE channel SET archived_at = now() WHERE id = $1`, channelID); err != nil {
		t.Fatalf("archive onboarding channel: %v", err)
	}
	req := newRequest(http.MethodPost, "/api/agent/messages/send", map[string]any{
		"target":  "#" + channelName,
		"content": "must not send after archive",
	})
	req = withChatTestWorkspaceCtx(t, req)
	req.Header.Set("X-Actor-Source", "agent_inbox_token")
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Agent-Inbox-Event-ID", event.ID)
	req.Header.Set("X-Agent-Inbox-Delivery-ID", event.DeliveryID)
	req.Header.Set("X-Task-ID", event.ID)
	rec := httptest.NewRecorder()
	testHandler.AgentTransportSendMessage(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("send after onboarding channel archive: status=%d body=%s, want 409", rec.Code, rec.Body.String())
	}

	var onboardingStatus, inboxOutcome string
	var visible int
	if err := testPool.QueryRow(ctx, `
		SELECT onboarding.status, COALESCE(inbox.terminal_outcome, '')
		FROM channel_agent_onboarding onboarding
		JOIN agent_inbox_event inbox ON inbox.channel_onboarding_id = onboarding.id
		WHERE inbox.id = $1`, event.ID).Scan(&onboardingStatus, &inboxOutcome); err != nil {
		t.Fatalf("load archived onboarding terminal state: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM channel_message
		WHERE channel_id = $1 AND author_type = 'agent' AND author_id = $2`, channelID, agentID).Scan(&visible); err != nil {
		t.Fatalf("count archived onboarding messages: %v", err)
	}
	if onboardingStatus != "expired" || inboxOutcome != "expired" || visible != 0 {
		t.Fatalf("archived onboarding state = onboarding:%q inbox:%q visible:%d", onboardingStatus, inboxOutcome, visible)
	}
}

func TestChannelOnboardingSystemGeneralWaitsForDurablePublication(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Onboarding General "+uuid.NewString()[:8], nil)

	var onboardingID, channelID, generationID, sourceType, sourceActorID, publicationStatus string
	if err := testPool.QueryRow(ctx, `
		SELECT onboarding.id::text, onboarding.channel_id::text,
		       onboarding.membership_generation_id::text,
		       onboarding.source_type,
		       COALESCE(onboarding.source_actor_id::text, ''),
		       onboarding.publication_status
		FROM channel_agent_onboarding onboarding
		JOIN channel channel_row ON channel_row.id = onboarding.channel_id
		WHERE onboarding.agent_id = $1 AND channel_row.system_key = 'general'`, agentID).Scan(
		&onboardingID, &channelID, &generationID, &sourceType, &sourceActorID, &publicationStatus,
	); err != nil {
		t.Fatalf("load system-general onboarding: %v", err)
	}
	if onboardingID == "" || generationID == "" || sourceType != "system_invariant" || sourceActorID != "" || publicationStatus != "pending" {
		t.Fatalf("system-general onboarding = id:%q generation:%q source:%q actor:%q publication:%q", onboardingID, generationID, sourceType, sourceActorID, publicationStatus)
	}

	before := drainChannelOnboardingForTest(t, handlerTestRuntimeID(t))
	if len(before.Events) != 0 {
		t.Fatalf("system-general onboarding leased before publication: %+v", before.Events)
	}
	if err := testHandler.publishChannelOnboardingSystemMessageForGeneration(ctx, parseUUID(generationID)); err != nil {
		t.Fatalf("publish system-general onboarding generation: %v", err)
	}
	after := drainChannelOnboardingForTest(t, handlerTestRuntimeID(t))
	if len(after.Events) != 1 || after.Events[0].AgentID != agentID || after.Events[0].ChannelID != channelID || after.Events[0].Reason != protocol.ChannelOnboardingReason {
		t.Fatalf("system-general onboarding after publication = %+v", after.Events)
	}
	completeChannelOnboardingForTest(t, after.Events[0], "", protocol.ChannelOnboardingDecisionSkipped, http.StatusOK)
}

func TestChannelOnboardingPublicationFailureStaysRetryableAndBlocksLease(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Onboarding Publish Failure "+uuid.NewString()[:8], nil)

	var onboardingID, generationID string
	if err := testPool.QueryRow(ctx, `
		SELECT onboarding.id::text, onboarding.membership_generation_id::text
		FROM channel_agent_onboarding onboarding
		JOIN channel channel_row ON channel_row.id = onboarding.channel_id
		WHERE onboarding.agent_id = $1 AND channel_row.system_key = 'general'`, agentID).Scan(&onboardingID, &generationID); err != nil {
		t.Fatalf("load pending system-general onboarding: %v", err)
	}

	noAckHandler := *testHandler
	noAckHandler.Bus = events.New()
	err := noAckHandler.publishChannelOnboardingSystemMessageForGeneration(ctx, parseUUID(generationID))
	if err == nil || !strings.Contains(err.Error(), "did not acknowledge") {
		t.Fatalf("missing-listener publication error = %v", err)
	}
	assertChannelOnboardingPublicationRetryable(t, onboardingID, 1)
	if drain := drainChannelOnboardingForTest(t, handlerTestRuntimeID(t)); len(drain.Events) != 0 {
		t.Fatalf("missing-listener publication became leaseable: %+v", drain.Events)
	}

	panicBus := events.New()
	panicBus.SubscribeAll(func(events.Event) {
		panic("realtime listener panic")
	})
	panicHandler := *testHandler
	panicHandler.Bus = panicBus
	err = panicHandler.publishChannelOnboardingSystemMessageForGeneration(ctx, parseUUID(generationID))
	if err == nil || !strings.Contains(err.Error(), "did not acknowledge") {
		t.Fatalf("panicked-listener publication error = %v", err)
	}
	assertChannelOnboardingPublicationRetryable(t, onboardingID, 2)
	if drain := drainChannelOnboardingForTest(t, handlerTestRuntimeID(t)); len(drain.Events) != 0 {
		t.Fatalf("panicked-listener publication became leaseable: %+v", drain.Events)
	}

	wantRelayErr := errors.New("relay unavailable")
	failedBus := events.New()
	failedBus.SubscribeAll(func(event events.Event) {
		if event.RealtimeDeliveryAck != nil {
			event.RealtimeDeliveryAck(wantRelayErr)
		}
	})
	failedHandler := *testHandler
	failedHandler.Bus = failedBus
	err = failedHandler.publishChannelOnboardingSystemMessageForGeneration(ctx, parseUUID(generationID))
	if !errors.Is(err, wantRelayErr) {
		t.Fatalf("relay-failure publication error = %v, want %v", err, wantRelayErr)
	}
	assertChannelOnboardingPublicationRetryable(t, onboardingID, 3)
	if drain := drainChannelOnboardingForTest(t, handlerTestRuntimeID(t)); len(drain.Events) != 0 {
		t.Fatalf("relay-failure publication became leaseable: %+v", drain.Events)
	}

	if err := testHandler.publishChannelOnboardingSystemMessageForGeneration(ctx, parseUUID(generationID)); err != nil {
		t.Fatalf("retry confirmed system-general publication: %v", err)
	}
	var publicationStatus string
	var publicationAttempt int
	if err := testPool.QueryRow(ctx, `
		SELECT publication_status, publication_attempt
		FROM channel_agent_onboarding WHERE id = $1`, onboardingID).Scan(&publicationStatus, &publicationAttempt); err != nil {
		t.Fatalf("load confirmed publication state: %v", err)
	}
	if publicationStatus != "published" || publicationAttempt != 4 {
		t.Fatalf("confirmed publication = status:%q attempt:%d, want published/4", publicationStatus, publicationAttempt)
	}
	drain := drainChannelOnboardingForTest(t, handlerTestRuntimeID(t))
	if len(drain.Events) != 1 || drain.Events[0].AgentID != agentID {
		t.Fatalf("confirmed publication drain = %+v", drain.Events)
	}
	completeChannelOnboardingForTest(t, drain.Events[0], "", protocol.ChannelOnboardingDecisionSkipped, http.StatusOK)
}

func assertChannelOnboardingPublicationRetryable(t *testing.T, onboardingID string, wantAttempt int) {
	t.Helper()
	var status, publicationStatus string
	var publicationAttempt int
	var leasePresent, publishedPresent bool
	if err := testPool.QueryRow(context.Background(), `
		SELECT status,
		       publication_status,
		       publication_attempt,
		       publication_lease_expires_at IS NOT NULL,
		       published_at IS NOT NULL
		FROM channel_agent_onboarding
		WHERE id = $1`, onboardingID).Scan(
		&status, &publicationStatus, &publicationAttempt, &leasePresent, &publishedPresent,
	); err != nil {
		t.Fatalf("load failed publication state: %v", err)
	}
	if status != "pending" || publicationStatus != "pending" || publicationAttempt != wantAttempt || leasePresent || publishedPresent {
		t.Fatalf("failed publication = status:%q publication:%q attempt:%d lease:%v published:%v", status, publicationStatus, publicationAttempt, leasePresent, publishedPresent)
	}
}

func TestChannelOnboardingRemovedGenerationCannotUseFastPublicationPath(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Onboarding Removed Publish "+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "onboarding-removed-publish-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (
		  channel_id, workspace_id, member_type, member_id, join_source,
		  added_by_type, added_by_id
		)
		VALUES ($1, $2, 'agent', $3, 'manual', 'user', $4)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID, testUserID); err != nil {
		t.Fatalf("insert pending onboarding generation: %v", err)
	}

	var onboardingID, generationID, systemMessageID string
	if err := testPool.QueryRow(ctx, `
		SELECT onboarding.id::text,
		       onboarding.membership_generation_id::text,
		       onboarding.system_message_id::text
		FROM channel_agent_onboarding onboarding
		WHERE onboarding.channel_id = $1 AND onboarding.agent_id = $2`, channelID, agentID).Scan(
		&onboardingID, &generationID, &systemMessageID,
	); err != nil {
		t.Fatalf("load removable pending onboarding: %v", err)
	}

	published := 0
	testHandler.Bus.Subscribe(protocol.EventChannelMessage, func(event events.Event) {
		if event.RealtimeEventID == systemMessageID {
			published++
		}
	})
	if _, err := testPool.Exec(ctx, `
		DELETE FROM channel_member
		WHERE channel_id = $1
		  AND member_type = 'agent'
		  AND member_id = $2
		  AND generation_id = $3`, channelID, agentID, generationID); err != nil {
		t.Fatalf("remove pending onboarding generation: %v", err)
	}
	if err := testHandler.publishChannelOnboardingSystemMessageForGeneration(ctx, parseUUID(generationID)); err != nil {
		t.Fatalf("fast publication after removal: %v", err)
	}

	var status, publicationStatus string
	var leasePresent bool
	if err := testPool.QueryRow(ctx, `
		SELECT status, publication_status, publication_lease_expires_at IS NOT NULL
		FROM channel_agent_onboarding WHERE id = $1`, onboardingID).Scan(&status, &publicationStatus, &leasePresent); err != nil {
		t.Fatalf("load removed generation publication state: %v", err)
	}
	if status != "expired" || publicationStatus != "pending" || leasePresent || published != 0 {
		t.Fatalf("removed generation = status:%q publication:%q lease:%v events:%d", status, publicationStatus, leasePresent, published)
	}
	if drain := drainChannelOnboardingForTest(t, handlerTestRuntimeID(t)); len(drain.Events) != 0 {
		t.Fatalf("removed generation became leaseable: %+v", drain.Events)
	}
}

func TestChannelOnboardingPublicationSerializesClaimBeforeConcurrentRemoval(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Onboarding Publish Remove Race "+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "onboarding-publish-remove-race-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (
		  channel_id, workspace_id, member_type, member_id, join_source,
		  added_by_type, added_by_id
		)
		VALUES ($1, $2, 'agent', $3, 'manual', 'user', $4)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID, testUserID); err != nil {
		t.Fatalf("insert pending onboarding generation: %v", err)
	}

	var onboardingID, generationID, systemMessageID string
	if err := testPool.QueryRow(ctx, `
		SELECT id::text, membership_generation_id::text, system_message_id::text
		FROM channel_agent_onboarding
		WHERE channel_id = $1 AND agent_id = $2`, channelID, agentID).Scan(
		&onboardingID, &generationID, &systemMessageID,
	); err != nil {
		t.Fatalf("load race onboarding generation: %v", err)
	}

	enteredFanout := make(chan struct{}, 1)
	releaseFanout := make(chan struct{})
	publishedEvents := 0
	testHandler.Bus.Subscribe(protocol.EventChannelMessage, func(event events.Event) {
		if event.RealtimeEventID != systemMessageID {
			return
		}
		publishedEvents++
		enteredFanout <- struct{}{}
		<-releaseFanout
	})

	publishDone := make(chan error, 1)
	go func() {
		publishDone <- testHandler.publishChannelOnboardingSystemMessageForGeneration(ctx, parseUUID(generationID))
	}()
	select {
	case <-enteredFanout:
	case <-time.After(5 * time.Second):
		t.Fatal("publication did not reach blocked fanout after claim")
	}

	removeDone := make(chan error, 1)
	go func() {
		_, err := testPool.Exec(ctx, `
			DELETE FROM channel_member
			WHERE channel_id = $1
			  AND member_type = 'agent'
			  AND member_id = $2
			  AND generation_id = $3`, channelID, agentID, generationID)
		removeDone <- err
	}()
	select {
	case err := <-removeDone:
		t.Fatalf("membership removal completed before publication transaction released generation lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	close(releaseFanout)
	select {
	case err := <-publishDone:
		if err != nil {
			t.Fatalf("publish claimed generation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("publication did not finish after fanout release")
	}
	select {
	case err := <-removeDone:
		if err != nil {
			t.Fatalf("remove generation after publication commit: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("membership removal did not finish after publication commit")
	}

	var status, publicationStatus string
	var publishedAtPresent bool
	if err := testPool.QueryRow(ctx, `
		SELECT status, publication_status, published_at IS NOT NULL
		FROM channel_agent_onboarding WHERE id = $1`, onboardingID).Scan(
		&status, &publicationStatus, &publishedAtPresent,
	); err != nil {
		t.Fatalf("load serialized publication/removal state: %v", err)
	}
	if status != "expired" || publicationStatus != "published" || !publishedAtPresent || publishedEvents != 1 {
		t.Fatalf("serialized publication/removal = status:%q publication:%q published_at:%v events:%d", status, publicationStatus, publishedAtPresent, publishedEvents)
	}
	if drain := drainChannelOnboardingForTest(t, handlerTestRuntimeID(t)); len(drain.Events) != 0 {
		t.Fatalf("removed generation became leaseable after serialized publication: %+v", drain.Events)
	}
}

func TestChannelOnboardingHumanAddCreatesNoOnboarding(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	channelID := seedChannelForTest(t, "onboarding-human-"+uuid.NewString(), testUserID)
	var humanID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (email, name, display_name)
		VALUES ($1, $2, $3)
		RETURNING id`, "onboarding-"+uuid.NewString()+"@example.com", "onboarding-human-"+uuid.NewString()[:8], "Onboarding Human").Scan(&humanID); err != nil {
		t.Fatalf("create onboarding human: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, humanID) })
	if _, err := testPool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')`, testWorkspaceID, humanID); err != nil {
		t.Fatalf("create onboarding workspace member: %v", err)
	}

	req := newRequest(http.MethodPost, "/api/channels/"+channelID+"/members", AddChannelMemberRequest{MemberType: "user", MemberID: humanID})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.AddChannelMember(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add human channel member: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var onboardingRows, addedSystemRows int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM channel_agent_onboarding WHERE channel_id = $1`, channelID).Scan(&onboardingRows); err != nil {
		t.Fatalf("count human-add onboardings: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM channel_message
		WHERE channel_id = $1 AND author_type = 'system' AND parts->0->>'event' = 'channel_member_added'`, channelID).Scan(&addedSystemRows); err != nil {
		t.Fatalf("count human-add system rows: %v", err)
	}
	if onboardingRows != 0 || addedSystemRows != 1 {
		t.Fatalf("human add ledger = onboarding:%d system_rows:%d, want 0/1", onboardingRows, addedSystemRows)
	}
}
