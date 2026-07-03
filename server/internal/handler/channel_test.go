package handler

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/multica-ai/multica/server/internal/service"
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

func TestChannelChatDoneNoReplyAndReactionCommandsAreNotPersisted(t *testing.T) {
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
	assertNoChannelMessageContent(t, channelID, reactionCommand)

	var legacyReactionCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message_reaction
		WHERE channel_message_id = $1 AND actor_type = 'agent' AND actor_id = $2 AND emoji = '🎉'`, trigger.ID, agentID).Scan(&legacyReactionCount); err != nil {
		t.Fatalf("count legacy channel reaction: %v", err)
	}
	if legacyReactionCount != 1 {
		t.Fatalf("message react count = %d, want 1", legacyReactionCount)
	}

	legacyReactionCommand := fmt.Sprintf("multica channel react %s 🚀", trigger.ID)
	testHandler.handleChannelChatDone(events.Event{Payload: protocol.ChatDonePayload{
		ChatSessionID: sessionID,
		Type:          protocol.ChatOutputKindMessage,
		Content:       legacyReactionCommand,
	}})
	assertNoChannelMessageContent(t, channelID, legacyReactionCommand)

	var legacyChannelReactionCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message_reaction
		WHERE channel_message_id = $1 AND actor_type = 'agent' AND actor_id = $2 AND emoji = '🚀'`, trigger.ID, agentID).Scan(&legacyChannelReactionCount); err != nil {
		t.Fatalf("count legacy channel reaction: %v", err)
	}
	if legacyChannelReactionCount != 1 {
		t.Fatalf("legacy channel reaction count = %d, want 1", legacyChannelReactionCount)
	}

	malformedReactionCommand := "multica channel react not-a-message-id 👍"
	testHandler.handleChannelChatDone(events.Event{Payload: protocol.ChatDonePayload{
		ChatSessionID: sessionID,
		Type:          protocol.ChatOutputKindMessage,
		Content:       malformedReactionCommand,
	}})
	assertNoChannelMessageContent(t, channelID, malformedReactionCommand)
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

	var tasks int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_task_queue atq
		JOIN channel_agent_session cas ON cas.chat_session_id = atq.chat_session_id
		WHERE cas.channel_id = $1`, channelID).Scan(&tasks); err != nil {
		t.Fatalf("count ambient tasks: %v", err)
	}
	if tasks != len(agentIDs) {
		t.Fatalf("repeated ambient fanout created %d tasks, want %d", tasks, len(agentIDs))
	}
}

func TestChannelAmbientGateSkipsObviousNoiseBeforeTaskCreation(t *testing.T) {
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

	var sessions, tasks int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_agent_session
		WHERE channel_id = $1`, channelID).Scan(&sessions); err != nil {
		t.Fatalf("count channel agent sessions: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_task_queue atq
		JOIN channel_agent_session cas ON cas.chat_session_id = atq.chat_session_id
		WHERE cas.channel_id = $1`, channelID).Scan(&tasks); err != nil {
		t.Fatalf("count ambient tasks: %v", err)
	}
	if sessions != 0 || tasks != 0 {
		t.Fatalf("noise ambient created %d sessions and %d tasks, want none", sessions, tasks)
	}
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

	assertChannelAgentTaskPriorityCounts(t, channelID, agentID, 1, 1)
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
	var tasks int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_task_queue q
		JOIN channel_agent_session cas ON cas.chat_session_id = q.chat_session_id
		WHERE cas.channel_id = $1 AND cas.agent_id = $2`, channelID, agentID).Scan(&tasks); err != nil {
		t.Fatalf("count dispatched tasks: %v", err)
	}
	if tasks != 1 {
		t.Fatalf("agent dispatch tasks = %d, want 1", tasks)
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
	var tasks int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_task_queue q
		JOIN channel_agent_session cas ON cas.chat_session_id = q.chat_session_id
		WHERE cas.channel_id = $1 AND cas.agent_id = $2`, channelID, agentID).Scan(&tasks); err != nil {
		t.Fatalf("count DM dispatched tasks: %v", err)
	}
	if tasks != 1 {
		t.Fatalf("DM agent dispatch tasks = %d, want 1", tasks)
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
	if ch == nil || ch.RealUnreadCount != 0 {
		t.Fatalf("thread read leaked into channel unread: %+v", ch)
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
	followup, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "follow up", "multica", nil, pgtype.UUID{}, parseUUID(root.ID), strPtr("ambient-thread-root"), 0)
	if err != nil {
		t.Fatalf("insert follow-up: %v", err)
	}
	testHandler.dispatchChannelThreadReplyMentions(ctx, ch, followup, parseUUID(testUserID))

	assertChannelAgentTaskPriorityCounts(t, channelID, participantID, 1, 1)
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

func assertChannelAgentTaskPriorityCounts(t *testing.T, channelID, agentID string, wantAmbient, wantDirect int) {
	t.Helper()
	ctx := context.Background()
	var ambientTasks, directTasks int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_task_queue atq
		JOIN channel_agent_session cas ON cas.chat_session_id = atq.chat_session_id
		WHERE cas.channel_id = $1 AND cas.agent_id = $2 AND atq.priority = 1`, channelID, agentID).Scan(&ambientTasks); err != nil {
		t.Fatalf("count ambient tasks: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_task_queue atq
		JOIN channel_agent_session cas ON cas.chat_session_id = atq.chat_session_id
		WHERE cas.channel_id = $1 AND cas.agent_id = $2 AND atq.priority = 2`, channelID, agentID).Scan(&directTasks); err != nil {
		t.Fatalf("count direct tasks: %v", err)
	}
	if ambientTasks != wantAmbient || directTasks != wantDirect {
		t.Fatalf("channel agent tasks = ambient:%d direct:%d, want %d/%d", ambientTasks, directTasks, wantAmbient, wantDirect)
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

func TestChannelOfflineRuntimeDoesNotQueueOrShowActiveTask(t *testing.T) {
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
	if _, err := testHandler.TaskService.EnqueueChatTask(ctx, session, parseUUID(testUserID)); !errors.Is(err, service.ErrChatTaskAgentNoRuntime) {
		t.Fatalf("enqueue offline chat task error = %v, want ErrChatTaskAgentNoRuntime", err)
	}

	var queuedCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_task_queue WHERE chat_session_id = $1
	`, chatSessionID).Scan(&queuedCount); err != nil {
		t.Fatalf("count queued tasks: %v", err)
	}
	if queuedCount != 0 {
		t.Fatalf("offline enqueue created %d tasks, want 0", queuedCount)
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
	var page []ChannelMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page 1: %v", err)
	}
	if got := threadContents(page); fmt.Sprint(got) != "[root reply-2 reply-3]" {
		t.Fatalf("page 1 contents = %v", got)
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

	ch := listedChannelForUser(t, channelID, readerID)
	if ch == nil {
		t.Fatal("channel missing from list")
	}
	if ch.RealUnreadCount != 1 || ch.UnreadCount != 1 {
		t.Fatalf("seq unread = real:%d total:%d, want 1/1; second seq=%d", ch.RealUnreadCount, ch.UnreadCount, second.Seq)
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
	var count int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1 AND content = $2`, channelID, content).Scan(&count); err != nil {
		t.Fatalf("count channel message content: %v", err)
	}
	if count != 0 {
		t.Fatalf("channel message content %q was persisted %d time(s)", content, count)
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
