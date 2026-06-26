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
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestChannelMentionStoresThreadContextAndBridgesAgentReply(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Channel Helper", nil)
	var channelID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, created_by)
		VALUES ($1, $2, $3)
		RETURNING id`, testWorkspaceID, "thread-context", testUserID).Scan(&channelID); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID) })
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'user', $3), ($1, $2, 'agent', $4)`, channelID, testWorkspaceID, testUserID, agentID); err != nil {
		t.Fatalf("seed members: %v", err)
	}

	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "@Channel Helper please join", "multica", nil, strPtr("debate-thread"), 2)
	if err != nil {
		t.Fatalf("insert trigger: %v", err)
	}

	testHandler.dispatchChannelMentions(ctx, ch, trigger, parseUUID(testUserID))

	var sessionID string
	if err := testPool.QueryRow(ctx, `SELECT chat_session_id FROM channel_agent_session WHERE channel_id = $1 AND agent_id = $2`, channelID, agentID).Scan(&sessionID); err != nil {
		t.Fatalf("channel agent session not created: %v", err)
	}
	var threadID string
	var depth int
	var prompt string
	if err := testPool.QueryRow(ctx, `
		SELECT thread_id, trigger_depth, content
		FROM chat_message
		WHERE chat_session_id = $1 AND role = 'user'
		ORDER BY created_at DESC
		LIMIT 1`, sessionID).Scan(&threadID, &depth, &prompt); err != nil {
		t.Fatalf("load prompt message: %v", err)
	}
	if threadID != "debate-thread" || depth != 2 {
		t.Fatalf("prompt thread/depth = %q/%d, want debate-thread/2", threadID, depth)
	}
	if !strings.Contains(prompt, "Recent channel messages:") || !strings.Contains(prompt, "@Channel Helper please join") {
		t.Fatalf("prompt missing channel context/current message:\n%s", prompt)
	}

	testHandler.handleChannelChatDone(events.Event{Payload: protocol.ChatDonePayload{ChatSessionID: sessionID, Content: "@Channel Helper says hi"}})
	var authorType, replyThread string
	var replyDepth int
	if err := testPool.QueryRow(ctx, `
		SELECT author_type, thread_id, trigger_depth
		FROM channel_message
		WHERE channel_id = $1 AND content = '[@Channel Helper says hi]'
		LIMIT 1`, channelID).Scan(&authorType, &replyThread, &replyDepth); err == nil {
		t.Fatalf("unexpected bracketed reply row: %s %s %d", authorType, replyThread, replyDepth)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT author_type, thread_id, trigger_depth
		FROM channel_message
		WHERE channel_id = $1 AND content = '@Channel Helper says hi'
		LIMIT 1`, channelID).Scan(&authorType, &replyThread, &replyDepth); err != nil {
		t.Fatalf("load bridged reply: %v", err)
	}
	if authorType != "agent" || replyThread != "debate-thread" || replyDepth != 3 {
		t.Fatalf("bridged reply = %s/%q/%d, want agent/debate-thread/3", authorType, replyThread, replyDepth)
	}
}

func TestChannelMentionNotifiesHumanMember(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Mention Bot", nil)
	var channelID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, created_by)
		VALUES ($1, $2, $3)
		RETURNING id`, testWorkspaceID, "human-mention", testUserID).Scan(&channelID); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID) })
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM inbox_item WHERE recipient_id = $1 AND type = 'mentioned'`, testUserID)
	})
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'user', $3), ($1, $2, 'agent', $4)`, channelID, testWorkspaceID, testUserID, agentID); err != nil {
		t.Fatalf("seed members: %v", err)
	}

	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}

	// Agent posts a message that @-mentions the human member.
	content := fmt.Sprintf("On it [@Tester](mention://member/%s) — taking a look now.", testUserID)
	msg, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "agent", parseUUID(agentID), "Mention Bot", content, "multica", nil, strPtr("t1"), 1)
	if err != nil {
		t.Fatalf("insert agent message: %v", err)
	}

	testHandler.notifyChannelMemberMentions(ctx, ch, msg)

	var inboxID, recipientID, itemType, actorType, chID, msgID, actorName string
	if err := testPool.QueryRow(ctx, `
		SELECT id, recipient_id, type, actor_type,
		       details->>'channel_id', details->>'message_id', details->>'actor_name'
		FROM inbox_item
		WHERE recipient_id = $1 AND type = 'mentioned' AND issue_id IS NULL
		ORDER BY created_at DESC LIMIT 1`, testUserID).
		Scan(&inboxID, &recipientID, &itemType, &actorType, &chID, &msgID, &actorName); err != nil {
		t.Fatalf("inbox item not created for mentioned member: %v", err)
	}
	if actorType != "agent" || actorName != "Mention Bot" {
		t.Fatalf("actor = %s/%q, want agent/Mention Bot", actorType, actorName)
	}
	if chID != channelID || msgID != msg.ID {
		t.Fatalf("details channel/message = %s/%s, want %s/%s", chID, msgID, channelID, msg.ID)
	}

	// The author themselves must never be notified about their own mention.
	var selfCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM inbox_item
		WHERE recipient_id = $1 AND type = 'mentioned'`, agentID).Scan(&selfCount); err != nil {
		t.Fatalf("count author inbox: %v", err)
	}
	if selfCount != 0 {
		t.Fatalf("author received %d self-mention inbox items, want 0", selfCount)
	}
}

func TestChannelArchiveHidesFromListFreezesWritesAndRestores(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	channelID := seedChannelForTest(t, "archive-freeze-"+uuid.NewString(), testUserID)
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "before archive", "multica", nil, strPtr("archive-thread"), 0); err != nil {
		t.Fatalf("seed message: %v", err)
	}

	req := newRequest(http.MethodPost, "/api/channels/"+channelID+"/archive", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.ArchiveChannel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("archive channel: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var archived ChannelResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &archived); err != nil {
		t.Fatalf("decode archive response: %v", err)
	}
	if archived.ArchivedAt == nil || archived.ArchivedBy == nil {
		t.Fatalf("archive response missing archived fields: %+v", archived)
	}

	if ch := listedChannelForTest(t, channelID); ch != nil {
		t.Fatalf("archived channel still visible in list: %+v", *ch)
	}
	archivedListed := archivedListedChannelForTest(t, channelID)
	if archivedListed == nil {
		t.Fatal("archived channel missing from archived list")
	}
	if archivedListed.ArchivedAt == nil {
		t.Fatalf("archived list response missing archived_at: %+v", *archivedListed)
	}

	req = newRequest(http.MethodPost, "/api/channels/"+channelID+"/messages", map[string]string{"content": "should be blocked"})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec = httptest.NewRecorder()
	testHandler.SendChannelMessage(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("send to archived channel: status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = newRequest(http.MethodGet, "/api/channels/"+channelID+"/messages", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec = httptest.NewRecorder()
	testHandler.ListChannelMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list archived channel messages: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var messages []ChannelMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &messages); err != nil {
		t.Fatalf("decode archived channel messages: %v", err)
	}
	if len(messages) != 1 || messages[0].Content != "before archive" {
		t.Fatalf("archived channel history = %+v, want seeded message", messages)
	}

	req = newRequest(http.MethodPost, "/api/channels/"+channelID+"/restore", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec = httptest.NewRecorder()
	testHandler.RestoreChannel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("restore channel: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if ch := listedChannelForTest(t, channelID); ch == nil {
		t.Fatal("restored channel missing from list")
	}
	if ch := archivedListedChannelForTest(t, channelID); ch != nil {
		t.Fatalf("restored channel still visible in archived list: %+v", *ch)
	}
}

func TestChannelArchivePlainMemberForbidden(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	memberID := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "archive-permission-"+uuid.NewString(), testUserID, memberID)

	req := newRequestAs(memberID, http.MethodPost, "/api/channels/"+channelID+"/archive", nil)
	req = withChannelTestWorkspaceCtx(t, req, memberID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.ArchiveChannel(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("plain member archive: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var archived bool
	if err := testPool.QueryRow(context.Background(), `SELECT archived_at IS NOT NULL FROM channel WHERE id = $1`, channelID).Scan(&archived); err != nil {
		t.Fatalf("load archive state: %v", err)
	}
	if archived {
		t.Fatal("plain member archived the channel")
	}
}

func TestChannelPinAndManualUnreadArePerUserListState(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	channelID := seedChannelForTest(t, "pin-unread-"+uuid.NewString(), testUserID)

	req := newRequest(http.MethodPut, "/api/channels/"+channelID+"/pin", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.PinChannel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("pin channel: status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = newRequest(http.MethodPost, "/api/channels/"+channelID+"/unread", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec = httptest.NewRecorder()
	testHandler.MarkChannelUnread(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mark channel unread: status=%d body=%s", rec.Code, rec.Body.String())
	}

	ch := listedChannelForTest(t, channelID)
	if ch == nil {
		t.Fatal("channel missing from list")
	}
	if ch.PinnedAt == nil {
		t.Fatalf("listed channel missing pinned_at: %+v", *ch)
	}
	if !ch.ManuallyUnread || ch.UnreadCount == 0 {
		t.Fatalf("listed channel missing manual unread state: %+v", *ch)
	}

	req = newRequest(http.MethodPost, "/api/channels/"+channelID+"/read", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec = httptest.NewRecorder()
	testHandler.MarkChannelRead(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mark channel read: status=%d body=%s", rec.Code, rec.Body.String())
	}

	ch = listedChannelForTest(t, channelID)
	if ch == nil {
		t.Fatal("channel missing from list after read")
	}
	if ch.ManuallyUnread || ch.UnreadCount != 0 {
		t.Fatalf("manual unread not cleared by read: %+v", *ch)
	}
	if ch.PinnedAt == nil {
		t.Fatalf("read unexpectedly cleared pin: %+v", *ch)
	}
}

func seedChannelForTest(t *testing.T, name string, memberIDs ...string) string {
	t.Helper()
	ctx := context.Background()
	var channelID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, created_by)
		VALUES ($1, $2, $3)
		RETURNING id`, testWorkspaceID, name, testUserID).Scan(&channelID); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID) })
	for _, memberID := range memberIDs {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
			VALUES ($1, $2, 'user', $3)
			ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, memberID); err != nil {
			t.Fatalf("seed channel member: %v", err)
		}
	}
	return channelID
}

func createChannelPlainMember(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	email := "channel-plain-" + uuid.NewString() + "@multica.test"
	var userID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ('Channel Plain Member', $1)
		RETURNING id`, email).Scan(&userID); err != nil {
		t.Fatalf("create plain member user: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'member')`, testWorkspaceID, userID); err != nil {
		t.Fatalf("add plain member: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM member WHERE workspace_id = $1 AND user_id = $2`, testWorkspaceID, userID)
		testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})
	return userID
}

func listedChannelForTest(t *testing.T, channelID string) *ChannelResponse {
	t.Helper()
	req := newRequest(http.MethodGet, "/api/channels", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	rec := httptest.NewRecorder()
	testHandler.ListChannels(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list channels: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var channels []ChannelResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &channels); err != nil {
		t.Fatalf("decode channels: %v", err)
	}
	for i := range channels {
		if channels[i].ID == channelID {
			return &channels[i]
		}
	}
	return nil
}

func archivedListedChannelForTest(t *testing.T, channelID string) *ChannelResponse {
	t.Helper()
	req := newRequest(http.MethodGet, "/api/channels?archived=true", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	rec := httptest.NewRecorder()
	testHandler.ListChannels(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list archived channels: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var channels []ChannelResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &channels); err != nil {
		t.Fatalf("decode archived channels: %v", err)
	}
	for i := range channels {
		if channels[i].ID == channelID {
			return &channels[i]
		}
	}
	return nil
}

func withChannelTestWorkspaceCtx(t *testing.T, req *http.Request, userID string) *http.Request {
	t.Helper()
	memberRow, err := testHandler.Queries.GetMemberByUserAndWorkspace(context.Background(), db.GetMemberByUserAndWorkspaceParams{
		UserID:      util.MustParseUUID(userID),
		WorkspaceID: util.MustParseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("load test member row: %v", err)
	}
	return req.WithContext(middleware.SetMemberContext(req.Context(), testWorkspaceID, memberRow))
}
