package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// withChatTestWorkspaceCtx injects the workspace+member context that the
// real chi middleware chain would normally set. SendChatMessage (and most
// other chat handlers) read workspace ID from ctxWorkspaceID; without this
// the test harness, which calls handlers directly, gets "invalid workspace
// id" on the parseUUIDOrBadRequest call inside SendChatMessage.
func withChatTestWorkspaceCtx(t *testing.T, req *http.Request) *http.Request {
	t.Helper()
	memberRow, err := testHandler.Queries.GetMemberByUserAndWorkspace(context.Background(), db.GetMemberByUserAndWorkspaceParams{
		UserID:      util.MustParseUUID(testUserID),
		WorkspaceID: util.MustParseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("load test member row: %v", err)
	}
	return req.WithContext(middleware.SetMemberContext(req.Context(), testWorkspaceID, memberRow))
}

func TestChatMessageToResponseUnwrapsAssistantStructuredMessageSend(t *testing.T) {
	const visible = "Hello from structured output"
	resp := chatMessageToResponse(db.ChatMessage{
		ID:            util.MustParseUUID("11111111-1111-1111-1111-111111111111"),
		ChatSessionID: util.MustParseUUID("22222222-2222-2222-2222-222222222222"),
		Role:          "assistant",
		Content:       `Assistant reply: {"action":"message_send","output":"` + visible + `","parts":[{"type":"text","text":"` + visible + `"}]}`,
	}, nil)

	if resp.Content != visible {
		t.Fatalf("content = %q, want %q", resp.Content, visible)
	}
	if len(resp.Parts) != 1 || resp.Parts[0].Type != protocol.MessagePartTypeText || resp.Parts[0].Text != visible {
		t.Fatalf("parts = %+v, want one text part", resp.Parts)
	}
}

func TestChatMessageToResponseLeavesUserStructuredJSONAlone(t *testing.T) {
	raw := `{"action":"message_send","output":"not from an agent"}`
	resp := chatMessageToResponse(db.ChatMessage{
		ID:            util.MustParseUUID("11111111-1111-1111-1111-111111111111"),
		ChatSessionID: util.MustParseUUID("22222222-2222-2222-2222-222222222222"),
		Role:          "user",
		Content:       raw,
	}, nil)

	if resp.Content != raw {
		t.Fatalf("content = %q, want raw user JSON", resp.Content)
	}
	if len(resp.Parts) != 0 {
		t.Fatalf("parts = %+v, want none", resp.Parts)
	}
}

// TestSendChatMessage_LinksAttachments verifies that attachments uploaded
// against a chat_session (chat_message_id NULL) are back-filled with the
// message_id when SendChatMessage receives the matching attachment_ids.
func TestSendChatMessage_LinksAttachments(t *testing.T) {
	origStorage := testHandler.Storage
	testHandler.Storage = &mockStorage{}
	defer func() { testHandler.Storage = origStorage }()

	agentID := createHandlerTestAgent(t, "ChatSendAttachAgent", []byte("[]"))
	sessionID := createHandlerTestChatSession(t, agentID)

	// 1. Upload a file against the chat session.
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "send-link.png")
	part.Write([]byte("\x89PNG\r\n\x1a\nbytes"))
	writer.WriteField("chat_session_id", sessionID)
	writer.Close()

	uploadReq := httptest.NewRequest("POST", "/api/upload-file", &body)
	uploadReq.Header.Set("Content-Type", writer.FormDataContentType())
	uploadReq.Header.Set("X-User-ID", testUserID)
	uploadReq.Header.Set("X-Workspace-ID", testWorkspaceID)

	uploadW := httptest.NewRecorder()
	testHandler.UploadFile(uploadW, uploadReq)
	if uploadW.Code != http.StatusOK {
		t.Fatalf("upload precondition: %d %s", uploadW.Code, uploadW.Body.String())
	}
	var uploadResp AttachmentResponse
	if err := json.Unmarshal(uploadW.Body.Bytes(), &uploadResp); err != nil {
		t.Fatalf("decode upload: %v", err)
	}
	attachmentID := uploadResp.ID
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM attachment WHERE id = $1`, attachmentID)
	})

	// 2. Send a chat message that references the attachment.
	sendReq := newRequest("POST", "/api/chat-sessions/"+sessionID+"/messages", map[string]any{
		"content":        "look at this ![](" + uploadResp.URL + ")",
		"attachment_ids": []string{attachmentID},
	})
	sendReq = withURLParam(sendReq, "sessionId", sessionID)
	sendReq = withChatTestWorkspaceCtx(t, sendReq)
	sendW := httptest.NewRecorder()
	testHandler.SendChatMessage(sendW, sendReq)
	if sendW.Code != http.StatusCreated {
		t.Fatalf("SendChatMessage: expected 201, got %d: %s", sendW.Code, sendW.Body.String())
	}

	var sendResp SendChatMessageResponse
	if err := json.Unmarshal(sendW.Body.Bytes(), &sendResp); err != nil {
		t.Fatalf("decode send: %v", err)
	}
	if sendResp.MessageID == "" {
		t.Fatal("expected non-empty message_id in send response")
	}
	if sendResp.TaskID == "" {
		t.Fatal("expected non-empty task_id in send response")
	}

	var messageTaskID string
	if err := testPool.QueryRow(
		context.Background(),
		`SELECT COALESCE(task_id::text, '') FROM chat_message WHERE id = $1`,
		sendResp.MessageID,
	).Scan(&messageTaskID); err != nil {
		t.Fatalf("query chat message task id: %v", err)
	}
	if messageTaskID != sendResp.TaskID {
		t.Fatalf("chat message task_id mismatch: want %s, got %s", sendResp.TaskID, messageTaskID)
	}

	// 3. Verify the attachment row now points at the new message.
	var dbMessageID *string
	if err := testPool.QueryRow(
		context.Background(),
		`SELECT chat_message_id::text FROM attachment WHERE id = $1`,
		attachmentID,
	).Scan(&dbMessageID); err != nil {
		t.Fatalf("query attachment: %v", err)
	}
	if dbMessageID == nil {
		t.Fatal("chat_message_id is still NULL after send")
	}
	if *dbMessageID != sendResp.MessageID {
		t.Fatalf("chat_message_id mismatch: want %s, got %s", sendResp.MessageID, *dbMessageID)
	}
}

func TestSendChatMessageStoresStickerParts(t *testing.T) {
	agentID := createHandlerTestAgent(t, "ChatStickerPartsAgent", []byte("[]"))
	sessionID := createHandlerTestChatSession(t, agentID)

	req := newRequest("POST", "/api/chat-sessions/"+sessionID+"/messages", map[string]any{
		"parts": []protocol.MessagePart{{
			Type:      protocol.MessagePartTypeSticker,
			StickerID: "hi",
		}},
	})
	req = withURLParam(req, "sessionId", sessionID)
	req = withChatTestWorkspaceCtx(t, req)
	w := httptest.NewRecorder()
	testHandler.SendChatMessage(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("SendChatMessage: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp SendChatMessageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode send response: %v", err)
	}
	var content string
	var rawParts []byte
	if err := testPool.QueryRow(context.Background(), `SELECT content, parts FROM chat_message WHERE id = $1`, resp.MessageID).Scan(&content, &rawParts); err != nil {
		t.Fatalf("load stored chat message: %v", err)
	}
	var parts []protocol.MessagePart
	if err := json.Unmarshal(rawParts, &parts); err != nil {
		t.Fatalf("decode stored parts: %v", err)
	}
	if len(parts) != 1 || parts[0].PackID != "builtin" || parts[0].StickerID != "hi" || parts[0].Alt == "" {
		t.Fatalf("stored parts = %+v, want normalized builtin hi sticker", parts)
	}
	if content != parts[0].Alt {
		t.Fatalf("stored content = %q, want sticker alt fallback %q", content, parts[0].Alt)
	}
}

// TestUpdateChatSession_RenamesTitle confirms PATCH writes the new title,
// returns the updated row, and the server-side row reflects it.
func TestUpdateChatSession_RenamesTitle(t *testing.T) {
	agentID := createHandlerTestAgent(t, "ChatRenameAgent", []byte("[]"))
	sessionID := createHandlerTestChatSession(t, agentID)

	req := newRequest("PATCH", "/api/chat/sessions/"+sessionID, map[string]any{
		"title": "  Renamed Session  ",
	})
	req = withURLParam(req, "sessionId", sessionID)
	req = withChatTestWorkspaceCtx(t, req)
	w := httptest.NewRecorder()
	testHandler.UpdateChatSession(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateChatSession: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp ChatSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode update: %v", err)
	}
	if resp.Title != "Renamed Session" {
		t.Fatalf("response title: want %q, got %q", "Renamed Session", resp.Title)
	}

	var dbTitle string
	if err := testPool.QueryRow(
		context.Background(),
		`SELECT title FROM chat_session WHERE id = $1`,
		sessionID,
	).Scan(&dbTitle); err != nil {
		t.Fatalf("query chat_session: %v", err)
	}
	if dbTitle != "Renamed Session" {
		t.Fatalf("db title: want %q, got %q", "Renamed Session", dbTitle)
	}
}

// TestUpdateChatSession_RejectsBlank refuses an empty/whitespace title with 400.
// (Untitled is a render-side fallback, not a stored value.)
func TestUpdateChatSession_RejectsBlank(t *testing.T) {
	agentID := createHandlerTestAgent(t, "ChatRenameBlankAgent", []byte("[]"))
	sessionID := createHandlerTestChatSession(t, agentID)

	req := newRequest("PATCH", "/api/chat/sessions/"+sessionID, map[string]any{
		"title": "   ",
	})
	req = withURLParam(req, "sessionId", sessionID)
	req = withChatTestWorkspaceCtx(t, req)
	w := httptest.NewRecorder()
	testHandler.UpdateChatSession(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("UpdateChatSession blank: expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestUpdateChatSession_ArchivesAndRestores soft-archives a session then
// restores it. Archived sessions stay listable (status=all) but refuse sends.
func TestUpdateChatSession_ArchivesAndRestores(t *testing.T) {
	agentID := createHandlerTestAgent(t, "ChatArchiveAgent", []byte("[]"))
	sessionID := createHandlerTestChatSession(t, agentID)

	archiveReq := newRequest("PATCH", "/api/chat/sessions/"+sessionID, map[string]any{
		"status": "archived",
	})
	archiveReq = withURLParam(archiveReq, "sessionId", sessionID)
	archiveReq = withChatTestWorkspaceCtx(t, archiveReq)
	w := httptest.NewRecorder()
	testHandler.UpdateChatSession(w, archiveReq)
	if w.Code != http.StatusOK {
		t.Fatalf("archive: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var archived ChatSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &archived); err != nil {
		t.Fatalf("decode archive: %v", err)
	}
	if archived.Status != "archived" {
		t.Fatalf("archive status: want archived, got %q", archived.Status)
	}

	sendReq := newRequest("POST", "/api/chat-sessions/"+sessionID+"/messages", map[string]any{
		"content": "should fail",
	})
	sendReq = withURLParam(sendReq, "sessionId", sessionID)
	sendReq = withChatTestWorkspaceCtx(t, sendReq)
	w = httptest.NewRecorder()
	testHandler.SendChatMessage(w, sendReq)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("send on archived: expected 400, got %d: %s", w.Code, w.Body.String())
	}

	restoreReq := newRequest("PATCH", "/api/chat/sessions/"+sessionID, map[string]any{
		"status": "active",
	})
	restoreReq = withURLParam(restoreReq, "sessionId", sessionID)
	restoreReq = withChatTestWorkspaceCtx(t, restoreReq)
	w = httptest.NewRecorder()
	testHandler.UpdateChatSession(w, restoreReq)
	if w.Code != http.StatusOK {
		t.Fatalf("restore: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var restored ChatSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &restored); err != nil {
		t.Fatalf("decode restore: %v", err)
	}
	if restored.Status != "active" {
		t.Fatalf("restore status: want active, got %q", restored.Status)
	}
}

// TestUpdateChatSession_RejectsInvalidStatus refuses unknown status values.
func TestUpdateChatSession_RejectsInvalidStatus(t *testing.T) {
	agentID := createHandlerTestAgent(t, "ChatBadStatusAgent", []byte("[]"))
	sessionID := createHandlerTestChatSession(t, agentID)

	req := newRequest("PATCH", "/api/chat/sessions/"+sessionID, map[string]any{
		"status": "deleted",
	})
	req = withURLParam(req, "sessionId", sessionID)
	req = withChatTestWorkspaceCtx(t, req)
	w := httptest.NewRecorder()
	testHandler.UpdateChatSession(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid status: expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestListChatSessions_ExcludesChannelBackedSessions keeps the chat/bubble
// history free of group/DM channel shells — both live channel_agent_session
// bindings and orphan "#channelName" titles left after a binding delete.
func TestListChatSessions_ExcludesChannelBackedSessions(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "BubbleFilterBot", []byte("[]"))

	var dmSessionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title)
		VALUES ($1, $2, $3, 'genuine bubble') RETURNING id`,
		testWorkspaceID, agentID, testUserID).Scan(&dmSessionID); err != nil {
		t.Fatalf("create dm session: %v", err)
	}

	var channelID, boundSessionID, orphanSessionID, deletedNameSessionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, created_by)
		VALUES ($1, 'bubble-filter-chan', $2) RETURNING id`,
		testWorkspaceID, testUserID).Scan(&channelID); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title)
		VALUES ($1, $2, $3, '#bubble-filter-chan') RETURNING id`,
		testWorkspaceID, agentID, testUserID).Scan(&boundSessionID); err != nil {
		t.Fatalf("create bound channel session: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_agent_session (channel_id, agent_id, chat_session_id)
		VALUES ($1, $2, $3)`, channelID, agentID, boundSessionID); err != nil {
		t.Fatalf("link channel session: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title)
		VALUES ($1, $2, $3, '#bubble-filter-chan') RETURNING id`,
		testWorkspaceID, agentID, testUserID).Scan(&orphanSessionID); err != nil {
		t.Fatalf("create orphan channel-titled session: %v", err)
	}
	// Renamed/deleted channel leftover: #token title with no matching channel row.
	if err := testPool.QueryRow(ctx, `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title)
		VALUES ($1, $2, $3, '#multica_jhp研发群') RETURNING id`,
		testWorkspaceID, agentID, testUserID).Scan(&deletedNameSessionID); err != nil {
		t.Fatalf("create deleted-name hash title session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id=$1`, channelID)
		_, _ = testPool.Exec(context.Background(),
			`DELETE FROM chat_session WHERE id IN ($1,$2,$3,$4)`,
			dmSessionID, boundSessionID, orphanSessionID, deletedNameSessionID)
	})

	req := newRequest("GET", "/api/chat/sessions?status=all", nil)
	req = withChatTestWorkspaceCtx(t, req)
	w := httptest.NewRecorder()
	testHandler.ListChatSessions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListChatSessions: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var listed []ChatSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	ids := map[string]bool{}
	for _, s := range listed {
		ids[s.ID] = true
	}
	if !ids[dmSessionID] {
		t.Fatalf("genuine bubble session %s missing from ListChatSessions", dmSessionID)
	}
	if ids[boundSessionID] {
		t.Fatalf("channel-bound session %s leaked into ListChatSessions", boundSessionID)
	}
	if ids[orphanSessionID] {
		t.Fatalf("orphan #channel-titled session %s leaked into ListChatSessions", orphanSessionID)
	}
	if ids[deletedNameSessionID] {
		t.Fatalf("hash-token session %s leaked into ListChatSessions", deletedNameSessionID)
	}
}

// TestCreateChatSession_NotChannelBound confirms a plain CreateChatSession
// (DM bubble path) never inserts channel_agent_session — so group wake
// builders that only read channel_message + channel_agent_session cannot
// see bubble transcript.
func TestCreateChatSession_NotChannelBound(t *testing.T) {
	agentID := createHandlerTestAgent(t, "ChatBubbleIsolateAgent", []byte("[]"))

	req := newRequest("POST", "/api/chat/sessions", map[string]any{
		"agent_id": agentID,
		"title":    "Focus bubble",
	})
	req = withChatTestWorkspaceCtx(t, req)
	w := httptest.NewRecorder()
	testHandler.CreateChatSession(w, req)
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("CreateChatSession: expected 200/201, got %d: %s", w.Code, w.Body.String())
	}
	var created ChatSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected session id")
	}

	var bound int
	if err := testPool.QueryRow(
		context.Background(),
		`SELECT count(*) FROM channel_agent_session WHERE chat_session_id = $1`,
		created.ID,
	).Scan(&bound); err != nil {
		t.Fatalf("query channel_agent_session: %v", err)
	}
	if bound != 0 {
		t.Fatalf("plain chat_session must not be channel-bound; got %d bindings", bound)
	}

	listReq := newRequest("GET", "/api/chat/sessions?status=all", nil)
	listReq = withChatTestWorkspaceCtx(t, listReq)
	w = httptest.NewRecorder()
	testHandler.ListChatSessions(w, listReq)
	if w.Code != http.StatusOK {
		t.Fatalf("ListChatSessions: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var listed []ChatSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	found := false
	for _, s := range listed {
		if s.ID == created.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("bubble session %s missing from ListChatSessions", created.ID)
	}
}

// TestSendChatMessage_InvalidAttachmentIDs rejects malformed UUIDs in
// attachment_ids with 400 before any side effects (no message row created).
func TestSendChatMessage_InvalidAttachmentIDs(t *testing.T) {
	agentID := createHandlerTestAgent(t, "ChatBadAttachAgent", []byte("[]"))
	sessionID := createHandlerTestChatSession(t, agentID)

	req := newRequest("POST", "/api/chat-sessions/"+sessionID+"/messages", map[string]any{
		"content":        "hi",
		"attachment_ids": []string{"not-a-uuid"},
	})
	req = withURLParam(req, "sessionId", sessionID)
	req = withChatTestWorkspaceCtx(t, req)
	w := httptest.NewRecorder()
	testHandler.SendChatMessage(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("SendChatMessage with bad attachment id: expected 400, got %d: %s", w.Code, w.Body.String())
	}

	// Confirm no message row was created.
	var count int
	if err := testPool.QueryRow(
		context.Background(),
		`SELECT count(*) FROM chat_message WHERE chat_session_id = $1`,
		sessionID,
	).Scan(&count); err != nil {
		t.Fatalf("count chat_message: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 chat_message rows after rejected send, got %d", count)
	}
}

func fetchChatMessagesPageForTest(t *testing.T, sessionID string, params url.Values) ChatMessagesPageResponse {
	t.Helper()
	target := "/api/chat/sessions/" + sessionID + "/messages/page"
	if encoded := params.Encode(); encoded != "" {
		target += "?" + encoded
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("X-User-ID", testUserID)
	req = withURLParam(req, "sessionId", sessionID)
	req = withChatTestWorkspaceCtx(t, req)
	w := httptest.NewRecorder()
	testHandler.ListChatMessagesPage(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListChatMessagesPage: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var page ChatMessagesPageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page messages: %v", err)
	}
	return page
}

func TestListChatMessagesPage_UsesCursorWithoutChangingLegacyList(t *testing.T) {
	agentID := createHandlerTestAgent(t, "ChatCursorPaginationAgent", []byte("[]"))
	sessionID := createHandlerTestChatSession(t, agentID)

	for i, content := range []string{"oldest", "middle", "newest"} {
		_, err := testPool.Exec(
			context.Background(),
			`INSERT INTO chat_message (chat_session_id, role, content, created_at)
			 VALUES ($1, 'user', $2, timestamp '2026-01-01 00:00:00' + ($3::int * interval '1 second'))`,
			sessionID,
			content,
			i,
		)
		if err != nil {
			t.Fatalf("insert chat message %d: %v", i, err)
		}
	}

	legacyReq := httptest.NewRequest(http.MethodGet, "/api/chat/sessions/"+sessionID+"/messages", nil)
	legacyReq.Header.Set("X-User-ID", testUserID)
	legacyReq = withURLParam(legacyReq, "sessionId", sessionID)
	legacyReq = withChatTestWorkspaceCtx(t, legacyReq)
	legacyW := httptest.NewRecorder()
	testHandler.ListChatMessages(legacyW, legacyReq)
	if legacyW.Code != http.StatusOK {
		t.Fatalf("ListChatMessages: expected 200, got %d: %s", legacyW.Code, legacyW.Body.String())
	}
	var legacy []ChatMessageResponse
	if err := json.Unmarshal(legacyW.Body.Bytes(), &legacy); err != nil {
		t.Fatalf("decode legacy messages: %v", err)
	}
	if len(legacy) != 3 || legacy[0].Content != "oldest" || legacy[2].Content != "newest" {
		t.Fatalf("legacy messages = %#v", legacy)
	}

	latest := fetchChatMessagesPageForTest(t, sessionID, url.Values{"limit": {"2"}})
	if latest.Limit != 2 || !latest.HasMore || latest.NextCursor == nil {
		t.Fatalf("latest page metadata = %#v", latest)
	}
	if len(latest.Messages) != 2 || latest.Messages[0].Content != "middle" || latest.Messages[1].Content != "newest" {
		t.Fatalf("latest page messages = %#v", latest)
	}

	older := fetchChatMessagesPageForTest(t, sessionID, url.Values{
		"limit":             {"2"},
		"before_created_at": {latest.NextCursor.CreatedAt},
		"before_id":         {latest.NextCursor.ID},
	})
	if older.HasMore || older.NextCursor != nil {
		t.Fatalf("older page metadata = %#v", older)
	}
	if len(older.Messages) != 1 || older.Messages[0].Content != "oldest" {
		t.Fatalf("older page messages = %#v", older)
	}
}

func TestListChatMessagesPage_CursorTieBreaksSameTimestampWithoutDupesOrGaps(t *testing.T) {
	agentID := createHandlerTestAgent(t, "ChatCursorTieBreakAgent", []byte("[]"))
	sessionID := createHandlerTestChatSession(t, agentID)

	contents := []string{"a", "b", "c", "d", "e"}
	for _, content := range contents {
		_, err := testPool.Exec(
			context.Background(),
			`INSERT INTO chat_message (chat_session_id, role, content, created_at)
			 VALUES ($1, 'user', $2, timestamp '2026-01-01 00:00:00')`,
			sessionID,
			content,
		)
		if err != nil {
			t.Fatalf("insert chat message %q: %v", content, err)
		}
	}

	seen := map[string]bool{}
	var ordered []string
	params := url.Values{"limit": {"2"}}
	for {
		page := fetchChatMessagesPageForTest(t, sessionID, params)
		for _, msg := range page.Messages {
			if seen[msg.ID] {
				t.Fatalf("duplicate message id %s across cursor pages", msg.ID)
			}
			seen[msg.ID] = true
			ordered = append(ordered, msg.Content)
		}
		if !page.HasMore {
			if page.NextCursor != nil {
				t.Fatalf("terminal page has next cursor: %#v", page.NextCursor)
			}
			break
		}
		if page.NextCursor == nil {
			t.Fatalf("has_more page missing next cursor: %#v", page)
		}
		params = url.Values{
			"limit":             {"2"},
			"before_created_at": {page.NextCursor.CreatedAt},
			"before_id":         {page.NextCursor.ID},
		}
	}

	if len(ordered) != len(contents) {
		t.Fatalf("expected %d messages across pages, got %d: %v", len(contents), len(ordered), ordered)
	}
	// Pages are newest-window first and chronological within each page. With all
	// timestamps equal, the id tie-break must still produce a deterministic,
	// gap-free traversal.
	for _, content := range contents {
		found := false
		for _, got := range ordered {
			if got == content {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing content %q across cursor pages: %v", content, ordered)
		}
	}
}

func TestListChatMessagesPage_RejectsInvalidLimit(t *testing.T) {
	agentID := createHandlerTestAgent(t, "ChatPaginationBadLimitAgent", []byte("[]"))
	sessionID := createHandlerTestChatSession(t, agentID)

	req := httptest.NewRequest(http.MethodGet, "/api/chat/sessions/"+sessionID+"/messages/page?limit=0", nil)
	req.Header.Set("X-User-ID", testUserID)
	req = withURLParam(req, "sessionId", sessionID)
	req = withChatTestWorkspaceCtx(t, req)
	w := httptest.NewRecorder()
	testHandler.ListChatMessagesPage(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("ListChatMessagesPage invalid limit: expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetPendingChatTaskIncludesInboxEventID(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "pending-chat-inbox-"+uuid.NewString(), []byte(`{}`))
	sessionID := createHandlerTestChatSession(t, agentID)

	var eventID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
			workspace_id, agent_id, chat_session_id, reason, requires_wake, status,
			priority, seq_from, seq_to, started_at
		)
		VALUES ($1, $2, $3, 'dm', true, 'draining', 100, 1, 1, now())
		RETURNING id
	`, testWorkspaceID, agentID, sessionID).Scan(&eventID); err != nil {
		t.Fatalf("insert inbox event: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, eventID) })

	req := newRequest(http.MethodGet, "/api/chat/sessions/"+sessionID+"/pending-task", nil)
	req = withRouteParams(req, "sessionId", sessionID)
	req = withChatTestWorkspaceCtx(t, req)
	w := httptest.NewRecorder()

	testHandler.GetPendingChatTask(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp PendingChatTaskResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.TaskID != eventID || resp.Status != "running" || resp.InboxEventID == nil || *resp.InboxEventID != eventID {
		t.Fatalf("pending task response = %+v, want canonical task/inbox %s", resp, eventID)
	}
}

func TestCancelChatAgentInboxEventCancelsPendingChatTask(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "cancel-chat-inbox-"+uuid.NewString(), []byte(`{}`))
	sessionID := createHandlerTestChatSession(t, agentID)

	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (agent_id, runtime_id, status, priority, issue_id, chat_session_id, created_at)
		VALUES ($1, (SELECT runtime_id FROM agent WHERE id = $1), 'draining', 0, NULL, $2, now() - interval '5 seconds')
		RETURNING id
	`, agentID, sessionID).Scan(&taskID); err != nil {
		t.Fatalf("insert pending chat task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, taskID) })

	var eventID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
			workspace_id, agent_id, chat_session_id, reason, requires_wake, status,
			priority, seq_from, seq_to
		)
		VALUES ($1, $2, $3, 'dm', true, 'draining', 100, 1, 1)
		RETURNING id
	`, testWorkspaceID, agentID, sessionID).Scan(&eventID); err != nil {
		t.Fatalf("insert inbox event: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, eventID) })

	req := newRequest(http.MethodPost, "/api/chat/sessions/"+sessionID+"/agent-inbox/events/"+eventID+"/cancel", nil)
	req = withRouteParams(req, "sessionId", sessionID, "eventId", eventID)
	req = withChatTestWorkspaceCtx(t, req)
	w := httptest.NewRecorder()

	testHandler.CancelChatAgentInboxEvent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp ChannelCancelAgentInboxEventResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.OK || resp.InboxEventID != eventID || resp.AgentID != agentID || resp.Status != "cancelled" {
		t.Fatalf("cancel response = %+v", resp)
	}

	var status, outcome string
	if err := testPool.QueryRow(ctx, `
		SELECT status, COALESCE(terminal_outcome, '')
		FROM agent_inbox_event
		WHERE id = $1
	`, eventID).Scan(&status, &outcome); err != nil {
		t.Fatalf("load cancelled inbox event: %v", err)
	}
	if status != "suppressed" || outcome != "no_reply" {
		t.Fatalf("cancelled inbox event status=%q outcome=%q", status, outcome)
	}
}

func TestListChatAgentInboxEventTimeline_HidesRawThinkingAndProjectsActivityMessages(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "chat-transcript-"+uuid.NewString(), []byte(`{}`))
	sessionID := createHandlerTestChatSession(t, agentID)
	runtimeID := handlerTestRuntimeID(t)

	var eventID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
			workspace_id, agent_id, chat_session_id, reason, requires_wake, status,
			priority, seq_from, seq_to
		)
		VALUES ($1, $2, $3, 'dm', true, 'draining', 100, 1, 3)
		RETURNING id
	`, testWorkspaceID, agentID, sessionID).Scan(&eventID); err != nil {
		t.Fatalf("insert inbox event: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, eventID)
		testPool.Exec(context.Background(), `DELETE FROM agent_activity_event WHERE details->>'inbox_event_id' = $1`, eventID)
	})

	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_activity_event (
			workspace_id, agent_id, runtime_id, event_kind, event_type,
			severity, target_kind, message, details, visibility, created_at
		)
		VALUES
			($1, $2, $3, 'thinking', 'thinking', 'info', 'dm', 'Planning',
			 jsonb_build_object('inbox_event_id', $4::text, 'seq', 1),
			 'user_facing', now() - interval '2 seconds'),
			($1, $2, $3, 'tool_call', 'tool_use', 'info', 'dm', '',
			 jsonb_build_object(
				'inbox_event_id', $4::text,
				'seq', 2,
				'tool', 'bash',
				'input', jsonb_build_object('cmd', 'raft message send --send-draft')
			 ),
			 'user_facing', now() - interval '1 second'),
			($1, $2, $3, 'text', 'text', 'info', 'dm', 'Done',
			 jsonb_build_object('inbox_event_id', $4::text, 'seq', 3),
			 'user_facing', now())
	`, testWorkspaceID, agentID, runtimeID, eventID); err != nil {
		t.Fatalf("insert inbox activity rows: %v", err)
	}

	req := newRequest(http.MethodGet, "/api/chat/sessions/"+sessionID+"/agent-inbox-events/"+eventID+"/timeline", nil)
	req = withRouteParams(req, "sessionId", sessionID, "eventId", eventID)
	req = withChatTestWorkspaceCtx(t, req)
	w := httptest.NewRecorder()

	testHandler.ListChatAgentInboxEventTimeline(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp []protocol.TaskMessagePayload
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp) != 2 {
		t.Fatalf("expected 2 projected messages without raw thinking, got %d: %+v", len(resp), resp)
	}
	if resp[0].TaskID != eventID || resp[0].Seq != 2 || resp[0].Type != "tool_use" || resp[0].Tool != "bash" {
		t.Fatalf("unexpected tool projection: %+v", resp[0])
	}
	if got := resp[0].Input["cmd"]; got != "raft message send --send-draft" {
		t.Fatalf("projected tool input cmd = %q", got)
	}
	if resp[1].Seq != 3 || resp[1].Type != "text" || resp[1].Content != "Done" {
		t.Fatalf("unexpected text projection: %+v", resp[1])
	}

	req = newRequest(http.MethodGet, "/api/chat/sessions/"+sessionID+"/agent-inbox-events/"+eventID+"/timeline?since=1", nil)
	req = withRouteParams(req, "sessionId", sessionID, "eventId", eventID)
	req = withChatTestWorkspaceCtx(t, req)
	w = httptest.NewRecorder()
	testHandler.ListChatAgentInboxEventTimeline(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for since query, got %d: %s", w.Code, w.Body.String())
	}
	resp = nil
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode since response: %v", err)
	}
	if len(resp) != 2 || resp[0].Seq != 2 || resp[1].Seq != 3 {
		t.Fatalf("unexpected since response: %+v", resp)
	}
}

func TestListChatAgentInboxEventTimeline_RejectsWrongSession(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "chat-transcript-wrong-session-"+uuid.NewString(), []byte(`{}`))
	sessionID := createHandlerTestChatSession(t, agentID)
	otherSessionID := createHandlerTestChatSession(t, agentID)

	var eventID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
			workspace_id, agent_id, chat_session_id, reason, requires_wake, status,
			priority, seq_from, seq_to
		)
		VALUES ($1, $2, $3, 'dm', true, 'draining', 100, 1, 1)
		RETURNING id
	`, testWorkspaceID, agentID, otherSessionID).Scan(&eventID); err != nil {
		t.Fatalf("insert inbox event: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, eventID)
	})

	req := newRequest(http.MethodGet, "/api/chat/sessions/"+sessionID+"/agent-inbox-events/"+eventID+"/timeline", nil)
	req = withRouteParams(req, "sessionId", sessionID, "eventId", eventID)
	req = withChatTestWorkspaceCtx(t, req)
	w := httptest.NewRecorder()

	testHandler.ListChatAgentInboxEventTimeline(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for wrong session, got %d: %s", w.Code, w.Body.String())
	}
}
