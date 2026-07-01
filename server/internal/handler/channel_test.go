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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
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
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "@Channel Helper please join", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("debate-thread"), 2)
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

func TestChannelRementionInterruptsRunningTaskWithFreshSession(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Remention Agent", nil)
	var channelID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, created_by)
		VALUES ($1, $2, $3)
		RETURNING id`, testWorkspaceID, "remention-interrupt", testUserID).Scan(&channelID); err != nil {
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
	first, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "@Remention Agent start the long task", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("interrupt-thread"), 0)
	if err != nil {
		t.Fatalf("insert first trigger: %v", err)
	}
	testHandler.dispatchChannelMentions(ctx, ch, first, parseUUID(testUserID))

	var sessionID string
	if err := testPool.QueryRow(ctx, `SELECT chat_session_id FROM channel_agent_session WHERE channel_id = $1 AND agent_id = $2`, channelID, agentID).Scan(&sessionID); err != nil {
		t.Fatalf("channel agent session not created: %v", err)
	}
	var firstTaskID string
	if err := testPool.QueryRow(ctx, `
		SELECT id FROM agent_task_queue
		WHERE chat_session_id = $1
		ORDER BY created_at DESC LIMIT 1`, sessionID).Scan(&firstTaskID); err != nil {
		t.Fatalf("load first task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE agent_task_queue SET status = 'running', started_at = now() WHERE id = $1`, firstTaskID); err != nil {
		t.Fatalf("mark first task running: %v", err)
	}

	second, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "@Remention Agent stop and use this corrected direction", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("interrupt-thread"), 1)
	if err != nil {
		t.Fatalf("insert second trigger: %v", err)
	}
	testHandler.dispatchChannelMentions(ctx, ch, second, parseUUID(testUserID))

	var oldStatus, oldReason string
	if err := testPool.QueryRow(ctx, `
		SELECT status, COALESCE(failure_reason, '')
		FROM agent_task_queue WHERE id = $1`, firstTaskID).Scan(&oldStatus, &oldReason); err != nil {
		t.Fatalf("load interrupted task: %v", err)
	}
	if oldStatus != "cancelled" || oldReason != "followup_interrupt" {
		t.Fatalf("first task = (%q, %q), want (cancelled, followup_interrupt)", oldStatus, oldReason)
	}

	var fresh bool
	if err := testPool.QueryRow(ctx, `
		SELECT force_fresh_session
		FROM agent_task_queue
		WHERE chat_session_id = $1 AND id <> $2
		ORDER BY created_at DESC LIMIT 1`, sessionID, firstTaskID).Scan(&fresh); err != nil {
		t.Fatalf("load fresh follow-up task: %v", err)
	}
	if !fresh {
		t.Fatal("re-mention follow-up task should force a fresh session")
	}
}

func TestChannelMentionedAgentsMatchesHandleDisplayAndStructuredID(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	handle := "identity_handle_" + suffix
	displayName := "Identity Display " + suffix
	agentID := createHandlerTestAgent(t, handle, nil)
	decoyID := createHandlerTestAgent(t, "identity_decoy_"+suffix, nil)
	if _, err := testPool.Exec(ctx, `UPDATE agent SET display_name = $2 WHERE id = $1`, agentID, displayName); err != nil {
		t.Fatalf("set agent display_name: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE agent SET display_name = $2 WHERE id = $1`, decoyID, "Identity Decoy "+suffix); err != nil {
		t.Fatalf("set decoy display_name: %v", err)
	}

	channelID := seedChannelForTest(t, "identity-mentions-"+suffix, testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3), ($1, $2, 'agent', $4)`,
		channelID, testWorkspaceID, agentID, decoyID,
	); err != nil {
		t.Fatalf("seed agent members: %v", err)
	}

	cases := []struct {
		name    string
		content string
	}{
		{"bare handle", "please @" + handle + " jump in"},
		{"bare display", "please @" + displayName + " jump in"},
		{"structured id", fmt.Sprintf("please [@Old Label](mention://agent/%s) jump in", agentID)},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			agents := testHandler.channelMentionedAgents(ctx, testWorkspaceID, channelID, tt.content)
			if len(agents) != 1 {
				t.Fatalf("channelMentionedAgents returned %d agents, want 1: %+v", len(agents), agents)
			}
			if got := uuidToString(agents[0].ID); got != agentID {
				t.Fatalf("mentioned agent = %s, want %s", got, agentID)
			}
			if agents[0].Name != handle {
				t.Fatalf("mentioned handle = %q, want %q", agents[0].Name, handle)
			}
			if agents[0].DisplayName != displayName {
				t.Fatalf("mentioned display_name = %q, want %q", agents[0].DisplayName, displayName)
			}
		})
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
	msg, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "agent", parseUUID(agentID), "Mention Bot", content, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("t1"), 1)
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

func TestChannelMutedMemberDoesNotReceiveMentionInbox(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	memberID := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "muted-mention-"+uuid.NewString(), testUserID, memberID)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM inbox_item WHERE recipient_id = $1 AND type = 'mentioned'`, memberID)
	})

	req := newRequestAs(memberID, http.MethodPut, "/api/channels/"+channelID+"/mute", nil)
	req = withChannelTestWorkspaceCtx(t, req, memberID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.MuteChannel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mute channel: status=%d body=%s", rec.Code, rec.Body.String())
	}

	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	content := fmt.Sprintf("ping [@Channel Plain Member](mention://member/%s)", memberID)
	msg, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", content, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("muted"), 0)
	if err != nil {
		t.Fatalf("insert mention message: %v", err)
	}
	testHandler.notifyChannelMemberMentions(ctx, ch, msg)

	var count int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM inbox_item
		WHERE recipient_id = $1 AND type = 'mentioned'`, memberID).Scan(&count); err != nil {
		t.Fatalf("count inbox items: %v", err)
	}
	if count != 0 {
		t.Fatalf("muted member received %d mention inbox item(s), want 0", count)
	}
}

func TestSendChannelMessageReplyReturnsSummary(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	channelID := seedChannelForTest(t, "quote-reply-"+uuid.NewString(), testUserID)
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "root message", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("quote-root"), 0)
	if err != nil {
		t.Fatalf("insert root message: %v", err)
	}

	req := newRequest(http.MethodPost, "/api/channels/"+channelID+"/messages", map[string]any{
		"content":             "replying",
		"reply_to_message_id": root.ID,
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.SendChannelMessage(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("send reply: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created ChannelMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created message: %v", err)
	}
	if created.ReplyToMessageID == nil || *created.ReplyToMessageID != root.ID {
		t.Fatalf("created reply_to_message_id = %v, want %s", created.ReplyToMessageID, root.ID)
	}
	if created.ReplyTo == nil || created.ReplyTo.ID != root.ID || created.ReplyTo.Content != "root message" {
		t.Fatalf("created reply summary = %+v, want root summary", created.ReplyTo)
	}

	req = newRequest(http.MethodGet, "/api/channels/"+channelID+"/messages", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec = httptest.NewRecorder()
	testHandler.ListChannelMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list messages: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var messages []ChannelMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &messages); err != nil {
		t.Fatalf("decode listed messages: %v", err)
	}
	for _, msg := range messages {
		if msg.ID == created.ID {
			if msg.ReplyTo == nil || msg.ReplyTo.ID != root.ID {
				t.Fatalf("listed reply summary = %+v, want root", msg.ReplyTo)
			}
			return
		}
	}
	t.Fatalf("created message %s missing from list", created.ID)
}

func TestChannelMessageReactionCanBeRemoved(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	channelID := seedChannelForTest(t, "reaction-remove-"+uuid.NewString(), testUserID)
	msg, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "hello", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("reaction-thread"), 0)
	if err != nil {
		t.Fatalf("insert channel message: %v", err)
	}

	req := newRequest(http.MethodPost, "/api/channels/"+channelID+"/messages/"+msg.ID+"/reactions", map[string]string{"emoji": "👍"})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withRouteParams(req, "channelId", channelID, "messageId", msg.ID)
	rec := httptest.NewRecorder()
	testHandler.AddChannelMessageReaction(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add reaction: status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = newRequest(http.MethodGet, "/api/channels/"+channelID+"/messages", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec = httptest.NewRecorder()
	testHandler.ListChannelMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list messages after add: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var messages []ChannelMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &messages); err != nil {
		t.Fatalf("decode messages after add: %v", err)
	}
	if len(messages) != 1 || len(messages[0].Reactions) != 1 {
		t.Fatalf("expected one reaction after add, got %#v", messages)
	}

	req = newRequest(http.MethodDelete, "/api/channels/"+channelID+"/messages/"+msg.ID+"/reactions", map[string]string{"emoji": "👍"})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withRouteParams(req, "channelId", channelID, "messageId", msg.ID)
	rec = httptest.NewRecorder()
	testHandler.RemoveChannelMessageReaction(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("remove reaction: status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = newRequest(http.MethodGet, "/api/channels/"+channelID+"/messages", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec = httptest.NewRecorder()
	testHandler.ListChannelMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list messages after remove: status=%d body=%s", rec.Code, rec.Body.String())
	}
	messages = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &messages); err != nil {
		t.Fatalf("decode messages after remove: %v", err)
	}
	if len(messages) != 1 || len(messages[0].Reactions) != 0 {
		t.Fatalf("expected no reactions after remove, got %#v", messages)
	}
}

func TestSearchChannelMessagesReturnsStableResults(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	channelID := seedChannelForTest(t, "message-search-"+uuid.NewString(), testUserID)
	first, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "Alpha apple", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("s1"), 0)
	if err != nil {
		t.Fatalf("insert first message: %v", err)
	}
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "Beta banana", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("s2"), 0); err != nil {
		t.Fatalf("insert second message: %v", err)
	}
	threadReply, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "Nested pear", "multica", nil, pgtype.UUID{}, parseUUID(first.ID), strPtr("s1"), 0)
	if err != nil {
		t.Fatalf("insert thread reply: %v", err)
	}

	req := newRequest(http.MethodGet, "/api/channels/"+channelID+"/messages/search?q=alp&limit=10", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.SearchChannelMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("search messages: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp ChannelMessageSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode search response: %v", err)
	}
	if resp.Query != "alp" || resp.Total != 1 || len(resp.Results) != 1 {
		t.Fatalf("search response = %+v, want one alp hit", resp)
	}
	if resp.Results[0].MessageID != first.ID || resp.Results[0].Content != "Alpha apple" {
		t.Fatalf("search result = %+v, want first message", resp.Results[0])
	}

	req = newRequest(http.MethodGet, "/api/channels/"+channelID+"/messages/search?q=nested&limit=10", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec = httptest.NewRecorder()
	testHandler.SearchChannelMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("search thread reply: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode thread search response: %v", err)
	}
	if resp.Total != 1 || len(resp.Results) != 1 || resp.Results[0].MessageID != threadReply.ID {
		t.Fatalf("thread search response = %+v, want one thread reply hit", resp)
	}
	if resp.Results[0].ThreadRootMessageID == nil || *resp.Results[0].ThreadRootMessageID != first.ID {
		t.Fatalf("thread search root = %v, want %s", resp.Results[0].ThreadRootMessageID, first.ID)
	}
}

func TestChannelArchiveHidesFromListFreezesWritesAndRestores(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	channelID := seedChannelForTest(t, "archive-freeze-"+uuid.NewString(), testUserID)
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "before archive", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("archive-thread"), 0); err != nil {
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
	if !ch.ManuallyUnread || ch.UnreadCount == 0 || ch.RealUnreadCount != 0 {
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
	if ch.ManuallyUnread || ch.UnreadCount != 0 || ch.RealUnreadCount != 0 {
		t.Fatalf("manual unread not cleared by read: %+v", *ch)
	}
	if ch.PinnedAt == nil {
		t.Fatalf("read unexpectedly cleared pin: %+v", *ch)
	}
}

func TestChannelThreadReplyMetadataReadAndMainTimelineFiltering(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	replierID := createChannelPlainMember(t)
	bystanderID := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "thread-meta-"+uuid.NewString(), testUserID, replierID, bystanderID)
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Root Author", "root topic", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("root-thread"), 0)
	if err != nil {
		t.Fatalf("insert root: %v", err)
	}

	req := newRequestAs(replierID, http.MethodPost, "/api/channels/"+channelID+"/messages/"+root.ID+"/thread", map[string]string{"content": "thread reply"})
	req = withChannelTestWorkspaceCtx(t, req, replierID)
	req = withURLParams(req, "channelId", channelID, "messageId", root.ID)
	rec := httptest.NewRecorder()
	testHandler.SendChannelMessageThreadReply(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("send thread reply: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var reply ChannelMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatalf("decode thread reply: %v", err)
	}
	if reply.ThreadRootMessageID == nil || *reply.ThreadRootMessageID != root.ID {
		t.Fatalf("thread reply root = %v, want %s", reply.ThreadRootMessageID, root.ID)
	}

	rootTimeline := listedMessagesForUser(t, channelID, testUserID)
	if len(rootTimeline) != 1 || rootTimeline[0].ID != root.ID {
		t.Fatalf("main timeline messages = %+v, want root only", rootTimeline)
	}
	if rootTimeline[0].ThreadReplyCount != 1 || rootTimeline[0].ThreadLastReplyAt == nil || !rootTimeline[0].ThreadFollowed || rootTimeline[0].ThreadUnreadCount != 1 {
		t.Fatalf("root author thread metadata = %+v, want followed unread root", rootTimeline[0])
	}

	bystanderTimeline := listedMessagesForUser(t, channelID, bystanderID)
	if len(bystanderTimeline) != 1 || bystanderTimeline[0].ThreadReplyCount != 1 || bystanderTimeline[0].ThreadUnreadCount != 0 || bystanderTimeline[0].ThreadFollowed {
		t.Fatalf("bystander thread metadata = %+v, want reply count but no unread/follow", bystanderTimeline)
	}

	req = newRequest(http.MethodPost, "/api/channels/"+channelID+"/messages/"+root.ID+"/thread/read", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParams(req, "channelId", channelID, "messageId", root.ID)
	rec = httptest.NewRecorder()
	testHandler.MarkChannelThreadRead(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mark thread read: status=%d body=%s", rec.Code, rec.Body.String())
	}

	rootTimeline = listedMessagesForUser(t, channelID, testUserID)
	if rootTimeline[0].ThreadUnreadCount != 0 || !rootTimeline[0].ThreadFollowed {
		t.Fatalf("thread read metadata = %+v, want followed unread cleared", rootTimeline[0])
	}
	ch := listedChannelForTest(t, channelID)
	if ch == nil || ch.RealUnreadCount == 0 {
		t.Fatalf("thread read unexpectedly cleared channel unread: %+v", ch)
	}
}

func TestChannelThreadReplyWithoutMentionDoesNotAmbientDispatch(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Thread Ambient Guard", nil)
	channelID := seedChannelForTest(t, "thread-no-ambient-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "root", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("ambient-guard-root"), 0)
	if err != nil {
		t.Fatalf("insert root: %v", err)
	}

	req := newRequest(http.MethodPost, "/api/channels/"+channelID+"/messages/"+root.ID+"/thread", map[string]string{"content": "hi"})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParams(req, "channelId", channelID, "messageId", root.ID)
	rec := httptest.NewRecorder()
	testHandler.SendChannelMessageThreadReply(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("send thread reply: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var sessionCount, taskCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_agent_session
		WHERE channel_id = $1`, channelID).Scan(&sessionCount); err != nil {
		t.Fatalf("count channel agent sessions: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_task_queue atq
		JOIN channel_agent_session cas ON cas.chat_session_id = atq.chat_session_id
		WHERE cas.channel_id = $1`, channelID).Scan(&taskCount); err != nil {
		t.Fatalf("count channel agent tasks: %v", err)
	}
	if sessionCount != 0 || taskCount != 0 {
		t.Fatalf("plain thread reply created %d agent sessions and %d tasks; want none", sessionCount, taskCount)
	}
}

func TestChannelThreadPlainReplyDispatchesOnlyParticipatingAgent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	participantID := createHandlerTestAgent(t, "Thread Fullstack", nil)
	bystanderID := createHandlerTestAgent(t, "Thread Bystander", nil)
	channelID := seedChannelForTest(t, "thread-followup-agent-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3), ($1, $2, 'agent', $4)`, channelID, testWorkspaceID, participantID, bystanderID); err != nil {
		t.Fatalf("seed agent members: %v", err)
	}
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "root", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("followup-agent-root"), 0)
	if err != nil {
		t.Fatalf("insert root: %v", err)
	}
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "agent", parseUUID(participantID), "Thread Fullstack", "agent answer", "multica", nil, pgtype.UUID{}, parseUUID(root.ID), strPtr("followup-agent-root"), 1); err != nil {
		t.Fatalf("insert agent reply: %v", err)
	}
	followup, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "hi", "multica", nil, pgtype.UUID{}, parseUUID(root.ID), strPtr("followup-agent-root"), 0)
	if err != nil {
		t.Fatalf("insert follow-up: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}

	testHandler.dispatchChannelThreadReplyMentions(ctx, ch, followup, parseUUID(testUserID))

	assertChannelAgentTaskCounts(t, channelID, participantID, bystanderID, 1, 0)
}

func TestChannelThreadPlainReplyDispatchesRootMentionedAgent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	participantID := createHandlerTestAgent(t, "Thread Root Helper", nil)
	bystanderID := createHandlerTestAgent(t, "Thread Root Bystander", nil)
	channelID := seedChannelForTest(t, "thread-root-mention-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3), ($1, $2, 'agent', $4)`, channelID, testWorkspaceID, participantID, bystanderID); err != nil {
		t.Fatalf("seed agent members: %v", err)
	}
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "@Thread Root Helper please help", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("root-mention-followup"), 0)
	if err != nil {
		t.Fatalf("insert root: %v", err)
	}
	followup, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "hi", "multica", nil, pgtype.UUID{}, parseUUID(root.ID), strPtr("root-mention-followup"), 0)
	if err != nil {
		t.Fatalf("insert follow-up: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}

	testHandler.dispatchChannelThreadReplyMentions(ctx, ch, followup, parseUUID(testUserID))

	assertChannelAgentTaskCounts(t, channelID, participantID, bystanderID, 1, 0)
}

func assertChannelAgentTaskCounts(t *testing.T, channelID, participantID, bystanderID string, wantParticipant, wantBystander int) {
	t.Helper()
	ctx := context.Background()
	var participantTasks, bystanderTasks int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_task_queue atq
		JOIN channel_agent_session cas ON cas.chat_session_id = atq.chat_session_id
		WHERE cas.channel_id = $1 AND cas.agent_id = $2`, channelID, participantID).Scan(&participantTasks); err != nil {
		t.Fatalf("count participant tasks: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_task_queue atq
		JOIN channel_agent_session cas ON cas.chat_session_id = atq.chat_session_id
		WHERE cas.channel_id = $1 AND cas.agent_id = $2`, channelID, bystanderID).Scan(&bystanderTasks); err != nil {
		t.Fatalf("count bystander tasks: %v", err)
	}
	if participantTasks != wantParticipant || bystanderTasks != wantBystander {
		t.Fatalf("thread follow-up tasks = participant:%d bystander:%d, want %d/%d", participantTasks, bystanderTasks, wantParticipant, wantBystander)
	}
}

func TestChannelThreadNoNestedAndArchiveReadOnly(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	channelID := seedChannelForTest(t, "thread-guards-"+uuid.NewString(), testUserID)
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "root", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("guard-root"), 0)
	if err != nil {
		t.Fatalf("insert root: %v", err)
	}
	reply, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "reply", "multica", nil, pgtype.UUID{}, parseUUID(root.ID), strPtr("guard-root"), 0)
	if err != nil {
		t.Fatalf("insert reply: %v", err)
	}

	req := newRequest(http.MethodPost, "/api/channels/"+channelID+"/messages/"+reply.ID+"/thread", map[string]string{"content": "nested"})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParams(req, "channelId", channelID, "messageId", reply.ID)
	rec := httptest.NewRecorder()
	testHandler.SendChannelMessageThreadReply(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("nested thread reply: status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = newRequest(http.MethodPost, "/api/channels/"+channelID+"/archive", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec = httptest.NewRecorder()
	testHandler.ArchiveChannel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("archive channel: status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = newRequest(http.MethodGet, "/api/channels/"+channelID+"/messages/"+root.ID+"/thread", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParams(req, "channelId", channelID, "messageId", root.ID)
	rec = httptest.NewRecorder()
	testHandler.ListChannelMessageThread(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list archived thread: status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = newRequest(http.MethodPost, "/api/channels/"+channelID+"/messages/"+root.ID+"/thread", map[string]string{"content": "blocked"})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParams(req, "channelId", channelID, "messageId", root.ID)
	rec = httptest.NewRecorder()
	testHandler.SendChannelMessageThreadReply(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("send archived thread: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestChannelThreadPaginationUsesOlderCursor(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	channelID := seedChannelForTest(t, "thread-page-"+uuid.NewString(), testUserID)
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "root", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("page-root"), 0)
	if err != nil {
		t.Fatalf("insert root: %v", err)
	}
	base := time.Date(2026, 6, 29, 10, 0, 0, 0, time.UTC)
	for i := 1; i <= 3; i++ {
		msg, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", fmt.Sprintf("reply-%d", i), "multica", nil, pgtype.UUID{}, parseUUID(root.ID), strPtr("page-root"), 0)
		if err != nil {
			t.Fatalf("insert reply %d: %v", i, err)
		}
		if _, err := testPool.Exec(ctx, `UPDATE channel_message SET created_at = $1 WHERE id = $2`, base.Add(time.Duration(i)*time.Minute), msg.ID); err != nil {
			t.Fatalf("pin reply time %d: %v", i, err)
		}
	}

	req := newRequest(http.MethodGet, "/api/channels/"+channelID+"/messages/"+root.ID+"/thread?limit=2", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParams(req, "channelId", channelID, "messageId", root.ID)
	rec := httptest.NewRecorder()
	testHandler.ListChannelMessageThread(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list thread page 1: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var page []ChannelMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page 1: %v", err)
	}
	if got := threadContents(page); fmt.Sprint(got) != "[root reply-2 reply-3]" {
		t.Fatalf("page 1 contents = %v", got)
	}
	before, beforeID := rec.Header().Get("X-Next-Before"), rec.Header().Get("X-Next-Before-Id")
	if before == "" || beforeID == "" {
		t.Fatalf("page 1 missing cursor before=%q before_id=%q", before, beforeID)
	}

	req = newRequest(http.MethodGet, "/api/channels/"+channelID+"/messages/"+root.ID+"/thread?limit=2&before="+url.QueryEscape(before)+"&before_id="+beforeID, nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParams(req, "channelId", channelID, "messageId", root.ID)
	rec = httptest.NewRecorder()
	testHandler.ListChannelMessageThread(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list thread page 2: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page 2: %v", err)
	}
	if got := threadContents(page); fmt.Sprint(got) != "[root reply-1]" {
		t.Fatalf("page 2 contents = %v", got)
	}
	if rec.Header().Get("X-Next-Before") != "" || rec.Header().Get("X-Next-Before-Id") != "" {
		t.Fatalf("page 2 should not emit cursor, headers=%v", rec.Header())
	}
}

func TestChannelMessagesPaginationUsesOlderCursor(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	channelID := seedChannelForTest(t, "message-page-"+uuid.NewString(), testUserID)
	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	for i := 1; i <= 4; i++ {
		msg, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", fmt.Sprintf("msg-%d", i), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr(fmt.Sprintf("message-page-%d", i)), 0)
		if err != nil {
			t.Fatalf("insert message %d: %v", i, err)
		}
		if _, err := testPool.Exec(ctx, `UPDATE channel_message SET created_at = $1 WHERE id = $2`, base.Add(time.Duration(i)*time.Minute), msg.ID); err != nil {
			t.Fatalf("pin message time %d: %v", i, err)
		}
	}

	req := newRequest(http.MethodGet, "/api/channels/"+channelID+"/messages?limit=2", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.ListChannelMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list page 1: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var page ChannelMessagesPageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page 1: %v", err)
	}
	if page.Limit != 2 || !page.HasMore || page.NextCursor == nil {
		t.Fatalf("page 1 metadata = limit:%d has_more:%t cursor:%+v", page.Limit, page.HasMore, page.NextCursor)
	}
	if got := threadContents(page.Messages); fmt.Sprint(got) != "[msg-3 msg-4]" {
		t.Fatalf("page 1 contents = %v", got)
	}

	req = newRequest(http.MethodGet, "/api/channels/"+channelID+"/messages?limit=2&before_created_at="+url.QueryEscape(page.NextCursor.CreatedAt)+"&before_id="+page.NextCursor.ID, nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec = httptest.NewRecorder()
	testHandler.ListChannelMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list page 2: status=%d body=%s", rec.Code, rec.Body.String())
	}
	page = ChannelMessagesPageResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page 2: %v", err)
	}
	if page.Limit != 2 || page.HasMore || page.NextCursor != nil {
		t.Fatalf("page 2 metadata = limit:%d has_more:%t cursor:%+v", page.Limit, page.HasMore, page.NextCursor)
	}
	if got := threadContents(page.Messages); fmt.Sprint(got) != "[msg-1 msg-2]" {
		t.Fatalf("page 2 contents = %v", got)
	}
}

func TestChannelMessagesPaginationRejectsInvalidParams(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	channelID := seedChannelForTest(t, "message-page-invalid-"+uuid.NewString(), testUserID)
	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "invalid limit", path: "/api/channels/" + channelID + "/messages?limit=0"},
		{name: "partial cursor", path: "/api/channels/" + channelID + "/messages?before_id=" + uuid.NewString()},
		{name: "invalid cursor time", path: "/api/channels/" + channelID + "/messages?before_created_at=nope&before_id=" + uuid.NewString()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := newRequest(http.MethodGet, tc.path, nil)
			req = withChannelTestWorkspaceCtx(t, req, testUserID)
			req = withURLParam(req, "channelId", channelID)
			rec := httptest.NewRecorder()
			testHandler.ListChannelMessages(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestChannelThreadMentionedAgentReplyStaysInThread(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Thread Agent", nil)
	channelID := seedChannelForTest(t, "thread-agent-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "root", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("ui-thread"), 0)
	if err != nil {
		t.Fatalf("insert root: %v", err)
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "@Thread Agent can you answer here?", "multica", nil, pgtype.UUID{}, parseUUID(root.ID), strPtr("ui-thread"), 0)
	if err != nil {
		t.Fatalf("insert trigger: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	testHandler.dispatchChannelThreadReplyMentions(ctx, ch, trigger, parseUUID(testUserID))

	var sessionID string
	if err := testPool.QueryRow(ctx, `SELECT chat_session_id FROM channel_agent_session WHERE channel_id = $1 AND agent_id = $2`, channelID, agentID).Scan(&sessionID); err != nil {
		t.Fatalf("channel agent session not created: %v", err)
	}
	var promptThreadRoot string
	if err := testPool.QueryRow(ctx, `
		SELECT channel_thread_root_message_id
		FROM chat_message
		WHERE chat_session_id = $1 AND role = 'user'
		ORDER BY created_at DESC
		LIMIT 1`, sessionID).Scan(&promptThreadRoot); err != nil {
		t.Fatalf("load prompt thread root: %v", err)
	}
	if promptThreadRoot != root.ID {
		t.Fatalf("prompt thread root = %q, want %s", promptThreadRoot, root.ID)
	}

	testHandler.handleChannelChatDone(events.Event{Payload: protocol.ChatDonePayload{ChatSessionID: sessionID, Content: "answer in thread"}})
	var replyRoot string
	if err := testPool.QueryRow(ctx, `
		SELECT thread_root_message_id
		FROM channel_message
		WHERE channel_id = $1 AND author_type = 'agent' AND content = 'answer in thread'
		LIMIT 1`, channelID).Scan(&replyRoot); err != nil {
		t.Fatalf("load agent thread reply: %v", err)
	}
	if replyRoot != root.ID {
		t.Fatalf("agent reply thread root = %q, want %s", replyRoot, root.ID)
	}
}

func TestAddChannelMembersRejectsPrivateAgentForPlainMember(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID, _, memberID := privateAgentTestFixture(t)
	channelID := seedChannelForTest(t, "private-agent-batch-"+uuid.NewString(), memberID, testUserID)

	req := newRequestAs(memberID, http.MethodPost, "/api/channels/"+channelID+"/members/batch", AddChannelMembersRequest{Members: []AddChannelMemberRequest{
		{MemberType: "agent", MemberID: agentID},
	}})
	req = withChannelTestWorkspaceCtx(t, req, memberID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.AddChannelMembers(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("batch add private agent as plain member: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var count int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'agent' AND member_id = $3`, channelID, testWorkspaceID, agentID).Scan(&count); err != nil {
		t.Fatalf("count private agent channel members: %v", err)
	}
	if count != 0 {
		t.Fatalf("private agent was added by unauthorized batch request; count=%d", count)
	}
}

func TestAddChannelMemberRejectsDMChannel(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	cleanupDMArtifacts(t)
	agentID := createHandlerTestAgent(t, "DM Member Guard", nil)
	thirdUserID := createChannelPlainMember(t)
	channelID := seedAgentDMChannel(t, agentID)

	req := newRequest(http.MethodPost, "/api/channels/"+channelID+"/members", AddChannelMemberRequest{MemberType: "user", MemberID: thirdUserID})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.AddChannelMember(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("single add member to dm: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var count int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'user' AND member_id = $3`, channelID, testWorkspaceID, thirdUserID).Scan(&count); err != nil {
		t.Fatalf("count third dm member: %v", err)
	}
	if count != 0 {
		t.Fatalf("dm channel accepted a third member via single add; count=%d", count)
	}
}

func TestAddChannelMembersRejectsDMChannel(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	cleanupDMArtifacts(t)
	agentID := createHandlerTestAgent(t, "DM Batch Guard", nil)
	thirdUserID := createChannelPlainMember(t)
	channelID := seedAgentDMChannel(t, agentID)

	req := newRequest(http.MethodPost, "/api/channels/"+channelID+"/members/batch", AddChannelMembersRequest{Members: []AddChannelMemberRequest{
		{MemberType: "user", MemberID: thirdUserID},
	}})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.AddChannelMembers(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("batch add member to dm: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var count int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'user' AND member_id = $3`, channelID, testWorkspaceID, thirdUserID).Scan(&count); err != nil {
		t.Fatalf("count third dm member: %v", err)
	}
	if count != 0 {
		t.Fatalf("dm channel accepted a third member via batch add; count=%d", count)
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
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	name := "channel_plain_" + suffix
	email := "channel-plain-" + suffix + "@multica.test"
	var userID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ($1, $2)
		RETURNING id`, name, email).Scan(&userID); err != nil {
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
	return listedChannelForUser(t, channelID, testUserID)
}

func listedChannelForUser(t *testing.T, channelID, userID string) *ChannelResponse {
	t.Helper()
	req := newRequestAs(userID, http.MethodGet, "/api/channels", nil)
	req = withChannelTestWorkspaceCtx(t, req, userID)
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

func listedMessagesForUser(t *testing.T, channelID, userID string) []ChannelMessageResponse {
	t.Helper()
	req := newRequestAs(userID, http.MethodGet, "/api/channels/"+channelID+"/messages", nil)
	req = withChannelTestWorkspaceCtx(t, req, userID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.ListChannelMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list channel messages: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var messages []ChannelMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &messages); err != nil {
		t.Fatalf("decode messages: %v", err)
	}
	return messages
}

func threadContents(messages []ChannelMessageResponse) []string {
	out := make([]string, 0, len(messages))
	for _, msg := range messages {
		out = append(out, msg.Content)
	}
	return out
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
