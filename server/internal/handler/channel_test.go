package handler

import (
	"context"
	"encoding/json"

	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
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

func TestChannelMessageResponseUsesRaftTypeField(t *testing.T) {
	body, err := json.Marshal(ChannelMessageResponse{
		ID:          "message-1",
		ChannelID:   "channel-1",
		WorkspaceID: "workspace-1",
		Type:        "system",
		AuthorName:  "system",
		Content:     "runtime outdated",
	})
	if err != nil {
		t.Fatalf("marshal channel message: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal channel message: %v", err)
	}
	if payload["type"] != "system" {
		t.Fatalf("type = %v, want system", payload["type"])
	}
	if _, ok := payload["author_type"]; ok {
		t.Fatalf("channel message response exposed legacy author_type: %s", string(body))
	}
}

func TestCreateChannelDuplicateNameReturnsCodedConflict(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	name := "duplicate-channel-" + uuid.NewString()
	first := httptest.NewRecorder()
	req := withChannelTestWorkspaceCtx(t, newRequest(http.MethodPost, "/api/channels", map[string]any{"name": name}), testUserID)
	testHandler.CreateChannel(first, req)
	if first.Code != http.StatusCreated {
		t.Fatalf("initial create: status=%d body=%s", first.Code, first.Body.String())
	}

	var created ChannelResponse
	if err := json.Unmarshal(first.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created channel: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, created.ID)
	})

	duplicate := httptest.NewRecorder()
	req = withChannelTestWorkspaceCtx(t, newRequest(http.MethodPost, "/api/channels", map[string]any{"name": name}), testUserID)
	testHandler.CreateChannel(duplicate, req)
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate create: status=%d body=%s", duplicate.Code, duplicate.Body.String())
	}

	var body map[string]string
	if err := json.Unmarshal(duplicate.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode duplicate error body: %v", err)
	}
	if body["code"] != channelNameTakenCode {
		t.Fatalf("duplicate error code = %q, want %q; body=%v", body["code"], channelNameTakenCode, body)
	}
	if body["error"] != "channel name already exists" {
		t.Fatalf("duplicate error message = %q", body["error"])
	}
}

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
	var threadID, promptRoot string
	var depth int
	var prompt string
	if err := testPool.QueryRow(ctx, `
		SELECT thread_id, channel_thread_root_message_id, trigger_depth, content
		FROM chat_message
		WHERE chat_session_id = $1 AND role = 'user'
		ORDER BY created_at DESC
		LIMIT 1`, sessionID).Scan(&threadID, &promptRoot, &depth, &prompt); err != nil {
		t.Fatalf("load prompt message: %v", err)
	}
	if threadID != "debate-thread" || promptRoot != trigger.ID || depth != 2 {
		t.Fatalf("prompt thread/root/depth = %q/%q/%d, want debate-thread/%s/2", threadID, promptRoot, depth, trigger.ID)
	}
	if strings.Contains(prompt, "Recent channel messages from this channel only (bounded window):") {
		t.Fatalf("prompt should not repeat the trigger in recent channel context:\n%s", prompt)
	}
	if count := strings.Count(prompt, "@Channel Helper please join"); count != 1 {
		t.Fatalf("current trigger should appear exactly once, got %d:\n%s", count, prompt)
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
	var replyRoot string
	if err := testPool.QueryRow(ctx, `
		SELECT author_type, thread_id, thread_root_message_id, trigger_depth
		FROM channel_message
		WHERE channel_id = $1 AND content = '@Channel Helper says hi'
		LIMIT 1`, channelID).Scan(&authorType, &replyThread, &replyRoot, &replyDepth); err != nil {
		t.Fatalf("load bridged reply: %v", err)
	}
	if authorType != "agent" || replyThread != "debate-thread" || replyRoot != trigger.ID || replyDepth != 3 {
		t.Fatalf("bridged reply = %s/%q/%q/%d, want agent/debate-thread/%s/3", authorType, replyThread, replyRoot, replyDepth, trigger.ID)
	}
}

func TestChannelChatDoneNoReplyAndReactionPayloadOnly(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Reaction Agent", nil)
	channelID := seedChannelForTest(t, "no-reply-reaction-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}

	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "@Reaction Agent react only", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert trigger: %v", err)
	}
	testHandler.dispatchChannelMentions(ctx, ch, trigger, parseUUID(testUserID))

	var sessionID string
	if err := testPool.QueryRow(ctx, `SELECT chat_session_id FROM channel_agent_session WHERE channel_id = $1 AND agent_id = $2`, channelID, agentID).Scan(&sessionID); err != nil {
		t.Fatalf("channel agent session not created: %v", err)
	}

	noReplyContent := "internal analysis should not become a channel reply"
	testHandler.handleChannelChatDone(events.Event{Payload: protocol.ChatDonePayload{
		ChatSessionID: sessionID,
		Type:          protocol.ChatOutputKindNoReply,
		Content:       noReplyContent,
	}})
	assertNoChannelMessageContent(t, channelID, noReplyContent)

	testHandler.handleChannelChatDone(events.Event{Payload: protocol.ChatDonePayload{
		ChatSessionID: sessionID,
		Type:          protocol.ChatOutputKindReaction,
		Reaction:      &protocol.ChatReactionPayload{MessageID: trigger.ID, Emoji: "👍"},
	}})
	assertNoChannelMessageContent(t, channelID, "👍")

	var reactionCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message_reaction
		WHERE channel_message_id = $1 AND actor_type = 'agent' AND actor_id = $2 AND emoji = '👍'`, trigger.ID, agentID).Scan(&reactionCount); err != nil {
		t.Fatalf("count channel reaction: %v", err)
	}
	if reactionCount != 1 {
		t.Fatalf("reaction count = %d, want 1", reactionCount)
	}

	reactionCommand := fmt.Sprintf("multica message react --message %s --emoji 🎉", trigger.ID)
	testHandler.handleChannelChatDone(events.Event{Payload: protocol.ChatDonePayload{
		ChatSessionID: sessionID,
		Type:          protocol.ChatOutputKindMessage,
		Content:       reactionCommand,
	}})
	assertChannelMessageContentCount(t, channelID, reactionCommand, 1)

	var legacyReactionCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message_reaction
		WHERE channel_message_id = $1 AND actor_type = 'agent' AND actor_id = $2 AND emoji = '🎉'`, trigger.ID, agentID).Scan(&legacyReactionCount); err != nil {
		t.Fatalf("count legacy channel reaction: %v", err)
	}
	if legacyReactionCount != 0 {
		t.Fatalf("message react count = %d, want 0", legacyReactionCount)
	}

	legacyReactionCommand := fmt.Sprintf("multica channel react %s 🚀", trigger.ID)
	testHandler.handleChannelChatDone(events.Event{Payload: protocol.ChatDonePayload{
		ChatSessionID: sessionID,
		Type:          protocol.ChatOutputKindMessage,
		Content:       legacyReactionCommand,
	}})
	assertChannelMessageContentCount(t, channelID, legacyReactionCommand, 1)

	var legacyChannelReactionCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message_reaction
		WHERE channel_message_id = $1 AND actor_type = 'agent' AND actor_id = $2 AND emoji = '🚀'`, trigger.ID, agentID).Scan(&legacyChannelReactionCount); err != nil {
		t.Fatalf("count legacy channel reaction: %v", err)
	}
	if legacyChannelReactionCount != 0 {
		t.Fatalf("legacy channel reaction count = %d, want 0", legacyChannelReactionCount)
	}

	if _, err := testPool.Exec(ctx, `
		UPDATE channel_message
		SET content = '', parts = '[]'::jsonb, deleted_at = now()
		WHERE id = $1`, trigger.ID); err != nil {
		t.Fatalf("soft delete trigger message: %v", err)
	}
	if _, err := testPool.Exec(ctx, `DELETE FROM channel_message_reaction WHERE channel_message_id = $1`, trigger.ID); err != nil {
		t.Fatalf("clear trigger reactions: %v", err)
	}
	testHandler.handleChannelChatDone(events.Event{Payload: protocol.ChatDonePayload{
		ChatSessionID: sessionID,
		Type:          protocol.ChatOutputKindReaction,
		Reaction:      &protocol.ChatReactionPayload{MessageID: trigger.ID, Emoji: "🧯"},
	}})
	assertNoChannelMessageContent(t, channelID, "🧯")

	var deletedReactionCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message_reaction
		WHERE channel_message_id = $1 AND actor_type = 'agent' AND actor_id = $2`, trigger.ID, agentID).Scan(&deletedReactionCount); err != nil {
		t.Fatalf("count deleted-message channel reaction: %v", err)
	}
	if deletedReactionCount != 0 {
		t.Fatalf("deleted-message reaction count = %d, want 0", deletedReactionCount)
	}

}

func TestChannelChatDoneSuppressedDaemonOutdatedDoesNotWriteSystemMessage(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Daemon Outdated Agent", nil)
	channelID := seedChannelForTest(t, "daemon-outdated-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}

	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "@Daemon Outdated Agent reply", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert trigger: %v", err)
	}
	testHandler.dispatchChannelMentions(ctx, ch, trigger, parseUUID(testUserID))

	var sessionID string
	if err := testPool.QueryRow(ctx, `SELECT chat_session_id FROM channel_agent_session WHERE channel_id = $1 AND agent_id = $2`, channelID, agentID).Scan(&sessionID); err != nil {
		t.Fatalf("channel agent session not created: %v", err)
	}

	testHandler.handleChannelChatDone(events.Event{Payload: protocol.ChatDonePayload{
		ChatSessionID:          sessionID,
		TaskID:                 uuid.NewString(),
		Type:                   protocol.ChatOutputKindNoReply,
		OutputSuppressedReason: protocol.ChannelOutputSuppressedReasonDaemonOutdated,
	}})

	var messageCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM channel_message
		WHERE channel_id = $1 AND author_type IN ('agent', 'system')
	`, channelID).Scan(&messageCount); err != nil {
		t.Fatalf("count visible output messages: %v", err)
	}
	if messageCount != 0 {
		t.Fatalf("visible output message count = %d, want 0", messageCount)
	}
}

func TestChannelChatDoneSuppressedTraceOnlyDoesNotWriteSystemMessage(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Trace Only Agent", nil)
	channelID := seedChannelForTest(t, "trace-only-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}

	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "@Trace Only Agent reply", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert trigger: %v", err)
	}
	testHandler.dispatchChannelMentions(ctx, ch, trigger, parseUUID(testUserID))

	var sessionID string
	if err := testPool.QueryRow(ctx, `SELECT chat_session_id FROM channel_agent_session WHERE channel_id = $1 AND agent_id = $2`, channelID, agentID).Scan(&sessionID); err != nil {
		t.Fatalf("channel agent session not created: %v", err)
	}

	testHandler.handleChannelChatDone(events.Event{Payload: protocol.ChatDonePayload{
		ChatSessionID:          sessionID,
		TaskID:                 uuid.NewString(),
		Type:                   protocol.ChatOutputKindNoReply,
		OutputSuppressedReason: protocol.ChannelOutputSuppressedReasonInvalidAction,
	}})

	var messageCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM channel_message
		WHERE channel_id = $1 AND author_type IN ('agent', 'system')
	`, channelID).Scan(&messageCount); err != nil {
		t.Fatalf("count visible output messages: %v", err)
	}
	if messageCount != 0 {
		t.Fatalf("visible output message count = %d, want 0", messageCount)
	}
}

func TestChannelRementionFollowupDoesNotCancelRunningTask(t *testing.T) {
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

	var agentSessionID string
	if err := testPool.QueryRow(ctx, `
		SELECT s.id
		FROM agent_session s
		WHERE s.channel_id = $1 AND s.agent_id = $2`, channelID, agentID).Scan(&agentSessionID); err != nil {
		t.Fatalf("agent session not created: %v", err)
	}
	var firstEventID string
	if err := testPool.QueryRow(ctx, `
		SELECT id
		FROM agent_inbox_event
		WHERE agent_session_id = $1 AND requires_wake = true
		ORDER BY created_at DESC LIMIT 1`, agentSessionID).Scan(&firstEventID); err != nil {
		t.Fatalf("load first inbox event: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE agent_inbox_event SET status = 'draining', claimed_at = now() WHERE id = $1`, firstEventID); err != nil {
		t.Fatalf("mark first inbox event draining: %v", err)
	}

	second, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "@Remention Agent stop and use this corrected direction", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("interrupt-thread"), 1)
	if err != nil {
		t.Fatalf("insert second trigger: %v", err)
	}
	testHandler.dispatchChannelMentions(ctx, ch, second, parseUUID(testUserID))

	var oldStatus string
	if err := testPool.QueryRow(ctx, `
		SELECT status
		FROM agent_inbox_event WHERE id = $1`, firstEventID).Scan(&oldStatus); err != nil {
		t.Fatalf("load interrupted inbox event: %v", err)
	}
	// #311: the system must NOT cancel an in-flight directed run when a follow-up
	// mention arrives. The first inbox event stays draining; the follow-up waits
	// behind it so both requests get answered instead of the earlier one being
	// silently dropped.
	if oldStatus != "draining" {
		t.Fatalf("first inbox event status = %q, want draining — #311 abolished the followup cancel", oldStatus)
	}
	var eventCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_inbox_event
		WHERE agent_session_id = $1 AND requires_wake = true`, agentSessionID).Scan(&eventCount); err != nil {
		t.Fatalf("count session inbox events: %v", err)
	}
	if eventCount != 2 {
		t.Fatalf("session inbox event count = %d, want 2 (first still draining + queued follow-up)", eventCount)
	}
}

func TestChannelAmbientGateBoundsRepeatedAmbientFanout(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	withChannelAmbientGateTestConfig(t)
	channelID := seedChannelForTest(t, "ambient-gate-bounds-"+uuid.NewString(), testUserID)
	agentIDs := make([]string, 0, 16)
	for i := 0; i < 16; i++ {
		agentID := createHandlerTestAgent(t, fmt.Sprintf("Ambient Gate Agent %02d %s", i, uuid.NewString()[:8]), nil)
		agentIDs = append(agentIDs, agentID)
		if _, err := testPool.Exec(ctx, `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
			VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
			t.Fatalf("seed agent member: %v", err)
		}
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}

	for i := 0; i < 3; i++ {
		trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", fmt.Sprintf("ordinary ambient update %d", i), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("ambient-gate-bounds"), 0)
		if err != nil {
			t.Fatalf("insert ambient trigger %d: %v", i, err)
		}
		testHandler.dispatchChannelMessageToAgents(ctx, ch, trigger, parseUUID(testUserID))
	}

	var lastSeq int64
	if err := testPool.QueryRow(ctx, `
		SELECT last_seq
		FROM conversation
		WHERE channel_id = $1`, channelID).Scan(&lastSeq); err != nil {
		t.Fatalf("load conversation last_seq: %v", err)
	}
	var events, sessions, wakeEvents int
	var minSeq, maxSeq int64
	if err := testPool.QueryRow(ctx, `
		SELECT count(*),
		       count(DISTINCT agent_session_id),
		       count(*) FILTER (WHERE requires_wake),
		       COALESCE(min(seq_to), 0),
		       COALESCE(max(seq_to), 0)
		FROM agent_inbox_event
		WHERE channel_id = $1 AND reason = 'ambient'`, channelID).Scan(&events, &sessions, &wakeEvents, &minSeq, &maxSeq); err != nil {
		t.Fatalf("count ambient inbox events: %v", err)
	}
	if events != len(agentIDs) || sessions != len(agentIDs) || wakeEvents != 0 || minSeq != lastSeq || maxSeq != lastSeq {
		t.Fatalf("ambient inbox events=%d sessions=%d wake=%d min=%d max=%d, want events/sessions=%d wake=0 seq=%d", events, sessions, wakeEvents, minSeq, maxSeq, len(agentIDs), lastSeq)
	}
}

// TestChannelAmbientDispatchNotSkippedForAtMention is the #328 regression: a
// collective instruction that @-mentions a non-agent (a human) must still be
// delivered to the channel's agents as ambient context, so each model — not the
// server — decides whether it's addressed. Before the fix,
// dispatchChannelMessageToAgents returned early on any "@" in the content, so
// agents never saw "一起 @xx 打个招呼".
func TestChannelAmbientDispatchNotSkippedForAtMention(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	withChannelAmbientGateTestConfig(t)
	agentID := createHandlerTestAgent(t, "Ambient AtMention Agent "+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "ambient-atmention-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}

	// Collective instruction that @-mentions a human who is NOT a channel agent.
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "大家一起 @some-human 打个招呼", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("ambient-atmention"), 0)
	if err != nil {
		t.Fatalf("insert at-mention trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, trigger, parseUUID(testUserID))

	assertChannelAgentInboxEventCounts(t, channelID, agentID, 1, 0)
}

func TestChannelAmbientUnreadPromptKeepsLatestTriggerWhenCursorIsStale(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Ambient Latest Trigger "+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "ambient-latest-trigger-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	agent, err := testHandler.Queries.GetAgent(ctx, parseUUID(agentID))
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}

	for i := 0; i < channelContextMessageLimit+3; i++ {
		if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", fmt.Sprintf("old ambient backlog %02d", i), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0); err != nil {
			t.Fatalf("insert old ambient message %d: %v", i, err)
		}
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "大家一起 @Frank An 打个招呼吧！", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert collective trigger: %v", err)
	}

	prompt := testHandler.buildChannelAmbientUnreadPromptWithDB(ctx, testHandler.DB, ch, agent, trigger, 0, trigger.Seq)
	if !strings.Contains(prompt, "大家一起 @Frank An 打个招呼吧！") {
		t.Fatalf("ambient prompt omitted latest trigger:\n%s", prompt)
	}
	if strings.Contains(prompt, "old ambient backlog 00") {
		t.Fatalf("ambient prompt kept oldest backlog instead of latest window:\n%s", prompt)
	}
	if strings.Contains(prompt, "everyone/all agents") {
		t.Fatalf("ambient prompt still contains removed all-agents force-reply wording:\n%s", prompt)
	}
}

func TestChannelAmbientGateSerializesConcurrentSameAgentAmbient(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	withChannelAmbientGateTestConfig(t)
	agentID := createHandlerTestAgent(t, "Ambient Concurrent Gate "+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "ambient-concurrent-gate-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}

	start := make(chan struct{})
	const sends = 8
	var wg sync.WaitGroup
	for i := 0; i < sends; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", fmt.Sprintf("ordinary concurrent ambient update %d", i), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("ambient-concurrent-gate"), 0)
			if err != nil {
				t.Errorf("insert ambient trigger %d: %v", i, err)
				return
			}
			testHandler.dispatchChannelMessageToAgents(ctx, ch, trigger, parseUUID(testUserID))
		}()
	}
	close(start)
	wg.Wait()

	var lastSeq, inboxSeq int64
	var events int
	if err := testPool.QueryRow(ctx, `
		SELECT c.last_seq, COALESCE(max(e.seq_to), 0), count(e.id)
		FROM conversation c
		LEFT JOIN agent_inbox_event e ON e.conversation_id = c.id
		 AND e.agent_id = $2
		 AND e.reason = 'ambient'
		WHERE c.channel_id = $1
		GROUP BY c.last_seq`, channelID, agentID).Scan(&lastSeq, &inboxSeq, &events); err != nil {
		t.Fatalf("load coalesced ambient inbox event: %v", err)
	}
	if events != 1 || inboxSeq != lastSeq {
		t.Fatalf("ambient inbox events=%d seq=%d, want one coalesced event at last_seq=%d", events, inboxSeq, lastSeq)
	}
}

func TestChannelAgentInboxDrainAckDirectedMention(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentName := "Inbox Drain Agent " + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentName, nil)
	runtimeID := handlerTestRuntimeID(t)
	channelID := seedChannelForTest(t, "agent-inbox-drain-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}

	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "setup context before mention", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("inbox-drain"), 0); err != nil {
		t.Fatalf("insert setup message: %v", err)
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "[@"+agentName+"](mention://agent/"+agentID+") please answer", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("inbox-drain"), 0)
	if err != nil {
		t.Fatalf("insert mention trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, trigger, parseUUID(testUserID))

	drainReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "agent-inbox-drain-daemon")
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
		t.Fatalf("drain returned %d events, want 1: %s", len(drainResp.Events), drainRec.Body.String())
	}
	got := drainResp.Events[0]
	if got.AgentID != agentID || got.Reason != "mention" || !got.RequiresWake || got.SeqTo != trigger.Seq {
		t.Fatalf("drained event = %+v, want mention wake for agent %s seq %d", got, agentID, trigger.Seq)
	}

	partialAckReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+got.ID+"/ack", AckAgentInboxEventRequest{
		DeliveryID:  got.DeliveryID,
		LeaseToken:  got.LeaseToken,
		SeenUpToSeq: got.SeqTo - 1,
	}, testWorkspaceID, "agent-inbox-drain-daemon")
	partialAckReq = withURLParam(partialAckReq, "eventId", got.ID)
	partialAckRec := httptest.NewRecorder()
	testHandler.AckAgentInboxEvent(partialAckRec, partialAckReq)
	if partialAckRec.Code != http.StatusConflict {
		t.Fatalf("partial ack status=%d body=%s, want 409", partialAckRec.Code, partialAckRec.Body.String())
	}

	ackReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+got.ID+"/ack", AckAgentInboxEventRequest{
		DeliveryID:  got.DeliveryID,
		LeaseToken:  got.LeaseToken,
		SeenUpToSeq: got.SeqTo,
	}, testWorkspaceID, "agent-inbox-drain-daemon")
	ackReq = withURLParam(ackReq, "eventId", got.ID)
	ackRec := httptest.NewRecorder()
	testHandler.AckAgentInboxEvent(ackRec, ackReq)
	if ackRec.Code != http.StatusOK {
		t.Fatalf("ack inbox event: status=%d body=%s", ackRec.Code, ackRec.Body.String())
	}

	var eventStatus string
	var lastDrainedSeq int64
	if err := testPool.QueryRow(ctx, `
		SELECT e.status, s.last_drained_seq
		FROM agent_inbox_event e
		JOIN agent_session s ON s.id = e.agent_session_id
		WHERE e.id = $1`, got.ID).Scan(&eventStatus, &lastDrainedSeq); err != nil {
		t.Fatalf("load acked inbox event: %v", err)
	}
	if eventStatus != "acked" || lastDrainedSeq != got.SeqTo {
		t.Fatalf("acked inbox event = (%q, last_drained_seq=%d), want acked/%d", eventStatus, lastDrainedSeq, got.SeqTo)
	}
}

func TestChannelAgentInboxCompleteDirectedMentionWritesReply(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentName := "Inbox Complete Agent " + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentName, nil)
	runtimeID := handlerTestRuntimeID(t)
	channelID := seedChannelForTest(t, "agent-inbox-complete-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "[@"+agentName+"](mention://agent/"+agentID+") please answer from inbox complete", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("inbox-complete"), 0)
	if err != nil {
		t.Fatalf("insert mention trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, trigger, parseUUID(testUserID))

	drainReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "agent-inbox-complete-daemon")
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
		t.Fatalf("drain returned %d events, want 1: %s", len(drainResp.Events), drainRec.Body.String())
	}
	got := drainResp.Events[0]
	if got.Task == nil {
		t.Fatalf("drained directed event missing executable task: %+v", got)
	}
	if got.Task.ID != got.ID || got.Task.ChatSessionID == "" || got.Task.InboxEvent == nil {
		t.Fatalf("drained task = %+v, want inbox-backed chat task for event %s", got.Task, got.ID)
	}

	testHandler.Bus.Subscribe(protocol.EventChatDone, func(e events.Event) {
		payload, ok := e.Payload.(protocol.ChatDonePayload)
		if ok && payload.TaskID == got.ID {
			testHandler.handleChannelChatDone(e)
		}
	})

	const reply = "Plain final reply from inbox complete"
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_event_delivery
		SET lease_expires_at = now() - interval '1 second'
		WHERE id = $1`, got.DeliveryID); err != nil {
		t.Fatalf("expire delivery before complete: %v", err)
	}
	completeReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+got.ID+"/complete", CompleteAgentInboxEventRequest{
		DeliveryID: got.DeliveryID,
		LeaseToken: got.LeaseToken,
		TaskCompleteRequest: TaskCompleteRequest{
			Output: reply,
		},
	}, testWorkspaceID, "agent-inbox-complete-daemon")
	completeReq = withURLParam(completeReq, "eventId", got.ID)
	completeRec := httptest.NewRecorder()
	testHandler.CompleteAgentInboxEvent(completeRec, completeReq)
	if completeRec.Code != http.StatusOK {
		t.Fatalf("complete inbox event: status=%d body=%s", completeRec.Code, completeRec.Body.String())
	}
	assertChannelMessageContentCount(t, channelID, reply, 1)

	var status string
	if err := testPool.QueryRow(ctx, `SELECT status FROM agent_inbox_event WHERE id = $1`, got.ID).Scan(&status); err != nil {
		t.Fatalf("load inbox event status: %v", err)
	}
	if status != "acked" {
		t.Fatalf("inbox event status = %q, want acked", status)
	}
}

func TestChannelAgentInboxTransportCanSendStickerAndSuppressesFinalOutput(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentName := "Inbox Sticker Agent " + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentName, nil)
	runtimeID := handlerTestRuntimeID(t)
	if _, err := testPool.Exec(ctx, `UPDATE agent_runtime SET owner_id = $1 WHERE id = $2`, testUserID, runtimeID); err != nil {
		t.Fatalf("seed runtime owner: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `UPDATE agent_runtime SET owner_id = NULL WHERE id = $1`, runtimeID)
	})
	channelID := seedChannelForTest(t, "agent-inbox-sticker-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "[@"+agentName+"](mention://agent/"+agentID+") send a sticker", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("inbox-sticker"), 0)
	if err != nil {
		t.Fatalf("insert mention trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, trigger, parseUUID(testUserID))

	drainReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "agent-inbox-sticker-daemon")
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
	if len(drainResp.Events) != 1 || drainResp.Events[0].Task == nil || drainResp.Events[0].Task.AuthToken == "" {
		t.Fatalf("drain response missing inbox task auth token: %s", drainRec.Body.String())
	}
	got := drainResp.Events[0]

	clientID := "inbox-sticker-" + uuid.NewString()
	sendReq := newRequest(http.MethodPost, "/api/agent/messages/send", map[string]any{
		"parts": []protocol.MessagePart{{
			Type:      protocol.MessagePartTypeSticker,
			StickerID: "huaji",
		}},
		"client_message_id": clientID,
	})
	sendReq = withChatTestWorkspaceCtx(t, sendReq)
	sendReq.Header.Set("X-Actor-Source", "agent_inbox_token")
	sendReq.Header.Set("X-Agent-ID", agentID)
	sendReq.Header.Set("X-Agent-Inbox-Event-ID", got.ID)
	sendReq.Header.Set("X-Agent-Inbox-Delivery-ID", got.DeliveryID)
	sendReq.Header.Set("X-Task-ID", got.ID)
	sendRec := httptest.NewRecorder()
	testHandler.AgentTransportSendMessage(sendRec, sendReq)
	if sendRec.Code != http.StatusCreated {
		t.Fatalf("inbox transport sticker send: status=%d body=%s", sendRec.Code, sendRec.Body.String())
	}
	var sendBody AgentTransportSendResponse
	if err := json.Unmarshal(sendRec.Body.Bytes(), &sendBody); err != nil {
		t.Fatalf("decode inbox transport send: %v", err)
	}
	if len(sendBody.Message.Parts) != 1 || sendBody.Message.Parts[0].Type != protocol.MessagePartTypeSticker || sendBody.Message.Parts[0].StickerID != "huaji" {
		t.Fatalf("sticker message parts = %+v, want huaji sticker", sendBody.Message.Parts)
	}
	var taskAuditRows, inboxAuditRows int
	if err := testPool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE task_id IS NOT NULL),
			count(*) FILTER (WHERE inbox_event_id = $1)
		FROM agent_task_transport_audit
		WHERE agent_id = $2 AND action = 'message_send'`,
		got.ID, agentID).Scan(&taskAuditRows, &inboxAuditRows); err != nil {
		t.Fatalf("count transport audit rows: %v", err)
	}
	if taskAuditRows != 0 || inboxAuditRows != 1 {
		t.Fatalf("transport audit task rows=%d inbox rows=%d, want 0/1", taskAuditRows, inboxAuditRows)
	}

	finalText := "duplicate final after inbox sticker " + uuid.NewString()
	completeReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+got.ID+"/complete", CompleteAgentInboxEventRequest{
		DeliveryID: got.DeliveryID,
		LeaseToken: got.LeaseToken,
		TaskCompleteRequest: TaskCompleteRequest{
			Output: finalText,
		},
	}, testWorkspaceID, "agent-inbox-sticker-daemon")
	completeReq = withURLParam(completeReq, "eventId", got.ID)
	completeRec := httptest.NewRecorder()
	testHandler.CompleteAgentInboxEvent(completeRec, completeReq)
	if completeRec.Code != http.StatusOK {
		t.Fatalf("complete inbox event: status=%d body=%s", completeRec.Code, completeRec.Body.String())
	}
	assertNoChannelMessageContent(t, channelID, finalText)
}

func TestChannelAgentInboxDrainReclaimsExpiredDelivery(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentName := "Inbox Reclaim Agent " + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentName, nil)
	runtimeID := handlerTestRuntimeID(t)
	channelID := seedChannelForTest(t, "agent-inbox-reclaim-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "[@"+agentName+"](mention://agent/"+agentID+") please answer", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("inbox-reclaim"), 0)
	if err != nil {
		t.Fatalf("insert mention trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, trigger, parseUUID(testUserID))

	drainReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "agent-inbox-reclaim-daemon")
	drainReq = withURLParam(drainReq, "runtimeId", runtimeID)
	drainRec := httptest.NewRecorder()
	testHandler.DrainAgentInboxByRuntime(drainRec, drainReq)
	if drainRec.Code != http.StatusOK {
		t.Fatalf("first drain inbox: status=%d body=%s", drainRec.Code, drainRec.Body.String())
	}
	var drainResp DrainAgentInboxResponse
	if err := json.Unmarshal(drainRec.Body.Bytes(), &drainResp); err != nil {
		t.Fatalf("decode first drain response: %v", err)
	}
	if len(drainResp.Events) != 1 {
		t.Fatalf("first drain returned %d events, want 1: %s", len(drainResp.Events), drainRec.Body.String())
	}
	firstDelivery := drainResp.Events[0]
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_event_delivery
		SET lease_expires_at = now() - interval '1 second'
		WHERE id = $1`, firstDelivery.DeliveryID); err != nil {
		t.Fatalf("expire first delivery: %v", err)
	}

	drainReq = newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "agent-inbox-reclaim-daemon")
	drainReq = withURLParam(drainReq, "runtimeId", runtimeID)
	drainRec = httptest.NewRecorder()
	testHandler.DrainAgentInboxByRuntime(drainRec, drainReq)
	if drainRec.Code != http.StatusOK {
		t.Fatalf("second drain inbox: status=%d body=%s", drainRec.Code, drainRec.Body.String())
	}
	if err := json.Unmarshal(drainRec.Body.Bytes(), &drainResp); err != nil {
		t.Fatalf("decode second drain response: %v", err)
	}
	if len(drainResp.Events) != 1 {
		t.Fatalf("second drain returned %d events, want 1: %s", len(drainResp.Events), drainRec.Body.String())
	}
	secondDelivery := drainResp.Events[0]
	if secondDelivery.ID != firstDelivery.ID || secondDelivery.DeliveryID == firstDelivery.DeliveryID {
		t.Fatalf("second drain = %+v, want same event with a new delivery after expiry", secondDelivery)
	}

	var eventStatus, oldDeliveryStatus, newDeliveryStatus string
	if err := testPool.QueryRow(ctx, `
		SELECT e.status, old_delivery.status, new_delivery.status
		FROM agent_inbox_event e
		JOIN agent_event_delivery old_delivery ON old_delivery.id = $2
		JOIN agent_event_delivery new_delivery ON new_delivery.id = $3
		WHERE e.id = $1`, firstDelivery.ID, firstDelivery.DeliveryID, secondDelivery.DeliveryID).Scan(&eventStatus, &oldDeliveryStatus, &newDeliveryStatus); err != nil {
		t.Fatalf("load reclaimed inbox states: %v", err)
	}
	if eventStatus != "draining" || oldDeliveryStatus != "expired" || newDeliveryStatus != "leased" {
		t.Fatalf("reclaimed states = event:%q old:%q new:%q, want draining/expired/leased", eventStatus, oldDeliveryStatus, newDeliveryStatus)
	}
}

func TestChannelAgentInboxDrainAckAmbientAdvancesSessionCursor(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentName := "Inbox Ambient Agent " + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentName, nil)
	runtimeID := handlerTestRuntimeID(t)
	channelID := seedChannelForTest(t, "agent-inbox-ambient-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}

	first, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "ordinary ambient one", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("inbox-ambient"), 0)
	if err != nil {
		t.Fatalf("insert first ambient message: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, first, parseUUID(testUserID))

	drainReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "agent-inbox-ambient-daemon")
	drainReq = withURLParam(drainReq, "runtimeId", runtimeID)
	drainRec := httptest.NewRecorder()
	testHandler.DrainAgentInboxByRuntime(drainRec, drainReq)
	if drainRec.Code != http.StatusOK {
		t.Fatalf("drain ambient inbox: status=%d body=%s", drainRec.Code, drainRec.Body.String())
	}
	var drainResp DrainAgentInboxResponse
	if err := json.Unmarshal(drainRec.Body.Bytes(), &drainResp); err != nil {
		t.Fatalf("decode ambient drain response: %v", err)
	}
	if len(drainResp.Events) != 1 {
		t.Fatalf("ambient drain returned %d events, want 1: %s", len(drainResp.Events), drainRec.Body.String())
	}
	got := drainResp.Events[0]
	if got.AgentID != agentID || got.Reason != "ambient" || got.RequiresWake || got.SeqTo != first.Seq {
		t.Fatalf("drained ambient event = %+v, want ambient context for agent %s seq %d", got, agentID, first.Seq)
	}
	if len(got.Messages) != 1 || got.Messages[0].Content != "ordinary ambient one" {
		t.Fatalf("ambient drain messages = %+v, want first ambient message only", got.Messages)
	}

	ackReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+got.ID+"/ack", AckAgentInboxEventRequest{
		DeliveryID:  got.DeliveryID,
		LeaseToken:  got.LeaseToken,
		SeenUpToSeq: got.SeqTo,
	}, testWorkspaceID, "agent-inbox-ambient-daemon")
	ackReq = withURLParam(ackReq, "eventId", got.ID)
	ackRec := httptest.NewRecorder()
	testHandler.AckAgentInboxEvent(ackRec, ackReq)
	if ackRec.Code != http.StatusOK {
		t.Fatalf("ack ambient inbox event: status=%d body=%s", ackRec.Code, ackRec.Body.String())
	}

	second, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "ordinary ambient two", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("inbox-ambient"), 0)
	if err != nil {
		t.Fatalf("insert second ambient message: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, second, parseUUID(testUserID))

	drainReq = newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "agent-inbox-ambient-daemon")
	drainReq = withURLParam(drainReq, "runtimeId", runtimeID)
	drainRec = httptest.NewRecorder()
	testHandler.DrainAgentInboxByRuntime(drainRec, drainReq)
	if drainRec.Code != http.StatusOK {
		t.Fatalf("drain second ambient inbox: status=%d body=%s", drainRec.Code, drainRec.Body.String())
	}
	if err := json.Unmarshal(drainRec.Body.Bytes(), &drainResp); err != nil {
		t.Fatalf("decode second ambient drain response: %v", err)
	}
	if len(drainResp.Events) != 1 {
		t.Fatalf("second ambient drain returned %d events, want 1: %s", len(drainResp.Events), drainRec.Body.String())
	}
	got = drainResp.Events[0]
	if got.SeqFrom != first.Seq+1 || got.SeqTo != second.Seq {
		t.Fatalf("second ambient range = [%d,%d], want [%d,%d]", got.SeqFrom, got.SeqTo, first.Seq+1, second.Seq)
	}
	if len(got.Messages) != 1 || got.Messages[0].Content != "ordinary ambient two" {
		t.Fatalf("second ambient drain messages = %+v, want second message only", got.Messages)
	}
}

func TestChannelAgentInboxFailDirectedMention(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentName := "Inbox Fail Agent " + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentName, nil)
	runtimeID := handlerTestRuntimeID(t)
	channelID := seedChannelForTest(t, "agent-inbox-fail-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "[@"+agentName+"](mention://agent/"+agentID+") please answer", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("inbox-fail"), 0)
	if err != nil {
		t.Fatalf("insert mention trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, trigger, parseUUID(testUserID))

	drainReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "agent-inbox-fail-daemon")
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
		t.Fatalf("drain returned %d events, want 1: %s", len(drainResp.Events), drainRec.Body.String())
	}
	got := drainResp.Events[0]

	failReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+got.ID+"/fail", FailAgentInboxEventRequest{
		DeliveryID: got.DeliveryID,
		LeaseToken: got.LeaseToken,
		Error:      "model crashed",
	}, testWorkspaceID, "agent-inbox-fail-daemon")
	failReq = withURLParam(failReq, "eventId", got.ID)
	failRec := httptest.NewRecorder()
	testHandler.FailAgentInboxEvent(failRec, failReq)
	if failRec.Code != http.StatusOK {
		t.Fatalf("fail inbox event: status=%d body=%s", failRec.Code, failRec.Body.String())
	}

	var eventStatus, deliveryStatus, lastError string
	if err := testPool.QueryRow(ctx, `
		SELECT e.status, d.status, COALESCE(e.last_error, '')
		FROM agent_inbox_event e
		JOIN agent_event_delivery d ON d.inbox_event_id = e.id
		WHERE e.id = $1 AND d.id = $2`, got.ID, got.DeliveryID).Scan(&eventStatus, &deliveryStatus, &lastError); err != nil {
		t.Fatalf("load failed inbox event: %v", err)
	}
	if eventStatus != "failed" || deliveryStatus != "failed" || !strings.Contains(lastError, "model crashed") {
		t.Fatalf("failed inbox states = event:%q delivery:%q error:%q, want failed/failed/model crashed", eventStatus, deliveryStatus, lastError)
	}
}

func TestChannelAgentInboxRejectsStaleDeliveryAck(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentName := "Inbox Stale Ack Agent " + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentName, nil)
	runtimeID := handlerTestRuntimeID(t)
	channelID := seedChannelForTest(t, "agent-inbox-stale-ack-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "[@"+agentName+"](mention://agent/"+agentID+") please answer", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("inbox-stale-ack"), 0)
	if err != nil {
		t.Fatalf("insert mention trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, trigger, parseUUID(testUserID))

	drainReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "agent-inbox-stale-daemon")
	drainReq = withURLParam(drainReq, "runtimeId", runtimeID)
	drainRec := httptest.NewRecorder()
	testHandler.DrainAgentInboxByRuntime(drainRec, drainReq)
	if drainRec.Code != http.StatusOK {
		t.Fatalf("first drain inbox: status=%d body=%s", drainRec.Code, drainRec.Body.String())
	}
	var drainResp DrainAgentInboxResponse
	if err := json.Unmarshal(drainRec.Body.Bytes(), &drainResp); err != nil {
		t.Fatalf("decode first drain response: %v", err)
	}
	if len(drainResp.Events) != 1 {
		t.Fatalf("first drain returned %d events, want 1: %s", len(drainResp.Events), drainRec.Body.String())
	}
	firstDelivery := drainResp.Events[0]

	failReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+firstDelivery.ID+"/fail", FailAgentInboxEventRequest{
		DeliveryID: firstDelivery.DeliveryID,
		LeaseToken: firstDelivery.LeaseToken,
		Error:      "transient failure",
	}, testWorkspaceID, "agent-inbox-stale-daemon")
	failReq = withURLParam(failReq, "eventId", firstDelivery.ID)
	failRec := httptest.NewRecorder()
	testHandler.FailAgentInboxEvent(failRec, failReq)
	if failRec.Code != http.StatusOK {
		t.Fatalf("fail first delivery: status=%d body=%s", failRec.Code, failRec.Body.String())
	}

	drainReq = newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "agent-inbox-stale-daemon")
	drainReq = withURLParam(drainReq, "runtimeId", runtimeID)
	drainRec = httptest.NewRecorder()
	testHandler.DrainAgentInboxByRuntime(drainRec, drainReq)
	if drainRec.Code != http.StatusOK {
		t.Fatalf("second drain inbox: status=%d body=%s", drainRec.Code, drainRec.Body.String())
	}
	if err := json.Unmarshal(drainRec.Body.Bytes(), &drainResp); err != nil {
		t.Fatalf("decode second drain response: %v", err)
	}
	if len(drainResp.Events) != 1 {
		t.Fatalf("second drain returned %d events, want 1: %s", len(drainResp.Events), drainRec.Body.String())
	}
	secondDelivery := drainResp.Events[0]
	if secondDelivery.ID != firstDelivery.ID || secondDelivery.DeliveryID == firstDelivery.DeliveryID {
		t.Fatalf("second drain = %+v, want same event with a new delivery after failure", secondDelivery)
	}

	staleAckReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+firstDelivery.ID+"/ack", AckAgentInboxEventRequest{
		DeliveryID:  firstDelivery.DeliveryID,
		LeaseToken:  firstDelivery.LeaseToken,
		SeenUpToSeq: firstDelivery.SeqTo,
	}, testWorkspaceID, "agent-inbox-stale-daemon")
	staleAckReq = withURLParam(staleAckReq, "eventId", firstDelivery.ID)
	staleAckRec := httptest.NewRecorder()
	testHandler.AckAgentInboxEvent(staleAckRec, staleAckReq)
	if staleAckRec.Code != http.StatusConflict {
		t.Fatalf("stale ack status=%d body=%s, want 409", staleAckRec.Code, staleAckRec.Body.String())
	}

	var eventStatus, oldDeliveryStatus, newDeliveryStatus string
	var lastDrainedSeq int64
	if err := testPool.QueryRow(ctx, `
		SELECT e.status, old_delivery.status, new_delivery.status, s.last_drained_seq
		FROM agent_inbox_event e
		JOIN agent_session s ON s.id = e.agent_session_id
		JOIN agent_event_delivery old_delivery ON old_delivery.id = $2
		JOIN agent_event_delivery new_delivery ON new_delivery.id = $3
		WHERE e.id = $1`, firstDelivery.ID, firstDelivery.DeliveryID, secondDelivery.DeliveryID).Scan(&eventStatus, &oldDeliveryStatus, &newDeliveryStatus, &lastDrainedSeq); err != nil {
		t.Fatalf("load stale ack states: %v", err)
	}
	if eventStatus != "draining" || oldDeliveryStatus != "failed" || newDeliveryStatus != "leased" || lastDrainedSeq != 0 {
		t.Fatalf("stale ack states = event:%q old:%q new:%q last_drained:%d, want draining/failed/leased/0", eventStatus, oldDeliveryStatus, newDeliveryStatus, lastDrainedSeq)
	}

	testHandler.Bus.Subscribe(protocol.EventChatDone, func(e events.Event) {
		payload, ok := e.Payload.(protocol.ChatDonePayload)
		if ok && payload.TaskID == firstDelivery.ID {
			testHandler.handleChannelChatDone(e)
		}
	})
	const staleReply = "stale complete must not write visible output"
	staleCompleteReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+firstDelivery.ID+"/complete", CompleteAgentInboxEventRequest{
		DeliveryID: firstDelivery.DeliveryID,
		LeaseToken: firstDelivery.LeaseToken,
		TaskCompleteRequest: TaskCompleteRequest{
			Output: staleReply,
		},
	}, testWorkspaceID, "agent-inbox-stale-daemon")
	staleCompleteReq = withURLParam(staleCompleteReq, "eventId", firstDelivery.ID)
	staleCompleteRec := httptest.NewRecorder()
	testHandler.CompleteAgentInboxEvent(staleCompleteRec, staleCompleteReq)
	if staleCompleteRec.Code != http.StatusConflict {
		t.Fatalf("stale complete status=%d body=%s, want 409", staleCompleteRec.Code, staleCompleteRec.Body.String())
	}
	assertChannelMessageContentCount(t, channelID, staleReply, 0)
	var staleChatMessages int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM chat_message
		WHERE chat_session_id = $1 AND role = 'assistant' AND content = $2
	`, secondDelivery.Task.ChatSessionID, staleReply).Scan(&staleChatMessages); err != nil {
		t.Fatalf("count stale complete chat messages: %v", err)
	}
	if staleChatMessages != 0 {
		t.Fatalf("stale complete chat messages = %d, want 0", staleChatMessages)
	}

	ackReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+secondDelivery.ID+"/ack", AckAgentInboxEventRequest{
		DeliveryID:  secondDelivery.DeliveryID,
		LeaseToken:  secondDelivery.LeaseToken,
		SeenUpToSeq: secondDelivery.SeqTo,
	}, testWorkspaceID, "agent-inbox-stale-daemon")
	ackReq = withURLParam(ackReq, "eventId", secondDelivery.ID)
	ackRec := httptest.NewRecorder()
	testHandler.AckAgentInboxEvent(ackRec, ackReq)
	if ackRec.Code != http.StatusOK {
		t.Fatalf("ack current delivery after stale rejection: status=%d body=%s", ackRec.Code, ackRec.Body.String())
	}
}

type recordingTaskWakeup struct {
	mu    sync.Mutex
	calls int
}

func (r *recordingTaskWakeup) NotifyTaskAvailable(_, _ string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
}

func (r *recordingTaskWakeup) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func TestChannelAmbientGateTxPathDoesNotWakeBeforeCommit(t *testing.T) {
	if testHandler == nil || testPool == nil || testHandler.TaskService == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	withChannelAmbientGateTestConfig(t)
	agentID := createHandlerTestAgent(t, "Ambient Tx No Wake Gate "+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "ambient-tx-no-wake-gate-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	agent, err := testHandler.Queries.GetAgent(ctx, parseUUID(agentID))
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "ordinary ambient update", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("ambient-tx-no-wake"), 0)
	if err != nil {
		t.Fatalf("insert ambient trigger: %v", err)
	}

	recorder := &recordingTaskWakeup{}
	oldWakeup := testHandler.TaskService.Wakeup
	testHandler.TaskService.Wakeup = recorder
	t.Cleanup(func() {
		testHandler.TaskService.Wakeup = oldWakeup
	})

	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback(ctx)
	task, ok := testHandler.createChannelAmbientPromptTaskTx(ctx, tx, ch, agent, trigger, parseUUID(testUserID))
	if !ok {
		t.Fatal("create ambient prompt task in tx failed")
	}
	if got := recorder.Count(); got != 0 {
		t.Fatalf("ambient tx helper woke daemon before commit: got %d wakeups, want 0", got)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit tx: %v", err)
	}
	if got := recorder.Count(); got != 0 {
		t.Fatalf("ambient tx helper woke daemon during commit: got %d wakeups, want 0", got)
	}
	testHandler.TaskService.PublishChatTaskQueued(ctx, task, false)
	if got := recorder.Count(); got != 1 {
		t.Fatalf("post-commit publish wakeups = %d, want 1", got)
	}
}

func TestChannelAmbientGateFailsClosedOnStatsError(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	withChannelAmbientGateTestConfig(t)
	agentID := createHandlerTestAgent(t, "Ambient Fail Closed Gate "+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "ambient-fail-closed-gate-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	agent, err := testHandler.Queries.GetAgent(ctx, parseUUID(agentID))
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}
	trigger := ChannelMessageResponse{
		ID:        uuid.NewString(),
		Content:   "ordinary ambient update",
		Type:      "user",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	cancelledCtx, cancel := context.WithCancel(ctx)
	cancel()
	if testHandler.shouldDispatchChannelAmbientObservation(cancelledCtx, ch, trigger, agent) {
		t.Fatal("ambient gate allowed dispatch after stats query error; want fail-closed")
	}
}

func TestChannelAmbientGateRecordsObviousNoiseAsAmbientInboxOnly(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	withChannelAmbientGateTestConfig(t)
	agentID := createHandlerTestAgent(t, "Ambient Noise Gate "+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "ambient-noise-gate-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "!!!", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert noise trigger: %v", err)
	}

	testHandler.dispatchChannelMessageToAgents(ctx, ch, trigger, parseUUID(testUserID))

	var sessions, events int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_session
		WHERE channel_id = $1`, channelID).Scan(&sessions); err != nil {
		t.Fatalf("count agent sessions: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_inbox_event
		WHERE channel_id = $1`, channelID).Scan(&events); err != nil {
		t.Fatalf("count inbox events: %v", err)
	}
	if sessions != 1 || events != 1 {
		t.Fatalf("noise ambient created %d sessions and %d inbox events, want one ambient-only delivery", sessions, events)
	}
	assertChannelAgentInboxEventCounts(t, channelID, agentID, 1, 0)
}

func TestChannelAmbientGateDoesNotBlockDirectMention(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	withChannelAmbientGateTestConfig(t)
	agentName := "Ambient Direct Gate " + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentName, nil)
	channelID := seedChannelForTest(t, "ambient-direct-gate-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	ambient, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "ordinary ambient work", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert ambient trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, ambient, parseUUID(testUserID))

	direct, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "@"+agentName+" please answer", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert direct trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, direct, parseUUID(testUserID))

	assertChannelAgentInboxEventCounts(t, channelID, agentID, 1, 1)
}

func TestChannelAmbientGreetingPromptUsesReactionOnlyForSingleAgentChannel(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Ambient Greeting Single "+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "ambient-greeting-single-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	agent, err := testHandler.Queries.GetAgent(ctx, parseUUID(agentID))
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "hi", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert greeting trigger: %v", err)
	}

	prompt := testHandler.buildChannelAmbientUnreadPromptWithDB(ctx, testHandler.DB, ch, agent, trigger, 0, trigger.Seq)
	for _, want := range []string{
		"respond with a 👋 reaction to the reaction target only and do not create a text reply",
		"This also applies when you are the only agent in the channel",
		"Reaction target message id: " + trigger.ID,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("single-agent greeting prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestChannelAmbientGreetingPromptUsesReactionOnlyForLargerChannel(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Ambient Greeting Larger "+uuid.NewString()[:8], nil)
	otherAgentID := createHandlerTestAgent(t, "Ambient Greeting Larger Peer "+uuid.NewString()[:8], nil)
	otherUserID := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "ambient-greeting-larger-"+uuid.NewString(), testUserID, otherUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3), ($1, $2, 'agent', $4)`, channelID, testWorkspaceID, agentID, otherAgentID); err != nil {
		t.Fatalf("seed agent members: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	agent, err := testHandler.Queries.GetAgent(ctx, parseUUID(agentID))
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "hello", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert greeting trigger: %v", err)
	}

	prompt := testHandler.buildChannelAmbientUnreadPromptWithDB(ctx, testHandler.DB, ch, agent, trigger, 0, trigger.Seq)
	for _, want := range []string{
		"casual greeting or small talk",
		"respond with a 👋 reaction to the reaction target only",
		"do not create a text reply",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("larger-channel greeting prompt missing %q:\n%s", want, prompt)
		}
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
	var page ChannelMessagesPageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode listed messages: %v", err)
	}
	for _, msg := range page.Messages {
		if msg.ID == created.ID {
			if msg.ReplyTo == nil || msg.ReplyTo.ID != root.ID {
				t.Fatalf("listed reply summary = %+v, want root", msg.ReplyTo)
			}
			return
		}
	}
	t.Fatalf("created message %s missing from list", created.ID)
}

func TestSendChannelMessageQuoteCapturesSnapshotAndHidesDeletedSource(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	channelID := seedChannelForTest(t, "quote-snapshot-"+uuid.NewString(), testUserID)
	source, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "quoted before edit", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("quote-source"), 0)
	if err != nil {
		t.Fatalf("insert source message: %v", err)
	}

	req := newRequest(http.MethodPost, "/api/channels/"+channelID+"/messages", map[string]any{
		"content":        "new message",
		"quoteMessageId": source.ID,
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.SendChannelMessage(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("send quote: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created ChannelMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created message: %v", err)
	}
	if created.QuoteMessageID == nil || *created.QuoteMessageID != source.ID {
		t.Fatalf("created quote_message_id = %v, want %s", created.QuoteMessageID, source.ID)
	}
	if created.Quote == nil || created.Quote.Status != "active" || created.Quote.Snapshot == nil || created.Quote.Snapshot.Content != "quoted before edit" {
		t.Fatalf("created quote = %+v, want active snapshot", created.Quote)
	}

	if _, err := testPool.Exec(ctx, `UPDATE channel_message SET content = 'edited source', edited_at = now() WHERE id = $1`, source.ID); err != nil {
		t.Fatalf("edit source: %v", err)
	}
	req = newRequest(http.MethodGet, "/api/channels/"+channelID+"/messages", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec = httptest.NewRecorder()
	testHandler.ListChannelMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list messages after edit: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var page ChannelMessagesPageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode listed messages: %v", err)
	}
	listed := findChannelMessageForTest(page.Messages, created.ID)
	if listed == nil || listed.Quote == nil || listed.Quote.Snapshot == nil || listed.Quote.Snapshot.Content != "quoted before edit" {
		t.Fatalf("listed quote after edit = %+v, want original snapshot", listed)
	}

	if _, err := testPool.Exec(ctx, `UPDATE channel_message SET content = '', parts = '[]'::jsonb, deleted_at = now() WHERE id = $1`, source.ID); err != nil {
		t.Fatalf("delete source: %v", err)
	}
	req = newRequest(http.MethodGet, "/api/channels/"+channelID+"/messages", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec = httptest.NewRecorder()
	testHandler.ListChannelMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list messages after delete: status=%d body=%s", rec.Code, rec.Body.String())
	}
	page = ChannelMessagesPageResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode listed messages after delete: %v", err)
	}
	listed = findChannelMessageForTest(page.Messages, created.ID)
	if listed == nil || listed.Quote == nil || listed.Quote.Status != "deleted" || listed.Quote.Snapshot != nil {
		t.Fatalf("listed quote after delete = %+v, want deleted without snapshot", listed)
	}
}

func TestSendChannelMessageQuoteRejectsCrossChannelTarget(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	channelID := seedChannelForTest(t, "quote-cross-channel-"+uuid.NewString(), testUserID)
	otherChannelID := seedChannelForTest(t, "quote-cross-channel-other-"+uuid.NewString(), testUserID)
	other, err := testHandler.insertChannelMessage(ctx, parseUUID(otherChannelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "other channel", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("quote-other"), 0)
	if err != nil {
		t.Fatalf("insert other message: %v", err)
	}

	req := newRequest(http.MethodPost, "/api/channels/"+channelID+"/messages", map[string]any{
		"content":        "new message",
		"quoteMessageId": other.ID,
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.SendChannelMessage(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("send cross-channel quote status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
}

func findChannelMessageForTest(messages []ChannelMessageResponse, id string) *ChannelMessageResponse {
	for i := range messages {
		if messages[i].ID == id {
			return &messages[i]
		}
	}
	return nil
}

func TestSendChannelMessagePublishesOnlyChannelMemberRecipients(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	memberID := createChannelPlainMember(t)
	bystanderID := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "recipient-scope-"+uuid.NewString(), testUserID, memberID)
	content := "recipient scoped secret " + uuid.NewString()
	eventsSeen := make(chan events.Event, 1)
	testHandler.Bus.Subscribe(protocol.EventChannelMessage, func(e events.Event) {
		msg, ok := e.Payload.(ChannelMessageResponse)
		if ok && msg.Content == content {
			eventsSeen <- e
		}
	})

	req := newRequest(http.MethodPost, "/api/channels/"+channelID+"/messages", map[string]any{
		"content": content,
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.SendChannelMessage(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("send message: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var ev events.Event
	select {
	case ev = <-eventsSeen:
	default:
		t.Fatal("expected channel message event")
	}
	got := map[string]bool{}
	for _, recipientID := range ev.RecipientUserIDs {
		got[recipientID] = true
	}
	for _, want := range []string{testUserID, memberID} {
		if !got[want] {
			t.Fatalf("missing recipient %s in %+v", want, ev.RecipientUserIDs)
		}
	}
	if got[bystanderID] {
		t.Fatalf("workspace bystander %s received channel-scoped event recipients %+v", bystanderID, ev.RecipientUserIDs)
	}
}

func TestSendChannelMessageClientMessageIDDedupesTopLevelWithSideEffects(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Idempotency Group Bot", nil)
	channelID := seedChannelForTest(t, "client-id-group-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}

	content := "@Idempotency Group Bot please handle " + uuid.NewString()
	clientID := "client-" + uuid.NewString()
	eventsSeen := make(chan events.Event, 4)
	testHandler.Bus.Subscribe(protocol.EventChannelMessage, func(e events.Event) {
		msg, ok := e.Payload.(ChannelMessageResponse)
		if ok && msg.Content == content {
			eventsSeen <- e
		}
	})

	first := sendChannelMessageForTest(t, channelID, testUserID, map[string]any{
		"content":           content,
		"client_message_id": clientID,
	})
	if first.Code != http.StatusCreated {
		t.Fatalf("first send: status=%d body=%s", first.Code, first.Body.String())
	}
	var created ChannelMessageResponse
	if err := json.Unmarshal(first.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.ClientMessageID == nil || *created.ClientMessageID != clientID {
		t.Fatalf("client_message_id = %v, want %s", created.ClientMessageID, clientID)
	}

	second := sendChannelMessageForTest(t, channelID, testUserID, map[string]any{
		"content":           content,
		"client_message_id": clientID,
	})
	if second.Code != http.StatusOK {
		t.Fatalf("duplicate send: status=%d body=%s", second.Code, second.Body.String())
	}
	var duplicate ChannelMessageResponse
	if err := json.Unmarshal(second.Body.Bytes(), &duplicate); err != nil {
		t.Fatalf("decode duplicate: %v", err)
	}
	if duplicate.ID != created.ID || duplicate.Seq != created.Seq {
		t.Fatalf("duplicate response = %s/%d, want existing %s/%d", duplicate.ID, duplicate.Seq, created.ID, created.Seq)
	}

	var rows int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1 AND client_message_id = $2`, channelID, clientID).Scan(&rows); err != nil {
		t.Fatalf("count channel messages: %v", err)
	}
	if rows != 1 {
		t.Fatalf("channel_message rows = %d, want 1", rows)
	}
	if got := len(eventsSeen); got != 1 {
		t.Fatalf("published channel message events = %d, want 1", got)
	}
	var wakeEvents int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_inbox_event
		WHERE channel_id = $1 AND agent_id = $2 AND requires_wake`, channelID, agentID).Scan(&wakeEvents); err != nil {
		t.Fatalf("count dispatched inbox events: %v", err)
	}
	if wakeEvents != 1 {
		t.Fatalf("agent dispatch inbox events = %d, want 1", wakeEvents)
	}
}

func TestSendChannelMessageClientMessageIDConflictOnChangedPayload(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	channelID := seedChannelForTest(t, "client-id-conflict-"+uuid.NewString(), testUserID)
	replyTarget, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "reply target", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("reply-target-thread"), 0)
	if err != nil {
		t.Fatalf("insert reply target: %v", err)
	}
	clientID := "client-" + uuid.NewString()
	base := sendChannelMessageForTest(t, channelID, testUserID, map[string]any{
		"content":           "stable payload",
		"client_message_id": clientID,
	})
	if base.Code != http.StatusCreated {
		t.Fatalf("base send: status=%d body=%s", base.Code, base.Body.String())
	}

	cases := []struct {
		name string
		body map[string]any
	}{
		{
			name: "content",
			body: map[string]any{"content": "changed payload", "client_message_id": clientID},
		},
		{
			name: "parts",
			body: map[string]any{
				"content":           "stable payload",
				"parts":             []protocol.MessagePart{{Type: protocol.MessagePartTypeText, Text: "client side structured copy"}},
				"client_message_id": clientID,
			},
		},
		{
			name: "reply target",
			body: map[string]any{
				"content":             "stable payload",
				"reply_to_message_id": replyTarget.ID,
				"client_message_id":   clientID,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := sendChannelMessageForTest(t, channelID, testUserID, tc.body)
			if rec.Code != http.StatusConflict {
				t.Fatalf("conflicting %s send: status=%d body=%s", tc.name, rec.Code, rec.Body.String())
			}
		})
	}

	var rows int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1 AND client_message_id = $2`, channelID, clientID).Scan(&rows); err != nil {
		t.Fatalf("count channel messages: %v", err)
	}
	if rows != 1 {
		t.Fatalf("channel_message rows after conflicts = %d, want 1", rows)
	}
}

func TestSendChannelMessageClientMessageIDDedupesDMDispatch(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	cleanupDMArtifacts(t)
	agentID := createHandlerTestAgent(t, "DM Idempotency Bot", nil)
	channelID := seedAgentDMChannel(t, agentID)
	content := "dm retry " + uuid.NewString()
	clientID := "client-" + uuid.NewString()

	first := sendChannelMessageForTest(t, channelID, testUserID, map[string]any{
		"content":           content,
		"client_message_id": clientID,
	})
	if first.Code != http.StatusCreated {
		t.Fatalf("first DM send: status=%d body=%s", first.Code, first.Body.String())
	}
	second := sendChannelMessageForTest(t, channelID, testUserID, map[string]any{
		"content":           content,
		"client_message_id": clientID,
	})
	if second.Code != http.StatusOK {
		t.Fatalf("duplicate DM send: status=%d body=%s", second.Code, second.Body.String())
	}

	var rows int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1 AND client_message_id = $2`, channelID, clientID).Scan(&rows); err != nil {
		t.Fatalf("count DM channel messages: %v", err)
	}
	if rows != 1 {
		t.Fatalf("DM channel_message rows = %d, want 1", rows)
	}
	var wakeEvents int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_inbox_event
		WHERE channel_id = $1 AND agent_id = $2 AND requires_wake`, channelID, agentID).Scan(&wakeEvents); err != nil {
		t.Fatalf("count DM dispatched inbox events: %v", err)
	}
	if wakeEvents != 1 {
		t.Fatalf("DM agent dispatch inbox events = %d, want 1", wakeEvents)
	}
}

func TestSendChannelMessageThreadReplyClientMessageIDDedupes(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	channelID := seedChannelForTest(t, "client-id-thread-"+uuid.NewString(), testUserID)
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Root Author", "thread root", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("root-thread-"+uuid.NewString()), 0)
	if err != nil {
		t.Fatalf("insert root: %v", err)
	}
	content := "thread retry " + uuid.NewString()
	clientID := "client-" + uuid.NewString()

	first := sendChannelThreadReplyForTest(t, channelID, root.ID, testUserID, map[string]any{
		"content":           content,
		"client_message_id": clientID,
	})
	if first.Code != http.StatusCreated {
		t.Fatalf("first thread reply: status=%d body=%s", first.Code, first.Body.String())
	}
	var created ChannelMessageResponse
	if err := json.Unmarshal(first.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode thread reply: %v", err)
	}
	second := sendChannelThreadReplyForTest(t, channelID, root.ID, testUserID, map[string]any{
		"content":           content,
		"client_message_id": clientID,
	})
	if second.Code != http.StatusOK {
		t.Fatalf("duplicate thread reply: status=%d body=%s", second.Code, second.Body.String())
	}
	var duplicate ChannelMessageResponse
	if err := json.Unmarshal(second.Body.Bytes(), &duplicate); err != nil {
		t.Fatalf("decode duplicate thread reply: %v", err)
	}
	if duplicate.ID != created.ID {
		t.Fatalf("duplicate thread reply id = %s, want %s", duplicate.ID, created.ID)
	}

	var replies int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1 AND thread_root_message_id = $2 AND client_message_id = $3`, channelID, root.ID, clientID).Scan(&replies); err != nil {
		t.Fatalf("count thread replies: %v", err)
	}
	if replies != 1 {
		t.Fatalf("thread replies = %d, want 1", replies)
	}
	rootTimeline := listedMessagesForUser(t, channelID, testUserID)
	if len(rootTimeline) != 1 || rootTimeline[0].ThreadReplyCount != 1 {
		t.Fatalf("root timeline = %+v, want exactly one thread reply counted", rootTimeline)
	}
}

func TestSendChannelMessageThreadReplyShowInChannelDefaultAndFalseStayThreadOnly(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	channelID := seedChannelForTest(t, "thread-show-in-channel-default-"+uuid.NewString(), testUserID)
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Root Author", "thread root", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("thread-default-"+uuid.NewString()), 0)
	if err != nil {
		t.Fatalf("insert root: %v", err)
	}

	defaultSend := sendChannelThreadReplyForTest(t, channelID, root.ID, testUserID, map[string]any{
		"content": "default thread-only reply",
	})
	if defaultSend.Code != http.StatusCreated {
		t.Fatalf("default thread reply: status=%d body=%s", defaultSend.Code, defaultSend.Body.String())
	}
	falseSend := sendChannelThreadReplyForTest(t, channelID, root.ID, testUserID, map[string]any{
		"content":         "explicit false thread-only reply",
		"show_in_channel": false,
	})
	if falseSend.Code != http.StatusCreated {
		t.Fatalf("explicit false thread reply: status=%d body=%s", falseSend.Code, falseSend.Body.String())
	}

	mainTimeline := listedMessagesForUser(t, channelID, testUserID)
	if len(mainTimeline) != 1 || mainTimeline[0].ID != root.ID {
		t.Fatalf("main timeline = %+v, want only root", mainTimeline)
	}
	if mainTimeline[0].ThreadReplyCount != 2 {
		t.Fatalf("thread reply count = %d, want 2", mainTimeline[0].ThreadReplyCount)
	}
}

func TestSendChannelMessageThreadReplyShowInChannelProjectsSameMessageWithoutWakePollution(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Thread Projection Agent", nil)
	channelID := seedChannelForTest(t, "thread-show-in-channel-true-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Root Author", "thread root", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("thread-projection-"+uuid.NewString()), 0)
	if err != nil {
		t.Fatalf("insert root: %v", err)
	}
	content := "please [@Thread Projection Agent](mention://agent/" + agentID + ") review from the thread"
	clientID := "client-" + uuid.NewString()

	first := sendChannelThreadReplyForTest(t, channelID, root.ID, testUserID, map[string]any{
		"content":           content,
		"client_message_id": clientID,
		"show_in_channel":   true,
	})
	if first.Code != http.StatusCreated {
		t.Fatalf("first show-in-channel thread reply: status=%d body=%s", first.Code, first.Body.String())
	}
	var reply ChannelMessageResponse
	if err := json.Unmarshal(first.Body.Bytes(), &reply); err != nil {
		t.Fatalf("decode thread reply: %v", err)
	}
	if reply.ThreadRootMessageID == nil || *reply.ThreadRootMessageID != root.ID {
		t.Fatalf("thread reply root = %v, want %s", reply.ThreadRootMessageID, root.ID)
	}
	if reply.ThreadRoot == nil || reply.ThreadRoot.ID != root.ID || reply.ThreadRoot.Content != root.Content {
		t.Fatalf("thread root summary = %+v, want root %s", reply.ThreadRoot, root.ID)
	}

	second := sendChannelThreadReplyForTest(t, channelID, root.ID, testUserID, map[string]any{
		"content":           content,
		"client_message_id": clientID,
		"show_in_channel":   true,
	})
	if second.Code != http.StatusOK {
		t.Fatalf("duplicate show-in-channel thread reply: status=%d body=%s", second.Code, second.Body.String())
	}
	var duplicate ChannelMessageResponse
	if err := json.Unmarshal(second.Body.Bytes(), &duplicate); err != nil {
		t.Fatalf("decode duplicate thread reply: %v", err)
	}
	if duplicate.ID != reply.ID {
		t.Fatalf("duplicate reply id = %s, want %s", duplicate.ID, reply.ID)
	}

	mainTimeline := listedMessagesForUser(t, channelID, testUserID)
	if len(mainTimeline) != 2 {
		t.Fatalf("main timeline = %+v, want root + projected reply", mainTimeline)
	}
	projected := mainTimeline[1]
	if projected.ID != reply.ID {
		t.Fatalf("projected message id = %s, want thread reply %s", projected.ID, reply.ID)
	}
	if projected.Content != content {
		t.Fatalf("projected content = %q, want %q", projected.Content, content)
	}
	if projected.ThreadRootMessageID == nil || *projected.ThreadRootMessageID != root.ID {
		t.Fatalf("projected thread_root_message_id = %v, want %s", projected.ThreadRootMessageID, root.ID)
	}
	if projected.ThreadRoot == nil || projected.ThreadRoot.ID != root.ID || projected.ThreadRoot.Content != root.Content {
		t.Fatalf("projected thread root summary = %+v, want root %s", projected.ThreadRoot, root.ID)
	}

	var threadReplies, projectedRows, carriers, wakeEvents int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1 AND thread_root_message_id = $2`, channelID, root.ID).Scan(&threadReplies); err != nil {
		t.Fatalf("count thread replies: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1
		  AND thread_root_message_id = $2
		  AND main_timeline_visible`, channelID, root.ID).Scan(&projectedRows); err != nil {
		t.Fatalf("count projected thread replies: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1
		  AND thread_root_message_id IS NULL
		  AND reply_to_message_id = $2`, channelID, reply.ID).Scan(&carriers); err != nil {
		t.Fatalf("count legacy thread carriers: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_inbox_event
		WHERE channel_id = $1 AND agent_id = $2 AND requires_wake`, channelID, agentID).Scan(&wakeEvents); err != nil {
		t.Fatalf("count dispatched inbox events: %v", err)
	}
	if threadReplies != 1 || projectedRows != 1 || carriers != 0 || wakeEvents != 1 {
		t.Fatalf("side effects = threadReplies:%d projectedRows:%d carriers:%d wakeEvents:%d, want 1/1/0/1", threadReplies, projectedRows, carriers, wakeEvents)
	}
}

func TestSendChannelMessageThreadReplyShowInChannelUnsupportedOrInvalidFailsClosed(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "DM Thread Projection Agent", nil)
	dmChannelID := seedAgentDMChannel(t, agentID)
	dmRoot, err := testHandler.insertChannelMessage(ctx, parseUUID(dmChannelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "dm root", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("dm-thread-projection-"+uuid.NewString()), 0)
	if err != nil {
		t.Fatalf("insert dm root: %v", err)
	}
	unsupported := sendChannelThreadReplyForTest(t, dmChannelID, dmRoot.ID, testUserID, map[string]any{
		"content":         "dm should fail closed",
		"show_in_channel": true,
	})
	if unsupported.Code != http.StatusBadRequest {
		t.Fatalf("unsupported DM show_in_channel: status=%d body=%s", unsupported.Code, unsupported.Body.String())
	}
	assertNoThreadRepliesForRoot(t, dmChannelID, dmRoot.ID)

	groupChannelID := seedChannelForTest(t, "thread-show-in-channel-invalid-"+uuid.NewString(), testUserID)
	groupRoot, err := testHandler.insertChannelMessage(ctx, parseUUID(groupChannelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "group root", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("invalid-thread-projection-"+uuid.NewString()), 0)
	if err != nil {
		t.Fatalf("insert group root: %v", err)
	}
	invalid := sendChannelThreadReplyForTest(t, groupChannelID, groupRoot.ID, testUserID, map[string]any{
		"content":         "invalid bool should fail closed",
		"show_in_channel": "true",
	})
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid show_in_channel: status=%d body=%s", invalid.Code, invalid.Body.String())
	}
	assertNoThreadRepliesForRoot(t, groupChannelID, groupRoot.ID)
}

func TestSendChannelMessageClientMessageIDDedupesAttachmentsAndParts(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	channelID := seedChannelForTest(t, "client-id-attachments-"+uuid.NewString(), testUserID)
	var attachmentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO attachment (workspace_id, channel_id, uploader_type, uploader_id, filename, url, content_type, size_bytes)
		VALUES ($1, $2, 'member', $3, 'retry.txt', 's3://retry.txt', 'text/plain', 7)
		RETURNING id`, testWorkspaceID, channelID, testUserID).Scan(&attachmentID); err != nil {
		t.Fatalf("seed attachment: %v", err)
	}
	content := "with parts " + uuid.NewString()
	clientID := "client-" + uuid.NewString()
	body := map[string]any{
		"content": content,
		"parts": []protocol.MessagePart{
			{Type: protocol.MessagePartTypeText, Text: content},
			{Type: protocol.MessagePartTypeSticker, StickerID: "hi"},
		},
		"attachment_ids":    []string{attachmentID},
		"client_message_id": clientID,
	}
	first := sendChannelMessageForTest(t, channelID, testUserID, body)
	if first.Code != http.StatusCreated {
		t.Fatalf("first send with attachment: status=%d body=%s", first.Code, first.Body.String())
	}
	second := sendChannelMessageForTest(t, channelID, testUserID, body)
	if second.Code != http.StatusOK {
		t.Fatalf("duplicate send with attachment: status=%d body=%s", second.Code, second.Body.String())
	}
	var duplicate ChannelMessageResponse
	if err := json.Unmarshal(second.Body.Bytes(), &duplicate); err != nil {
		t.Fatalf("decode duplicate response: %v", err)
	}
	if len(duplicate.Attachments) != 1 || duplicate.Attachments[0].ID != attachmentID {
		t.Fatalf("duplicate attachments = %+v, want seeded attachment", duplicate.Attachments)
	}
	if len(duplicate.Parts) != 2 || duplicate.Parts[0].Type != protocol.MessagePartTypeText || duplicate.Parts[1].Type != protocol.MessagePartTypeSticker || duplicate.Parts[1].PackID != "builtin" || duplicate.Parts[1].StickerID != "hi" {
		t.Fatalf("duplicate parts = %+v, want text plus normalized builtin sticker", duplicate.Parts)
	}

	var bound int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM attachment
		WHERE id = $1 AND channel_message_id = $2`, attachmentID, duplicate.ID).Scan(&bound); err != nil {
		t.Fatalf("count attachment bindings: %v", err)
	}
	if bound != 1 {
		t.Fatalf("attachment binding rows = %d, want 1", bound)
	}
	var messages int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1 AND client_message_id = $2`, channelID, clientID).Scan(&messages); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if messages != 1 {
		t.Fatalf("channel_message rows = %d, want 1", messages)
	}
}

func TestSendChannelMessageClientMessageIDConcurrentDuplicates(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	channelID := seedChannelForTest(t, "client-id-concurrent-"+uuid.NewString(), testUserID)
	content := "concurrent retry " + uuid.NewString()
	clientID := "client-" + uuid.NewString()
	const attempts = 6
	var wg sync.WaitGroup
	statuses := make(chan int, attempts)
	ids := make(chan string, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := sendChannelMessageForTest(t, channelID, testUserID, map[string]any{
				"content":           content,
				"client_message_id": clientID,
			})
			statuses <- rec.Code
			var msg ChannelMessageResponse
			if rec.Code == http.StatusCreated || rec.Code == http.StatusOK {
				if err := json.Unmarshal(rec.Body.Bytes(), &msg); err == nil {
					ids <- msg.ID
				}
			}
		}()
	}
	wg.Wait()
	close(statuses)
	close(ids)

	created := 0
	ok := 0
	for status := range statuses {
		switch status {
		case http.StatusCreated:
			created++
		case http.StatusOK:
			ok++
		default:
			t.Fatalf("unexpected status from concurrent duplicate send: %d", status)
		}
	}
	if created != 1 || ok != attempts-1 {
		t.Fatalf("statuses created=%d ok=%d, want 1/%d", created, ok, attempts-1)
	}
	seenIDs := map[string]struct{}{}
	for id := range ids {
		seenIDs[id] = struct{}{}
	}
	if len(seenIDs) != 1 {
		t.Fatalf("response ids = %+v, want one shared message id", seenIDs)
	}

	var rows int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1 AND client_message_id = $2`, channelID, clientID).Scan(&rows); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if rows != 1 {
		t.Fatalf("channel_message rows = %d, want 1", rows)
	}
}

func TestSendChannelMessageStoresStickerParts(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	channelID := seedChannelForTest(t, "sticker-parts-"+uuid.NewString(), testUserID)
	req := newRequest(http.MethodPost, "/api/channels/"+channelID+"/messages", map[string]any{
		"content": "",
		"parts": []protocol.MessagePart{{
			Type:      protocol.MessagePartTypeSticker,
			StickerID: "hi",
		}},
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.SendChannelMessage(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("send sticker parts: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var created ChannelMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created message: %v", err)
	}
	if len(created.Parts) != 1 || created.Parts[0].Type != protocol.MessagePartTypeSticker || created.Parts[0].PackID != "builtin" || created.Parts[0].StickerID != "hi" || created.Parts[0].Alt == "" {
		t.Fatalf("created parts = %+v, want normalized builtin sticker", created.Parts)
	}
	if created.Content != created.Parts[0].Alt {
		t.Fatalf("created content = %q, want sticker alt fallback %q", created.Content, created.Parts[0].Alt)
	}

	var storedParts []byte
	if err := testPool.QueryRow(context.Background(), `SELECT parts FROM channel_message WHERE id = $1`, created.ID).Scan(&storedParts); err != nil {
		t.Fatalf("load stored parts: %v", err)
	}
	var decoded []protocol.MessagePart
	if err := json.Unmarshal(storedParts, &decoded); err != nil {
		t.Fatalf("decode stored parts: %v", err)
	}
	if len(decoded) != 1 || decoded[0].StickerID != "hi" || decoded[0].PackID != "builtin" {
		t.Fatalf("stored parts = %+v, want builtin hi sticker", decoded)
	}
}

func TestSendChannelMessageRejectsUnknownStickerPart(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	channelID := seedChannelForTest(t, "bad-sticker-parts-"+uuid.NewString(), testUserID)
	req := newRequest(http.MethodPost, "/api/channels/"+channelID+"/messages", map[string]any{
		"parts": []protocol.MessagePart{{
			Type:      protocol.MessagePartTypeSticker,
			StickerID: "does-not-exist",
		}},
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.SendChannelMessage(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("send bad sticker parts: status=%d body=%s", rec.Code, rec.Body.String())
	}
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
	var page ChannelMessagesPageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode messages after add: %v", err)
	}
	messages := page.Messages
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
	page = ChannelMessagesPageResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode messages after remove: %v", err)
	}
	messages = page.Messages
	if len(messages) != 1 || len(messages[0].Reactions) != 0 {
		t.Fatalf("expected no reactions after remove, got %#v", messages)
	}
}

func TestChannelMessageEditDeleteOwnOnlyKeepsTombstoneNoWake(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	otherUserID := createWorkspaceMemberUser(t, "Message Editor Other", "message-editor-other-"+randomID()+"@multica.test")
	agentID := createHandlerTestAgent(t, "Message Edit Delete Agent "+randomID(), nil)
	channelID := seedChannelForTest(t, "message-edit-delete-"+uuid.NewString(), testUserID, otherUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
		ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed channel agent member: %v", err)
	}
	msg, err := testHandler.insertChannelMessageWithParts(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "original", []protocol.MessagePart{
		{Type: protocol.MessagePartTypeText, Text: "original"},
	}, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("edit-delete-thread"), 0)
	if err != nil {
		t.Fatalf("insert channel message: %v", err)
	}
	var attachmentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO attachment (workspace_id, channel_id, channel_message_id, uploader_type, uploader_id, filename, url, content_type, size_bytes)
		VALUES ($1, $2, $3, 'member', $4, 'secret.png', 's3://secret.png', 'image/png', 12)
		RETURNING id`, testWorkspaceID, channelID, msg.ID, testUserID).Scan(&attachmentID); err != nil {
		t.Fatalf("seed channel message attachment: %v", err)
	}
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(otherUserID), "Other", "thread reply", "multica", nil, pgtype.UUID{}, parseUUID(msg.ID), msg.ThreadID, 0); err != nil {
		t.Fatalf("insert thread reply: %v", err)
	}
	eventsSeen := make(chan events.Event, 4)
	testHandler.Bus.Subscribe(protocol.EventChannelMessage, func(e events.Event) {
		payload, ok := e.Payload.(ChannelMessageResponse)
		if ok && payload.ID == msg.ID {
			eventsSeen <- e
		}
	})

	sessionsBefore, tasksBefore := channelAgentRunCountsForTest(t, channelID)

	req := newRequestAs(otherUserID, http.MethodPatch, "/api/channels/"+channelID+"/messages/"+msg.ID, map[string]any{"content": "stolen"})
	req = withChannelTestWorkspaceCtx(t, req, otherUserID)
	req = withRouteParams(req, "channelId", channelID, "messageId", msg.ID)
	rec := httptest.NewRecorder()
	testHandler.UpdateChannelMessage(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("non-owner edit: status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = newRequest(http.MethodPatch, "/api/channels/"+channelID+"/messages/"+msg.ID, map[string]any{
		"content": "corrected",
		"parts": []protocol.MessagePart{{
			Type: protocol.MessagePartTypeText,
			Text: "corrected",
		}},
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withRouteParams(req, "channelId", channelID, "messageId", msg.ID)
	rec = httptest.NewRecorder()
	testHandler.UpdateChannelMessage(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner edit: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var edited ChannelMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &edited); err != nil {
		t.Fatalf("decode edited message: %v", err)
	}
	if edited.Content != "corrected" || edited.EditedAt == nil || edited.DeletedAt != nil {
		t.Fatalf("edited message = %#v, want corrected with edited_at only", edited)
	}
	var editEvent events.Event
	select {
	case editEvent = <-eventsSeen:
	default:
		t.Fatal("expected edit channel:message event")
	}
	editPayload, ok := editEvent.Payload.(ChannelMessageResponse)
	if !ok || editPayload.ID != msg.ID || editPayload.Content != "corrected" || editPayload.EditedAt == nil || editPayload.DeletedAt != nil {
		t.Fatalf("edit event payload = %#v", editEvent.Payload)
	}

	req = newRequestAs(otherUserID, http.MethodDelete, "/api/channels/"+channelID+"/messages/"+msg.ID, nil)
	req = withChannelTestWorkspaceCtx(t, req, otherUserID)
	req = withRouteParams(req, "channelId", channelID, "messageId", msg.ID)
	rec = httptest.NewRecorder()
	testHandler.DeleteChannelMessage(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("non-owner delete: status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = newRequest(http.MethodPost, "/api/channels/"+channelID+"/messages/"+msg.ID+"/reactions", map[string]string{"emoji": "👍"})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withRouteParams(req, "channelId", channelID, "messageId", msg.ID)
	rec = httptest.NewRecorder()
	testHandler.AddChannelMessageReaction(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add reaction before delete: status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = newRequest(http.MethodDelete, "/api/channels/"+channelID+"/messages/"+msg.ID, nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withRouteParams(req, "channelId", channelID, "messageId", msg.ID)
	rec = httptest.NewRecorder()
	testHandler.DeleteChannelMessage(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("owner delete: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var deleteEvent events.Event
	select {
	case deleteEvent = <-eventsSeen:
	default:
		t.Fatal("expected delete channel:message event")
	}
	deletePayload, ok := deleteEvent.Payload.(ChannelMessageResponse)
	if !ok || deletePayload.ID != msg.ID || deletePayload.Content != "" || len(deletePayload.Parts) != 0 || deletePayload.DeletedAt == nil {
		t.Fatalf("delete event payload = %#v", deleteEvent.Payload)
	}
	if len(deletePayload.Attachments) != 0 || len(deletePayload.Reactions) != 0 || deletePayload.ReplyTo != nil || deletePayload.ThreadRoot != nil {
		t.Fatalf("delete event leaked satellites: %#v", deletePayload)
	}
	if deletePayload.ThreadReplyCount != 1 {
		t.Fatalf("delete event thread_reply_count = %d, want 1", deletePayload.ThreadReplyCount)
	}

	req = newRequest(http.MethodPost, "/api/channels/"+channelID+"/messages/"+msg.ID+"/reactions", map[string]string{"emoji": "👍"})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withRouteParams(req, "channelId", channelID, "messageId", msg.ID)
	rec = httptest.NewRecorder()
	testHandler.AddChannelMessageReaction(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("add reaction after delete: status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = newRequest(http.MethodGet, "/api/channels/"+channelID+"/messages", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec = httptest.NewRecorder()
	testHandler.ListChannelMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list messages after delete: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var page ChannelMessagesPageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode messages after delete: %v", err)
	}
	if len(page.Messages) != 1 {
		t.Fatalf("messages after delete = %#v, want one tombstone", page.Messages)
	}
	tombstone := page.Messages[0]
	if tombstone.ID != msg.ID || tombstone.Content != "" || len(tombstone.Parts) != 0 || tombstone.EditedAt == nil || tombstone.DeletedAt == nil {
		t.Fatalf("tombstone message = %#v, want empty content/parts with edited_at and deleted_at", tombstone)
	}
	if len(tombstone.Reactions) != 0 {
		t.Fatalf("tombstone reactions = %#v, want cleared", tombstone.Reactions)
	}
	if len(tombstone.Attachments) != 0 {
		t.Fatalf("tombstone attachments = %#v, want clipped", tombstone.Attachments)
	}
	if tombstone.ThreadReplyCount != 1 {
		t.Fatalf("tombstone thread_reply_count = %d, want 1", tombstone.ThreadReplyCount)
	}
	var stillBound int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM attachment
		WHERE id = $1 AND channel_message_id = $2`, attachmentID, msg.ID).Scan(&stillBound); err != nil {
		t.Fatalf("count attachment binding: %v", err)
	}
	if stillBound != 1 {
		t.Fatalf("attachment binding rows = %d, want preserved audit row", stillBound)
	}
	sessionsAfter, tasksAfter := channelAgentRunCountsForTest(t, channelID)
	if sessionsAfter != sessionsBefore || tasksAfter != tasksBefore {
		t.Fatalf("edit/delete should not enqueue or open agent sessions: sessions %d->%d tasks %d->%d", sessionsBefore, sessionsAfter, tasksBefore, tasksAfter)
	}
}

func TestChannelMessageDeleteWithoutRepliesDisappearsFromReadModel(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	channelID := seedChannelForTest(t, "message-delete-hide-"+uuid.NewString(), testUserID)
	msg, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "remove me", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("delete-hide-thread"), 0)
	if err != nil {
		t.Fatalf("insert channel message: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO attachment (workspace_id, channel_id, channel_message_id, uploader_type, uploader_id, filename, url, content_type, size_bytes)
		VALUES ($1, $2, $3, 'member', $4, 'secret.txt', 's3://secret.txt', 'text/plain', 9)`,
		testWorkspaceID, channelID, msg.ID, testUserID); err != nil {
		t.Fatalf("seed attachment: %v", err)
	}

	req := newRequest(http.MethodDelete, "/api/channels/"+channelID+"/messages/"+msg.ID, nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withRouteParams(req, "channelId", channelID, "messageId", msg.ID)
	rec := httptest.NewRecorder()
	testHandler.DeleteChannelMessage(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status=%d body=%s", rec.Code, rec.Body.String())
	}

	messages := listedMessagesForUser(t, channelID, testUserID)
	for _, listed := range messages {
		if listed.ID == msg.ID {
			t.Fatalf("deleted message without replies still listed: %#v", listed)
		}
	}
}

func TestSystemChannelMessageRejectsOrdinaryAbilities(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	channelID := seedChannelForTest(t, "system-ability-guard-"+uuid.NewString(), testUserID)
	msg, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "system", pgtype.UUID{}, "system", "runtime notice", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert system channel message: %v", err)
	}

	req := newRequest(http.MethodPost, "/api/channels/"+channelID+"/messages/"+msg.ID+"/reactions", map[string]string{"emoji": "👍"})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withRouteParams(req, "channelId", channelID, "messageId", msg.ID)
	rec := httptest.NewRecorder()
	testHandler.AddChannelMessageReaction(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("add reaction to system message: status=%d body=%s", rec.Code, rec.Body.String())
	}

	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_message_reaction (channel_message_id, workspace_id, actor_type, actor_id, emoji)
		VALUES ($1, $2, 'member', $3, '👍')`, parseUUID(msg.ID), parseUUID(testWorkspaceID), parseUUID(testUserID)); err != nil {
		t.Fatalf("seed system reaction row: %v", err)
	}
	req = newRequest(http.MethodDelete, "/api/channels/"+channelID+"/messages/"+msg.ID+"/reactions", map[string]string{"emoji": "👍"})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withRouteParams(req, "channelId", channelID, "messageId", msg.ID)
	rec = httptest.NewRecorder()
	testHandler.RemoveChannelMessageReaction(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("remove reaction from system message: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var reactionCount int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM channel_message_reaction WHERE channel_message_id = $1`, msg.ID).Scan(&reactionCount); err != nil {
		t.Fatalf("count system reactions: %v", err)
	}
	if reactionCount != 1 {
		t.Fatalf("system reaction row should remain untouched, got count=%d", reactionCount)
	}

	req = newRequest(http.MethodGet, "/api/channels/"+channelID+"/messages/"+msg.ID+"/thread", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withRouteParams(req, "channelId", channelID, "messageId", msg.ID)
	rec = httptest.NewRecorder()
	testHandler.ListChannelMessageThread(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("open system message thread: status=%d body=%s", rec.Code, rec.Body.String())
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
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "system", pgtype.UUID{}, "system", "Alpha system message", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0); err != nil {
		t.Fatalf("insert system message: %v", err)
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
	var page ChannelMessagesPageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode archived channel messages: %v", err)
	}
	messages := page.Messages
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

func TestListChannels_UnwrapsStructuredAgentLastMessagePreview(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Channel Preview Bot "+uuid.NewString()[:8], []byte("[]"))
	channelID := seedChannelForTest(t, "structured-preview-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
		ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed channel agent member: %v", err)
	}
	raw := `Assistant reply: {"action":"message_send","output":"Clean channel preview","parts":[{"type":"text","text":"Clean channel preview"}]}`
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "agent", parseUUID(agentID), "Channel Preview Bot", raw, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0); err != nil {
		t.Fatalf("seed structured agent channel message: %v", err)
	}

	ch := listedChannelForTest(t, channelID)
	if ch == nil || ch.LastMessage == nil {
		t.Fatalf("listed channel preview missing for %s: %+v", channelID, ch)
	}
	if ch.LastMessage.Content != "Clean channel preview" {
		t.Fatalf("last message content = %q, want clean preview", ch.LastMessage.Content)
	}
	if len(ch.LastMessage.Parts) != 1 || ch.LastMessage.Parts[0].Type != protocol.MessagePartTypeText || ch.LastMessage.Parts[0].Text != "Clean channel preview" {
		t.Fatalf("last message parts = %+v, want one clean text part", ch.LastMessage.Parts)
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
	if ch == nil || ch.RealUnreadCount != 0 {
		t.Fatalf("thread read leaked into channel unread: %+v", ch)
	}
}

func TestChannelThreadReplyShowInChannelCountsOnlyMainTimelineUnread(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	replierID := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "thread-projection-unread-"+uuid.NewString(), testUserID, replierID)
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Root Author", "root topic", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("projection-unread-root-"+uuid.NewString()), 0)
	if err != nil {
		t.Fatalf("insert root: %v", err)
	}
	markChannelReadForTest(t, channelID, testUserID)

	req := newRequestAs(replierID, http.MethodPost, "/api/channels/"+channelID+"/messages/"+root.ID+"/thread", map[string]any{
		"content":         "thread reply visible in channel",
		"show_in_channel": true,
	})
	req = withChannelTestWorkspaceCtx(t, req, replierID)
	req = withURLParams(req, "channelId", channelID, "messageId", root.ID)
	rec := httptest.NewRecorder()
	testHandler.SendChannelMessageThreadReply(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("send projected thread reply: status=%d body=%s", rec.Code, rec.Body.String())
	}

	rootTimeline := listedMessagesForUser(t, channelID, testUserID)
	if len(rootTimeline) != 2 {
		t.Fatalf("main timeline = %+v, want root + projected reply", rootTimeline)
	}
	if rootTimeline[0].ThreadReplyCount != 1 || rootTimeline[0].ThreadUnreadCount != 0 {
		t.Fatalf("root metadata = %+v, want reply counted without thread unread", rootTimeline[0])
	}
	if rootTimeline[1].ThreadRootMessageID == nil || *rootTimeline[1].ThreadRootMessageID != root.ID {
		t.Fatalf("projected reply root = %v, want %s", rootTimeline[1].ThreadRootMessageID, root.ID)
	}

	ch := listedChannelForUser(t, channelID, testUserID)
	if ch == nil || ch.RealUnreadCount != 1 || ch.UnreadCount != 1 {
		t.Fatalf("channel unread = %+v, want one main-timeline unread", ch)
	}
}

func TestChannelThreadReadModelExposesParticipantsAndPendingWake(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Thread Contract Helper", nil)
	channelID := seedChannelForTest(t, "thread-read-model-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "root", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("thread-read-model"), 0)
	if err != nil {
		t.Fatalf("insert root: %v", err)
	}
	rec := sendChannelThreadReplyForTest(t, channelID, root.ID, testUserID, map[string]any{"content": "[@Thread Contract Helper](mention://agent/" + agentID + ") can you take this?"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("send thread mention: status=%d body=%s", rec.Code, rec.Body.String())
	}

	page, raw := listedThreadForUser(t, channelID, root.ID, testUserID)
	if len(page.Messages) == 0 {
		t.Fatal("thread response missing root")
	}
	gotRoot := page.Messages[0]
	timeline := listedMessagesForUser(t, channelID, testUserID)
	if len(timeline) != 1 || len(timeline[0].ThreadParticipants) == 0 || len(timeline[0].ThreadWakeAnnotations) == 0 {
		t.Fatalf("main timeline root missing thread read-model fields: %+v", timeline)
	}
	if len(gotRoot.ThreadParticipants) != 2 {
		t.Fatalf("thread participants = %+v, want root user + mentioned agent", gotRoot.ThreadParticipants)
	}
	participants := map[string]ChannelThreadParticipant{}
	for _, participant := range gotRoot.ThreadParticipants {
		participants[participant.Key] = participant
	}
	if _, ok := participants["user:"+testUserID]; !ok {
		t.Fatalf("participants missing root user: %+v", gotRoot.ThreadParticipants)
	}
	agentParticipant, ok := participants["agent:"+agentID]
	if !ok || !agentParticipant.Followed {
		t.Fatalf("participants missing followed agent: %+v", gotRoot.ThreadParticipants)
	}
	if len(gotRoot.ThreadWakeAnnotations) != 1 {
		t.Fatalf("wake annotations = %+v, want one pending agent", gotRoot.ThreadWakeAnnotations)
	}
	annotation := gotRoot.ThreadWakeAnnotations[0]
	if annotation.Key != "agent:"+agentID || annotation.State != "pending" || annotation.MemberID != agentID || annotation.Reason != nil {
		t.Fatalf("pending wake annotation = %+v, want non-leaky pending agent", annotation)
	}
	for _, forbidden := range []string{"task_id", "pending_from_seq", "pending_to_seq", "delivered_to_seq", "channel_ambient_pending_wake"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("thread read-model response leaked %q: %s", forbidden, raw)
		}
	}
}

func TestChannelThreadReadModelSurfacesReplyAndAckStates(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Thread State Helper", nil)
	channelID := seedChannelForTest(t, "thread-state-model-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}

	replyRoot := dispatchThreadMentionForTest(t, channelID, agentID, "thread-state-reply")
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "agent", parseUUID(agentID), "Thread State Helper", "visible answer", "multica", nil, pgtype.UUID{}, parseUUID(replyRoot.ID), replyRoot.ThreadID, 1); err != nil {
		t.Fatalf("insert visible agent reply: %v", err)
	}
	replyPage, _ := listedThreadForUser(t, channelID, replyRoot.ID, testUserID)
	if got := wakeStateForAgent(t, replyPage.Messages[0], agentID); got.State != "replied" || got.Reason != nil {
		t.Fatalf("reply wake state = %+v, want replied", got)
	}

	rec := sendChannelThreadReplyForTest(t, channelID, replyRoot.ID, testUserID, map[string]any{"content": "[@Thread State Helper](mention://agent/" + agentID + ") react if done"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("send follow-up thread mention: status=%d body=%s", rec.Code, rec.Body.String())
	}
	ackEventID := latestChannelAgentInboxEventForRootForTest(t, replyRoot.ID, agentID)
	if _, err := testPool.Exec(ctx, `UPDATE agent_inbox_event SET status = 'acked', acked_at = now(), updated_at = now() WHERE id = $1`, ackEventID); err != nil {
		t.Fatalf("ack follow-up inbox event: %v", err)
	}
	ackPage, _ := listedThreadForUser(t, channelID, replyRoot.ID, testUserID)
	if got := wakeStateForAgent(t, ackPage.Messages[0], agentID); got.State != "acked" || got.Reason != nil {
		t.Fatalf("follow-up ack wake state = %+v, want acked despite older visible reply", got)
	}
}

func TestChannelThreadReplyWithoutMentionCreatesAmbientInboxOnly(t *testing.T) {
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

	var ambientEvents, wakeEvents int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE reason = 'ambient' AND requires_wake = false),
		       count(*) FILTER (WHERE requires_wake)
		FROM agent_inbox_event
		WHERE channel_id = $1 AND agent_id = $2`, channelID, agentID).Scan(&ambientEvents, &wakeEvents); err != nil {
		t.Fatalf("count thread reply inbox events: %v", err)
	}
	if ambientEvents != 1 || wakeEvents != 0 {
		t.Fatalf("plain thread reply inbox events = ambient:%d wake:%d, want 1/0", ambientEvents, wakeEvents)
	}
}

func TestChannelThreadPlainReplyCreatesAmbientForChannelAgents(t *testing.T) {
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

	assertChannelAgentInboxEventCounts(t, channelID, participantID, 1, 0)
	assertChannelAgentInboxEventCounts(t, channelID, bystanderID, 1, 0)
}

func TestChannelThreadPlainReplyAfterRootMentionCreatesAmbientOnly(t *testing.T) {
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

	assertChannelAgentInboxEventCounts(t, channelID, participantID, 1, 0)
	assertChannelAgentInboxEventCounts(t, channelID, bystanderID, 1, 0)
}

func TestChannelAmbientGateDoesNotBlockThreadDirectedContinuation(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	withChannelAmbientGateTestConfig(t)
	participantID := createHandlerTestAgent(t, "Ambient Thread Helper "+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "ambient-thread-gate-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, participantID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	ambient, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "ordinary ambient work", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert ambient trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, ambient, parseUUID(testUserID))

	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "root", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("ambient-thread-root"), 0)
	if err != nil {
		t.Fatalf("insert root: %v", err)
	}
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "agent", parseUUID(participantID), "Ambient Thread Helper", "agent answer", "multica", nil, pgtype.UUID{}, parseUUID(root.ID), strPtr("ambient-thread-root"), 1); err != nil {
		t.Fatalf("insert agent reply: %v", err)
	}
	var participantName string
	if err := testPool.QueryRow(ctx, `SELECT name FROM agent WHERE id = $1`, participantID).Scan(&participantName); err != nil {
		t.Fatalf("load participant name: %v", err)
	}
	followup, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "@"+participantName+" follow up", "multica", nil, pgtype.UUID{}, parseUUID(root.ID), strPtr("ambient-thread-root"), 0)
	if err != nil {
		t.Fatalf("insert follow-up: %v", err)
	}
	testHandler.dispatchChannelThreadReplyMentions(ctx, ch, followup, parseUUID(testUserID))

	assertChannelAgentInboxEventCounts(t, channelID, participantID, 1, 1)
}

func assertChannelAgentInboxEventCounts(t *testing.T, channelID, agentID string, wantAmbient, wantWake int) {
	t.Helper()
	ctx := context.Background()
	var ambientEvents, wakeEvents int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE reason = 'ambient' AND requires_wake = false),
		       count(*) FILTER (WHERE requires_wake = true)
		FROM agent_inbox_event
		WHERE channel_id = $1 AND agent_id = $2`, channelID, agentID).Scan(&ambientEvents, &wakeEvents); err != nil {
		t.Fatalf("count inbox events: %v", err)
	}
	if ambientEvents != wantAmbient || wakeEvents != wantWake {
		t.Fatalf("channel agent inbox events for %s = ambient:%d wake:%d, want %d/%d", agentID, ambientEvents, wakeEvents, wantAmbient, wantWake)
	}
}

func withChannelAmbientGateTestConfig(t *testing.T) {
	t.Helper()
	prev := testHandler.cfg
	testHandler.cfg.ChannelAmbientGateMode = channelAmbientGateModeGate
	testHandler.cfg.ChannelAmbientGateWindow = time.Minute
	testHandler.cfg.ChannelAmbientGateMaxRecentPerAgent = 1
	testHandler.cfg.ChannelAmbientGateMaxRecentPerChannel = 32
	testHandler.cfg.ChannelAmbientGateMaxRecentPerRuntime = 64
	t.Cleanup(func() { testHandler.cfg = prev })
}

func TestChannelOfflineRuntimeQueuesButDoesNotShowActiveTask(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	suffix := uuid.NewString()
	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, owner_id, last_seen_at
		)
		VALUES ($1, $2, 'Offline Channel Runtime', 'local', 'test-offline', 'offline', 'test runtime', '{}'::jsonb, $3, now())
		RETURNING id
	`, testWorkspaceID, "offline-channel-"+suffix, testUserID).Scan(&runtimeID); err != nil {
		t.Fatalf("create offline runtime: %v", err)
	}

	var agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id,
			instructions, custom_env, custom_args, mcp_config
		)
		VALUES ($1, 'Offline Channel Agent', '', 'local', '{}'::jsonb, $2, 'private', 1, $3, '', '{}'::jsonb, '[]'::jsonb, '[]'::jsonb)
		RETURNING id
	`, testWorkspaceID, runtimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatalf("create offline agent: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})

	channelID := seedChannelForTest(t, "offline-active-task-"+suffix, testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
	`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed offline agent member: %v", err)
	}

	var chatSessionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title, runtime_id)
		VALUES ($1, $2, $3, 'offline channel session', $4)
		RETURNING id
	`, testWorkspaceID, agentID, testUserID, runtimeID).Scan(&chatSessionID); err != nil {
		t.Fatalf("create chat session: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_agent_session (channel_id, agent_id, chat_session_id)
		VALUES ($1, $2, $3)
	`, channelID, agentID, chatSessionID); err != nil {
		t.Fatalf("create channel agent session: %v", err)
	}

	session, err := testHandler.Queries.GetChatSession(ctx, parseUUID(chatSessionID))
	if err != nil {
		t.Fatalf("load chat session: %v", err)
	}
	if _, err := testHandler.TaskService.EnqueueChatTask(ctx, session, parseUUID(testUserID)); err != nil {
		t.Fatalf("enqueue offline chat task failed (should queue for later): %v", err)
	}

	var queuedCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_task_queue WHERE chat_session_id = $1
	`, chatSessionID).Scan(&queuedCount); err != nil {
		t.Fatalf("count queued tasks: %v", err)
	}
	if queuedCount != 1 {
		t.Fatalf("offline enqueue created %d tasks, want 1 (queued for reconnect)", queuedCount)
	}

	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, chat_session_id, status, priority, initiator_user_id)
		VALUES ($1, $2, $3, 'queued', 2, $4)
	`, agentID, runtimeID, chatSessionID, testUserID); err != nil {
		t.Fatalf("seed historical offline queued task: %v", err)
	}

	req := withURLParam(newRequest(http.MethodGet, "/api/channels/"+channelID+"/active-tasks", nil), "channelId", channelID)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	rec := httptest.NewRecorder()
	testHandler.ListChannelActiveTasks(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list active tasks: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp ChannelActiveTasksResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode active tasks: %v", err)
	}
	if len(resp.Tasks) != 0 {
		t.Fatalf("active tasks = %#v, want none for offline runtime", resp.Tasks)
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
	var page ChannelThreadMessagesPageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page 1: %v", err)
	}
	if got := threadContents(page.Messages); fmt.Sprint(got) != "[root reply-2 reply-3]" {
		t.Fatalf("page 1 contents = %v", got)
	}
	if page.NextCursor == nil || page.NextCursor.BeforeSeq == 0 || page.NextCursor.Before == "" || page.NextCursor.BeforeID == "" {
		t.Fatalf("page 1 body cursor = %+v, want seq/time/id", page.NextCursor)
	}
	beforeSeq, before, beforeID := rec.Header().Get("X-Next-Before-Seq"), rec.Header().Get("X-Next-Before"), rec.Header().Get("X-Next-Before-Id")
	if beforeSeq == "" || before == "" || beforeID == "" {
		t.Fatalf("page 1 missing cursor before_seq=%q before=%q before_id=%q", beforeSeq, before, beforeID)
	}

	req = newRequest(http.MethodGet, "/api/channels/"+channelID+"/messages/"+root.ID+"/thread?limit=2&before_seq="+beforeSeq, nil)
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
	if got := threadContents(page.Messages); fmt.Sprint(got) != "[root reply-1]" {
		t.Fatalf("page 2 contents = %v", got)
	}
	if page.NextCursor != nil {
		t.Fatalf("page 2 should not emit body cursor, got %+v", page.NextCursor)
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
	if page.NextCursor.Seq == 0 {
		t.Fatalf("page 1 cursor missing seq: %+v", page.NextCursor)
	}
	if got := threadContents(page.Messages); fmt.Sprint(got) != "[msg-3 msg-4]" {
		t.Fatalf("page 1 contents = %v", got)
	}

	req = newRequest(http.MethodGet, "/api/channels/"+channelID+"/messages?limit=2&before_seq="+strconv.FormatInt(page.NextCursor.Seq, 10), nil)
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

func TestChannelMessagesAlwaysReturnsPageEnvelope(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	channelID := seedChannelForTest(t, "message-page-envelope-"+uuid.NewString(), testUserID)
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "enveloped", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("message-page-envelope"), 0); err != nil {
		t.Fatalf("insert message: %v", err)
	}

	req := newRequest(http.MethodGet, "/api/channels/"+channelID+"/messages", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.ListChannelMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list messages: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var page ChannelMessagesPageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page envelope: %v", err)
	}
	if page.Limit != channelMessagesDefaultLimit || page.HasMore || page.NextCursor != nil {
		t.Fatalf("page metadata = limit:%d has_more:%t cursor:%+v", page.Limit, page.HasMore, page.NextCursor)
	}
	if len(page.Messages) != 1 || page.Messages[0].Content != "enveloped" {
		t.Fatalf("page messages = %+v, want one enveloped message", page.Messages)
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
		{name: "invalid seq cursor", path: "/api/channels/" + channelID + "/messages?before_seq=bad"},
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

func TestChannelMessagesExposeConversationSeq(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	channelID := seedChannelForTest(t, "message-seq-"+uuid.NewString(), testUserID)
	first, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "first", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("message-seq-first"), 0)
	if err != nil {
		t.Fatalf("insert first: %v", err)
	}
	second, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "second", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("message-seq-second"), 0)
	if err != nil {
		t.Fatalf("insert second: %v", err)
	}
	if first.Seq < 1 || second.Seq != first.Seq+1 {
		t.Fatalf("message seqs = %d, %d; want monotonic per conversation", first.Seq, second.Seq)
	}

	var conversationID string
	var lastSeq int64
	if err := testPool.QueryRow(ctx, `
		SELECT id, last_seq
		FROM conversation
		WHERE channel_id = $1`, channelID).Scan(&conversationID, &lastSeq); err != nil {
		t.Fatalf("load conversation: %v", err)
	}
	if conversationID != channelID || lastSeq != second.Seq {
		t.Fatalf("conversation = id:%s last_seq:%d, want id:%s last_seq:%d", conversationID, lastSeq, channelID, second.Seq)
	}
}

func TestChannelUnreadUsesConversationSeq(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	readerID := createChannelPlainMember(t)
	writerID := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "message-seq-unread-"+uuid.NewString(), readerID, writerID)
	first, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(writerID), "Writer", "before read", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("message-seq-unread-1"), 0)
	if err != nil {
		t.Fatalf("insert first: %v", err)
	}

	req := newRequestAs(readerID, http.MethodPost, "/api/channels/"+channelID+"/read", nil)
	req = withChannelTestWorkspaceCtx(t, req, readerID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.MarkChannelRead(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mark read: status=%d body=%s", rec.Code, rec.Body.String())
	}

	second, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(writerID), "Writer", "after read old timestamp", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("message-seq-unread-2"), 0)
	if err != nil {
		t.Fatalf("insert second: %v", err)
	}
	oldTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := testPool.Exec(ctx, `UPDATE channel_message SET created_at = $1 WHERE id = $2`, oldTime, second.ID); err != nil {
		t.Fatalf("pin second old timestamp: %v", err)
	}

	var lastReadSeq int64
	if err := testPool.QueryRow(ctx, `
		SELECT last_read_seq
		FROM channel_read
		WHERE channel_id = $1 AND user_id = $2`, channelID, readerID).Scan(&lastReadSeq); err != nil {
		t.Fatalf("load channel read seq: %v", err)
	}
	if lastReadSeq != first.Seq {
		t.Fatalf("last_read_seq = %d, want first seq %d", lastReadSeq, first.Seq)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE channel_read
		SET last_read_seq = $3
		WHERE channel_id = $1 AND user_id = $2`, channelID, readerID, second.Seq); err != nil {
		t.Fatalf("make legacy channel_read stale: %v", err)
	}

	ch := listedChannelForUser(t, channelID, readerID)
	if ch == nil {
		t.Fatal("channel missing from list")
	}
	if ch.RealUnreadCount != 1 || ch.UnreadCount != 1 {
		t.Fatalf("seq unread = real:%d total:%d, want 1/1; second seq=%d", ch.RealUnreadCount, ch.UnreadCount, second.Seq)
	}
}

func TestMarkChannelReadCursorIsMonotonic(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	readerID := createChannelPlainMember(t)
	writerID := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "cursor-monotonic-"+uuid.NewString(), readerID, writerID)

	// Insert two messages so the conversation has seq 1 and 2.
	first, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(writerID), "Writer", "first", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("cursor-monotonic-1"), 0)
	if err != nil {
		t.Fatalf("insert first: %v", err)
	}
	second, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(writerID), "Writer", "second", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("cursor-monotonic-2"), 0)
	if err != nil {
		t.Fatalf("insert second: %v", err)
	}
	_ = first

	// Mark read — advances cursor to second.Seq (the conversation last_seq).
	req := newRequestAs(readerID, http.MethodPost, "/api/channels/"+channelID+"/read", nil)
	req = withChannelTestWorkspaceCtx(t, req, readerID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.MarkChannelRead(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mark read (first): status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Simulate a stale/slow request: manually regress channel_read to an older seq.
	if _, err := testPool.Exec(ctx, `
		UPDATE channel_read SET last_read_seq = $3
		WHERE channel_id = $1 AND user_id = $2`,
		channelID, readerID, first.Seq); err != nil {
		t.Fatalf("regress channel_read: %v", err)
	}
	// Also regress conversation_member if it exists.
	if _, err := testPool.Exec(ctx, `
		UPDATE conversation_member SET last_read_seq = $3
		WHERE member_type = 'user' AND member_id = $2
		AND EXISTS (SELECT 1 FROM conversation WHERE id = conversation_member.conversation_id AND channel_id = $1)`,
		channelID, readerID, first.Seq); err != nil {
		t.Fatalf("regress conversation_member: %v", err)
	}

	// Mark read again — the GREATEST guard must NOT move the cursor backwards.
	// The conversation last_seq is still second.Seq, so this should advance to second.Seq,
	// not regress to first.Seq.
	req2 := newRequestAs(readerID, http.MethodPost, "/api/channels/"+channelID+"/read", nil)
	req2 = withChannelTestWorkspaceCtx(t, req2, readerID)
	req2 = withURLParam(req2, "channelId", channelID)
	rec2 := httptest.NewRecorder()
	testHandler.MarkChannelRead(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("mark read (second): status=%d body=%s", rec2.Code, rec2.Body.String())
	}

	// Verify cursor is at second.Seq, not first.Seq.
	var convSeq int64
	if err := testPool.QueryRow(ctx, `
		SELECT last_read_seq FROM conversation_member cm
		JOIN conversation c ON c.id = cm.conversation_id
		WHERE c.channel_id = $1 AND cm.member_type = 'user' AND cm.member_id = $2`,
		channelID, readerID).Scan(&convSeq); err == nil {
		if convSeq < second.Seq {
			t.Fatalf("conversation_member cursor regressed: got %d, want >= %d", convSeq, second.Seq)
		}
	}

	// No unread messages after the mark-read.
	ch := listedChannelForUser(t, channelID, readerID)
	if ch == nil {
		t.Fatal("channel missing from list")
	}
	if ch.RealUnreadCount != 0 {
		t.Fatalf("expected 0 unread after mark read, got real=%d", ch.RealUnreadCount)
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

	var agentParticipants int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM thread_participant
		WHERE root_message_id = $1
		  AND member_type = 'agent'
		  AND member_id = $2
		  AND followed_at IS NOT NULL
		  AND wake_state = 'active'`, root.ID, agentID).Scan(&agentParticipants); err != nil {
		t.Fatalf("count agent thread participant: %v", err)
	}
	if agentParticipants != 1 {
		t.Fatalf("agent thread participant count=%d, want 1", agentParticipants)
	}

	var sessionID string
	if err := testPool.QueryRow(ctx, `SELECT chat_session_id FROM channel_agent_session WHERE channel_id = $1 AND agent_id = $2`, channelID, agentID).Scan(&sessionID); err != nil {
		t.Fatalf("channel agent session not created: %v", err)
	}
	var promptThreadRoot, prompt string
	if err := testPool.QueryRow(ctx, `
		SELECT channel_thread_root_message_id, content
		FROM chat_message
		WHERE chat_session_id = $1 AND role = 'user'
		ORDER BY created_at DESC
		LIMIT 1`, sessionID).Scan(&promptThreadRoot, &prompt); err != nil {
		t.Fatalf("load prompt thread root: %v", err)
	}
	if promptThreadRoot != root.ID {
		t.Fatalf("prompt thread root = %q, want %s", promptThreadRoot, root.ID)
	}
	for _, want := range []string{"Thread context (root message first, then bounded recent replies from this thread only):", "root", "@Thread Agent can you answer here?"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("thread prompt missing %q:\n%s", want, prompt)
		}
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

func TestAddChannelMemberEmitsSystemEventOnce(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	targetID := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "member-add-event-"+uuid.NewString(), testUserID)

	req := newRequestAs(testUserID, http.MethodPost, "/api/channels/"+channelID+"/members", AddChannelMemberRequest{MemberType: "user", MemberID: targetID})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.AddChannelMember(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add member: status=%d body=%s", rec.Code, rec.Body.String())
	}

	event := latestChannelSystemEventForTest(t, channelID)
	assertChannelMemberSystemEvent(t, event, channelMemberAddedEvent, testUserID, "user", targetID, "user")

	req = newRequestAs(testUserID, http.MethodPost, "/api/channels/"+channelID+"/members", AddChannelMemberRequest{MemberType: "user", MemberID: targetID})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec = httptest.NewRecorder()
	testHandler.AddChannelMember(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("duplicate add member: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := countChannelSystemMessagesForTest(t, channelID); got != 1 {
		t.Fatalf("system message count after duplicate add = %d, want 1", got)
	}
}

func TestAddChannelMembersBatchEmitsOnlyInsertedSystemEvents(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	existingID := createChannelPlainMember(t)
	newID := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "member-batch-event-"+uuid.NewString(), testUserID, existingID)

	req := newRequestAs(testUserID, http.MethodPost, "/api/channels/"+channelID+"/members/batch", AddChannelMembersRequest{Members: []AddChannelMemberRequest{
		{MemberType: "user", MemberID: existingID},
		{MemberType: "user", MemberID: newID},
	}})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.AddChannelMembers(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("batch add members: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := countChannelSystemMessagesForTest(t, channelID); got != 1 {
		t.Fatalf("batch system message count = %d, want only inserted member event", got)
	}
	event := latestChannelSystemEventForTest(t, channelID)
	assertChannelMemberSystemEvent(t, event, channelMemberAddedEvent, testUserID, "user", newID, "user")
}

func TestAddChannelMemberSystemEventIncludesAgentTargetRef(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "channel_event_agent_"+strings.ReplaceAll(uuid.NewString(), "-", ""), nil)
	channelID := seedChannelForTest(t, "member-add-agent-event-"+uuid.NewString(), testUserID)

	req := newRequestAs(testUserID, http.MethodPost, "/api/channels/"+channelID+"/members", AddChannelMemberRequest{MemberType: "agent", MemberID: agentID})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.AddChannelMember(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add agent member: status=%d body=%s", rec.Code, rec.Body.String())
	}

	event := latestChannelSystemEventForTest(t, channelID)
	assertChannelMemberSystemEvent(t, event, channelMemberAddedEvent, testUserID, "user", agentID, "agent")
}

func TestRemoveChannelMemberEmitsRemovedSystemEventForRemainingMembers(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	targetID := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "member-remove-event-"+uuid.NewString(), testUserID, targetID)

	req := newRequestAs(testUserID, http.MethodDelete, "/api/channels/"+channelID+"/members/user/"+targetID, nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withRouteParams(req, "channelId", channelID, "memberType", "user", "memberId", targetID)
	rec := httptest.NewRecorder()
	testHandler.RemoveChannelMember(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove member: status=%d body=%s", rec.Code, rec.Body.String())
	}

	event := latestChannelSystemEventForTest(t, channelID)
	assertChannelMemberSystemEvent(t, event, channelMemberRemovedEvent, testUserID, "user", targetID, "user")

	listReq := newRequestAs(targetID, http.MethodGet, "/api/channels/"+channelID+"/messages", nil)
	listReq = withChannelTestWorkspaceCtx(t, listReq, targetID)
	listReq = withURLParam(listReq, "channelId", channelID)
	listRec := httptest.NewRecorder()
	testHandler.ListChannelMessages(listRec, listReq)
	if listRec.Code != http.StatusForbidden {
		t.Fatalf("removed member list messages: status=%d body=%s", listRec.Code, listRec.Body.String())
	}
}

func TestRemoveChannelMemberEmitsLeftSystemEventForSelfRemove(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	memberID := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "member-left-event-"+uuid.NewString(), testUserID, memberID)

	req := newRequestAs(memberID, http.MethodDelete, "/api/channels/"+channelID+"/members/user/"+memberID, nil)
	req = withChannelTestWorkspaceCtx(t, req, memberID)
	req = withRouteParams(req, "channelId", channelID, "memberType", "user", "memberId", memberID)
	rec := httptest.NewRecorder()
	testHandler.RemoveChannelMember(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("self remove member: status=%d body=%s", rec.Code, rec.Body.String())
	}

	event := latestChannelSystemEventForTest(t, channelID)
	assertChannelMemberSystemEvent(t, event, channelMemberLeftEvent, memberID, "user", memberID, "user")
}

func TestChannelMemberSystemMessageDoesNotCountAsUnread(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	readerID := createChannelPlainMember(t)
	targetID := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "member-event-unread-"+uuid.NewString(), testUserID, readerID)

	req := newRequestAs(readerID, http.MethodPost, "/api/channels/"+channelID+"/read", nil)
	req = withChannelTestWorkspaceCtx(t, req, readerID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.MarkChannelRead(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mark read: status=%d body=%s", rec.Code, rec.Body.String())
	}

	addReq := newRequestAs(testUserID, http.MethodPost, "/api/channels/"+channelID+"/members", AddChannelMemberRequest{MemberType: "user", MemberID: targetID})
	addReq = withChannelTestWorkspaceCtx(t, addReq, testUserID)
	addReq = withURLParam(addReq, "channelId", channelID)
	addRec := httptest.NewRecorder()
	testHandler.AddChannelMember(addRec, addReq)
	if addRec.Code != http.StatusCreated {
		t.Fatalf("add member: status=%d body=%s", addRec.Code, addRec.Body.String())
	}

	ch := listedChannelForUser(t, channelID, readerID)
	if ch == nil {
		t.Fatal("channel missing from reader list")
	}
	if ch.RealUnreadCount != 0 || ch.UnreadCount != 0 {
		t.Fatalf("system member event counted as unread: real=%d total=%d", ch.RealUnreadCount, ch.UnreadCount)
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

func TestRemoveChannelMemberRejectsDMChannel(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	cleanupDMArtifacts(t)
	agentID := createHandlerTestAgent(t, "DM Remove Guard", nil)
	channelID := seedAgentDMChannel(t, agentID)

	req := newRequest(http.MethodDelete, "/api/channels/"+channelID+"/members/agent/"+agentID, nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withRouteParams(req, "channelId", channelID, "memberType", "agent", "memberId", agentID)
	rec := httptest.NewRecorder()
	testHandler.RemoveChannelMember(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("remove member from dm: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var count int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'agent' AND member_id = $3`, channelID, testWorkspaceID, agentID).Scan(&count); err != nil {
		t.Fatalf("count dm agent member: %v", err)
	}
	if count != 1 {
		t.Fatalf("dm channel agent member count=%d, want 1", count)
	}
}

func TestRemoveChannelMemberRequiresManagerForOtherMembers(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	memberID := createChannelPlainMember(t)
	targetID := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "remove-member-permission-"+uuid.NewString(), testUserID, memberID, targetID)

	req := newRequestAs(memberID, http.MethodDelete, "/api/channels/"+channelID+"/members/user/"+targetID, nil)
	req = withChannelTestWorkspaceCtx(t, req, memberID)
	req = withRouteParams(req, "channelId", channelID, "memberType", "user", "memberId", targetID)
	rec := httptest.NewRecorder()
	testHandler.RemoveChannelMember(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("plain member removed another member: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var count int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'user' AND member_id = $3`, channelID, testWorkspaceID, targetID).Scan(&count); err != nil {
		t.Fatalf("count target member: %v", err)
	}
	if count != 1 {
		t.Fatalf("target member count=%d, want 1", count)
	}

	selfReq := newRequestAs(memberID, http.MethodDelete, "/api/channels/"+channelID+"/members/user/"+memberID, nil)
	selfReq = withChannelTestWorkspaceCtx(t, selfReq, memberID)
	selfReq = withRouteParams(selfReq, "channelId", channelID, "memberType", "user", "memberId", memberID)
	selfRec := httptest.NewRecorder()
	testHandler.RemoveChannelMember(selfRec, selfReq)
	if selfRec.Code != http.StatusOK {
		t.Fatalf("plain member self-remove: status=%d body=%s", selfRec.Code, selfRec.Body.String())
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'user' AND member_id = $3`, channelID, testWorkspaceID, memberID).Scan(&count); err != nil {
		t.Fatalf("count self member: %v", err)
	}
	if count != 0 {
		t.Fatalf("self member count=%d, want 0", count)
	}
}

func TestImportLarkChannelMessageRequiresChannelMember(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	memberID := createChannelPlainMember(t)
	larkChatID := "oc_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	channelID := seedChannelForTest(t, "lark-import-permission-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		UPDATE channel
		SET lark_chat_id = $1
		WHERE id = $2 AND workspace_id = $3`, larkChatID, channelID, testWorkspaceID); err != nil {
		t.Fatalf("set lark chat id: %v", err)
	}

	req := newRequestAs(memberID, http.MethodPost, "/api/lark/channel-messages/import", ImportLarkChannelMessageRequest{
		LarkChatID: larkChatID,
		AuthorName: "Feishu",
		Content:    "imported private content",
	})
	req = withChannelTestWorkspaceCtx(t, req, memberID)
	rec := httptest.NewRecorder()
	testHandler.ImportLarkChannelMessage(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("lark import by non-channel member: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var count int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1 AND workspace_id = $2 AND source = 'lark'`, channelID, testWorkspaceID).Scan(&count); err != nil {
		t.Fatalf("count imported lark messages: %v", err)
	}
	if count != 0 {
		t.Fatalf("non-member lark import persisted %d message(s)", count)
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

func channelAgentRunCountsForTest(t *testing.T, channelID string) (int, int) {
	t.Helper()
	ctx := context.Background()
	var sessions int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM channel_agent_session WHERE channel_id = $1`, channelID).Scan(&sessions); err != nil {
		t.Fatalf("count channel agent sessions: %v", err)
	}
	var tasks int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_task_queue atq
		JOIN channel_agent_session cas ON cas.chat_session_id = atq.chat_session_id
		WHERE cas.channel_id = $1`, channelID).Scan(&tasks); err != nil {
		t.Fatalf("count channel agent tasks: %v", err)
	}
	return sessions, tasks
}

func sendChannelMessageForTest(t *testing.T, channelID, userID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	req := newRequestAs(userID, http.MethodPost, "/api/channels/"+channelID+"/messages", body)
	req = withChannelTestWorkspaceCtx(t, req, userID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.SendChannelMessage(rec, req)
	return rec
}

func sendChannelThreadReplyForTest(t *testing.T, channelID, rootID, userID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	req := newRequestAs(userID, http.MethodPost, "/api/channels/"+channelID+"/messages/"+rootID+"/thread", body)
	req = withChannelTestWorkspaceCtx(t, req, userID)
	req = withURLParams(req, "channelId", channelID, "messageId", rootID)
	rec := httptest.NewRecorder()
	testHandler.SendChannelMessageThreadReply(rec, req)
	return rec
}

func assertNoChannelMessageContent(t *testing.T, channelID, content string) {
	t.Helper()
	assertChannelMessageContentCount(t, channelID, content, 0)
}

func assertChannelMessageContentCount(t *testing.T, channelID, content string, want int) {
	t.Helper()
	var count int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1 AND content = $2`, channelID, content).Scan(&count); err != nil {
		t.Fatalf("count channel message content: %v", err)
	}
	if count != want {
		t.Fatalf("channel message content %q count = %d, want %d", content, count, want)
	}
}

func latestChannelSystemEventForTest(t *testing.T, channelID string) channelMemberSystemEventPart {
	t.Helper()
	var rawParts []byte
	if err := testPool.QueryRow(context.Background(), `
		SELECT parts
		FROM channel_message
		WHERE channel_id = $1 AND author_type = 'system'
		ORDER BY seq DESC
		LIMIT 1`, channelID).Scan(&rawParts); err != nil {
		t.Fatalf("load latest system message: %v", err)
	}
	var parts []protocol.MessagePart
	if err := json.Unmarshal(rawParts, &parts); err != nil {
		t.Fatalf("decode system message parts: %v", err)
	}
	if len(parts) != 1 || strings.TrimSpace(parts[0].Text) == "" {
		t.Fatalf("system message parts = %+v, want one structured text part", parts)
	}
	var event channelMemberSystemEventPart
	if err := json.Unmarshal([]byte(parts[0].Text), &event); err != nil {
		t.Fatalf("decode system event part %q: %v", parts[0].Text, err)
	}
	return event
}

func assertChannelMemberSystemEvent(t *testing.T, event channelMemberSystemEventPart, wantEvent, actorID, actorType, targetID, targetType string) {
	t.Helper()
	if event.Event != wantEvent {
		t.Fatalf("event = %q, want %q; full=%+v", event.Event, wantEvent, event)
	}
	if event.Params.ActorID != actorID {
		t.Fatalf("actor_id = %q, want %q; full=%+v", event.Params.ActorID, actorID, event)
	}
	if event.Params.ActorType != actorType {
		t.Fatalf("actor_type = %q, want %q; full=%+v", event.Params.ActorType, actorType, event)
	}
	if event.Params.TargetID != targetID {
		t.Fatalf("target_id = %q, want %q; full=%+v", event.Params.TargetID, targetID, event)
	}
	if event.Params.TargetType != targetType {
		t.Fatalf("target_type = %q, want %q; full=%+v", event.Params.TargetType, targetType, event)
	}
	if strings.TrimSpace(event.Params.ActorHandle) == "" {
		t.Fatalf("actor_handle is empty; full=%+v", event)
	}
	if strings.TrimSpace(event.Params.TargetHandle) == "" {
		t.Fatalf("target_handle is empty; full=%+v", event)
	}
	if strings.TrimSpace(event.Params.ActorDisplayName) == "" {
		t.Fatalf("actor_display_name is empty; full=%+v", event)
	}
	if strings.TrimSpace(event.Params.TargetDisplayName) == "" {
		t.Fatalf("target_display_name is empty; full=%+v", event)
	}
	if strings.TrimSpace(event.Params.ActorName) == "" {
		t.Fatalf("actor_name is empty; full=%+v", event)
	}
	if strings.TrimSpace(event.Params.TargetName) == "" {
		t.Fatalf("target_name is empty; full=%+v", event)
	}
}

func countChannelSystemMessagesForTest(t *testing.T, channelID string) int {
	t.Helper()
	var count int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1 AND author_type = 'system'`, channelID).Scan(&count); err != nil {
		t.Fatalf("count system messages: %v", err)
	}
	return count
}

func assertNoThreadRepliesForRoot(t *testing.T, channelID, rootID string) {
	t.Helper()
	var count int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1 AND thread_root_message_id = $2`, channelID, rootID).Scan(&count); err != nil {
		t.Fatalf("count thread replies: %v", err)
	}
	if count != 0 {
		t.Fatalf("thread replies for root %s = %d, want 0", rootID, count)
	}
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
	var page ChannelMessagesPageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode messages: %v", err)
	}
	return page.Messages
}

func markChannelReadForTest(t *testing.T, channelID, userID string) {
	t.Helper()
	req := newRequestAs(userID, http.MethodPost, "/api/channels/"+channelID+"/read", nil)
	req = withChannelTestWorkspaceCtx(t, req, userID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.MarkChannelRead(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mark channel read: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func listedThreadForUser(t *testing.T, channelID, rootID, userID string) (ChannelThreadMessagesPageResponse, string) {
	t.Helper()
	req := newRequestAs(userID, http.MethodGet, "/api/channels/"+channelID+"/messages/"+rootID+"/thread", nil)
	req = withChannelTestWorkspaceCtx(t, req, userID)
	req = withURLParams(req, "channelId", channelID, "messageId", rootID)
	rec := httptest.NewRecorder()
	testHandler.ListChannelMessageThread(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list channel thread: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var page ChannelThreadMessagesPageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode thread page: %v", err)
	}
	return page, rec.Body.String()
}

func dispatchThreadMentionForTest(t *testing.T, channelID, agentID, threadID string) ChannelMessageResponse {
	t.Helper()
	ctx := context.Background()
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "root "+threadID, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr(threadID), 0)
	if err != nil {
		t.Fatalf("insert root: %v", err)
	}
	rec := sendChannelThreadReplyForTest(t, channelID, root.ID, testUserID, map[string]any{"content": "[@Thread State Helper](mention://agent/" + agentID + ") please check"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("send thread mention: status=%d body=%s", rec.Code, rec.Body.String())
	}
	return root
}

func latestChannelAgentInboxEventForRootForTest(t *testing.T, rootID, agentID string) string {
	t.Helper()
	var eventID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT e.id
		FROM agent_inbox_event e
		JOIN channel_message cm ON cm.id = e.source_message_id
		WHERE cm.thread_root_message_id = $1
		  AND e.agent_id = $2
		  AND e.requires_wake
		ORDER BY e.created_at DESC, e.id DESC
		LIMIT 1`, rootID, agentID).Scan(&eventID); err != nil {
		t.Fatalf("load latest channel agent inbox event for root: %v", err)
	}
	return eventID
}

func wakeStateForAgent(t *testing.T, root ChannelMessageResponse, agentID string) ChannelThreadWakeAnnotation {
	t.Helper()
	for _, annotation := range root.ThreadWakeAnnotations {
		if annotation.MemberType == "agent" && annotation.MemberID == agentID {
			return annotation
		}
	}
	t.Fatalf("missing wake annotation for agent %s in %+v", agentID, root.ThreadWakeAnnotations)
	return ChannelThreadWakeAnnotation{}
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
