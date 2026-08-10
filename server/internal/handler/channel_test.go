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

func TestFinalizeAgentChannelMessageDMDropsUnanchoredReferences(t *testing.T) {
	content, parts, err := (&Handler{}).finalizeAgentChannelMessage(context.Background(), ChannelResponse{Kind: "dm"}, "hello @untrusted", []protocol.MessagePart{{
		Type:       protocol.MessagePartTypeReference,
		RefType:    "mention",
		RefSubType: "agent",
		RefID:      uuid.NewString(),
		Label:      "@untrusted",
	}})
	if err != nil {
		t.Fatalf("finalize dm message: %v", err)
	}
	if content != "hello @untrusted" {
		t.Fatalf("content = %q", content)
	}
	if len(parts) != 0 {
		t.Fatalf("dm finalizer retained unanchored caller reference: %+v", parts)
	}
}

func TestFinalizeAgentChannelMessageDMResolvesChannelReferences(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	targetName := "dm-channel-ref-" + uuid.NewString()[:8]
	targetChannelID := seedChannelForTest(t, targetName, testUserID)
	dmAgentID := createHandlerTestAgent(t, "DM Channel Ref Peer "+uuid.NewString(), []byte("[]"))
	dmChannelID := seedAgentDMChannel(t, dmAgentID)
	dm, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(dmChannelID))
	if !found {
		t.Fatal("dm channel not found after seed")
	}

	link := "[" + targetName + "](mention://channel/" + targetChannelID + ")"
	content := "structured " + link + " but bare #" + targetName + " stays text"
	gotContent, parts, err := testHandler.finalizeAgentChannelMessage(ctx, dm, content, nil)
	if err != nil {
		t.Fatalf("finalize dm channel references: %v", err)
	}
	if gotContent != content {
		t.Fatalf("content = %q, want %q", gotContent, content)
	}

	var refs []protocol.MessagePart
	for _, part := range parts {
		if part.Type == protocol.MessagePartTypeReference && part.RefType == "channel-ref" {
			refs = append(refs, part)
		}
	}
	if len(refs) != 1 {
		t.Fatalf("dm channel references = %+v, want only the structured anchor", refs)
	}
	ref := refs[0]
	if ref.RefID != targetChannelID || ref.Label != targetName {
		t.Fatalf("dm channel reference = %+v, want target %s / %q", ref, targetChannelID, targetName)
	}
	if ref.ContentStartUTF16 == nil || ref.ContentEndUTF16 == nil || *ref.ContentStartUTF16 >= *ref.ContentEndUTF16 {
		t.Fatalf("dm channel reference has no source anchor: %+v", ref)
	}
}

func TestFinalizeAgentChannelMessageOwnsVoiceSynthesisState(t *testing.T) {
	attachmentID := uuid.NewString()
	_, parts, err := (&Handler{}).finalizeAgentChannelMessage(
		context.Background(),
		ChannelResponse{Kind: "dm"},
		"spoken answer",
		[]protocol.MessagePart{
			{Type: protocol.MessagePartTypeText, Text: "spoken answer"},
			{
				Type:                protocol.MessagePartTypeVoice,
				AttachmentID:        attachmentID,
				Filename:            "runtime.mp3",
				ContentType:         "audio/mpeg",
				SizeBytes:           42,
				DurationMS:          900,
				TranscriptionStatus: protocol.VoiceTranscriptionCompleted,
			},
		},
	)
	if err != nil {
		t.Fatalf("finalize dm voice message: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("parts = %+v, want text and voice", parts)
	}
	voice := parts[1]
	if voice.SynthesisStatus != protocol.VoiceSynthesisPending {
		t.Fatalf("synthesis status = %q, want pending", voice.SynthesisStatus)
	}
	if voice.AttachmentID != "" || voice.Filename != "" || voice.ContentType != "" ||
		voice.SizeBytes != 0 || voice.DurationMS != 0 || voice.TranscriptionStatus != "" {
		t.Fatalf("finalizer retained runtime-owned voice artifact metadata: %+v", voice)
	}
}

func TestChannelVoiceReplyInstructionUsesStructuredVoicePart(t *testing.T) {
	voice := ChannelMessageResponse{
		Type:  "user",
		Parts: []protocol.MessagePart{{Type: protocol.MessagePartTypeVoice}},
	}
	if got := channelVoiceReplyInstruction(voice); !strings.Contains(got, "multica message send --voice") {
		t.Fatalf("voice instruction = %q", got)
	}
	typed := channelVoiceReplyInstruction(ChannelMessageResponse{Type: "user", Content: "请语音回复"})
	if !strings.Contains(typed, "explicitly asks") || !strings.Contains(typed, "runtime brief's voice-delivery path") {
		t.Fatalf("typed human message instruction = %q, want semantic voice-request guidance", typed)
	}
	if got := channelVoiceReplyInstruction(ChannelMessageResponse{Type: "agent", Parts: voice.Parts}); got != "" {
		t.Fatalf("agent-authored voice output must not force another voice reply, got %q", got)
	}
}

func TestChannelDirectedReplyInstructionDoesNotForceTextModality(t *testing.T) {
	if strings.Contains(channelDirectedReplyInstruction, "text answer") {
		t.Fatalf("directed reply instruction forces text and conflicts with voice requests: %q", channelDirectedReplyInstruction)
	}
	if strings.Contains(channelStickerReplyInstruction, "send text only") {
		t.Fatalf("sticker instruction forces text and conflicts with voice requests: %q", channelStickerReplyInstruction)
	}
	if !strings.Contains(channelDirectedReplyInstruction, "requested supported delivery modality") {
		t.Fatalf("directed reply instruction = %q, want requested-modality guidance", channelDirectedReplyInstruction)
	}
}

func TestFormatChannelMessageLineLabelsVoiceDirection(t *testing.T) {
	voicePart := []protocol.MessagePart{{Type: protocol.MessagePartTypeVoice}}
	human := formatChannelMessageLine(ChannelMessageResponse{
		AuthorName: "Frank",
		Type:       "user",
		Content:    "question",
		Parts:      voicePart,
	})
	if !strings.Contains(human, "user, voice input") {
		t.Fatalf("human line = %q, want voice input label", human)
	}
	agent := formatChannelMessageLine(ChannelMessageResponse{
		AuthorName: "Beckham",
		Type:       "agent",
		Content:    "answer",
		Parts:      voicePart,
	})
	if !strings.Contains(agent, "agent, voice reply") {
		t.Fatalf("agent line = %q, want voice reply label", agent)
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

func TestUpdateChannelSetsAvatarURL(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	name := "avatar-channel-" + uuid.NewString()
	create := httptest.NewRecorder()
	req := withChannelTestWorkspaceCtx(t, newRequest(http.MethodPost, "/api/channels", map[string]any{"name": name}), testUserID)
	testHandler.CreateChannel(create, req)
	if create.Code != http.StatusCreated {
		t.Fatalf("create: status=%d body=%s", create.Code, create.Body.String())
	}
	var created ChannelResponse
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created channel: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, created.ID)
	})
	if created.AvatarURL != nil {
		t.Fatalf("new channel avatar_url = %v, want nil", *created.AvatarURL)
	}

	avatar := "https://cdn.example.com/files/channel-icon.png"
	patch := httptest.NewRecorder()
	req = withChannelTestWorkspaceCtx(t, newRequest(http.MethodPatch, "/api/channels/"+created.ID, map[string]any{"avatar_url": avatar}), testUserID)
	req = withRouteParams(req, "channelId", created.ID)
	testHandler.UpdateChannel(patch, req)
	if patch.Code != http.StatusOK {
		t.Fatalf("update avatar: status=%d body=%s", patch.Code, patch.Body.String())
	}
	var updated ChannelResponse
	if err := json.Unmarshal(patch.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated channel: %v", err)
	}
	if updated.AvatarURL == nil || *updated.AvatarURL != avatar {
		t.Fatalf("updated avatar_url = %v, want %q", updated.AvatarURL, avatar)
	}

	list := httptest.NewRecorder()
	req = withChannelTestWorkspaceCtx(t, newRequest(http.MethodGet, "/api/channels", nil), testUserID)
	testHandler.ListChannels(list, req)
	if list.Code != http.StatusOK {
		t.Fatalf("list: status=%d body=%s", list.Code, list.Body.String())
	}
	var channels []ChannelResponse
	if err := json.Unmarshal(list.Body.Bytes(), &channels); err != nil {
		t.Fatalf("decode channels: %v", err)
	}
	found := false
	for _, ch := range channels {
		if ch.ID == created.ID {
			found = ch.AvatarURL != nil && *ch.AvatarURL == avatar
		}
	}
	if !found {
		t.Fatalf("ListChannels did not expose avatar_url for %s", created.ID)
	}

	tooLong := strings.Repeat("a", channelAvatarURLMaxLen+1)
	reject := httptest.NewRecorder()
	req = withChannelTestWorkspaceCtx(t, newRequest(http.MethodPatch, "/api/channels/"+created.ID, map[string]any{"avatar_url": tooLong}), testUserID)
	req = withRouteParams(req, "channelId", created.ID)
	testHandler.UpdateChannel(reject, req)
	if reject.Code != http.StatusBadRequest {
		t.Fatalf("oversized avatar_url: status=%d body=%s", reject.Code, reject.Body.String())
	}
}

func TestChannelMentionCreatesCanonicalDelivery(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentHandle := "channel-helper-" + uuid.NewString()[:8]
	agentID := createHandlerTestAgentOnRuntime(t, agentHandle, handlerTestRuntimeID(t))
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
		VALUES ($1, $2, 'user', $3), ($1, $2, 'agent', $4)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, testUserID, agentID); err != nil {
		t.Fatalf("seed members: %v", err)
	}

	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	triggerContent := "@" + agentHandle + " please review this"
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", triggerContent, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("debate-thread"), 2)
	if err != nil {
		t.Fatalf("insert trigger: %v", err)
	}

	testHandler.dispatchChannelMentions(ctx, ch, trigger, parseUUID(testUserID))
	testHandler.deliverCanonicalMessageToChannelAgents(ctx, ch, trigger)

	var target string
	if err := testPool.QueryRow(ctx, `
		SELECT target
		FROM agent_message_delivery
		WHERE message_id = $1 AND agent_id = $2`, trigger.ID, agentID).Scan(&target); err != nil {
		t.Fatalf("load canonical mention delivery: %v", err)
	}
	if target != "channel:"+channelID {
		t.Fatalf("canonical mention delivery target = %q, want channel:%s", target, channelID)
	}

	// #2295: mention is delivery-only; no task-shaped wake.
	var wakeCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_inbox_event
		WHERE agent_id = $1 AND source_message_id = $2 AND requires_wake = true`,
		agentID, trigger.ID).Scan(&wakeCount); err != nil {
		t.Fatalf("count mention wake inbox events: %v", err)
	}
	if wakeCount != 0 {
		t.Fatalf("mention wake inbox count = %d, want 0 (delivery-only)", wakeCount)
	}
}

func TestChannelGreetingMentionStaysOnMainTimeline(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Greeting Agent", nil)
	channelID := seedChannelForTest(t, "greeting-main-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "@Greeting Agent hi", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("greeting-thread"), 0)
	if err != nil {
		t.Fatalf("insert trigger: %v", err)
	}

	testHandler.dispatchChannelMentions(ctx, ch, trigger, parseUUID(testUserID))

	// Bridge assertion only needs a legacy session prompt without thread root.
	sessionID := ensureLegacyChannelChatBridgeForTest(t, ch, agentID, trigger, "greeting bridge prompt")

	testHandler.handleChannelChatDone(events.Event{Payload: protocol.ChatDonePayload{ChatSessionID: sessionID, Content: "hi there"}})
	var replyRoot *string
	if err := testPool.QueryRow(ctx, `
		SELECT thread_root_message_id::text
		FROM channel_message
		WHERE channel_id = $1 AND author_type = 'agent' AND content = 'hi there'
		LIMIT 1`, channelID).Scan(&replyRoot); err != nil {
		t.Fatalf("load greeting reply: %v", err)
	}
	if replyRoot != nil {
		t.Fatalf("greeting reply thread root = %q, want nil", *replyRoot)
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
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
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

	sessionID := ensureLegacyChannelChatBridgeForTest(t, ch, agentID, trigger, "react only bridge prompt")

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
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
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

	sessionID := ensureLegacyChannelChatBridgeForTest(t, ch, agentID, trigger, "legacy bridge prompt")

	testHandler.handleChannelChatDone(events.Event{Payload: protocol.ChatDonePayload{
		ChatSessionID:          sessionID,
		TaskID:                 uuid.NewString(),
		Type:                   protocol.ChatOutputKindNoReply,
		OutputSuppressedReason: protocol.ChannelOutputSuppressedReasonDaemonOutdated,
	}})

	var messageCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM channel_message
		WHERE channel_id = $1
		  AND (author_type = 'agent' OR (author_type = 'system' AND membership_generation_id IS NULL))
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
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
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

	sessionID := ensureLegacyChannelChatBridgeForTest(t, ch, agentID, trigger, "legacy bridge prompt")

	testHandler.handleChannelChatDone(events.Event{Payload: protocol.ChatDonePayload{
		ChatSessionID:          sessionID,
		TaskID:                 uuid.NewString(),
		Type:                   protocol.ChatOutputKindNoReply,
		OutputSuppressedReason: protocol.ChannelOutputSuppressedReasonInvalidAction,
	}})

	var messageCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM channel_message
		WHERE channel_id = $1
		  AND (author_type = 'agent' OR (author_type = 'system' AND membership_generation_id IS NULL))
	`, channelID).Scan(&messageCount); err != nil {
		t.Fatalf("count visible output messages: %v", err)
	}
	if messageCount != 0 {
		t.Fatalf("visible output message count = %d, want 0", messageCount)
	}
}

func TestChannelRementionFollowupCreatesIndependentCanonicalDeliveries(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentHandle := "remention-agent-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	agentID := createHandlerTestAgentOnRuntime(t, agentHandle, handlerTestRuntimeID(t))
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
		VALUES ($1, $2, 'user', $3), ($1, $2, 'agent', $4)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, testUserID, agentID); err != nil {
		t.Fatalf("seed members: %v", err)
	}

	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	firstContent := "@" + agentHandle + " start the long task"
	firstParts := []protocol.MessagePart{{Type: protocol.MessagePartTypeReference, RefType: "mention", RefSubType: "agent", RefID: agentID, Label: "@" + agentHandle}}
	first, err := testHandler.insertChannelMessageWithParts(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", firstContent, firstParts, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("interrupt-thread"), 0)
	if err != nil {
		t.Fatalf("insert first trigger: %v", err)
	}
	testHandler.dispatchChannelMentions(ctx, ch, first, parseUUID(testUserID))
	testHandler.deliverCanonicalMessageToChannelAgents(ctx, ch, first)

	secondContent := "@" + agentHandle + " stop and use this corrected direction"
	secondParts := []protocol.MessagePart{{Type: protocol.MessagePartTypeReference, RefType: "mention", RefSubType: "agent", RefID: agentID, Label: "@" + agentHandle}}
	second, err := testHandler.insertChannelMessageWithParts(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", secondContent, secondParts, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("interrupt-thread"), 1)
	if err != nil {
		t.Fatalf("insert second trigger: %v", err)
	}
	testHandler.dispatchChannelMentions(ctx, ch, second, parseUUID(testUserID))
	testHandler.deliverCanonicalMessageToChannelAgents(ctx, ch, second)

	var deliveryCount, inboxCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_message_delivery
		WHERE agent_id = $1 AND message_id IN ($2, $3)`, agentID, first.ID, second.ID).Scan(&deliveryCount); err != nil {
		t.Fatalf("count canonical follow-up deliveries: %v", err)
	}
	if deliveryCount != 2 {
		t.Fatalf("canonical follow-up delivery count = %d, want 2", deliveryCount)
	}
	// #2295: follow-up mentions stay delivery-only. Each message keeps its own
	// independent Delivery projection; no task-shaped wake is minted.
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_inbox_event
		WHERE agent_id = $1
		  AND requires_wake = true
		  AND reason = 'mention'
		  AND source_message_id IN ($2, $3)`, agentID, first.ID, second.ID).Scan(&inboxCount); err != nil {
		t.Fatalf("count mention wake inbox events: %v", err)
	}
	if inboxCount != 0 {
		t.Fatalf("mention wake inbox event count = %d, want 0 (delivery-only)", inboxCount)
	}
}

func TestChannelAgentInboxMessagesRecordRuntimeTrajectory(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentName := "Inbox Activity Agent " + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentName, nil)
	runtimeID := handlerTestRuntimeID(t)
	channelID := seedChannelForTest(t, "agent-inbox-activity-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "Product-task source context", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("inbox-activity"), 0)
	if err != nil {
		t.Fatalf("insert product-task source: %v", err)
	}
	eventID := createProductInboxEventForRuntime(t, runtimeID, agentID, channelID)
	if _, err := testPool.Exec(ctx, `UPDATE agent_inbox_event SET source_message_id = $2 WHERE id = $1`, eventID, trigger.ID); err != nil {
		t.Fatalf("link product task source: %v", err)
	}

	drainReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "agent-inbox-activity-daemon")
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
	var liveTaskMessages []protocol.TaskMessagePayload
	testHandler.Bus.Subscribe(protocol.EventTaskMessage, func(e events.Event) {
		payload, ok := e.Payload.(protocol.TaskMessagePayload)
		if ok && payload.TaskID == got.ID {
			liveTaskMessages = append(liveTaskMessages, payload)
		}
	})

	messagesReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+got.ID+"/messages", ReportAgentInboxMessagesRequest{
		DeliveryID: got.DeliveryID,
		LeaseToken: got.LeaseToken,
		Messages: []TaskMessageRequest{
			{Seq: 1, Type: "thinking", Content: "I should create the requested file."},
			{Seq: 2, Type: "tool_use", Tool: "terminal", Input: map[string]any{"command": "cat secret", "path": "/tmp/hello_world.txt"}},
			{Seq: 3, Type: "tool_result", Tool: "terminal", Output: "raw stdout should be diagnostic"},
			{Seq: 4, Type: "log", Content: "[Slock Wrapper] Starting Antigravity CLI..."},
			{Seq: 5, Type: "text", Content: "runtime stdout fallback should be diagnostic"},
			{Seq: 6, Type: "tool_use", Tool: "running", Input: map[string]any{"path": "/tmp/status_only.txt"}},
			{Seq: 7, Type: "tool_use", Tool: "write_file", Input: map[string]any{"path": "/Users/frank/Code/multica/server/internal/handler/channel_test.go"}},
			{Seq: 8, Type: "tool_use", Tool: "read", Input: map[string]any{"filePath": "/tmp/test.go", "basePath": "/repo"}},
			{Seq: 9, Type: "thinking"},
		},
	}, testWorkspaceID, "agent-inbox-activity-daemon")
	messagesReq = withURLParam(messagesReq, "eventId", got.ID)
	messagesRec := httptest.NewRecorder()
	testHandler.ReportAgentInboxMessages(messagesRec, messagesReq)
	if messagesRec.Code != http.StatusOK {
		t.Fatalf("report inbox messages: status=%d body=%s", messagesRec.Code, messagesRec.Body.String())
	}
	if len(liveTaskMessages) != 3 {
		t.Fatalf("live task messages = %+v, want mapped tools only", liveTaskMessages)
	}
	if liveTaskMessages[0].Seq != 2 || liveTaskMessages[0].Type != "tool_use" || liveTaskMessages[0].Tool != "bash" {
		t.Fatalf("live terminal payload = %+v, want canonical bash tool_use", liveTaskMessages[0])
	}
	if liveTaskMessages[1].Seq != 7 || liveTaskMessages[1].Tool != "write_file" || liveTaskMessages[2].Seq != 8 || liveTaskMessages[2].Tool != "read_file" {
		t.Fatalf("live file tool payloads = %+v, want write/read file without thinking or unmapped status", liveTaskMessages)
	}

}

func TestChannelAgentInboxFileClaimWithoutTransportIsSuppressed(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentName := "Inbox Artifact Agent " + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentName, nil)
	runtimeID := handlerTestRuntimeID(t)
	channelID := seedChannelForTest(t, "agent-inbox-artifact-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	createProductInboxEventForRuntime(t, runtimeID, agentID, channelID)

	drainReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "agent-inbox-artifact-daemon")
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

	completeReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+got.ID+"/complete", CompleteAgentInboxEventRequest{
		DeliveryID: got.DeliveryID,
		LeaseToken: got.LeaseToken,
		TaskCompleteRequest: TaskCompleteRequest{
			Output: "给你，hello_world.txt",
		},
	}, testWorkspaceID, "agent-inbox-artifact-daemon")
	completeReq = withURLParam(completeReq, "eventId", got.ID)
	completeRec := httptest.NewRecorder()
	testHandler.CompleteAgentInboxEvent(completeRec, completeReq)
	if completeRec.Code != http.StatusOK {
		t.Fatalf("complete inbox event: status=%d body=%s", completeRec.Code, completeRec.Body.String())
	}
	assertChannelMessageContentCount(t, channelID, "给你，hello_world.txt", 0)
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
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	createProductInboxEventForRuntime(t, runtimeID, agentID, channelID)

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
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	createProductInboxEventForRuntime(t, runtimeID, agentID, channelID)

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

	var chatDoneEvents int
	if testHandler.Bus != nil {
		testHandler.Bus.Subscribe(protocol.EventChatDone, func(e events.Event) {
			payload, ok := e.Payload.(protocol.ChatDonePayload)
			if ok && payload.TaskID == got.ID {
				chatDoneEvents++
			}
		})
	}

	failReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+got.ID+"/fail", FailAgentInboxEventRequest{
		DeliveryID:    got.DeliveryID,
		LeaseToken:    got.LeaseToken,
		Error:         "provider_auth_required: grok not logged in",
		FailureReason: "agent_error.provider_auth_or_access",
		ReasonCode:    "provider_auth_required",
	}, testWorkspaceID, "agent-inbox-fail-daemon")
	failReq = withURLParam(failReq, "eventId", got.ID)
	failRec := httptest.NewRecorder()
	testHandler.FailAgentInboxEvent(failRec, failReq)
	if failRec.Code != http.StatusOK {
		t.Fatalf("fail inbox event: status=%d body=%s", failRec.Code, failRec.Body.String())
	}

	var eventStatus, deliveryStatus, lastError, terminalOutcome, terminalDeliveryID string
	var retryable bool
	var terminalAt pgtype.Timestamptz
	if err := testPool.QueryRow(ctx, `
		SELECT e.status,
		       d.status,
		       COALESCE(e.last_error, ''),
		       COALESCE(e.terminal_outcome, ''),
		       COALESCE(e.terminal_delivery_id::text, ''),
		       e.retryable,
		       e.terminal_at
		FROM agent_inbox_event e
		JOIN agent_event_delivery d ON d.inbox_event_id = e.id
		WHERE e.id = $1 AND d.id = $2`, got.ID, got.DeliveryID).Scan(&eventStatus, &deliveryStatus, &lastError, &terminalOutcome, &terminalDeliveryID, &retryable, &terminalAt); err != nil {
		t.Fatalf("load failed inbox event: %v", err)
	}
	if eventStatus != "acked" || deliveryStatus != "acked" || !strings.Contains(lastError, "grok not logged in") {
		t.Fatalf("terminal inbox states = event:%q delivery:%q error:%q, want acked/acked/grok not logged in", eventStatus, deliveryStatus, lastError)
	}
	if terminalOutcome != "failed" || terminalDeliveryID != got.DeliveryID || !retryable || !terminalAt.Valid {
		t.Fatalf("failed terminal projection = outcome:%q delivery:%q retryable:%v terminal_at:%v, want failed/%s/retryable/timestamp", terminalOutcome, terminalDeliveryID, retryable, terminalAt.Valid, got.DeliveryID)
	}

	if got.ChatSessionID != "" {
		var assistantFailureMessages int
		if err := testPool.QueryRow(ctx, `
			SELECT count(*)
			FROM chat_message
			WHERE chat_session_id = $1
			  AND role = 'assistant'
			  AND task_id = $2`, got.ChatSessionID, got.ID).Scan(&assistantFailureMessages); err != nil {
			t.Fatalf("count failed inbox chat messages: %v", err)
		}
		if assistantFailureMessages != 0 {
			t.Fatalf("assistant failure chat messages = %d, want 0 (classified terminal failure belongs in Activity only)", assistantFailureMessages)
		}
	}

	var visibleAgentMessages int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1
		  AND author_type = 'agent'
		  AND author_id = $2`, channelID, agentID).Scan(&visibleAgentMessages); err != nil {
		t.Fatalf("count visible agent messages: %v", err)
	}
	if visibleAgentMessages != 0 {
		t.Fatalf("visible agent channel messages = %d, want 0 (terminal failure should not reply into the channel)", visibleAgentMessages)
	}
	if chatDoneEvents != 0 {
		t.Fatalf("chat:done events for failed inbox event = %d, want 0 (terminal failure should not synthesize chat completion)", chatDoneEvents)
	}

	drainReq = newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "agent-inbox-fail-daemon")
	drainReq = withURLParam(drainReq, "runtimeId", runtimeID)
	drainRec = httptest.NewRecorder()
	testHandler.DrainAgentInboxByRuntime(drainRec, drainReq)
	if drainRec.Code != http.StatusOK {
		t.Fatalf("second drain inbox: status=%d body=%s", drainRec.Code, drainRec.Body.String())
	}
	if err := json.Unmarshal(drainRec.Body.Bytes(), &drainResp); err != nil {
		t.Fatalf("decode second drain response: %v", err)
	}
	for _, event := range drainResp.Events {
		if event.Reason == channelOnboardingReason {
			continue
		}
		t.Fatalf("terminal failure drained unexpected event reason=%s id=%s", event.Reason, event.ID)
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
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	createProductInboxEventForRuntime(t, runtimeID, agentID, channelID)

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
	if secondDelivery.Task != nil && secondDelivery.Task.ChatSessionID != "" {
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

func TestChannelMentionedAgentsUsesHandlesOrStructuredIDs(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	handle := "wendy-" + suffix
	secondHandle := handle + "-2"
	uniqueHandle := "kai-" + suffix
	displayName := "Wendy"
	uniqueDisplay := "Kai"
	agentID := createHandlerTestAgent(t, handle, nil)
	secondAgentID := createHandlerTestAgent(t, secondHandle, nil)
	uniqueAgentID := createHandlerTestAgent(t, uniqueHandle, nil)
	for _, id := range []string{agentID, secondAgentID} {
		if _, err := testPool.Exec(ctx, `UPDATE agent SET display_name = $2 WHERE id = $1`, id, displayName); err != nil {
			t.Fatalf("set duplicate display_name: %v", err)
		}
	}
	if _, err := testPool.Exec(ctx, `UPDATE agent SET display_name = $2 WHERE id = $1`, uniqueAgentID, uniqueDisplay); err != nil {
		t.Fatalf("set unique display_name: %v", err)
	}

	channelID := seedChannelForTest(t, "identity-mentions-"+suffix, testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3), ($1, $2, 'agent', $4), ($1, $2, 'agent', $5)
ON CONFLICT DO NOTHING`,
		channelID, testWorkspaceID, agentID, secondAgentID, uniqueAgentID,
	); err != nil {
		t.Fatalf("seed agent members: %v", err)
	}

	cases := []struct {
		name    string
		content string
		parts   []protocol.MessagePart
		wantID  string
	}{
		{"bare unique handle", "please @" + handle + " jump in", nil, agentID},
		{"bare duplicate display name is not routable", "please @Wendy jump in", nil, ""},
		{"bare unique display alias is routable", "please @Kai jump in", nil, uniqueAgentID},
		{"bare unique display alias is case insensitive", "please @kai jump in", nil, uniqueAgentID},
		{"structured mention targets first duplicate", "please @Wendy jump in", []protocol.MessagePart{{Type: protocol.MessagePartTypeReference, RefType: "mention", RefSubType: "agent", RefID: agentID}}, agentID},
		{"structured mention targets second duplicate", "please @Wendy jump in", []protocol.MessagePart{{Type: protocol.MessagePartTypeReference, RefType: "mention", RefSubType: "agent", RefID: secondAgentID}}, secondAgentID},
		{"exact handle remains routable", "please @" + secondHandle + " review", nil, secondAgentID},
		{"handle prefix is not routable", "please @" + secondHandle + "extra", nil, ""},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			agents := testHandler.channelMentionedAgents(ctx, testWorkspaceID, channelID, tt.content, tt.parts)
			if tt.wantID == "" {
				if len(agents) != 0 {
					t.Fatalf("channelMentionedAgents returned %d agents, want none: %+v", len(agents), agents)
				}
				return
			}
			if len(agents) != 1 {
				t.Fatalf("channelMentionedAgents returned %d agents, want 1: %+v", len(agents), agents)
			}
			if got := uuidToString(agents[0].ID); got != tt.wantID {
				t.Fatalf("mentioned agent = %s, want %s", got, tt.wantID)
			}
		})
	}
}

func TestChannelLegacyAgentHandleTextDoesNotRoute(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	legacyHandle := "actor_" + suffix
	agentID := createHandlerTestAgent(t, "Legacy Agent "+suffix, nil)
	if _, err := testPool.Exec(ctx, `UPDATE agent SET name = $2 WHERE id = $1`, agentID, legacyHandle); err != nil {
		t.Fatalf("seed legacy agent handle: %v", err)
	}
	channelID := seedChannelForTest(t, "legacy-agent-text-"+suffix, testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed legacy agent member: %v", err)
	}

	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	for _, content := range []string{
		"historic @" + legacyHandle + " remains plain text",
		"historic @actor remains plain text",
	} {
		_, parts, err := testHandler.enrichChannelMessageMentions(ctx, ch, content, nil)
		if err != nil {
			t.Fatalf("enrich legacy handle text: %v", err)
		}
		for _, part := range parts {
			if part.Type == protocol.MessagePartTypeReference && part.RefType == "mention" && part.RefID == agentID {
				t.Fatalf("legacy handle unexpectedly became a structured mention: %+v", part)
			}
		}
		if agents := testHandler.channelMentionedAgents(ctx, testWorkspaceID, channelID, content, nil); len(agents) != 0 {
			t.Fatalf("legacy handle text routed to agents: %+v", agents)
		}
	}
}

func TestChannelBareMentionsBecomeStructuredMessageParts(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	agentName := "mention-agent-" + suffix
	agentID := createHandlerTestAgent(t, agentName, nil)
	memberID := createChannelPlainMember(t)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM inbox_item WHERE recipient_id = $1 AND type = 'mentioned'`, memberID)
	})
	if _, err := testPool.Exec(ctx, `UPDATE "user" SET display_name = $2 WHERE id = $1`, memberID, "Plain Person "+suffix); err != nil {
		t.Fatalf("set member display_name: %v", err)
	}
	channelID := seedChannelForTest(t, "structured-mentions-"+suffix, testUserID, memberID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}

	var memberName string
	if err := testPool.QueryRow(ctx, `SELECT name FROM "user" WHERE id = $1`, memberID).Scan(&memberName); err != nil {
		t.Fatalf("load member handle: %v", err)
	}
	content := "ping @" + agentName + " and @" + memberName + " plus @all"
	content, parts, err := testHandler.enrichChannelMessageMentions(ctx, ch, content, nil)
	if err != nil {
		t.Fatalf("enrich bare mentions: %v", err)
	}
	want := map[string]bool{
		"agent:" + agentID:   true,
		"member:" + memberID: true,
	}
	for _, part := range parts {
		if part.Type == protocol.MessagePartTypeReference && part.RefType == "mention" {
			delete(want, part.RefSubType+":"+part.RefID)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing structured mentions: %+v; parts=%+v", want, parts)
	}

	agents := testHandler.channelMentionedAgents(ctx, testWorkspaceID, channelID, content, parts)
	if len(agents) != 1 || uuidToString(agents[0].ID) != agentID {
		t.Fatalf("channelMentionedAgents = %+v, want %s", agents, agentID)
	}

	msg, err := testHandler.insertChannelMessageWithParts(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", content, parts, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert structured mention message: %v", err)
	}
	testHandler.notifyChannelMemberMentions(ctx, ch, msg)

	var count int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM inbox_item
		WHERE recipient_id = $1 AND type = 'mentioned' AND details->>'message_id' = $2`, memberID, msg.ID).Scan(&count); err != nil {
		t.Fatalf("count mention inbox: %v", err)
	}
	if count != 1 {
		t.Fatalf("mention inbox count = %d, want 1", count)
	}
}

func TestChannelBareIssueReferencesBecomeStructuredMessageParts(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	issueID := createTestIssue(t, "Structured issue reference", "todo", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })

	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	workspace, err := testHandler.Queries.GetWorkspace(ctx, parseUUID(testWorkspaceID))
	if err != nil {
		t.Fatalf("load workspace: %v", err)
	}
	identifier := workspace.IssuePrefix + "-" + strconv.Itoa(int(issue.Number))

	channelID := seedChannelForTest(t, "structured-issue-references-"+uuid.NewString()[:8], testUserID)
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}

	content := "现关闭 " + identifier + "。" + identifier + " 已按指示收尾，再次确认 " + identifier + "。"
	parts := []protocol.MessagePart{{
		Type: protocol.MessagePartTypeText,
		Text: "also see " + identifier,
	}}
	gotContent, gotParts, err := testHandler.enrichChannelMessageMentions(ctx, ch, content, parts)
	if err != nil {
		t.Fatalf("enrich issue references: %v", err)
	}
	if gotContent != content {
		t.Fatalf("content = %q, want bare identifier text unchanged %q", gotContent, content)
	}

	var references []protocol.MessagePart
	for _, part := range gotParts {
		if part.Type == protocol.MessagePartTypeReference && part.RefType == "issue-ref" {
			references = append(references, part)
		}
	}
	if len(references) != 3 {
		t.Fatalf("issue references = %+v, want one anchored typed ref per visible occurrence", references)
	}
	searchFrom := 0
	for i, ref := range references {
		if ref.RefSubType != "issue" || ref.RefID != issueID || ref.Label != identifier {
			t.Fatalf("issue reference[%d] = %+v, want canonical issue anchor %s / %q", i, ref, issueID, identifier)
		}
		if ref.ContentStartUTF16 == nil || ref.ContentEndUTF16 == nil || *ref.ContentStartUTF16 >= *ref.ContentEndUTF16 {
			t.Fatalf("issue reference[%d] is missing a content UTF-16 span: %+v", i, ref)
		}
		byteOffset := strings.Index(content[searchFrom:], identifier)
		if byteOffset < 0 {
			t.Fatalf("could not find visible identifier occurrence %d in %q", i, content)
		}
		byteStart := searchFrom + byteOffset
		wantStart, wantEnd := contentUTF16Span(content, byteStart, byteStart+len(identifier))
		if *ref.ContentStartUTF16 != wantStart || *ref.ContentEndUTF16 != wantEnd {
			t.Fatalf("issue reference[%d] span = [%d,%d), want exact occurrence [%d,%d)", i, *ref.ContentStartUTF16, *ref.ContentEndUTF16, wantStart, wantEnd)
		}
		searchFrom = byteStart + len(identifier)
	}

	codeOnly := "`" + identifier + "` [" + identifier + "](mention://issue/" + issueID + ")"
	_, noRefs, err := testHandler.enrichChannelMessageMentions(ctx, ch, codeOnly, nil)
	if err != nil {
		t.Fatalf("enrich issue markdown: %v", err)
	}
	for _, part := range noRefs {
		if part.Type == protocol.MessagePartTypeReference && part.RefType == "issue-ref" {
			t.Fatalf("code/legacy markdown text unexpectedly produced typed issue ref: %+v", part)
		}
	}
}

// TestChannelReferenceLinksBecomeStructuredMessageParts is task #912's
// backend half of Felix's PR #1607 (FE-only, ChannelReferenceExtension):
// the composer emits an already-resolved [Label](mention://channel/<id>)
// link, and the server's job is to verify + anchor it, not fuzzy-match text.
func TestChannelReferenceLinksBecomeStructuredMessageParts(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	targetChannelID := seedChannelForTest(t, "channel-ref-target-"+uuid.NewString()[:8], testUserID)
	sourceChannelID := seedChannelForTest(t, "channel-ref-source-"+uuid.NewString()[:8], testUserID)
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(sourceChannelID))
	if !found {
		t.Fatal("source channel not found after seed")
	}

	label := "team-a\\[eu\\]" // an escaped literal bracket in the label
	content := "see [" + label + "](mention://channel/" + targetChannelID + ") for context"
	gotContent, gotParts, err := testHandler.enrichChannelMessageMentions(ctx, ch, content, nil)
	if err != nil {
		t.Fatalf("enrich channel reference: %v", err)
	}
	if gotContent != content {
		t.Fatalf("content = %q, want markdown link text unchanged %q", gotContent, content)
	}

	var refs []protocol.MessagePart
	for _, part := range gotParts {
		if part.Type == protocol.MessagePartTypeReference && part.RefType == "channel-ref" {
			refs = append(refs, part)
		}
	}
	if len(refs) != 1 {
		t.Fatalf("channel references = %+v, want exactly 1", refs)
	}
	ref := refs[0]
	if ref.RefID != targetChannelID || ref.Label != "team-a[eu]" {
		t.Fatalf("channel reference = %+v, want ref_id=%s label=%q (unescaped)", ref, targetChannelID, "team-a[eu]")
	}
	if ref.ContentStartUTF16 == nil || ref.ContentEndUTF16 == nil || *ref.ContentStartUTF16 >= *ref.ContentEndUTF16 {
		t.Fatalf("channel reference is missing a content UTF-16 span: %+v", ref)
	}
	wantStart, wantEnd := contentUTF16Span(content, strings.Index(content, "["), strings.Index(content, ")")+1)
	if *ref.ContentStartUTF16 != wantStart || *ref.ContentEndUTF16 != wantEnd {
		t.Fatalf("channel reference span = [%d,%d), want the whole markdown link [%d,%d)", *ref.ContentStartUTF16, *ref.ContentEndUTF16, wantStart, wantEnd)
	}

	// A reference to a channel ID that doesn't exist in this workspace is
	// dropped, not an error — the composer shouldn't be able to author one,
	// but a dangling/foreign ID must never surface as a broken structured ref.
	danglingContent := "see [ghost](mention://channel/" + uuid.NewString() + ")"
	_, danglingParts, err := testHandler.enrichChannelMessageMentions(ctx, ch, danglingContent, nil)
	if err != nil {
		t.Fatalf("enrich dangling channel reference: %v", err)
	}
	for _, part := range danglingParts {
		if part.Type == protocol.MessagePartTypeReference && part.RefType == "channel-ref" {
			t.Fatalf("dangling channel id unexpectedly produced a channel-ref: %+v", part)
		}
	}

	// A reference to a real channel ID that is a DM (not a linkable group
	// channel) is dropped too — DMs are private 1:1s, not the shareable
	// channel-ref target the composer's own suggestion list excludes them for.
	dmAgentID := createHandlerTestAgent(t, "Channel Ref DM Peer "+uuid.NewString(), []byte("[]"))
	dmChannelID := seedAgentDMChannel(t, dmAgentID)
	dmContent := "see [dm](mention://channel/" + dmChannelID + ")"
	_, dmParts, err := testHandler.enrichChannelMessageMentions(ctx, ch, dmContent, nil)
	if err != nil {
		t.Fatalf("enrich dm channel reference: %v", err)
	}
	for _, part := range dmParts {
		if part.Type == protocol.MessagePartTypeReference && part.RefType == "channel-ref" {
			t.Fatalf("dm channel id unexpectedly produced a channel-ref: %+v", part)
		}
	}
}

// TestChannelBareChannelReferencesBecomeStructuredMessageParts is LRM-1153:
// agents (and humans typing without the composer's picker) write a plain
// `#channel-name` in prose. Before this resolver only the composer's
// [Label](mention://channel/<id>) link produced a channel-ref, so those bare
// occurrences shipped with no parts at all and the FE — which already renders
// channel-ref as a ChannelChip — had nothing to render, leaving raw `#name`
// text next to correctly chipped @mentions and issue refs in the same message.
func TestChannelBareChannelReferencesBecomeStructuredMessageParts(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	targetName := "pr-frontend-" + suffix
	targetChannelID := seedChannelForTest(t, targetName, testUserID)
	sourceChannelID := seedChannelForTest(t, "bare-channel-ref-source-"+suffix, testUserID)
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(sourceChannelID))
	if !found {
		t.Fatal("source channel not found after seed")
	}

	content := "巡检增量 #" + targetName + " 新反馈 → 详见 #" + targetName + "。"
	gotContent, gotParts, err := testHandler.enrichChannelMessageMentions(ctx, ch, content, nil)
	if err != nil {
		t.Fatalf("enrich bare channel references: %v", err)
	}
	if gotContent != content {
		t.Fatalf("content = %q, want bare channel text unchanged %q", gotContent, content)
	}

	var refs []protocol.MessagePart
	for _, part := range gotParts {
		if part.Type == protocol.MessagePartTypeReference && part.RefType == "channel-ref" {
			refs = append(refs, part)
		}
	}
	if len(refs) != 2 {
		t.Fatalf("channel references = %+v, want one anchored typed ref per visible #name occurrence", refs)
	}
	// The anchored span must cover the whole `#name` token (including the hash)
	// so the FE replaces the raw text with the chip instead of rendering a
	// stray leading `#`. The label stays the bare channel name, matching the
	// composer-authored link path (ChannelChip strips a leading `#` itself and
	// message-preview re-adds exactly one).
	token := "#" + targetName
	searchFrom := 0
	for i, ref := range refs {
		if ref.RefID != targetChannelID || ref.Label != targetName {
			t.Fatalf("channel reference[%d] = %+v, want ref_id=%s label=%q", i, ref, targetChannelID, targetName)
		}
		if ref.ContentStartUTF16 == nil || ref.ContentEndUTF16 == nil || *ref.ContentStartUTF16 >= *ref.ContentEndUTF16 {
			t.Fatalf("channel reference[%d] is missing a content UTF-16 span: %+v", i, ref)
		}
		byteOffset := strings.Index(content[searchFrom:], token)
		if byteOffset < 0 {
			t.Fatalf("could not find visible #name occurrence %d in %q", i, content)
		}
		byteStart := searchFrom + byteOffset
		wantStart, wantEnd := contentUTF16Span(content, byteStart, byteStart+len(token))
		if *ref.ContentStartUTF16 != wantStart || *ref.ContentEndUTF16 != wantEnd {
			t.Fatalf("channel reference[%d] span = [%d,%d), want exact #name occurrence [%d,%d)", i, *ref.ContentStartUTF16, *ref.ContentEndUTF16, wantStart, wantEnd)
		}
		searchFrom = byteStart + len(token)
	}

	// The composer-authored link must still resolve to exactly one ref — the
	// bare matcher must not double-anchor the label inside the markdown link.
	linked := "see [" + targetName + "](mention://channel/" + targetChannelID + ") plus `#" + targetName + "` in code"
	_, linkedParts, err := testHandler.enrichChannelMessageMentions(ctx, ch, linked, nil)
	if err != nil {
		t.Fatalf("enrich linked channel reference: %v", err)
	}
	var linkedRefs []protocol.MessagePart
	for _, part := range linkedParts {
		if part.Type == protocol.MessagePartTypeReference && part.RefType == "channel-ref" {
			linkedRefs = append(linkedRefs, part)
		}
	}
	if len(linkedRefs) != 1 {
		t.Fatalf("channel references = %+v, want only the composer link anchored (no code span, no nested label match)", linkedRefs)
	}

	// A DM channel is never a shareable bare target, and an unknown #token is
	// left as plain prose rather than guessed at.
	dmAgentID := createHandlerTestAgent(t, "Bare Channel Ref DM Peer "+uuid.NewString(), []byte("[]"))
	dmChannelID := seedAgentDMChannel(t, dmAgentID)
	var dmName string
	if err := testPool.QueryRow(ctx, `SELECT name FROM channel WHERE id = $1`, parseUUID(dmChannelID)).Scan(&dmName); err != nil {
		t.Fatalf("load dm channel name: %v", err)
	}
	noise := "#" + dmName + " and #definitely-not-a-channel-" + suffix + " and #1973"
	_, noiseParts, err := testHandler.enrichChannelMessageMentions(ctx, ch, noise, nil)
	if err != nil {
		t.Fatalf("enrich non-channel tokens: %v", err)
	}
	for _, part := range noiseParts {
		if part.Type == protocol.MessagePartTypeReference && part.RefType == "channel-ref" {
			t.Fatalf("dm/unknown #token unexpectedly produced a channel-ref: %+v", part)
		}
	}
}

func TestChannelLegacyActorMentionMarkdownIsRejected(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	agentID := createHandlerTestAgent(t, "markdown_mention_agent_"+suffix, nil)
	memberID := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "structured-mention-normalize-"+suffix, testUserID, memberID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}

	issueID := uuid.NewString()
	content := "ping [@Legacy Agent](mention://agent/" + agentID + ") and [MUL-123](mention://issue/" + issueID + ")"
	parts := []protocol.MessagePart{{
		Type: protocol.MessagePartTypeText,
		Text: "cc [@Legacy Member](mention://member/" + memberID + ")",
	}}

	_, _, err := testHandler.enrichChannelMessageMentions(ctx, ch, content, parts)
	if err == nil || !strings.Contains(err.Error(), "legacy actor mention syntax") {
		t.Fatalf("legacy actor mention error = %v, want explicit rejection", err)
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
		VALUES ($1, $2, 'user', $3), ($1, $2, 'agent', $4)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, testUserID, agentID); err != nil {
		t.Fatalf("seed members: %v", err)
	}

	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}

	// Agent posts a message that @-mentions the human member through a
	// structured reference, never a legacy actor URI.
	content := "On it @Tester — taking a look now."
	start := strings.Index(content, "@Tester")
	end := start + len("@Tester")
	startUTF16, endUTF16 := contentUTF16Span(content, start, end)
	parts := []protocol.MessagePart{{Type: protocol.MessagePartTypeReference, RefType: "mention", RefSubType: "member", RefID: testUserID, Label: "@Tester", ContentStartUTF16: &startUTF16, ContentEndUTF16: &endUTF16}}
	msg, err := testHandler.insertChannelMessageWithParts(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "agent", parseUUID(agentID), "Mention Bot", content, parts, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("t1"), 1)
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

func TestChannelMutedMemberMentionPiercesMute(t *testing.T) {
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
	content := "ping @Channel Plain Member"
	start := strings.Index(content, "@Channel Plain Member")
	end := start + len("@Channel Plain Member")
	startUTF16, endUTF16 := contentUTF16Span(content, start, end)
	parts := []protocol.MessagePart{{Type: protocol.MessagePartTypeReference, RefType: "mention", RefSubType: "member", RefID: memberID, Label: "@Channel Plain Member", ContentStartUTF16: &startUTF16, ContentEndUTF16: &endUTF16}}
	msg, err := testHandler.insertChannelMessageWithParts(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", content, parts, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("muted"), 0)
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
	if count != 1 {
		t.Fatalf("muted member received %d mention inbox item(s), want 1", count)
	}

	listed := listedChannelForUser(t, channelID, memberID)
	if listed == nil {
		t.Fatal("muted mentioned member cannot see channel")
	}
	if !listed.Muted || listed.MentionUnreadCount != 1 {
		t.Fatalf("muted mention channel state = muted:%t count:%d, want muted:true count:1", listed.Muted, listed.MentionUnreadCount)
	}
	if listed.NotifyLevel != "mentions" {
		t.Fatalf("legacy mute notify_level = %q, want mentions", listed.NotifyLevel)
	}
}

func TestChannelNotifyPreferenceAPIAndDualWrite(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	memberID := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "notify-pref-"+uuid.NewString(), testUserID, memberID)
	ctx := context.Background()

	putLevel := func(level string) map[string]any {
		t.Helper()
		req := newRequestAs(memberID, http.MethodPut, "/api/channels/"+channelID+"/notify-preference", map[string]string{"level": level})
		req = withChannelTestWorkspaceCtx(t, req, memberID)
		req = withURLParam(req, "channelId", channelID)
		rec := httptest.NewRecorder()
		testHandler.SetChannelNotifyPreference(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("put notify-preference %q: status=%d body=%s", level, rec.Code, rec.Body.String())
		}
		var resp map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode notify-preference response: %v", err)
		}
		return resp
	}

	loadState := func() (notifyLevel *string, mutedAtSet bool) {
		t.Helper()
		var level pgtype.Text
		var mutedAt pgtype.Timestamptz
		if err := testPool.QueryRow(ctx, `
			SELECT notify_level, muted_at
			FROM channel_member
			WHERE channel_id = $1 AND member_type = 'user' AND member_id = $2`,
			channelID, memberID).Scan(&level, &mutedAt); err != nil {
			t.Fatalf("load channel_member notify state: %v", err)
		}
		if level.Valid {
			s := level.String
			notifyLevel = &s
		}
		return notifyLevel, mutedAt.Valid
	}

	// default list value before any write
	listed := listedChannelForUser(t, channelID, memberID)
	if listed == nil {
		t.Fatal("channel missing from list")
	}
	if listed.NotifyLevel != "default" {
		t.Fatalf("initial notify_level = %q, want default", listed.NotifyLevel)
	}

	resp := putLevel("mentions")
	if resp["ok"] != true || resp["notify_level"] != "mentions" {
		t.Fatalf("mentions response = %#v", resp)
	}
	level, muted := loadState()
	if level == nil || *level != "mentions" || !muted {
		t.Fatalf("mentions db state level=%v muted=%v", level, muted)
	}
	listed = listedChannelForUser(t, channelID, memberID)
	if listed.NotifyLevel != "mentions" || !listed.Muted || listed.MutedAt == nil {
		t.Fatalf("list after mentions: level=%q muted=%t muted_at=%v", listed.NotifyLevel, listed.Muted, listed.MutedAt)
	}

	resp = putLevel("muted")
	if resp["notify_level"] != "muted" {
		t.Fatalf("muted response = %#v", resp)
	}
	level, muted = loadState()
	if level == nil || *level != "muted" || !muted {
		t.Fatalf("muted db state level=%v muted=%v", level, muted)
	}

	resp = putLevel("all")
	if resp["notify_level"] != "all" {
		t.Fatalf("all response = %#v", resp)
	}
	level, muted = loadState()
	if level == nil || *level != "all" || muted {
		t.Fatalf("all db state level=%v muted=%v (want all, muted_at cleared)", level, muted)
	}
	listed = listedChannelForUser(t, channelID, memberID)
	if listed.NotifyLevel != "all" || listed.Muted || listed.MutedAt != nil {
		t.Fatalf("list after all: level=%q muted=%t muted_at=%v", listed.NotifyLevel, listed.Muted, listed.MutedAt)
	}

	resp = putLevel("default")
	if resp["notify_level"] != "default" {
		t.Fatalf("default response = %#v", resp)
	}
	level, muted = loadState()
	if level != nil || muted {
		t.Fatalf("default db state level=%v muted=%v (want NULL/cleared)", level, muted)
	}
	listed = listedChannelForUser(t, channelID, memberID)
	if listed.NotifyLevel != "default" {
		t.Fatalf("list after default: level=%q", listed.NotifyLevel)
	}

	// invalid level → 400
	req := newRequestAs(memberID, http.MethodPut, "/api/channels/"+channelID+"/notify-preference", map[string]string{"level": "loud"})
	req = withChannelTestWorkspaceCtx(t, req, memberID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.SetChannelNotifyPreference(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid level status=%d, want 400", rec.Code)
	}

	// legacy mute → mentions; unmute → default
	req = newRequestAs(memberID, http.MethodPut, "/api/channels/"+channelID+"/mute", nil)
	req = withChannelTestWorkspaceCtx(t, req, memberID)
	req = withURLParam(req, "channelId", channelID)
	rec = httptest.NewRecorder()
	testHandler.MuteChannel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("legacy mute status=%d", rec.Code)
	}
	level, muted = loadState()
	if level == nil || *level != "mentions" || !muted {
		t.Fatalf("legacy mute dual-write level=%v muted=%v", level, muted)
	}

	req = newRequestAs(memberID, http.MethodDelete, "/api/channels/"+channelID+"/mute", nil)
	req = withChannelTestWorkspaceCtx(t, req, memberID)
	req = withURLParam(req, "channelId", channelID)
	rec = httptest.NewRecorder()
	testHandler.UnmuteChannel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("legacy unmute status=%d", rec.Code)
	}
	level, muted = loadState()
	if level != nil || muted {
		t.Fatalf("legacy unmute dual-write level=%v muted=%v", level, muted)
	}
}

func TestListChannelsMentionUnreadCountTracksReadCursor(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	memberID := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "mention-unread-count-"+uuid.NewString(), testUserID, memberID)
	var memberName string
	if err := testPool.QueryRow(ctx, `SELECT name FROM "user" WHERE id = $1`, memberID).Scan(&memberName); err != nil {
		t.Fatalf("load mentioned member: %v", err)
	}

	insertMention := func(content string) {
		t.Helper()
		start := strings.Index(content, "@"+memberName)
		end := start + len("@"+memberName)
		startUTF16, endUTF16 := contentUTF16Span(content, start, end)
		parts := []protocol.MessagePart{{Type: protocol.MessagePartTypeReference, RefType: "mention", RefSubType: "member", RefID: memberID, Label: "@" + memberName, ContentStartUTF16: &startUTF16, ContentEndUTF16: &endUTF16}}
		if _, err := testHandler.insertChannelMessageWithParts(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", content, parts, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0); err != nil {
			t.Fatalf("insert structured mention %q: %v", content, err)
		}
	}
	insertMention("@" + memberName + " first")
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "ordinary unread message", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0); err != nil {
		t.Fatalf("insert ordinary message: %v", err)
	}
	insertMention("@" + memberName + " second")

	listed := listedChannelForUser(t, channelID, memberID)
	if listed == nil {
		t.Fatal("mentioned member cannot see channel")
	}
	if listed.MentionUnreadCount != 2 {
		t.Fatalf("mention count = %d, want 2", listed.MentionUnreadCount)
	}

	markChannelReadForTest(t, channelID, memberID)
	listed = listedChannelForUser(t, channelID, memberID)
	if listed == nil {
		t.Fatal("mentioned member cannot see channel after marking read")
	}
	if listed.MentionUnreadCount != 0 {
		t.Fatalf("mention count after read = %d, want 0", listed.MentionUnreadCount)
	}
}

func TestListChannelsBoundsMemberAvatarStack(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	members := []string{testUserID}
	for i := 0; i < channelListMemberAvatarLimit+2; i++ {
		members = append(members, createChannelPlainMember(t))
	}
	channelID := seedChannelForTest(t, "member-stack-bound-"+uuid.NewString(), members...)

	listed := listedChannelForUser(t, channelID, testUserID)
	if listed == nil {
		t.Fatal("seeded channel missing from list")
	}
	if len(listed.Members) != channelListMemberAvatarLimit {
		t.Fatalf("listed members = %d, want capped stack %d", len(listed.Members), channelListMemberAvatarLimit)
	}
	if listed.Members[0].MemberID != testUserID {
		t.Fatalf("first avatar member = %s, want channel's first member %s", listed.Members[0].MemberID, testUserID)
	}
}

func TestListChannelsUsesMaintainedMainUnreadCount(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	otherID := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "main-unread-count-"+uuid.NewString(), testUserID, otherID)
	first, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(otherID), "Other", "unread one", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert first unread message: %v", err)
	}
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(otherID), "Other", "unread two", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0); err != nil {
		t.Fatalf("insert second unread message: %v", err)
	}
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "own message ignored", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0); err != nil {
		t.Fatalf("insert own message: %v", err)
	}
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "system", pgtype.UUID{}, "system", "system ignored", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0); err != nil {
		t.Fatalf("insert system message: %v", err)
	}
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(otherID), "Other", "thread reply ignored", "multica", nil, pgtype.UUID{}, parseUUID(first.ID), first.ThreadID, 0); err != nil {
		t.Fatalf("insert thread reply: %v", err)
	}

	listed := listedChannelForUser(t, channelID, testUserID)
	if listed == nil {
		t.Fatal("seeded channel missing from list")
	}
	if listed.RealUnreadCount != 2 || listed.UnreadCount != 2 {
		t.Fatalf("unread counts = real:%d display:%d, want 2", listed.RealUnreadCount, listed.UnreadCount)
	}

	markChannelReadForTest(t, channelID, testUserID)
	listed = listedChannelForUser(t, channelID, testUserID)
	if listed == nil || listed.RealUnreadCount != 0 || listed.UnreadCount != 0 {
		t.Fatalf("unread counts after read = %+v, want zero", listed)
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

func TestChannelPayloadsIncludeAgentAvatarURL(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Avatar Payload Bot", nil)
	avatarURL := "/files/agent-avatar.png"
	if _, err := testPool.Exec(ctx, `UPDATE agent SET avatar_url = $1 WHERE id = $2`, avatarURL, agentID); err != nil {
		t.Fatalf("seed agent avatar: %v", err)
	}
	viewerID := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "avatar-payload-"+uuid.NewString(), testUserID, viewerID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent channel member: %v", err)
	}
	content := "agent avatar payload " + uuid.NewString()
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "agent", parseUUID(agentID), "Avatar Payload Bot", content, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0); err != nil {
		t.Fatalf("insert agent message: %v", err)
	}

	messages := listedMessagesForUser(t, channelID, viewerID)
	var listed *ChannelMessageResponse
	for i := range messages {
		if messages[i].Content == content {
			listed = &messages[i]
			break
		}
	}
	if listed == nil {
		t.Fatalf("agent message %q not listed in %+v", content, messages)
	}
	if listed.AuthorAvatarURL == nil || *listed.AuthorAvatarURL != avatarURL {
		t.Fatalf("message author_avatar_url = %v, want %q", listed.AuthorAvatarURL, avatarURL)
	}

	req := newRequestAs(viewerID, http.MethodGet, "/api/channels/"+channelID+"/members", nil)
	req = withChannelTestWorkspaceCtx(t, req, viewerID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.ListChannelMembers(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list channel members: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var members []ChannelMemberResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &members); err != nil {
		t.Fatalf("decode channel members: %v", err)
	}
	var agentMember *ChannelMemberResponse
	for i := range members {
		if members[i].MemberType == "agent" && members[i].MemberID == agentID {
			agentMember = &members[i]
			break
		}
	}
	if agentMember == nil {
		t.Fatalf("agent member %s not listed in %+v", agentID, members)
	}
	if agentMember.AvatarURL == nil || *agentMember.AvatarURL != avatarURL {
		t.Fatalf("member avatar_url = %v, want %q", agentMember.AvatarURL, avatarURL)
	}

	channel := listedChannelForUser(t, channelID, viewerID)
	if channel == nil {
		t.Fatalf("channel %s not listed for viewer", channelID)
	}
	var brief *ChannelMemberBrief
	for i := range channel.Members {
		if channel.Members[i].MemberType == "agent" && channel.Members[i].MemberID == agentID {
			brief = &channel.Members[i]
			break
		}
	}
	if brief == nil {
		t.Fatalf("agent member brief %s not listed in %+v", agentID, channel.Members)
	}
	if brief.AvatarURL == nil || *brief.AvatarURL != avatarURL {
		t.Fatalf("member brief avatar_url = %v, want %q", brief.AvatarURL, avatarURL)
	}
}

func TestSendChannelMessageClientMessageIDDedupesTopLevelWithSideEffects(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgentOnRuntime(t, "Idempotency Group Bot", handlerTestRuntimeID(t))
	channelID := seedChannelForTest(t, "client-id-group-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
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
	var deliveryCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_message_delivery
		WHERE message_id = $1 AND agent_id = $2`, created.ID, agentID).Scan(&deliveryCount); err != nil {
		t.Fatalf("count canonical deliveries: %v", err)
	}
	if deliveryCount != 1 {
		t.Fatalf("canonical delivery count = %d, want 1", deliveryCount)
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
	agentID := createHandlerTestAgentOnRuntime(t, "DM Idempotency Bot", handlerTestRuntimeID(t))
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
	var deliveryCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_message_delivery d
		JOIN channel_message m ON m.id = d.message_id
		WHERE m.channel_id = $1 AND m.client_message_id = $2 AND d.agent_id = $3`, channelID, clientID, agentID).Scan(&deliveryCount); err != nil {
		t.Fatalf("count canonical DM deliveries: %v", err)
	}
	if deliveryCount != 1 {
		t.Fatalf("canonical DM delivery count = %d, want 1", deliveryCount)
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

func TestSendChannelMessageThreadReplyStaysThreadOnly(t *testing.T) {
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
	secondSend := sendChannelThreadReplyForTest(t, channelID, root.ID, testUserID, map[string]any{
		"content": "second thread-only reply",
	})
	if secondSend.Code != http.StatusCreated {
		t.Fatalf("second thread reply: status=%d body=%s", secondSend.Code, secondSend.Body.String())
	}

	mainTimeline := listedMessagesForUser(t, channelID, testUserID)
	if len(mainTimeline) != 1 || mainTimeline[0].ID != root.ID {
		t.Fatalf("main timeline = %+v, want only root", mainTimeline)
	}
	if mainTimeline[0].ThreadReplyCount != 2 {
		t.Fatalf("thread reply count = %d, want 2", mainTimeline[0].ThreadReplyCount)
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
			{Type: protocol.MessagePartTypeAttachment, AttachmentID: attachmentID},
		},
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
	if len(duplicate.Parts) != 3 ||
		duplicate.Parts[0].Type != protocol.MessagePartTypeText ||
		duplicate.Parts[1].Type != protocol.MessagePartTypeSticker ||
		duplicate.Parts[1].PackID != "builtin" ||
		duplicate.Parts[1].StickerID != "hi" ||
		duplicate.Parts[2].Type != protocol.MessagePartTypeAttachment ||
		duplicate.Parts[2].AttachmentID != attachmentID {
		t.Fatalf("duplicate parts = %+v, want text + sticker + attachment", duplicate.Parts)
	}

	var bound int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message_attachment
		WHERE attachment_id = $1 AND channel_message_id = $2`, attachmentID, duplicate.ID).Scan(&bound); err != nil {
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

func TestSendChannelMessageAttachmentOnlyFromParts(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	channelID := seedChannelForTest(t, "attachment-only-parts-"+uuid.NewString(), testUserID)
	var attachmentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO attachment (workspace_id, channel_id, uploader_type, uploader_id, filename, url, content_type, size_bytes)
		VALUES ($1, $2, 'member', $3, 'photo.png', 's3://photo.png', 'image/png', 42)
		RETURNING id`, testWorkspaceID, channelID, testUserID).Scan(&attachmentID); err != nil {
		t.Fatalf("seed attachment: %v", err)
	}

	rec := sendChannelMessageForTest(t, channelID, testUserID, map[string]any{
		"content": "",
		"parts": []protocol.MessagePart{{
			Type:         protocol.MessagePartTypeAttachment,
			AttachmentID: attachmentID,
			Filename:     "photo.png",
			ContentType:  "image/png",
			SizeBytes:    42,
		}},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("attachment-only send: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var created ChannelMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created message: %v", err)
	}
	if created.Content != "" {
		t.Fatalf("content = %q, want empty (no markdown image URL)", created.Content)
	}
	if strings.Contains(created.Content, "![") || strings.Contains(created.Content, "s3://") {
		t.Fatalf("content must not contain markdown/media URL, got %q", created.Content)
	}
	if len(created.Parts) != 1 || created.Parts[0].Type != protocol.MessagePartTypeAttachment || created.Parts[0].AttachmentID != attachmentID {
		t.Fatalf("parts = %+v, want single attachment part", created.Parts)
	}
	if len(created.Attachments) != 1 || created.Attachments[0].ID != attachmentID {
		t.Fatalf("attachments = %+v, want bound attachment %s", created.Attachments, attachmentID)
	}

	var boundMessageID string
	if err := testPool.QueryRow(ctx, `
		SELECT channel_message_id::text
		FROM channel_message_attachment
		WHERE attachment_id = $1`, attachmentID).Scan(&boundMessageID); err != nil {
		t.Fatalf("load attachment binding: %v", err)
	}
	if boundMessageID != created.ID {
		t.Fatalf("attachment bound to message %q, want %q", boundMessageID, created.ID)
	}
}

func TestSendChannelMessageReusesOwnedAttachmentAcrossChannels(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	firstChannelID := seedChannelForTest(t, "attachment-reuse-first-"+uuid.NewString(), testUserID)
	secondChannelID := seedChannelForTest(t, "attachment-reuse-second-"+uuid.NewString(), testUserID)
	var attachmentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO attachment (workspace_id, channel_id, uploader_type, uploader_id, filename, url, content_type, size_bytes)
		VALUES ($1, $2, 'member', $3, 'shared.png', 's3://shared.png', 'image/png', 42)
		RETURNING id`, testWorkspaceID, firstChannelID, testUserID).Scan(&attachmentID); err != nil {
		t.Fatalf("seed reusable attachment: %v", err)
	}

	send := func(channelID, content string) ChannelMessageResponse {
		t.Helper()
		rec := sendChannelMessageForTest(t, channelID, testUserID, map[string]any{
			"content": content,
			"parts": []protocol.MessagePart{
				{Type: protocol.MessagePartTypeText, Text: content},
				{Type: protocol.MessagePartTypeAttachment, AttachmentID: attachmentID},
			},
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("send shared attachment to channel %s: status=%d body=%s", channelID, rec.Code, rec.Body.String())
		}
		var message ChannelMessageResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &message); err != nil {
			t.Fatalf("decode shared attachment send: %v", err)
		}
		if len(message.Attachments) != 1 || message.Attachments[0].ID != attachmentID {
			t.Fatalf("message attachments=%+v, want shared attachment %s", message.Attachments, attachmentID)
		}
		return message
	}

	first := send(firstChannelID, "first channel copy")
	second := send(secondChannelID, "second channel copy")
	if first.ID == second.ID {
		t.Fatalf("two sends unexpectedly reused message id %s", first.ID)
	}

	var referenceCount, distinctMessageCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*), count(DISTINCT channel_message_id)
		FROM channel_message_attachment
		WHERE attachment_id = $1`, attachmentID).Scan(&referenceCount, &distinctMessageCount); err != nil {
		t.Fatalf("load reusable attachment references: %v", err)
	}
	if referenceCount != 2 || distinctMessageCount != 2 {
		t.Fatalf("reusable attachment references/messages=%d/%d, want 2/2", referenceCount, distinctMessageCount)
	}
}

func TestUpdateChannelMessageReplacesAttachmentReferencesFromParts(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	channelID := seedChannelForTest(t, "attachment-edit-references-"+uuid.NewString(), testUserID)
	seed := func(filename string) string {
		t.Helper()
		var attachmentID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO attachment (workspace_id, channel_id, uploader_type, uploader_id, filename, url, content_type, size_bytes)
			VALUES ($1, $2, 'member', $3, $4, 's3://'||$4, 'image/png', 42)
			RETURNING id`, testWorkspaceID, channelID, testUserID, filename).Scan(&attachmentID); err != nil {
			t.Fatalf("seed attachment %s: %v", filename, err)
		}
		return attachmentID
	}
	firstAttachmentID := seed("before-edit.png")
	secondAttachmentID := seed("after-edit.png")

	createdRec := sendChannelMessageForTest(t, channelID, testUserID, map[string]any{
		"content": "before edit",
		"parts": []protocol.MessagePart{
			{Type: protocol.MessagePartTypeText, Text: "before edit"},
			{Type: protocol.MessagePartTypeAttachment, AttachmentID: firstAttachmentID},
		},
	})
	if createdRec.Code != http.StatusCreated {
		t.Fatalf("create message before edit: status=%d body=%s", createdRec.Code, createdRec.Body.String())
	}
	var created ChannelMessageResponse
	if err := json.Unmarshal(createdRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created message: %v", err)
	}

	req := newRequest(http.MethodPatch, "/api/channels/"+channelID+"/messages/"+created.ID, map[string]any{
		"content": "after edit",
		"parts": []protocol.MessagePart{
			{Type: protocol.MessagePartTypeText, Text: "after edit"},
			{Type: protocol.MessagePartTypeAttachment, AttachmentID: secondAttachmentID},
		},
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withRouteParams(req, "channelId", channelID, "messageId", created.ID)
	rec := httptest.NewRecorder()
	testHandler.UpdateChannelMessage(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("replace message attachment: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var updated ChannelMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated message: %v", err)
	}
	if len(updated.Attachments) != 1 || updated.Attachments[0].ID != secondAttachmentID {
		t.Fatalf("updated attachments=%+v, want only %s", updated.Attachments, secondAttachmentID)
	}

	var firstReferences, secondReferences int
	if err := testPool.QueryRow(ctx, `
		SELECT
		  count(*) FILTER (WHERE attachment_id = $2),
		  count(*) FILTER (WHERE attachment_id = $3)
		FROM channel_message_attachment
		WHERE channel_message_id = $1`, created.ID, firstAttachmentID, secondAttachmentID).Scan(&firstReferences, &secondReferences); err != nil {
		t.Fatalf("load edited attachment references: %v", err)
	}
	if firstReferences != 0 || secondReferences != 1 {
		t.Fatalf("edited attachment references before/after=%d/%d, want 0/1", firstReferences, secondReferences)
	}
}

func TestSendChannelMessageTextWithTwoAttachmentParts(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	channelID := seedChannelForTest(t, "attachment-two-parts-"+uuid.NewString(), testUserID)
	seedUnbound := func(filename string) string {
		t.Helper()
		var id string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO attachment (workspace_id, channel_id, uploader_type, uploader_id, filename, url, content_type, size_bytes)
			VALUES ($1, $2, 'member', $3, $4, $5, 'image/png', 10)
			RETURNING id`, testWorkspaceID, channelID, testUserID, filename, "s3://"+filename).Scan(&id); err != nil {
			t.Fatalf("seed attachment %s: %v", filename, err)
		}
		return id
	}
	firstID := seedUnbound("first.png")
	secondID := seedUnbound("second.png")
	content := "here are two files " + uuid.NewString()

	rec := sendChannelMessageForTest(t, channelID, testUserID, map[string]any{
		"content": content,
		"parts": []protocol.MessagePart{
			{Type: protocol.MessagePartTypeText, Text: content},
			{Type: protocol.MessagePartTypeAttachment, AttachmentID: firstID, Filename: "first.png"},
			{Type: protocol.MessagePartTypeAttachment, AttachmentID: secondID, Filename: "second.png"},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("text + attachments send: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var created ChannelMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created message: %v", err)
	}
	if created.Content != content {
		t.Fatalf("content = %q, want %q", created.Content, content)
	}
	if len(created.Parts) != 3 ||
		created.Parts[0].Type != protocol.MessagePartTypeText ||
		created.Parts[1].Type != protocol.MessagePartTypeAttachment ||
		created.Parts[1].AttachmentID != firstID ||
		created.Parts[2].Type != protocol.MessagePartTypeAttachment ||
		created.Parts[2].AttachmentID != secondID {
		t.Fatalf("parts = %+v, want text then two attachment parts in order", created.Parts)
	}
	if len(created.Attachments) != 2 {
		t.Fatalf("attachments = %+v, want 2 bound attachments", created.Attachments)
	}
	gotIDs := map[string]struct{}{}
	for _, att := range created.Attachments {
		gotIDs[att.ID] = struct{}{}
	}
	if _, ok := gotIDs[firstID]; !ok {
		t.Fatalf("attachments missing first id %s: %+v", firstID, created.Attachments)
	}
	if _, ok := gotIDs[secondID]; !ok {
		t.Fatalf("attachments missing second id %s: %+v", secondID, created.Attachments)
	}

	var bound int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message_attachment
		WHERE channel_message_id = $1 AND attachment_id = ANY($2::uuid[])`,
		created.ID, []string{firstID, secondID}).Scan(&bound); err != nil {
		t.Fatalf("count bound attachments: %v", err)
	}
	if bound != 2 {
		t.Fatalf("bound attachment rows = %d, want 2", bound)
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

func TestSendChannelMessageStoresVoiceTranscriptPart(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	channelID := seedChannelForTest(t, "voice-parts-"+uuid.NewString(), testUserID)
	req := newRequest(http.MethodPost, "/api/channels/"+channelID+"/messages", map[string]any{
		"content": "spoken question",
		"parts": []protocol.MessagePart{
			{Type: protocol.MessagePartTypeText, Text: "spoken question"},
			{Type: protocol.MessagePartTypeVoice, DurationMS: 2400},
		},
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.SendChannelMessage(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("send voice parts: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var created ChannelMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created message: %v", err)
	}
	if created.Content != "spoken question" || len(created.Parts) != 2 ||
		created.Parts[1].Type != protocol.MessagePartTypeVoice || created.Parts[1].DurationMS != 2400 {
		t.Fatalf("created message = %+v, want transcript plus voice part", created)
	}
}

func TestSendChannelMessageBindsVoiceRecordingAttachment(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	channelID := seedChannelForTest(t, "voice-recording-"+uuid.NewString(), testUserID)
	var attachmentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO attachment (workspace_id, channel_id, uploader_type, uploader_id, filename, url, content_type, size_bytes)
		VALUES ($1, $2, 'member', $3, 'voice-recording.wav', 's3://voice-recording.wav', 'audio/wav', 48)
		RETURNING id`, testWorkspaceID, channelID, testUserID).Scan(&attachmentID); err != nil {
		t.Fatalf("seed voice recording: %v", err)
	}

	rec := sendChannelMessageForTest(t, channelID, testUserID, map[string]any{
		"content": "spoken question",
		"parts": []protocol.MessagePart{
			{Type: protocol.MessagePartTypeText, Text: "spoken question"},
			{
				Type:         protocol.MessagePartTypeVoice,
				DurationMS:   1800,
				AttachmentID: attachmentID,
				Filename:     "voice-recording.wav",
				ContentType:  "audio/wav",
				SizeBytes:    48,
			},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("send recorded voice: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var created ChannelMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created message: %v", err)
	}
	if len(created.Parts) != 2 || created.Parts[1].AttachmentID != attachmentID {
		t.Fatalf("created parts = %+v, want voice attachment %s", created.Parts, attachmentID)
	}
	if len(created.Attachments) != 1 || created.Attachments[0].ID != attachmentID {
		t.Fatalf("created attachments = %+v, want recording %s", created.Attachments, attachmentID)
	}

	var boundMessageID string
	if err := testPool.QueryRow(ctx, `
		SELECT channel_message_id::text FROM channel_message_attachment WHERE attachment_id = $1`, attachmentID).Scan(&boundMessageID); err != nil {
		t.Fatalf("load recording binding: %v", err)
	}
	if boundMessageID != created.ID {
		t.Fatalf("recording bound to %q, want %q", boundMessageID, created.ID)
	}
}

func TestAttachmentIDsFromPartsIncludesVoiceRecording(t *testing.T) {
	ids := attachmentIDsFromParts([]protocol.MessagePart{
		{Type: protocol.MessagePartTypeText, Text: "transcript"},
		{Type: protocol.MessagePartTypeVoice, AttachmentID: "recording-id"},
	})
	if len(ids) != 1 || ids[0] != "recording-id" {
		t.Fatalf("attachmentIDsFromParts = %v, want voice recording id", ids)
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
		INSERT INTO attachment (workspace_id, channel_id, uploader_type, uploader_id, filename, url, content_type, size_bytes)
		VALUES ($1, $2, 'member', $3, 'secret.png', 's3://secret.png', 'image/png', 12)
		RETURNING id`, testWorkspaceID, channelID, testUserID).Scan(&attachmentID); err != nil {
		t.Fatalf("seed channel message attachment: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_message_attachment (workspace_id, channel_message_id, attachment_id)
		VALUES ($1, $2, $3)`, testWorkspaceID, msg.ID, attachmentID); err != nil {
		t.Fatalf("seed channel message attachment reference: %v", err)
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
	var tombstone *ChannelMessageResponse
	for i := range page.Messages {
		if page.Messages[i].ID == msg.ID {
			tombstone = &page.Messages[i]
			break
		}
	}
	if tombstone == nil || tombstone.Content != "" || len(tombstone.Parts) != 0 || tombstone.EditedAt == nil || tombstone.DeletedAt == nil {
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
		FROM channel_message_attachment
		WHERE attachment_id = $1 AND channel_message_id = $2`, attachmentID, msg.ID).Scan(&stillBound); err != nil {
		t.Fatalf("count attachment binding: %v", err)
	}
	if stillBound != 0 {
		t.Fatalf("attachment binding rows = %d, want removed when edit drops the attachment part", stillBound)
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
	var attachmentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO attachment (workspace_id, channel_id, uploader_type, uploader_id, filename, url, content_type, size_bytes)
		VALUES ($1, $2, 'member', $3, 'secret.txt', 's3://secret.txt', 'text/plain', 9)
		RETURNING id`, testWorkspaceID, channelID, testUserID).Scan(&attachmentID); err != nil {
		t.Fatalf("seed attachment: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_message_attachment (workspace_id, channel_message_id, attachment_id)
		VALUES ($1, $2, $3)`, testWorkspaceID, msg.ID, attachmentID); err != nil {
		t.Fatalf("seed attachment reference: %v", err)
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

// LRM-874: author filter (user|agent) + include_thread + system exclusion.
func TestSearchChannelMessagesAuthorFilterAndIncludeThread(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	otherUserID := createChannelPlainMember(t)
	agentID := createHandlerTestAgent(t, "search-author-"+uuid.NewString(), nil)
	channelID := seedChannelForTest(t, "author-search-"+uuid.NewString(), testUserID, otherUserID)

	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "root by me", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("a1"), 0)
	if err != nil {
		t.Fatalf("insert root: %v", err)
	}
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(otherUserID), "Other", "by other user", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("a2"), 0); err != nil {
		t.Fatalf("insert other user: %v", err)
	}
	agentMsg, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "agent", parseUUID(agentID), "SearchBot", "agent says hello", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("a3"), 0)
	if err != nil {
		t.Fatalf("insert agent: %v", err)
	}
	threadReply, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "thread by me", "multica", nil, pgtype.UUID{}, parseUUID(root.ID), strPtr("a1"), 0)
	if err != nil {
		t.Fatalf("insert thread: %v", err)
	}
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "system", pgtype.UUID{}, "system", "system noise", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0); err != nil {
		t.Fatalf("insert system: %v", err)
	}

	// Author=me includes mainline + thread by default.
	req := newRequest(http.MethodGet, "/api/channels/"+channelID+"/messages/search?author_type=user&author_id="+testUserID+"&limit=20", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.SearchChannelMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("author search: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp ChannelMessageSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.IncludeThread || resp.AuthorType != "user" || resp.AuthorID != testUserID || resp.Scope != "channel" {
		t.Fatalf("echo fields = %+v", resp)
	}
	if resp.Total != 2 || len(resp.Results) != 2 {
		t.Fatalf("author=me want 2 hits, got total=%d results=%d (%+v)", resp.Total, len(resp.Results), resp.Results)
	}
	ids := map[string]bool{}
	for _, hit := range resp.Results {
		ids[hit.MessageID] = true
		if hit.Type != "user" {
			t.Fatalf("unexpected type %q", hit.Type)
		}
	}
	if !ids[root.ID] || !ids[threadReply.ID] {
		t.Fatalf("missing root/thread hits: %+v", ids)
	}
	for _, hit := range resp.Results {
		if hit.MessageID == threadReply.ID && !hit.InThread {
			t.Fatalf("thread reply should set in_thread")
		}
		if hit.MessageID == root.ID && hit.InThread {
			t.Fatalf("mainline should not set in_thread")
		}
	}

	// include_thread=false → only mainline by me.
	req = newRequest(http.MethodGet, "/api/channels/"+channelID+"/messages/search?author_type=user&author_id="+testUserID+"&include_thread=false&limit=20", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec = httptest.NewRecorder()
	testHandler.SearchChannelMessages(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode mainline: %v", err)
	}
	if resp.IncludeThread || resp.Total != 1 || len(resp.Results) != 1 || resp.Results[0].MessageID != root.ID {
		t.Fatalf("mainline-only response = %+v", resp)
	}

	// Agent author filter.
	req = newRequest(http.MethodGet, "/api/channels/"+channelID+"/messages/search?author_type=agent&author_id="+agentID+"&limit=20", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec = httptest.NewRecorder()
	testHandler.SearchChannelMessages(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode agent: %v", err)
	}
	if resp.Total != 1 || len(resp.Results) != 1 || resp.Results[0].MessageID != agentMsg.ID || resp.Results[0].Type != "agent" {
		t.Fatalf("agent author response = %+v", resp)
	}

	// Incomplete author filter → 400.
	req = newRequest(http.MethodGet, "/api/channels/"+channelID+"/messages/search?author_type=agent&limit=20", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec = httptest.NewRecorder()
	testHandler.SearchChannelMessages(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("incomplete author filter want 400 got %d", rec.Code)
	}
}

func TestSearchGlobalScopesAndPermissions(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	token := "globalsearch" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	otherUserID := createChannelWorkspaceMemberWithRole(t, "member")
	if _, err := testPool.Exec(ctx, `UPDATE "user" SET display_name = $1 WHERE id = $2`, "Search Person "+token, otherUserID); err != nil {
		t.Fatalf("seed searchable person: %v", err)
	}

	channelID := seedChannelForTest(t, "Search Channel "+token, testUserID)
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "visible message "+token, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("global-visible"), 0); err != nil {
		t.Fatalf("seed visible message: %v", err)
	}
	hiddenChannelID := seedChannelForTest(t, "Hidden Channel "+token, otherUserID)
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(hiddenChannelID), parseUUID(testWorkspaceID), "user", parseUUID(otherUserID), "Other", "hidden message "+token, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("global-hidden"), 0); err != nil {
		t.Fatalf("seed hidden message: %v", err)
	}
	dmID := seedChannelForTest(t, "Search DM "+token, testUserID)
	if _, err := testPool.Exec(ctx, `UPDATE channel SET kind = 'dm', description = $1 WHERE id = $2`, "dm "+token, dmID); err != nil {
		t.Fatalf("seed dm channel: %v", err)
	}

	req := newRequest(http.MethodGet, "/api/search?q="+token+"&scope=all&limit=10", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	rec := httptest.NewRecorder()
	testHandler.SearchGlobal(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("global search: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp GlobalSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode global search: %v", err)
	}
	if resp.Counts.Messages != 1 || len(resp.Messages) != 1 || resp.Messages[0].MessageID == "" {
		t.Fatalf("message search leaked or missed results: counts=%+v messages=%+v", resp.Counts, resp.Messages)
	}
	if resp.Messages[0].Snippet == "" || len(resp.Messages[0].HighlightRanges) != 1 {
		t.Fatalf("message search missing snippet/highlight: %+v", resp.Messages[0])
	}
	if resp.Counts.Channels != 1 || len(resp.Channels) != 1 || resp.Channels[0].ChannelID != channelID {
		t.Fatalf("channel search leaked or missed results: counts=%+v channels=%+v", resp.Counts, resp.Channels)
	}
	if resp.Counts.DMs != 1 || len(resp.DMs) != 1 || resp.DMs[0].ChannelID != dmID {
		t.Fatalf("dm search response = counts=%+v dms=%+v, want seeded dm only", resp.Counts, resp.DMs)
	}
	if resp.Counts.People != 1 || len(resp.People) != 1 || resp.People[0].ActorID != otherUserID {
		t.Fatalf("people search response = counts=%+v people=%+v, want seeded workspace member", resp.Counts, resp.People)
	}

	req = newRequest(http.MethodGet, "/api/search?q="+token+"&scope=messages&limit=10", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	rec = httptest.NewRecorder()
	testHandler.SearchGlobal(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("message-scope global search: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode message-scope global search: %v", err)
	}
	if len(resp.Messages) != 1 || len(resp.Channels) != 0 || len(resp.DMs) != 0 || len(resp.People) != 0 {
		t.Fatalf("scope=messages populated wrong buckets: %+v", resp)
	}

	req = newRequest(http.MethodGet, "/api/search?q="+token+"&scope=bad", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	rec = httptest.NewRecorder()
	testHandler.SearchGlobal(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_search_scope") {
		t.Fatalf("invalid scope response: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// LRM-874: workspace-scoped from:@ via GET /api/search?scope=messages&author_*.
func TestSearchGlobalMessagesByAuthor(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "ws-author-"+uuid.NewString(), nil)
	channelID := seedChannelForTest(t, "ws-author-ch-"+uuid.NewString(), testUserID)
	hiddenChannelID := seedChannelForTest(t, "ws-author-hidden-"+uuid.NewString(), createChannelPlainMember(t))

	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "agent", parseUUID(agentID), "Bot", "visible agent line", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("ws-a1"), 0); err != nil {
		t.Fatalf("seed visible agent msg: %v", err)
	}
	// Agent message in a channel the viewer cannot see must not leak.
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(hiddenChannelID), parseUUID(testWorkspaceID), "agent", parseUUID(agentID), "Bot", "hidden agent line", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("ws-a2"), 0); err != nil {
		t.Fatalf("seed hidden agent msg: %v", err)
	}

	req := newRequest(http.MethodGet, "/api/search?scope=messages&author_type=agent&author_id="+agentID+"&limit=20", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	rec := httptest.NewRecorder()
	testHandler.SearchGlobal(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("workspace author search: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp GlobalSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Counts.Messages != 1 || len(resp.Messages) != 1 {
		t.Fatalf("want 1 visible agent hit, got counts=%+v messages=%+v", resp.Counts, resp.Messages)
	}
	if resp.Messages[0].AuthorType != "agent" || resp.Messages[0].ChannelID != channelID {
		t.Fatalf("unexpected hit: %+v", resp.Messages[0])
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

func TestChannelPermanentDeleteRemovesChannelAndMessages(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	channelID := seedChannelForTest(t, "perm-delete-"+uuid.NewString(), testUserID)
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "bye forever", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0); err != nil {
		t.Fatalf("seed message: %v", err)
	}

	req := newRequest(http.MethodDelete, "/api/channels/"+channelID, nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.DeleteChannel(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete channel: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var exists bool
	if err := testPool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM channel WHERE id = $1)`, channelID).Scan(&exists); err != nil {
		t.Fatalf("check channel exists: %v", err)
	}
	if exists {
		t.Fatal("channel row still present after permanent delete")
	}
	var messageCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM channel_message WHERE channel_id = $1`, channelID).Scan(&messageCount); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if messageCount != 0 {
		t.Fatalf("messages remaining after delete: %d", messageCount)
	}
	if ch := listedChannelForTest(t, channelID); ch != nil {
		t.Fatalf("deleted channel still listed: %+v", *ch)
	}
	if ch := archivedListedChannelForTest(t, channelID); ch != nil {
		t.Fatalf("deleted channel still in archived list: %+v", *ch)
	}

	req = newRequest(http.MethodGet, "/api/channels/"+channelID+"/messages", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec = httptest.NewRecorder()
	testHandler.ListChannelMessages(rec, req)
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden {
		t.Fatalf("enter deleted channel: status=%d body=%s, want 404/403", rec.Code, rec.Body.String())
	}
}

func TestChannelPermanentDeleteWorksOnArchivedChannel(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	channelID := seedChannelForTest(t, "perm-delete-archived-"+uuid.NewString(), testUserID)
	req := newRequest(http.MethodPost, "/api/channels/"+channelID+"/archive", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.ArchiveChannel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("archive channel: status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = newRequest(http.MethodDelete, "/api/channels/"+channelID, nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec = httptest.NewRecorder()
	testHandler.DeleteChannel(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete archived channel: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var exists bool
	if err := testPool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM channel WHERE id = $1)`, channelID).Scan(&exists); err != nil {
		t.Fatalf("check channel exists: %v", err)
	}
	if exists {
		t.Fatal("archived channel still present after permanent delete")
	}
}

func TestChannelPermanentDeletePlainMemberForbidden(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	memberID := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "perm-delete-member-"+uuid.NewString(), testUserID, memberID)

	req := newRequestAs(memberID, http.MethodDelete, "/api/channels/"+channelID, nil)
	req = withChannelTestWorkspaceCtx(t, req, memberID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.DeleteChannel(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("plain member delete: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var exists bool
	if err := testPool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM channel WHERE id = $1)`, channelID).Scan(&exists); err != nil {
		t.Fatalf("check channel exists: %v", err)
	}
	if !exists {
		t.Fatal("plain member permanently deleted the channel")
	}
}

func TestChannelPermanentDeleteCreatorOnlyForbidden(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	// Channel creator who is only a workspace member may archive, but must not
	// permanently delete (owner/admin only).
	creatorID := createChannelPlainMember(t)
	ctx := context.Background()
	var channelID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, created_by)
		VALUES ($1, $2, $3)
		RETURNING id`, testWorkspaceID, "perm-delete-creator-"+uuid.NewString(), creatorID).Scan(&channelID); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID) })
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'user', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, creatorID); err != nil {
		t.Fatalf("seed creator membership: %v", err)
	}

	req := newRequestAs(creatorID, http.MethodDelete, "/api/channels/"+channelID, nil)
	req = withChannelTestWorkspaceCtx(t, req, creatorID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.DeleteChannel(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("creator-only delete: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var exists bool
	if err := testPool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM channel WHERE id = $1)`, channelID).Scan(&exists); err != nil {
		t.Fatalf("check channel exists: %v", err)
	}
	if !exists {
		t.Fatal("creator-only permanently deleted the channel")
	}
}

func TestChannelPermanentDeleteAdminAllowed(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	adminID := createChannelWorkspaceAdmin(t)
	channelID := seedChannelForTest(t, "perm-delete-admin-"+uuid.NewString(), testUserID, adminID)

	req := newRequestAs(adminID, http.MethodDelete, "/api/channels/"+channelID, nil)
	req = withChannelTestWorkspaceCtx(t, req, adminID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.DeleteChannel(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("admin delete: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var exists bool
	if err := testPool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM channel WHERE id = $1)`, channelID).Scan(&exists); err != nil {
		t.Fatalf("check channel exists: %v", err)
	}
	if exists {
		t.Fatal("admin delete left channel row")
	}
}

func TestChannelPermanentDeleteRejectsDM(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Perm Delete DM Bot", []byte("[]"))
	channelID := seedAgentDMChannel(t, agentID)
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID) })

	req := newRequest(http.MethodDelete, "/api/channels/"+channelID, nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.DeleteChannel(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("delete DM: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body["error"] != "direct messages cannot be permanently deleted" {
		t.Fatalf("error = %q, want DM rejection message", body["error"])
	}
	var exists bool
	if err := testPool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM channel WHERE id = $1)`, channelID).Scan(&exists); err != nil {
		t.Fatalf("check channel exists: %v", err)
	}
	if !exists {
		t.Fatal("DM channel was deleted")
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

func TestAgentUnfollowChannelThreadUpdatesAgentStateAndEmitsLinkedSystemEvent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentDisplayName := "Thread Unfollow 机器人 " + uuid.NewString()[:8]
	agentID := createHandlerTestAgentOnRuntime(t, agentDisplayName, handlerTestRuntimeID(t))
	var agentHandle string
	if err := testPool.QueryRow(ctx, `SELECT name FROM agent WHERE id = $1`, agentID).Scan(&agentHandle); err != nil {
		t.Fatalf("load canonical agent handle: %v", err)
	}
	channelID := seedChannelForTest(t, "thread-unfollow-agent-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent channel member: %v", err)
	}
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "root topic", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("thread-unfollow-root-"+uuid.NewString()), 0)
	if err != nil {
		t.Fatalf("insert root: %v", err)
	}
	testHandler.followChannelThreadUser(ctx, parseUUID(channelID), parseUUID(root.ID), parseUUID(testUserID), false)

	req := newRequestAs(testUserID, http.MethodDelete, "/api/channels/"+channelID+"/messages/"+root.ID+"/thread/follow", nil)
	req.Header.Set("X-Actor-Source", "agent_credential")
	req.Header.Set("X-Agent-ID", agentID)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParams(req, "channelId", channelID, "messageId", root.ID)
	rec := httptest.NewRecorder()
	testHandler.UnfollowChannelThread(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("agent unfollow thread: status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	testHandler.UnfollowChannelThread(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("repeat agent unfollow thread: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var agentFollowedAt pgtype.Timestamptz
	var agentWakeState string
	if err := testPool.QueryRow(ctx, `
		SELECT followed_at, wake_state
		FROM thread_participant
		WHERE root_message_id = $1 AND member_type = 'agent' AND member_id = $2`,
		root.ID, agentID).Scan(&agentFollowedAt, &agentWakeState); err != nil {
		t.Fatalf("load agent thread participant: %v", err)
	}
	if agentFollowedAt.Valid {
		t.Fatalf("agent followed_at still set after unfollow: %+v", agentFollowedAt)
	}
	if agentWakeState != "unfollowed" {
		t.Fatalf("agent wake_state=%q, want unfollowed", agentWakeState)
	}

	var userFollowedAt pgtype.Timestamptz
	if err := testPool.QueryRow(ctx, `
		SELECT followed_at
		FROM channel_thread_state
		WHERE root_message_id = $1 AND user_id = $2`,
		root.ID, testUserID).Scan(&userFollowedAt); err != nil {
		t.Fatalf("load user thread state: %v", err)
	}
	if !userFollowedAt.Valid {
		t.Fatal("owner user followed_at was cleared by agent unfollow")
	}

	var content string
	var rawParts []byte
	if err := testPool.QueryRow(ctx, `
		SELECT content, parts
		FROM channel_message
		WHERE channel_id = $1 AND thread_root_message_id = $2 AND author_type = 'system'
		ORDER BY seq DESC
		LIMIT 1`, channelID, root.ID).Scan(&content, &rawParts); err != nil {
		t.Fatalf("load thread unfollow system message: %v", err)
	}
	if strings.Contains(content, "mention://") {
		t.Fatalf("system content leaked legacy mention markdown: %q", content)
	}
	if !strings.Contains(content, "@"+agentHandle) || !strings.Contains(content, "unfollowed this thread") {
		t.Fatalf("system content = %q, want readable @handle fallback", content)
	}
	var eventRows int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1
		  AND thread_root_message_id = $2
		  AND author_type = 'system'
		  AND content LIKE '%unfollowed this thread%'`, channelID, root.ID).Scan(&eventRows); err != nil {
		t.Fatalf("count thread unfollow system events: %v", err)
	}
	if eventRows != 1 {
		t.Fatalf("thread unfollow system event rows = %d, want 1 after repeated request", eventRows)
	}
	var parts []protocol.MessagePart
	if err := json.Unmarshal(rawParts, &parts); err != nil {
		t.Fatalf("decode system parts: %v", err)
	}
	if len(parts) != 2 || parts[0].Type != protocol.MessagePartTypeSystemEvent || parts[0].Event != "thread_unfollowed" {
		t.Fatalf("system parts = %+v, want event and mention reference parts", parts)
	}
	event := threadUnfollowedSystemEventPart{Event: parts[0].Event}
	if err := json.Unmarshal(parts[0].EventParams, &event.Params); err != nil {
		t.Fatalf("decode thread unfollow event params %q: %v", parts[0].EventParams, err)
	}
	if event.Event != "thread_unfollowed" {
		t.Fatalf("event = %q, want thread_unfollowed", event.Event)
	}
	if event.Params.AgentID != agentID || event.Params.ActorID != agentID || event.Params.ActorType != "agent" {
		t.Fatalf("event params = %+v, want agent actor %s", event.Params, agentID)
	}
	if event.Params.ActorHandle != agentHandle {
		t.Fatalf("event actor handle = %q, want %q", event.Params.ActorHandle, agentHandle)
	}
	if event.Params.AgentName != agentDisplayName || event.Params.ActorDisplayName != agentDisplayName || event.Params.ActorName != agentDisplayName {
		t.Fatalf("event agent names = %+v, want display name %q", event.Params, agentDisplayName)
	}
	if mention := parts[1]; mention.Type != protocol.MessagePartTypeReference || mention.RefType != "mention" || mention.RefSubType != "agent" || mention.RefID != agentID || mention.Label != "@"+agentHandle {
		t.Fatalf("mention part = %+v, want structured agent @handle", mention)
	}

	mentioned, err := testHandler.insertChannelMessageWithParts(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "@"+agentHandle+" please facilitate this discussion after unfollow", []protocol.MessagePart{{
		Type:       protocol.MessagePartTypeReference,
		RefType:    "mention",
		RefSubType: "agent",
		RefID:      agentID,
		Label:      "@" + agentHandle,
	}}, "multica", nil, pgtype.UUID{}, parseUUID(root.ID), strPtr("thread-unfollow-mention-"+uuid.NewString()), 0)
	if err != nil {
		t.Fatalf("insert mention after unfollow: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("load channel after unfollow")
	}
	testHandler.dispatchChannelThreadReplyMentions(ctx, ch, mentioned, parseUUID(testUserID))
	testHandler.deliverCanonicalMessageToChannelAgents(ctx, ch, mentioned)
	if err := testPool.QueryRow(ctx, `
		SELECT followed_at, wake_state
		FROM thread_participant
		WHERE root_message_id = $1 AND member_type = 'agent' AND member_id = $2`,
		root.ID, agentID).Scan(&agentFollowedAt, &agentWakeState); err != nil {
		t.Fatalf("load agent state after directed mention: %v", err)
	}
	if agentFollowedAt.Valid || agentWakeState != "unfollowed" {
		t.Fatalf("directed mention changed explicit unfollow: followed_at=%+v wake_state=%q", agentFollowedAt, agentWakeState)
	}
	var deliveryTarget string
	if err := testPool.QueryRow(ctx, `
		SELECT target
		FROM agent_message_delivery
		WHERE agent_id = $1 AND message_id = $2`, agentID, mentioned.ID).Scan(&deliveryTarget); err != nil {
		t.Fatalf("load canonical directed-thread delivery: %v", err)
	}
	if deliveryTarget != "thread:"+root.ID {
		t.Fatalf("directed-thread delivery target = %q, want thread:%s", deliveryTarget, root.ID)
	}
}

func TestChannelThreadReplyDoesNotCreateMainTimelineUnread(t *testing.T) {
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
		"content": "thread reply remains in thread",
	})
	req = withChannelTestWorkspaceCtx(t, req, replierID)
	req = withURLParams(req, "channelId", channelID, "messageId", root.ID)
	rec := httptest.NewRecorder()
	testHandler.SendChannelMessageThreadReply(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("send thread reply: status=%d body=%s", rec.Code, rec.Body.String())
	}

	rootTimeline := listedMessagesForUser(t, channelID, testUserID)
	if len(rootTimeline) != 1 || rootTimeline[0].ID != root.ID {
		t.Fatalf("main timeline = %+v, want only root", rootTimeline)
	}
	if rootTimeline[0].ThreadReplyCount != 1 {
		t.Fatalf("root metadata = %+v, want reply counted", rootTimeline[0])
	}

	ch := listedChannelForUser(t, channelID, testUserID)
	if ch == nil || ch.RealUnreadCount != 0 || ch.UnreadCount != 0 {
		t.Fatalf("channel unread = %+v, want no main-timeline unread", ch)
	}
}

func TestChannelThreadReadModelExposesParticipantsWithoutTaskWake(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentHandle := "thread-contract-helper-" + uuid.NewString()[:8]
	agentID := createHandlerTestAgentOnRuntime(t, agentHandle, handlerTestRuntimeID(t))
	channelID := seedChannelForTest(t, "thread-read-model-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "root", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("thread-read-model"), 0)
	if err != nil {
		t.Fatalf("insert root: %v", err)
	}
	rec := sendChannelThreadReplyForTest(t, channelID, root.ID, testUserID, map[string]any{"content": "@" + agentHandle + " can you take this?"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("send thread mention: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var reply ChannelMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatalf("decode thread mention: %v", err)
	}
	var deliveryTarget string
	if err := testPool.QueryRow(ctx, `
		SELECT target
		FROM agent_message_delivery
		WHERE agent_id = $1 AND message_id = $2`, agentID, reply.ID).Scan(&deliveryTarget); err != nil {
		t.Fatalf("load canonical thread delivery: %v", err)
	}
	if deliveryTarget != "thread:"+root.ID {
		t.Fatalf("canonical thread delivery target = %q, want thread:%s", deliveryTarget, root.ID)
	}

	page, raw := listedThreadForUser(t, channelID, root.ID, testUserID)
	if len(page.Messages) == 0 {
		t.Fatal("thread response missing root")
	}
	gotRoot := page.Messages[0]
	timeline := listedMessagesForUser(t, channelID, testUserID)
	var timelineRoot *ChannelMessageResponse
	for i := range timeline {
		if timeline[i].ID == root.ID {
			timelineRoot = &timeline[i]
			break
		}
	}
	if timelineRoot == nil || len(timelineRoot.ThreadParticipants) == 0 {
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
		t.Fatalf("participants missing first-mention followed agent: %+v", gotRoot.ThreadParticipants)
	}
	for _, forbidden := range []string{"task_id", "thread_wake_annotations", "pending_from_seq", "pending_to_seq", "delivered_to_seq", "channel_ambient_pending_wake"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("thread read-model response leaked %q: %s", forbidden, raw)
		}
	}
}

func TestChannelThreadReadModelKeepsProductStateIndependentFromChat(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentHandle := "thread-state-helper-" + uuid.NewString()[:8]
	agentID := createHandlerTestAgentOnRuntime(t, agentHandle, handlerTestRuntimeID(t))
	channelID := seedChannelForTest(t, "thread-state-model-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}

	replyRoot := seedThreadProductInboxEventForTest(t, channelID, agentID, "thread-state-reply")
	rec := sendChannelThreadReplyForTest(t, channelID, replyRoot.ID, testUserID, map[string]any{"content": "@" + agentHandle + " react if done"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("send follow-up thread mention: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var followup ChannelMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &followup); err != nil {
		t.Fatalf("decode follow-up thread mention: %v", err)
	}
	var target string
	if err := testPool.QueryRow(ctx, `
		SELECT target
		FROM agent_message_delivery
		WHERE agent_id = $1 AND message_id = $2`, agentID, followup.ID).Scan(&target); err != nil {
		t.Fatalf("load canonical chat follow-up delivery: %v", err)
	}
	if target != "thread:"+replyRoot.ID {
		t.Fatalf("chat follow-up delivery target = %q, want thread:%s", target, replyRoot.ID)
	}
}

func TestChannelThreadContinuationPromptAllowsSilence(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Thread Silent Prompt "+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "thread-silent-prompt-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
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
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "root", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("silent-prompt-root"), 0)
	if err != nil {
		t.Fatalf("insert root: %v", err)
	}
	followup, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "never mind", "multica", nil, pgtype.UUID{}, parseUUID(root.ID), strPtr("silent-prompt-root"), 0)
	if err != nil {
		t.Fatalf("insert follow-up: %v", err)
	}

	prompt := testHandler.buildChannelThreadContinuationPrompt(ctx, ch, agent, followup)
	for _, want := range []string{
		"not a must-reply directed mention",
		"finish without visible output",
		"Message target for chat transport: #" + ch.Name + ":" + root.ID[:8],
		"Current follow-up:",
		"never mind",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("thread continuation prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, banned := range []string{
		"require a visible result",
		"This run is directly addressed to you",
	} {
		if strings.Contains(prompt, banned) {
			t.Fatalf("thread continuation prompt should not contain directed must-reply text %q:\n%s", banned, prompt)
		}
	}
}

func TestChannelDMThreadContinuationPromptIncludesDMThreadTarget(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "dm")
	agentID := agentIDForTask(t, taskID)
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("dm channel not found after seed")
	}
	agent, err := testHandler.Queries.GetAgent(ctx, parseUUID(agentID))
	if err != nil {
		t.Fatalf("load dm agent: %v", err)
	}
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "dm root", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("dm-prompt-root"), 0)
	if err != nil {
		t.Fatalf("insert dm root: %v", err)
	}
	followup, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "dm follow up", "multica", nil, pgtype.UUID{}, parseUUID(root.ID), strPtr("dm-prompt-root"), 0)
	if err != nil {
		t.Fatalf("insert dm follow-up: %v", err)
	}

	prompt := testHandler.buildChannelThreadContinuationPrompt(ctx, ch, agent, followup)
	wantTarget := "dm:@" + userHandleForTransportTest(t, testUserID) + ":" + root.ID[:8]
	for _, want := range []string{
		"thread inside a Multica DM",
		"Message target for chat transport: " + wantTarget,
		"dm follow up",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("dm thread continuation prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "thread inside Multica group chat") {
		t.Fatalf("dm thread continuation prompt mislabeled as group chat:\n%s", prompt)
	}
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

func assertChannelAgentWakeReason(t *testing.T, channelID, agentID, sourceMessageID, wantReason string) {
	t.Helper()
	assertChannelAgentWakeReasonPriority(t, channelID, agentID, sourceMessageID, wantReason, 10)
}

func assertChannelAgentWakeReasonPriority(t *testing.T, channelID, agentID, sourceMessageID, wantReason string, wantPriority int32) {
	t.Helper()
	ctx := context.Background()
	var reason string
	var requiresWake bool
	var priority int32
	if err := testPool.QueryRow(ctx, `
		SELECT reason, requires_wake, priority
		FROM agent_inbox_event
		WHERE channel_id = $1 AND agent_id = $2 AND source_message_id = $3
		ORDER BY created_at DESC
		LIMIT 1`, channelID, agentID, sourceMessageID).Scan(&reason, &requiresWake, &priority); err != nil {
		t.Fatalf("load wake inbox event: %v", err)
	}
	if reason != wantReason || !requiresWake || priority != wantPriority {
		t.Fatalf("wake inbox event = reason:%q requires_wake:%v priority:%d, want %q/true/%d", reason, requiresWake, priority, wantReason, wantPriority)
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

func TestListChannelsTreatsZeroReadSeqAsNoCursor(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	readerID := createChannelPlainMember(t)
	writerID := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "zero-read-cursor-"+uuid.NewString(), readerID, writerID)
	message, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(writerID), "Writer", "unread", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("zero-read-cursor"), 0)
	if err != nil {
		t.Fatalf("insert unread message: %v", err)
	}

	beforeRead := listedChannelForUser(t, channelID, readerID)
	if beforeRead == nil {
		t.Fatal("channel missing from list before mark read")
	}
	if beforeRead.LastReadSeq != nil {
		t.Fatalf("last_read_seq before first read = %d, want nil", *beforeRead.LastReadSeq)
	}
	if beforeRead.RealUnreadCount != 1 || beforeRead.UnreadCount != 1 {
		t.Fatalf("unread before first read = real:%d total:%d, want 1/1", beforeRead.RealUnreadCount, beforeRead.UnreadCount)
	}

	markChannelReadForTest(t, channelID, readerID)

	afterRead := listedChannelForUser(t, channelID, readerID)
	if afterRead == nil {
		t.Fatal("channel missing from list after mark read")
	}
	if afterRead.LastReadSeq == nil || *afterRead.LastReadSeq != message.Seq {
		t.Fatalf("last_read_seq after mark read = %v, want %d", afterRead.LastReadSeq, message.Seq)
	}
	if afterRead.RealUnreadCount != 0 || afterRead.UnreadCount != 0 {
		t.Fatalf("unread after mark read = real:%d total:%d, want 0/0", afterRead.RealUnreadCount, afterRead.UnreadCount)
	}
}

func TestMarkChannelReadEchoesNullForZeroPreviousCursor(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	readerID := createChannelPlainMember(t)
	writerID := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "zero-previous-read-cursor-"+uuid.NewString(), readerID, writerID)
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(writerID), "Writer", "unread", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("zero-previous-read-cursor"), 0); err != nil {
		t.Fatalf("insert unread message: %v", err)
	}

	req := newRequestAs(readerID, http.MethodPost, "/api/channels/"+channelID+"/read", nil)
	req = withChannelTestWorkspaceCtx(t, req, readerID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.MarkChannelRead(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mark read: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		OK                  bool   `json:"ok"`
		PreviousLastReadSeq *int64 `json:"previous_last_read_seq"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode mark-read response: %v body=%s", err, rec.Body.String())
	}
	if !response.OK {
		t.Fatalf("mark-read response ok=false: %s", rec.Body.String())
	}
	if response.PreviousLastReadSeq != nil {
		t.Fatalf("previous_last_read_seq = %d, want null", *response.PreviousLastReadSeq)
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
	agentHandle := "thread-agent-" + uuid.NewString()[:8]
	agentID := createHandlerTestAgentOnRuntime(t, agentHandle, handlerTestRuntimeID(t))
	channelID := seedChannelForTest(t, "thread-agent-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "root", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("ui-thread"), 0)
	if err != nil {
		t.Fatalf("insert root: %v", err)
	}
	triggerContent := "@" + agentHandle + " can you answer here?"
	rec := sendChannelThreadReplyForTest(t, channelID, root.ID, testUserID, map[string]any{"content": triggerContent})
	if rec.Code != http.StatusCreated {
		t.Fatalf("send thread mention: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var trigger ChannelMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &trigger); err != nil {
		t.Fatalf("decode thread mention: %v", err)
	}

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
		t.Fatalf("agent thread participant after first mention count=%d, want 1", agentParticipants)
	}

	var target string
	if err := testPool.QueryRow(ctx, `
		SELECT target
		FROM agent_message_delivery
		WHERE agent_id = $1 AND message_id = $2`, agentID, trigger.ID).Scan(&target); err != nil {
		t.Fatalf("load canonical thread delivery: %v", err)
	}
	if target != "thread:"+root.ID {
		t.Fatalf("canonical delivery target = %q, want thread:%s", target, root.ID)
	}
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "agent", parseUUID(agentID), agentHandle, "answer in thread", "multica", nil, pgtype.UUID{}, parseUUID(root.ID), strPtr("ui-thread"), 1); err != nil {
		t.Fatalf("insert agent thread reply: %v", err)
	}
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
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM thread_participant
		WHERE root_message_id = $1
		  AND member_type = 'agent'
		  AND member_id = $2
		  AND followed_at IS NOT NULL
		  AND wake_state = 'active'`, root.ID, agentID).Scan(&agentParticipants); err != nil {
		t.Fatalf("count agent thread participant after post: %v", err)
	}
	if agentParticipants != 1 {
		t.Fatalf("agent thread participant after post count=%d, want 1", agentParticipants)
	}
}

// TestListChannelInviteCandidatesExcludesExistingMembersButIncludesAllAgents
// supersedes the old "excludes private agents" contract (task #908: agent
// usage — including channel invites — is unconditional for every workspace
// member; the Wendy-name owner-only SQL carve-out was removed in #1613).
func TestListChannelInviteCandidatesExcludesExistingMembersButIncludesAllAgents(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	otherOwnedAgentID, _, memberID := privateAgentTestFixture(t)
	candidateID := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "invite-candidates-"+uuid.NewString(), memberID)

	req := newRequestAs(memberID, http.MethodGet, "/api/channels/"+channelID+"/invite-candidates", nil)
	req = withChannelTestWorkspaceCtx(t, req, memberID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.ListChannelInviteCandidates(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ListChannelInviteCandidates: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp ChannelInviteCandidatesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode invite candidates: %v", err)
	}
	if !channelInviteCandidatesContain(resp.Candidates, "user", candidateID) {
		t.Fatalf("candidate user %s missing from invite candidates: %+v", candidateID, resp.Candidates)
	}
	if channelInviteCandidatesContain(resp.Candidates, "user", memberID) {
		t.Fatalf("existing channel member %s leaked into invite candidates", memberID)
	}
	if !channelInviteCandidatesContain(resp.Candidates, "agent", otherOwnedAgentID) {
		t.Fatalf("agent %s owned by someone else missing from invite candidates (existence/usage is unconditional post-#908): %+v", otherOwnedAgentID, resp.Candidates)
	}

	var fullMemberCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM member WHERE workspace_id = $1`, testWorkspaceID).Scan(&fullMemberCount); err != nil {
		t.Fatalf("count workspace members: %v", err)
	}
	if len(resp.Candidates) == 0 && fullMemberCount > 1 {
		t.Fatalf("invite candidates came back empty despite workspace members being available")
	}
}

// TestListChannelInviteCandidatesIncludesNonOwnerWendy is the end-to-end
// regression test for the 2026-07-31 Wendy DM incident's second bug (found
// after B2 merged): ListChannelInviteCandidates had its own independent
// hardcoded exclusion for display_name IN ('Wendy', 'Windy', 'Joe'), missed
// by the agent_access.go cleanup because it was a raw SQL string literal,
// not a call to the retired predicate functions. Frank: "不要有特殊逻辑" —
// a non-owner member must be able to invite the workspace's shared Wendy
// into a channel just like any other agent.
func TestListChannelInviteCandidatesIncludesNonOwnerWendy(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID, _, memberID := privateAgentTestFixture(t)
	if _, err := testPool.Exec(context.Background(), `UPDATE agent SET display_name = 'Wendy' WHERE id = $1`, agentID); err != nil {
		t.Fatalf("rename agent to Wendy: %v", err)
	}
	channelID := seedChannelForTest(t, "invite-candidates-wendy-"+uuid.NewString(), memberID)

	req := newRequestAs(memberID, http.MethodGet, "/api/channels/"+channelID+"/invite-candidates", nil)
	req = withChannelTestWorkspaceCtx(t, req, memberID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.ListChannelInviteCandidates(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ListChannelInviteCandidates: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp ChannelInviteCandidatesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode invite candidates: %v", err)
	}
	if !channelInviteCandidatesContain(resp.Candidates, "agent", agentID) {
		t.Fatalf("non-owner member's shared Wendy %s missing from invite candidates: %+v", agentID, resp.Candidates)
	}
}

// TestListChannelInviteCandidatesSearchFindsWendy covers LRM-915 AC: from a
// channel that does not yet include Wendy, searching q=Wendy must return her
// for a non-owner plain member (server-side filter path, not only the empty-q
// full pool that the FE usually caches client-side).
func TestListChannelInviteCandidatesSearchFindsWendy(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID, _, memberID := privateAgentTestFixture(t)
	if _, err := testPool.Exec(context.Background(), `UPDATE agent SET display_name = 'Wendy', name = 'wendy' WHERE id = $1`, agentID); err != nil {
		t.Fatalf("rename agent to Wendy: %v", err)
	}
	channelID := seedChannelForTest(t, "invite-search-wendy-"+uuid.NewString(), memberID)

	req := newRequestAs(memberID, http.MethodGet, "/api/channels/"+channelID+"/invite-candidates?q=Wendy", nil)
	req = withChannelTestWorkspaceCtx(t, req, memberID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.ListChannelInviteCandidates(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ListChannelInviteCandidates q=Wendy: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp ChannelInviteCandidatesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode invite candidates: %v", err)
	}
	if !channelInviteCandidatesContain(resp.Candidates, "agent", agentID) {
		t.Fatalf("q=Wendy did not return non-owner Wendy %s: %+v", agentID, resp.Candidates)
	}
}

func channelInviteCandidatesContain(candidates []ChannelInviteCandidateResponse, memberType, memberID string) bool {
	for _, c := range candidates {
		if c.MemberType == memberType && c.MemberID == memberID {
			return true
		}
	}
	return false
}

// TestAddChannelMembersAllowsAnyAgentForPlainMember supersedes the old
// "rejects private agent for plain member" contract (task #908: adding an
// agent to a channel is a usage surface, not an internal-control surface, so
// it widens to every workspace member regardless of who owns the agent).
func TestAddChannelMembersAllowsAnyAgentForPlainMember(t *testing.T) {
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
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("batch add agent as plain member: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var count int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'agent' AND member_id = $3`, channelID, testWorkspaceID, agentID).Scan(&count); err != nil {
		t.Fatalf("count agent channel members: %v", err)
	}
	if count != 1 {
		t.Fatalf("agent was not added by batch request; count=%d", count)
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
	assertChannelMemberSystemEvent(t, event, channelMemberAddedEvent, testUserID, "human", targetID, "human")

	req = newRequestAs(testUserID, http.MethodPost, "/api/channels/"+channelID+"/members", AddChannelMemberRequest{MemberType: "user", MemberID: targetID})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec = httptest.NewRecorder()
	testHandler.AddChannelMember(rec, req)
	if rec.Code != http.StatusOK {
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
	assertChannelMemberSystemEvent(t, event, channelMemberAddedEvent, testUserID, "human", newID, "human")
}

func TestAddChannelMemberSystemEventIncludesAgentTargetRef(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	readerAgentID := createHandlerTestAgent(t, "channel_event_reader_"+strings.ReplaceAll(uuid.NewString(), "-", ""), nil)
	agentID := createHandlerTestAgent(t, "channel_event_agent_"+strings.ReplaceAll(uuid.NewString(), "-", ""), nil)
	channelID := seedChannelForTest(t, "member-add-agent-event-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, readerAgentID); err != nil {
		t.Fatalf("seed reader agent member: %v", err)
	}

	req := newRequestAs(testUserID, http.MethodPost, "/api/channels/"+channelID+"/members", AddChannelMemberRequest{MemberType: "agent", MemberID: agentID})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.AddChannelMember(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add agent member: status=%d body=%s", rec.Code, rec.Body.String())
	}

	event := latestChannelSystemEventForTest(t, channelID)
	assertChannelMemberSystemEvent(t, event, channelMemberAddedEvent, testUserID, "human", agentID, "agent")

	var inboxEvents int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_inbox_event
		WHERE channel_id = $1`, channelID).Scan(&inboxEvents); err != nil {
		t.Fatalf("count system-only inbox events: %v", err)
	}
	if inboxEvents != 0 {
		t.Fatalf("system-only member event created agent inbox events = %d, want 0", inboxEvents)
	}
}

func TestRemoveChannelMemberEmitsRemovedSystemEventForRemainingMembers(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	targetID := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "member-remove-event-"+uuid.NewString(), testUserID, targetID)

	req := newRequestAs(testUserID, http.MethodDelete, "/api/channels/"+channelID+"/members/user/"+targetID+"?expected_remove_effect=none", nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withRouteParams(req, "channelId", channelID, "memberType", "user", "memberId", targetID)
	rec := httptest.NewRecorder()
	testHandler.RemoveChannelMember(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove member: status=%d body=%s", rec.Code, rec.Body.String())
	}

	event := latestChannelSystemEventForTest(t, channelID)
	assertChannelMemberSystemEvent(t, event, channelMemberRemovedEvent, testUserID, "human", targetID, "human")

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

	req := newRequestAs(memberID, http.MethodDelete, "/api/channels/"+channelID+"/members/user/"+memberID+"?expected_remove_effect=none", nil)
	req = withChannelTestWorkspaceCtx(t, req, memberID)
	req = withRouteParams(req, "channelId", channelID, "memberType", "user", "memberId", memberID)
	rec := httptest.NewRecorder()
	testHandler.RemoveChannelMember(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("self remove member: status=%d body=%s", rec.Code, rec.Body.String())
	}

	event := latestChannelSystemEventForTest(t, channelID)
	assertChannelMemberSystemEvent(t, event, channelMemberLeftEvent, memberID, "human", memberID, "human")
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

	req := newRequest(http.MethodDelete, "/api/channels/"+channelID+"/members/agent/"+agentID+"?expected_remove_effect=none", nil)
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

	req := newRequestAs(memberID, http.MethodDelete, "/api/channels/"+channelID+"/members/user/"+targetID+"?expected_remove_effect=none", nil)
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

	selfReq := newRequestAs(memberID, http.MethodDelete, "/api/channels/"+channelID+"/members/user/"+memberID+"?expected_remove_effect=none", nil)
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

// ensureLegacyChannelChatBridgeForTest seeds channel_agent_session + optional
// prompt chat_message so legacy ChatDone bridge tests still exercise that path
// after ordinary wakes became channel-only (LRM-1079).
func ensureLegacyChannelChatBridgeForTest(t *testing.T, ch ChannelResponse, agentID string, trigger ChannelMessageResponse, prompt string) string {
	t.Helper()
	session, err := testHandler.ensureChannelAgentSession(context.Background(), ch, parseUUID(agentID), parseUUID(testUserID))
	if err != nil {
		t.Fatalf("ensure legacy channel chat bridge session: %v", err)
	}
	if strings.TrimSpace(prompt) != "" {
		if _, err := testHandler.createChannelAgentPromptMessageWithDB(context.Background(), testPool, session.ID, prompt, trigger); err != nil {
			t.Fatalf("seed legacy channel chat bridge prompt: %v", err)
		}
	}
	return uuidToString(session.ID)
}

func seedChannelForTest(t *testing.T, name string, memberIDs ...string) string {
	t.Helper()
	ctx := context.Background()
	// Ordinary-group auto-seed makes created_by an owner. Prefer the first
	// requested member as creator so callers can seed a channel that does NOT
	// include testUserID (pass only the intended members).
	createdBy := testUserID
	if len(memberIDs) > 0 {
		createdBy = memberIDs[0]
	}
	var channelID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, created_by)
		VALUES ($1, $2, $3)
		RETURNING id`, testWorkspaceID, name, createdBy).Scan(&channelID); err != nil {
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
		FROM agent_inbox_event atq
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
	if len(parts) != 1 || parts[0].Type != protocol.MessagePartTypeSystemEvent || strings.TrimSpace(parts[0].Event) == "" {
		t.Fatalf("system message parts = %+v, want one typed event part", parts)
	}
	event := channelMemberSystemEventPart{Event: parts[0].Event}
	if err := json.Unmarshal(parts[0].EventParams, &event.Params); err != nil {
		t.Fatalf("decode system event params %q: %v", parts[0].EventParams, err)
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
	return createChannelWorkspaceMemberWithRole(t, "member")
}

func createChannelWorkspaceAdmin(t *testing.T) string {
	t.Helper()
	return createChannelWorkspaceMemberWithRole(t, "admin")
}

func createChannelWorkspaceMemberWithRole(t *testing.T, role string) string {
	t.Helper()
	ctx := context.Background()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	name := "channel_" + role + "_" + suffix
	email := "channel-" + role + "-" + suffix + "@multica.test"
	var userID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ($1, $2)
		RETURNING id`, name, email).Scan(&userID); err != nil {
		t.Fatalf("create %s user: %v", role, err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, $3)`, testWorkspaceID, userID, role); err != nil {
		t.Fatalf("add %s member: %v", role, err)
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

func seedThreadProductInboxEventForTest(t *testing.T, channelID, agentID, threadID string) ChannelMessageResponse {
	t.Helper()
	ctx := context.Background()
	var agentName string
	if err := testPool.QueryRow(ctx, `SELECT name FROM agent WHERE id = $1`, agentID).Scan(&agentName); err != nil {
		t.Fatalf("load thread mention agent name: %v", err)
	}
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "root "+threadID, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr(threadID), 0)
	if err != nil {
		t.Fatalf("insert root: %v", err)
	}
	rec := sendChannelThreadReplyForTest(t, channelID, root.ID, testUserID, map[string]any{"content": "@" + agentName + " please check"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create thread product-task source: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var reply ChannelMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatalf("decode thread product-task source: %v", err)
	}
	eventID := createProductInboxEventForRuntime(t, handlerTestRuntimeID(t), agentID, channelID)
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_inbox_event
		SET source_message_id = $2, trigger_summary = 'Explicit product task'
		WHERE id = $1`, eventID, reply.ID); err != nil {
		t.Fatalf("link thread product task source: %v", err)
	}
	// Restored human-message wakes create a mention inbox event for the same
	// reply. Snapshot fixtures need exactly one explicit product task, so drop
	// the auto-dispatched wake(s) for this source message.
	if _, err := testPool.Exec(ctx, `
		DELETE FROM agent_inbox_event
		WHERE agent_id = $1
		  AND source_message_id = $2
		  AND id <> $3`, agentID, reply.ID, eventID); err != nil {
		t.Fatalf("clear auto mention wakes for product fixture: %v", err)
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
		ORDER BY e.created_at DESC, e.id DESC
		LIMIT 1`, rootID, agentID).Scan(&eventID); err != nil {
		t.Fatalf("load latest channel agent inbox event for root: %v", err)
	}
	return eventID
}

func setAgentInboxTerminalOutcomeForTest(t *testing.T, eventID, outcome string, retryable bool) string {
	t.Helper()
	ctx := context.Background()
	var deliveryID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_event_delivery (
			workspace_id,
			agent_session_id,
			inbox_event_id,
			runtime_id,
			status,
			acked_at
		)
		SELECT workspace_id,
		       agent_session_id,
		       id,
		       $2,
		       'acked',
		       now()
		FROM agent_inbox_event
		WHERE id = $1
		RETURNING id`, eventID, handlerTestRuntimeID(t)).Scan(&deliveryID); err != nil {
		t.Fatalf("insert terminal delivery: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_inbox_event
		SET status = 'acked',
		    acked_at = now(),
		    terminal_outcome = $2,
		    terminal_delivery_id = $3,
		    retryable = $4,
		    terminal_at = now(),
		    completed_at = now(),
		    updated_at = now()
		WHERE id = $1`, eventID, outcome, deliveryID, retryable); err != nil {
		t.Fatalf("set terminal outcome: %v", err)
	}
	return deliveryID
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
