package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestAgentTransportSendMessageIdempotentAndSuppressesFinalOutput(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	clientID := "transport-" + uuid.NewString()
	content := "hello via transport " + uuid.NewString()

	first := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"content":           content,
		"client_message_id": clientID,
	})
	if first.Code != http.StatusCreated {
		t.Fatalf("first transport send: status=%d body=%s", first.Code, first.Body.String())
	}
	var firstBody AgentTransportSendResponse
	if err := json.Unmarshal(first.Body.Bytes(), &firstBody); err != nil {
		t.Fatalf("decode first send: %v", err)
	}
	if !firstBody.Created {
		t.Fatal("first transport send created=false, want true")
	}
	if firstBody.Message.Content != content || firstBody.Message.ClientMessageID == nil || *firstBody.Message.ClientMessageID != clientID {
		t.Fatalf("first message payload mismatch: %+v", firstBody.Message)
	}

	second := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"content":           content,
		"client_message_id": clientID,
	})
	if second.Code != http.StatusCreated {
		t.Fatalf("second transport send: status=%d body=%s", second.Code, second.Body.String())
	}
	var secondBody AgentTransportSendResponse
	if err := json.Unmarshal(second.Body.Bytes(), &secondBody); err != nil {
		t.Fatalf("decode second send: %v", err)
	}
	if secondBody.Created {
		t.Fatal("second transport send created=true, want idempotent replay")
	}
	if secondBody.Message.ID != firstBody.Message.ID {
		t.Fatalf("idempotent replay message id=%s, want %s", secondBody.Message.ID, firstBody.Message.ID)
	}

	var messageRows int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1 AND author_type = 'agent' AND client_message_id = $2 AND content = $3`,
		channelID, clientID, content).Scan(&messageRows); err != nil {
		t.Fatalf("count idempotent channel messages: %v", err)
	}
	if messageRows != 1 {
		t.Fatalf("transport message rows=%d, want 1", messageRows)
	}
	assertAgentTransportAuditCount(t, taskID, agentTransportActionSend, 2)

	finalText := "duplicate final text " + uuid.NewString()
	done := completeTaskForTest(t, taskID, map[string]any{"output": finalText})
	if done.Code != http.StatusOK {
		t.Fatalf("complete task: status=%d body=%s", done.Code, done.Body.String())
	}
	assertTaskOutputSuppressedReason(t, taskID, protocol.ChannelOutputSuppressedReasonToolTransportOutput)
	assertNoChannelMessageContent(t, channelID, finalText)
}

func TestAgentTransportSendMessageStickerOnlyAndWithText(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)

	stickerOnlyID := "transport-sticker-" + uuid.NewString()
	stickerOnly := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"parts": []protocol.MessagePart{{
			Type:      protocol.MessagePartTypeSticker,
			StickerID: "hi",
		}},
		"client_message_id": stickerOnlyID,
	})
	if stickerOnly.Code != http.StatusCreated {
		t.Fatalf("sticker-only transport send: status=%d body=%s", stickerOnly.Code, stickerOnly.Body.String())
	}
	var stickerOnlyBody AgentTransportSendResponse
	if err := json.Unmarshal(stickerOnly.Body.Bytes(), &stickerOnlyBody); err != nil {
		t.Fatalf("decode sticker-only send: %v", err)
	}
	if len(stickerOnlyBody.Message.Parts) != 1 || stickerOnlyBody.Message.Parts[0].Type != protocol.MessagePartTypeSticker || stickerOnlyBody.Message.Parts[0].StickerID != "hi" {
		t.Fatalf("sticker-only parts = %+v, want hi sticker", stickerOnlyBody.Message.Parts)
	}

	explanation := "这个问题是因为 transport sticker test " + uuid.NewString()
	combinedID := "transport-combined-" + uuid.NewString()
	combined := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"content": explanation,
		"parts": []protocol.MessagePart{
			{Type: protocol.MessagePartTypeSticker, StickerID: "got-it"},
			{Type: protocol.MessagePartTypeText, Text: explanation},
		},
		"client_message_id": combinedID,
	})
	if combined.Code != http.StatusCreated {
		t.Fatalf("combined transport send: status=%d body=%s", combined.Code, combined.Body.String())
	}
	var combinedBody AgentTransportSendResponse
	if err := json.Unmarshal(combined.Body.Bytes(), &combinedBody); err != nil {
		t.Fatalf("decode combined send: %v", err)
	}
	if len(combinedBody.Message.Parts) != 2 ||
		combinedBody.Message.Parts[0].Type != protocol.MessagePartTypeSticker ||
		combinedBody.Message.Parts[0].StickerID != "got-it" ||
		combinedBody.Message.Parts[1].Type != protocol.MessagePartTypeText ||
		combinedBody.Message.Parts[1].Text != explanation {
		t.Fatalf("combined parts = %+v, want got-it sticker then text", combinedBody.Message.Parts)
	}

	var messageRows int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1 AND author_type = 'agent' AND client_message_id IN ($2, $3)`,
		channelID, stickerOnlyID, combinedID).Scan(&messageRows); err != nil {
		t.Fatalf("count transport sticker messages: %v", err)
	}
	if messageRows != 2 {
		t.Fatalf("transport sticker message rows=%d, want 2", messageRows)
	}
}

func TestAgentTransportSendMessageLinksOwnedAttachmentsOnly(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)

	ownedAttachmentID := seedUnboundAgentAttachmentForTest(t, agentID, "agent-file.png")
	otherAgentID := uuid.NewString()
	foreignAttachmentID := seedUnboundAgentAttachmentForTest(t, otherAgentID, "not-mine.png")

	clientID := "transport-attachment-" + uuid.NewString()
	resp := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"content":           "here's the file",
		"attachment_ids":    []string{ownedAttachmentID, foreignAttachmentID},
		"client_message_id": clientID,
	})
	if resp.Code != http.StatusCreated {
		t.Fatalf("transport send with attachments: status=%d body=%s", resp.Code, resp.Body.String())
	}
	var body AgentTransportSendResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode transport send: %v", err)
	}
	if len(body.Message.Attachments) != 1 || body.Message.Attachments[0].ID != ownedAttachmentID {
		t.Fatalf("message attachments = %+v, want only owned attachment %s", body.Message.Attachments, ownedAttachmentID)
	}

	var ownedChannelID, ownedMessageID string
	if err := testPool.QueryRow(ctx, `SELECT channel_id, channel_message_id FROM attachment WHERE id = $1`, ownedAttachmentID).Scan(&ownedChannelID, &ownedMessageID); err != nil {
		t.Fatalf("load owned attachment: %v", err)
	}
	if ownedChannelID != channelID || ownedMessageID != body.Message.ID {
		t.Fatalf("owned attachment not linked: channel_id=%s message_id=%s, want channel=%s message=%s", ownedChannelID, ownedMessageID, channelID, body.Message.ID)
	}

	var foreignChannelID, foreignMessageID pgtype.UUID
	if err := testPool.QueryRow(ctx, `SELECT channel_id, channel_message_id FROM attachment WHERE id = $1`, foreignAttachmentID).Scan(&foreignChannelID, &foreignMessageID); err != nil {
		t.Fatalf("load foreign attachment: %v", err)
	}
	if foreignChannelID.Valid || foreignMessageID.Valid {
		t.Fatalf("foreign attachment got linked: channel_id=%+v message_id=%+v, want both NULL", foreignChannelID, foreignMessageID)
	}
}

func TestAgentTransportSendThreadReplyIDFlattensToRoot(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)

	// Create a root message in the channel.
	var rootID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, seq)
		VALUES ($1, $2, 'user', $3, 'test-user', 'thread root msg', 1)
		RETURNING id`,
		channelID, testWorkspaceID, testUserID).Scan(&rootID); err != nil {
		t.Fatalf("create root message: %v", err)
	}

	// Create a thread reply under that root.
	var replyID string
	var threadID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, seq, reply_to_message_id, thread_root_message_id, thread_id, trigger_depth)
		VALUES ($1, $2, 'agent', $3, 'test-agent', 'thread reply msg', 2, $4, $4, gen_random_uuid()::text, 1)
		RETURNING id, thread_id`,
		channelID, testWorkspaceID, agentID, rootID).Scan(&replyID, &threadID); err != nil {
		t.Fatalf("create thread reply: %v", err)
	}

	// Look up the channel name for the target.
	var channelName string
	if err := testPool.QueryRow(ctx, `SELECT name FROM channel WHERE id = $1`, channelID).Scan(&channelName); err != nil {
		t.Fatalf("get channel name: %v", err)
	}

	// Send using the REPLY id as the thread target — should flatten to root.
	resp := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            "#" + channelName + ":" + replyID,
		"content":           "reply-to-thread-reply-id should flatten",
		"client_message_id": "flatten-test-" + uuid.NewString(),
	})
	if resp.Code != http.StatusCreated {
		t.Fatalf("send targeting thread reply id: status=%d body=%s", resp.Code, resp.Body.String())
	}
	var body AgentTransportSendResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// The message must land in the thread (thread_root_message_id = rootID, not the reply).
	if body.Message.ThreadRootMessageID == nil || *body.Message.ThreadRootMessageID != rootID {
		t.Fatalf("thread reply id did not flatten to root: got thread_root_message_id=%v, want %s",
			body.Message.ThreadRootMessageID, rootID)
	}
}

func TestAgentTransportReadSearchAndReactAudit(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	needle := "needle transport search " + uuid.NewString()
	seeded, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", needle, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("seed channel message: %v", err)
	}

	readRec := agentTransportReadForTest(t, taskID, agentID, map[string]any{"limit": 5})
	if readRec.Code != http.StatusOK {
		t.Fatalf("transport read: status=%d body=%s", readRec.Code, readRec.Body.String())
	}
	var readBody AgentTransportReadResponse
	if err := json.Unmarshal(readRec.Body.Bytes(), &readBody); err != nil {
		t.Fatalf("decode transport read: %v", err)
	}
	if !transportMessagesContain(readBody.Messages, seeded.ID, needle) {
		t.Fatalf("read messages did not include seeded message %s: %+v", seeded.ID, readBody.Messages)
	}

	searchRec := agentTransportSearchForTest(t, taskID, agentID, map[string]any{
		"query": "needle transport search",
		"limit": 10,
	})
	if searchRec.Code != http.StatusOK {
		t.Fatalf("transport search: status=%d body=%s", searchRec.Code, searchRec.Body.String())
	}
	var searchBody AgentTransportSearchResponse
	if err := json.Unmarshal(searchRec.Body.Bytes(), &searchBody); err != nil {
		t.Fatalf("decode transport search: %v", err)
	}
	if searchBody.Total < 1 || !transportSearchResultsContain(searchBody.Results, seeded.ID, needle) {
		t.Fatalf("search results did not include seeded message %s: %+v", seeded.ID, searchBody.Results)
	}

	reactClientID := "transport-react-" + uuid.NewString()
	reactRec := agentTransportReactForTest(t, taskID, agentID, map[string]any{
		"message_id":        seeded.ID,
		"emoji":             "+1",
		"client_message_id": reactClientID,
	})
	if reactRec.Code != http.StatusCreated {
		t.Fatalf("transport react: status=%d body=%s", reactRec.Code, reactRec.Body.String())
	}
	var reactBody AgentTransportReactResponse
	if err := json.Unmarshal(reactRec.Body.Bytes(), &reactBody); err != nil {
		t.Fatalf("decode transport react: %v", err)
	}
	if reactBody.Reaction.MessageID != seeded.ID || reactBody.Reaction.ActorID != agentID || reactBody.Reaction.Emoji != "+1" {
		t.Fatalf("reaction payload mismatch: %+v", reactBody.Reaction)
	}

	var reactionRows int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message_reaction
		WHERE channel_message_id = $1 AND actor_type = 'agent' AND actor_id = $2 AND emoji = '+1'`,
		seeded.ID, agentID).Scan(&reactionRows); err != nil {
		t.Fatalf("count agent reaction: %v", err)
	}
	if reactionRows != 1 {
		t.Fatalf("agent reaction rows=%d, want 1", reactionRows)
	}
	assertAgentTransportAuditCount(t, taskID, agentTransportActionRead, 1)
	assertAgentTransportAuditCount(t, taskID, agentTransportActionSearch, 1)
	assertAgentTransportAuditCount(t, taskID, agentTransportActionReact, 1)
}

func TestAgentTransportDMHandleTargetRejectsMissingAndAmbiguousHandles(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	taskID, _ := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)

	missing := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            "dm:@missing-" + uuid.NewString(),
		"content":           "should not send",
		"client_message_id": "transport-" + uuid.NewString(),
	})
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing dm handle target: status=%d body=%s", missing.Code, missing.Body.String())
	}

	handle := "ambiguous" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	lowerUserID := seedWorkspaceUserForTransportTargetTest(t, handle)
	upperUserID := seedWorkspaceUserForTransportTargetTest(t, strings.ToUpper(handle))
	if lowerUserID == upperUserID {
		t.Fatal("ambiguous fixture reused the same user id")
	}

	ambiguous := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            "dm:@" + handle,
		"content":           "should not send",
		"client_message_id": "transport-" + uuid.NewString(),
	})
	if ambiguous.Code != http.StatusBadRequest {
		t.Fatalf("ambiguous dm handle target: status=%d body=%s", ambiguous.Code, ambiguous.Body.String())
	}
}

func TestAgentTransportAutoRetryReassignsPendingWake(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	var chatSessionID string
	if err := testPool.QueryRow(ctx, `
		SELECT chat_session_id
		FROM agent_task_queue
		WHERE id = $1`, taskID).Scan(&chatSessionID); err != nil {
		t.Fatalf("load chat session: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_ambient_pending_wake (
			conversation_id, channel_id, workspace_id, agent_id, chat_session_id, task_id,
			status, pending_from_seq, pending_to_seq, delivered_to_seq
		)
		SELECT c.id, $1, $2, $3, $4, $5, 'queued', 1, 1, 0
		FROM conversation c
		WHERE c.channel_id = $1`,
		channelID, testWorkspaceID, agentID, chatSessionID, taskID); err != nil {
		t.Fatalf("seed pending wake: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_task_queue
		SET status = 'failed', failure_reason = 'runtime_offline', attempt = 1, max_attempts = 3
		WHERE id = $1`, taskID); err != nil {
		t.Fatalf("mark parent failed: %v", err)
	}
	parent, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(taskID))
	if err != nil {
		t.Fatalf("load failed parent task: %v", err)
	}

	child, err := testHandler.TaskService.MaybeRetryFailedTask(ctx, parent)
	if err != nil {
		t.Fatalf("MaybeRetryFailedTask: %v", err)
	}
	if child == nil {
		t.Fatal("MaybeRetryFailedTask returned nil child")
	}

	var pendingTaskID string
	if err := testPool.QueryRow(ctx, `
		SELECT task_id
		FROM channel_ambient_pending_wake
		WHERE channel_id = $1 AND agent_id = $2`, channelID, agentID).Scan(&pendingTaskID); err != nil {
		t.Fatalf("load pending wake task: %v", err)
	}
	if pendingTaskID != uuidToString(child.ID) {
		t.Fatalf("pending wake task_id=%s, want retry child %s", pendingTaskID, uuidToString(child.ID))
	}
}

func TestAgentTransportAutoRetryFailsClosedForSettledPendingWake(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	var chatSessionID string
	if err := testPool.QueryRow(ctx, `
		SELECT chat_session_id
		FROM agent_task_queue
		WHERE id = $1`, taskID).Scan(&chatSessionID); err != nil {
		t.Fatalf("load chat session: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_ambient_pending_wake (
			conversation_id, channel_id, workspace_id, agent_id, chat_session_id, task_id,
			status, pending_from_seq, pending_to_seq, delivered_to_seq, completed_at
		)
		SELECT c.id, $1, $2, $3, $4, $5, 'failed', 1, 1, 0, now()
		FROM conversation c
		WHERE c.channel_id = $1`,
		channelID, testWorkspaceID, agentID, chatSessionID, taskID); err != nil {
		t.Fatalf("seed settled pending wake: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_task_queue
		SET status = 'failed', failure_reason = 'runtime_offline', attempt = 1, max_attempts = 3
		WHERE id = $1`, taskID); err != nil {
		t.Fatalf("mark parent failed: %v", err)
	}
	parent, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(taskID))
	if err != nil {
		t.Fatalf("load failed parent task: %v", err)
	}

	child, err := testHandler.TaskService.MaybeRetryFailedTask(ctx, parent)
	if err == nil {
		t.Fatal("MaybeRetryFailedTask succeeded, want fail-closed error")
	}
	if child != nil {
		t.Fatalf("MaybeRetryFailedTask child=%s, want nil", uuidToString(child.ID))
	}

	var childCount int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM agent_task_queue
		WHERE parent_task_id = $1`, taskID).Scan(&childCount); err != nil {
		t.Fatalf("count retry children: %v", err)
	}
	if childCount != 0 {
		t.Fatalf("retry child count = %d, want 0", childCount)
	}
	var pendingTaskID string
	if err := testPool.QueryRow(ctx, `
		SELECT task_id
		FROM channel_ambient_pending_wake
		WHERE channel_id = $1 AND agent_id = $2`, channelID, agentID).Scan(&pendingTaskID); err != nil {
		t.Fatalf("load pending wake task: %v", err)
	}
	if pendingTaskID != taskID {
		t.Fatalf("pending wake task_id=%s, want original parent %s", pendingTaskID, taskID)
	}
}

func agentTransportSendForTest(t *testing.T, taskID, agentID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	req := agentTransportRequest(t, http.MethodPost, "/api/agent/messages/send", taskID, agentID, body)
	rec := httptest.NewRecorder()
	testHandler.AgentTransportSendMessage(rec, req)
	return rec
}

func agentTransportReactForTest(t *testing.T, taskID, agentID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	req := agentTransportRequest(t, http.MethodPost, "/api/agent/messages/react", taskID, agentID, body)
	rec := httptest.NewRecorder()
	testHandler.AgentTransportReactMessage(rec, req)
	return rec
}

func agentTransportReadForTest(t *testing.T, taskID, agentID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	req := agentTransportRequest(t, http.MethodPost, "/api/agent/messages/read", taskID, agentID, body)
	rec := httptest.NewRecorder()
	testHandler.AgentTransportReadMessages(rec, req)
	return rec
}

func agentTransportSearchForTest(t *testing.T, taskID, agentID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	req := agentTransportRequest(t, http.MethodPost, "/api/agent/messages/search", taskID, agentID, body)
	rec := httptest.NewRecorder()
	testHandler.AgentTransportSearchMessages(rec, req)
	return rec
}

func agentTransportRequest(t *testing.T, method, path, taskID, agentID string, body any) *http.Request {
	t.Helper()
	req := newRequest(method, path, body)
	req = withChatTestWorkspaceCtx(t, req)
	req.Header.Set("X-Actor-Source", "task_token")
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", taskID)
	return req
}

func agentIDForTask(t *testing.T, taskID string) string {
	t.Helper()
	var agentID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT agent_id
		FROM agent_task_queue
		WHERE id = $1`, taskID).Scan(&agentID); err != nil {
		t.Fatalf("load task agent_id: %v", err)
	}
	return agentID
}

func seedWorkspaceUserForTransportTargetTest(t *testing.T, name string) string {
	t.Helper()
	var userID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO "user" (name, display_name, email)
		VALUES ($1, $2, $3)
		RETURNING id`,
		name, name, name+"@example.test").Scan(&userID); err != nil {
		t.Fatalf("seed user %s: %v", name, err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID) })
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'member')`, testWorkspaceID, userID); err != nil {
		t.Fatalf("seed workspace member %s: %v", name, err)
	}
	return userID
}

func seedUnboundAgentAttachmentForTest(t *testing.T, agentID, filename string) string {
	t.Helper()
	var attachmentID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO attachment (workspace_id, uploader_type, uploader_id, filename, url, content_type, size_bytes)
		VALUES ($1, 'agent', $2, $3, 's3://'||$3, 'image/png', 42)
		RETURNING id`,
		testWorkspaceID, agentID, filename).Scan(&attachmentID); err != nil {
		t.Fatalf("seed unbound agent attachment: %v", err)
	}
	return attachmentID
}

func assertAgentTransportAuditCount(t *testing.T, taskID, action string, want int) {
	t.Helper()
	var got int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM agent_task_transport_audit
		WHERE task_id = $1 AND action = $2`, taskID, action).Scan(&got); err != nil {
		t.Fatalf("count transport audit %s: %v", action, err)
	}
	if got != want {
		t.Fatalf("transport audit %s count=%d, want %d", action, got, want)
	}
}

func transportMessagesContain(messages []ChannelMessageResponse, id, content string) bool {
	for _, msg := range messages {
		if msg.ID == id && msg.Content == content {
			return true
		}
	}
	return false
}

func transportSearchResultsContain(results []ChannelMessageSearchResult, id, content string) bool {
	for _, result := range results {
		if result.MessageID == id && result.Content == content {
			return true
		}
	}
	return false
}
