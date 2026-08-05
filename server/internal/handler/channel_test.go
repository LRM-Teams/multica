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

	"github.com/go-chi/chi/v5"
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
	prompt := buildChannelAmbientObservationPrompt(
		ChannelResponse{Name: "voice-test"},
		db.Agent{},
		ChannelMessageResponse{ID: "message-1", Type: "user", Content: "请用语音回复"},
	)
	if strings.Contains(prompt, "plain-text reply") || !strings.Contains(prompt, "runtime brief's voice-delivery path") {
		t.Fatalf("ambient prompt has conflicting voice delivery guidance: %q", prompt)
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

func TestChannelMentionStoresThreadContextAndBridgesAgentReply(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentHandle := "channel-helper-" + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentHandle, nil)
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

	// LRM-1079: ordinary mentions are channel-only (context prompt, no chat_session).
	var rawContext []byte
	if err := testPool.QueryRow(ctx, `
		SELECT context
		FROM agent_inbox_event
		WHERE channel_id = $1 AND agent_id = $2 AND reason = 'mention'
		ORDER BY created_at DESC
		LIMIT 1`, channelID, agentID).Scan(&rawContext); err != nil {
		t.Fatalf("load mention wake context: %v", err)
	}
	var wake channelWakeContext
	if err := json.Unmarshal(rawContext, &wake); err != nil {
		t.Fatalf("decode wake context: %v", err)
	}
	if wake.ThreadID != "debate-thread" || wake.TriggerDepth != 2 {
		t.Fatalf("wake thread/depth = %q/%d, want debate-thread/2", wake.ThreadID, wake.TriggerDepth)
	}
	prompt := wake.Prompt
	if !strings.Contains(prompt, agentHandle+" joined this channel") {
		t.Fatalf("prompt should retain the canonical membership context before the trigger:\n%s", prompt)
	}
	if count := strings.Count(prompt, triggerContent); count != 1 {
		t.Fatalf("current trigger should appear exactly once, got %d:\n%s", count, prompt)
	}

	// Legacy ChatDone bridge still works when a channel_agent_session exists
	// (env-dispatch / pre-migration). Seed session + prompt thread context only
	// for the bridge assertion.
	session, err := testHandler.ensureChannelAgentSession(ctx, ch, parseUUID(agentID), parseUUID(testUserID))
	if err != nil {
		t.Fatalf("ensure legacy bridge session: %v", err)
	}
	sessionID := uuidToString(session.ID)
	if _, err := testHandler.createChannelAgentPromptMessageWithDB(ctx, testPool, session.ID, prompt, trigger); err != nil {
		t.Fatalf("seed legacy bridge prompt: %v", err)
	}
	testHandler.handleChannelChatDone(events.Event{Payload: protocol.ChatDonePayload{
		ChatSessionID: sessionID,
		Content:       "@" + agentHandle + " says hi",
		Parts: []protocol.MessagePart{{
			Type:       protocol.MessagePartTypeReference,
			RefType:    "mention",
			RefSubType: "agent",
			RefID:      agentID,
			Label:      "@" + agentHandle,
		}},
	}})
	var authorType, replyThread string
	var replyDepth int
	replyContent := "@" + agentHandle + " says hi"
	if err := testPool.QueryRow(ctx, `
		SELECT author_type, thread_id, trigger_depth
		FROM channel_message
		WHERE channel_id = $1 AND content = $2
		LIMIT 1`, channelID, "["+replyContent+"]").Scan(&authorType, &replyThread, &replyDepth); err == nil {
		t.Fatalf("unexpected bracketed reply row: %s %s %d", authorType, replyThread, replyDepth)
	}
	var replyRoot pgtype.Text
	var rawReplyParts []byte
	if err := testPool.QueryRow(ctx, `
		SELECT author_type, thread_id, thread_root_message_id, trigger_depth, parts
		FROM channel_message
		WHERE channel_id = $1 AND content = $2
		LIMIT 1`, channelID, replyContent).Scan(&authorType, &replyThread, &replyRoot, &replyDepth, &rawReplyParts); err != nil {
		t.Fatalf("load bridged reply: %v", err)
	}
	if authorType != "agent" || replyThread != "debate-thread" || replyRoot.Valid || replyDepth != 3 {
		t.Fatalf("bridged reply = %s/%q/%+v/%d, want agent/debate-thread/no-root/3", authorType, replyThread, replyRoot, replyDepth)
	}
	var replyParts []protocol.MessagePart
	if err := json.Unmarshal(rawReplyParts, &replyParts); err != nil {
		t.Fatalf("decode bridged reply parts: %v", err)
	}
	start, end := contentUTF16Span(replyContent, 0, len("@"+agentHandle))
	anchoredMentions := 0
	for _, part := range replyParts {
		if part.Type == protocol.MessagePartTypeReference && part.RefType == "mention" && part.RefSubType == "agent" && part.RefID == agentID {
			if part.ContentStartUTF16 == nil || part.ContentEndUTF16 == nil || *part.ContentStartUTF16 != start || *part.ContentEndUTF16 != end {
				t.Fatalf("bridged reply mention span = %+v, want [%d,%d)", part, start, end)
			}
			anchoredMentions++
		}
	}
	if anchoredMentions != 1 {
		t.Fatalf("bridged reply agent references = %d, want exactly one anchored reference: %+v", anchoredMentions, replyParts)
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
		VALUES ($1, $2, 'user', $3), ($1, $2, 'agent', $4)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, testUserID, agentID); err != nil {
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

func TestChannelMessageWakeBoundsRepeatedOrdinaryFanout(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	withChannelAmbientGateTestConfig(t)
	channelID := seedChannelForTest(t, "ordinary-wake-bounds-"+uuid.NewString(), testUserID)
	agentIDs := make([]string, 0, 16)
	for i := 0; i < 16; i++ {
		agentID := createHandlerTestAgent(t, fmt.Sprintf("Ordinary Wake Agent %02d %s", i, uuid.NewString()[:8]), nil)
		agentIDs = append(agentIDs, agentID)
		if _, err := testPool.Exec(ctx, `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
			VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
			t.Fatalf("seed agent member: %v", err)
		}
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}

	for i := 0; i < 3; i++ {
		trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", fmt.Sprintf("ordinary channel update %d", i), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("ordinary-wake-bounds"), 0)
		if err != nil {
			t.Fatalf("insert ordinary trigger %d: %v", i, err)
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
		WHERE channel_id = $1 AND reason = 'channel_message'`, channelID).Scan(&events, &sessions, &wakeEvents, &minSeq, &maxSeq); err != nil {
		t.Fatalf("count channel message wake events: %v", err)
	}
	if events != len(agentIDs) || sessions != len(agentIDs) || wakeEvents != len(agentIDs) || minSeq != lastSeq || maxSeq != lastSeq {
		t.Fatalf("channel message wake events=%d sessions=%d wake=%d min=%d max=%d, want events/sessions/wake=%d seq=%d", events, sessions, wakeEvents, minSeq, maxSeq, len(agentIDs), lastSeq)
	}
	var prompt string
	if err := testPool.QueryRow(ctx, `
		SELECT COALESCE(context->>'prompt', '')
		FROM agent_inbox_event
		WHERE channel_id = $1 AND agent_id = $2 AND reason = 'channel_message'
		ORDER BY created_at DESC
		LIMIT 1`, channelID, agentIDs[0]).Scan(&prompt); err != nil {
		t.Fatalf("load coalesced wake prompt: %v", err)
	}
	if !strings.Contains(prompt, "ordinary channel update 2") || !strings.Contains(prompt, fmt.Sprintf("seq <= %d", lastSeq)) {
		t.Fatalf("coalesced wake prompt did not refresh to latest unread range: %q", prompt)
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
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
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

	assertChannelAgentInboxEventCounts(t, channelID, agentID, 0, 1)
	assertChannelAgentWakeReasonPriority(t, channelID, agentID, trigger.ID, channelMessageWakeReason, channelMessageWakePriority)
}

// TestChannelGroupCommandWakesAllAgentsRestoresAndongDefault is the regression for
// restoring Andong's wake-all contract: "大家"/@all must wake every unmuted agent
// with a silent-capable channel_message run, not only the group manager.
func TestChannelGroupCommandWakesAllAgentsRestoresAndongDefault(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	withChannelAmbientGateTestConfig(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	channelID := seedChannelForTest(t, "group-command-wake-all-"+suffix, testUserID)
	managerID := createHandlerTestAgent(t, "group-mgr-"+suffix, nil)
	peerID := createHandlerTestAgent(t, "group-peer-"+suffix, nil)
	for _, member := range []struct {
		agentID string
		role    string
	}{
		{agentID: managerID, role: "manager"},
		{agentID: peerID, role: "member"},
	} {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
			VALUES ($1, $2, 'agent', $3, $4)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, member.agentID, member.role); err != nil {
			t.Fatalf("seed agent member %s: %v", member.agentID, err)
		}
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}

	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "大家出来打个招呼", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("group-command-wake-all"), 0)
	if err != nil {
		t.Fatalf("insert group command: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, trigger, parseUUID(testUserID))

	for _, agentID := range []string{managerID, peerID} {
		assertChannelAgentInboxEventCounts(t, channelID, agentID, 0, 1)
		assertChannelAgentWakeReasonPriority(t, channelID, agentID, trigger.ID, channelMessageWakeReason, channelMessageWakePriority)
	}
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

func TestChannelAmbientUnreadPromptIncludesSystemRows(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Ambient System Reader "+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "ambient-system-read-"+uuid.NewString(), testUserID)
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
	systemNotice := "member event visible to runtime " + uuid.NewString()
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "system", pgtype.UUID{}, "system", systemNotice, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0); err != nil {
		t.Fatalf("insert system message: %v", err)
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "ordinary ambient after system row", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert ambient trigger: %v", err)
	}

	prompt := testHandler.buildChannelAmbientUnreadPromptWithDB(ctx, testHandler.DB, ch, agent, trigger, 0, trigger.Seq)
	if !strings.Contains(prompt, systemNotice) || !strings.Contains(prompt, "system (system): "+systemNotice) {
		t.Fatalf("ambient prompt omitted labeled system row:\n%s", prompt)
	}
}

func TestChannelMessageWakeSerializesConcurrentSameAgentOrdinaryMessages(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	withChannelAmbientGateTestConfig(t)
	agentID := createHandlerTestAgent(t, "Ordinary Wake Concurrent "+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "ordinary-wake-concurrent-"+uuid.NewString(), testUserID)
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

	start := make(chan struct{})
	const sends = 8
	var wg sync.WaitGroup
	for i := 0; i < sends; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", fmt.Sprintf("ordinary concurrent wake update %d", i), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("ordinary-wake-concurrent"), 0)
			if err != nil {
				t.Errorf("insert ordinary trigger %d: %v", i, err)
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
		 AND e.reason = 'channel_message'
		WHERE c.channel_id = $1
		GROUP BY c.last_seq`, channelID, agentID).Scan(&lastSeq, &inboxSeq, &events); err != nil {
		t.Fatalf("load coalesced channel message wake event: %v", err)
	}
	if events != 1 || inboxSeq != lastSeq {
		t.Fatalf("channel message wake events=%d seq=%d, want one coalesced event at last_seq=%d", events, inboxSeq, lastSeq)
	}
}

func TestChannelAgentInboxDrainAckDirectedMention(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentName := "inbox-drain-agent-" + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentName, nil)
	runtimeID := handlerTestRuntimeID(t)
	channelID := seedChannelForTest(t, "agent-inbox-drain-"+uuid.NewString(), testUserID)
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
	var queuedLifecycleEvents []events.Event
	var dispatchedLifecycleEvents []events.Event
	testHandler.Bus.Subscribe(protocol.EventTaskQueued, func(e events.Event) {
		queuedLifecycleEvents = append(queuedLifecycleEvents, e)
	})
	testHandler.Bus.Subscribe(protocol.EventTaskDispatch, func(e events.Event) {
		dispatchedLifecycleEvents = append(dispatchedLifecycleEvents, e)
	})

	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "setup context before mention", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("inbox-drain"), 0); err != nil {
		t.Fatalf("insert setup message: %v", err)
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "@"+agentName+" please answer", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("inbox-drain"), 0)
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
	inboxStatuses := func() []string {
		t.Helper()
		rows, err := testPool.Query(ctx, `
			SELECT COALESCE(details->>'status', '')
			FROM agent_activity_event
			WHERE workspace_id = $1
			  AND agent_id = $2
			  AND event_type = $3
			  AND details->>'inbox_event_id' = $4
			ORDER BY created_at ASC, id ASC`, testWorkspaceID, agentID, agentInboxStatusChangedEventType, got.ID)
		if err != nil {
			t.Fatalf("query inbox status activity: %v", err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var status string
			if err := rows.Scan(&status); err != nil {
				t.Fatalf("scan inbox status activity: %v", err)
			}
			out = append(out, status)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("inbox status rows: %v", err)
		}
		return out
	}
	assertAgentInboxTaskLifecycleEvent := func(eventType string, lifecycleEvents []events.Event, status string) {
		t.Helper()
		for _, event := range lifecycleEvents {
			if event.TaskID != got.ID {
				continue
			}
			if event.WorkspaceID != testWorkspaceID || event.ActorType != "system" {
				t.Fatalf("%s lifecycle event = %+v, want workspace %s system actor", eventType, event, testWorkspaceID)
			}
			payload, ok := event.Payload.(map[string]any)
			if !ok {
				t.Fatalf("%s lifecycle payload type = %T, want map", eventType, event.Payload)
			}
			if payload["task_id"] != got.ID || payload["inbox_event_id"] != got.ID || payload["agent_id"] != agentID || payload["status"] != status {
				t.Fatalf("%s lifecycle payload = %#v, want inbox event %s agent %s status %s", eventType, payload, got.ID, agentID, status)
			}
			if got.ChatSessionID != "" {
				if payload["chat_session_id"] != got.ChatSessionID {
					t.Fatalf("%s lifecycle chat_session_id = %#v, want %s", eventType, payload["chat_session_id"], got.ChatSessionID)
				}
			} else if _, ok := payload["chat_session_id"]; ok {
				t.Fatalf("%s lifecycle unexpectedly set chat_session_id for channel-only wake: %#v", eventType, payload["chat_session_id"])
			}
			return
		}
		t.Fatalf("missing %s lifecycle event for inbox event %s; got queued=%d dispatched=%d", eventType, got.ID, len(queuedLifecycleEvents), len(dispatchedLifecycleEvents))
	}
	assertAgentInboxTaskLifecycleEvent(protocol.EventTaskQueued, queuedLifecycleEvents, "queued")
	assertAgentInboxTaskLifecycleEvent(protocol.EventTaskDispatch, dispatchedLifecycleEvents, "running")
	statusEvent := db.AgentInboxEvent{
		ID:              parseUUID(got.ID),
		WorkspaceID:     parseUUID(testWorkspaceID),
		AgentID:         parseUUID(agentID),
		ChannelID:       parseUUID(channelID),
		SourceMessageID: parseUUID(trigger.ID),
		RequiresWake:    true,
	}
	if statuses := inboxStatuses(); len(statuses) != 0 {
		t.Fatalf("status activity after drain = %+v, want no generic working row", statuses)
	}
	testHandler.recordAgentInboxStatusActivity(ctx, statusEvent, parseUUID(runtimeID), parseUUID(got.DeliveryID), agentInboxStatusActivityWorking)
	if statuses := inboxStatuses(); len(statuses) != 0 {
		t.Fatalf("manual working status activity = %+v, want no generic working row", statuses)
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
	if statuses := inboxStatuses(); len(statuses) != 1 || statuses[0] != agentInboxStatusActivityIdle {
		t.Fatalf("status activity after ack = %+v, want [idle]", statuses)
	}
	testHandler.recordAgentInboxStatusActivity(ctx, statusEvent, parseUUID(runtimeID), parseUUID(got.DeliveryID), agentInboxStatusActivityIdle)
	if statuses := inboxStatuses(); len(statuses) != 1 || statuses[0] != agentInboxStatusActivityIdle {
		t.Fatalf("duplicate idle status activity = %+v, want one idle transition", statuses)
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

func TestChannelAgentInboxDrainDoesNotReplayFailedPromptBacklog(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentName := "Inbox Bounded Prompt Agent " + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentName, nil)
	runtimeID := handlerTestRuntimeID(t)
	channelID := seedChannelForTest(t, "agent-inbox-bounded-"+uuid.NewString(), testUserID)
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

	first, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "[@"+agentName+"](mention://agent/"+agentID+") first prompt", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("inbox-bounded"), 0)
	if err != nil {
		t.Fatalf("insert first trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, first, parseUUID(testUserID))

	var firstEventID string
	if err := testPool.QueryRow(ctx, `
		SELECT id FROM agent_inbox_event
		WHERE source_message_id = $1 AND agent_id = $2
		ORDER BY created_at DESC LIMIT 1`, first.ID, agentID).Scan(&firstEventID); err != nil {
		t.Fatalf("load first inbox event: %v", err)
	}
	setAgentInboxTerminalOutcomeForTest(t, firstEventID, "failed", true)
	const backlogMarker = "FAILED_PROMPT_BACKLOG_MARKER"
	if _, err := testPool.Exec(ctx, `
		UPDATE chat_message
		SET content = $2
		WHERE task_id = $1 AND role = 'user'`, firstEventID, strings.Repeat(backlogMarker, 12_000)); err != nil {
		t.Fatalf("inflate failed prompt: %v", err)
	}

	second, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "[@"+agentName+"](mention://agent/"+agentID+") current prompt only", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("inbox-bounded"), 0)
	if err != nil {
		t.Fatalf("insert second trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, second, parseUUID(testUserID))

	drainReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "agent-inbox-bounded-daemon")
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
		t.Fatalf("drain response missing runnable event: %s", drainRec.Body.String())
	}
	prompt := drainResp.Events[0].Task.ChatMessage
	if strings.Contains(prompt, backlogMarker) {
		t.Fatal("failed prompt backlog leaked into current inbox task")
	}
	if !strings.Contains(prompt, "current prompt only") {
		t.Fatalf("current prompt missing from inbox task: %q", prompt)
	}
	if len(prompt) >= 128*1024 {
		t.Fatalf("current inbox prompt is still large enough to hit Linux argv limits: %d bytes", len(prompt))
	}
}

func TestChannelAgentInboxCompleteDirectedMentionAfterNoPublicOutputObserveCompletes(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentHandle := "inbox-complete-agent-" + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentHandle, nil)
	runtimeID := handlerTestRuntimeID(t)
	channelID := seedChannelForTest(t, "agent-inbox-complete-"+uuid.NewString(), testUserID)
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
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "@"+agentHandle+" please answer from inbox complete", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("inbox-complete"), 0)
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
	if got.Task.ID != got.ID || got.Task.ChannelID == "" || got.Task.InboxEvent == nil || strings.TrimSpace(got.Task.ChatMessage) == "" {
		t.Fatalf("drained task = %+v, want channel-only inbox task for event %s", got.Task, got.ID)
	}
	if got.Task.ChatSessionID != "" {
		t.Fatalf("directed mention drain must be channel-only; got chat_session_id=%s", got.Task.ChatSessionID)
	}

	// Reproduce the exact interleaving from #646: while the original directed
	// public-response run is still active, the same agent consumes a later
	// no-public-output observation. The directed event's metadata must never
	// authorize completion final text as a visible channel message.
	observeSource, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "Later context that requires observation only", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("inbox-complete-observe"), 0)
	if err != nil {
		t.Fatalf("insert no-public-output observe source: %v", err)
	}
	var observeEventID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
			workspace_id, agent_session_id, conversation_id, channel_id,
			agent_id, runtime_id, source_message_id, reason,
			delivery_mode, response_mode, requires_wake, status,
			priority, seq_from, seq_to
		)
		SELECT workspace_id, agent_session_id, conversation_id, channel_id,
		       agent_id, runtime_id, $2, 'ambient',
		       'observe', 'no_public_output', false, 'acked',
		       0, $3, $3
		FROM agent_inbox_event
		WHERE id = $1
		RETURNING id
	`, got.ID, observeSource.ID, observeSource.Seq).Scan(&observeEventID); err != nil {
		t.Fatalf("seed consumed no-public-output observe: %v", err)
	}

	chatDoneEvents := 0
	testHandler.Bus.Subscribe(protocol.EventChatDone, func(e events.Event) {
		payload, ok := e.Payload.(protocol.ChatDonePayload)
		if ok && payload.TaskID == got.ID {
			chatDoneEvents++
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
	assertChannelMessageContentCount(t, channelID, reply, 0)
	// LRM-1079: channel-only wakes finalize without ChatDone; transport owns
	// visible output and complete settles ambient / typing directly.
	if got.ChatSessionID != "" && chatDoneEvents != 1 {
		t.Fatalf("legacy session completion chat-done events = %d, want 1", chatDoneEvents)
	}
	if got.ChatSessionID == "" && chatDoneEvents != 0 {
		t.Fatalf("channel-only completion unexpectedly published chat-done: %d", chatDoneEvents)
	}
	var completionReceipt struct {
		OK              bool   `json:"ok"`
		TerminalOutcome string `json:"terminal_outcome"`
		ResumeUnsafe    bool   `json:"resume_unsafe"`
	}
	if err := json.Unmarshal(completeRec.Body.Bytes(), &completionReceipt); err != nil {
		t.Fatalf("decode completion receipt: %v body=%s", err, completeRec.Body.String())
	}
	if !completionReceipt.OK || completionReceipt.TerminalOutcome != "no_reply" || completionReceipt.ResumeUnsafe {
		t.Fatalf("completion receipt = %+v, want ok=true terminal_outcome=no_reply resume_unsafe=false", completionReceipt)
	}
	var completionChatMessages int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM chat_message
		WHERE task_id = $1 AND role = 'assistant' AND content = $2
	`, got.ID, reply).Scan(&completionChatMessages); err != nil {
		t.Fatalf("count completion assistant messages: %v", err)
	}
	if completionChatMessages != 0 {
		t.Fatalf("completion assistant messages = %d, want 0", completionChatMessages)
	}

	var status, terminalOutcome, terminalDeliveryID, failureReason, lastError string
	var retryable bool
	var terminalAt pgtype.Timestamptz
	if err := testPool.QueryRow(ctx, `
		SELECT status,
		       COALESCE(terminal_outcome, ''),
		       COALESCE(terminal_delivery_id::text, ''),
		       retryable,
		       terminal_at,
		       COALESCE(failure_reason, ''),
		       COALESCE(last_error, '')
		FROM agent_inbox_event
		WHERE id = $1`, got.ID).Scan(
		&status, &terminalOutcome, &terminalDeliveryID, &retryable, &terminalAt, &failureReason, &lastError,
	); err != nil {
		t.Fatalf("load inbox event status: %v", err)
	}
	if status != "acked" {
		t.Fatalf("inbox event status = %q, want acked", status)
	}
	if terminalOutcome != "no_reply" || terminalDeliveryID != got.DeliveryID || retryable || !terminalAt.Valid {
		t.Fatalf("inbox completion terminal projection = outcome:%q delivery:%q retryable:%v terminal_at:%v, want no_reply/%s/non-retryable/timestamp", terminalOutcome, terminalDeliveryID, retryable, terminalAt.Valid, got.DeliveryID)
	}
	if failureReason != "" || lastError != "" {
		t.Fatalf("inbox completion failure = reason:%q error:%q, want empty", failureReason, lastError)
	}
	var observeMode, observeResponseMode, observeStatus string
	if err := testPool.QueryRow(ctx, `
		SELECT delivery_mode, response_mode, status
		FROM agent_inbox_event
		WHERE id = $1
	`, observeEventID).Scan(&observeMode, &observeResponseMode, &observeStatus); err != nil {
		t.Fatalf("load consumed observe event: %v", err)
	}
	if observeMode != "observe" || observeResponseMode != "no_public_output" || observeStatus != "acked" {
		t.Fatalf("observe event = %s/%s/%s, want observe/no_public_output/acked", observeMode, observeResponseMode, observeStatus)
	}

	statusRows, err := testPool.Query(ctx, `
		SELECT COALESCE(details->>'status', '')
		FROM agent_activity_event
		WHERE workspace_id = $1
		  AND agent_id = $2
		  AND event_type = $3
		  AND details->>'inbox_event_id' = $4
		ORDER BY created_at ASC, id ASC`, testWorkspaceID, agentID, agentInboxStatusChangedEventType, got.ID)
	if err != nil {
		t.Fatalf("query completion status activity: %v", err)
	}
	defer statusRows.Close()
	var statuses []string
	for statusRows.Next() {
		var status string
		if err := statusRows.Scan(&status); err != nil {
			t.Fatalf("scan completion status activity: %v", err)
		}
		statuses = append(statuses, status)
	}
	if err := statusRows.Err(); err != nil {
		t.Fatalf("completion status activity rows: %v", err)
	}
	if len(statuses) != 1 || statuses[0] != agentInboxStatusActivityIdle {
		t.Fatalf("completion status activity = %+v, want [idle]", statuses)
	}

	var failureCount int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM agent_activity_event
		WHERE workspace_id = $1
		  AND agent_id = $2
		  AND event_type = 'agent_inbox_failed'
		  AND details->>'inbox_event_id' = $3`, testWorkspaceID, agentID, got.ID).Scan(&failureCount); err != nil {
		t.Fatalf("query inbox failure activity: %v", err)
	}
	if failureCount != 0 {
		t.Fatalf("inbox failure activity count = %d, want 0", failureCount)
	}
}

func TestChannelAgentInboxCompletionInfersAbandonedFreshnessDraft(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentName := "Inbox Held Draft Agent " + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentName, nil)
	var agentHandle string
	if err := testPool.QueryRow(ctx, `SELECT name FROM agent WHERE id = $1`, agentID).Scan(&agentHandle); err != nil {
		t.Fatalf("load agent handle: %v", err)
	}
	runtimeID := handlerTestRuntimeID(t)
	channelID := seedChannelForTest(t, "agent-inbox-held-"+uuid.NewString(), testUserID)
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
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "@"+agentHandle+" reply after reviewing newer context", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("inbox-held"), 0)
	if err != nil {
		t.Fatalf("insert mention trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, trigger, parseUUID(testUserID))

	drainReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "agent-inbox-held-daemon")
	drainReq = withURLParam(drainReq, "runtimeId", runtimeID)
	drainRec := httptest.NewRecorder()
	testHandler.DrainAgentInboxByRuntime(drainRec, drainReq)
	if drainRec.Code != http.StatusOK {
		t.Fatalf("drain inbox: status=%d body=%s", drainRec.Code, drainRec.Body.String())
	}
	var drainResp DrainAgentInboxResponse
	if err := json.Unmarshal(drainRec.Body.Bytes(), &drainResp); err != nil || len(drainResp.Events) != 1 {
		t.Fatalf("decode held inbox drain: err=%v body=%s", err, drainRec.Body.String())
	}
	got := drainResp.Events[0]

	target := "#" + channelNameForTransportTest(t, channelID)
	producerFactID := "freshness_decision_fact:" + uuid.NewString()[:8]
	holdContext, err := json.Marshal(map[string]any{
		"held":             true,
		"subtype":          "freshness",
		"decision":         "local_hold",
		"producer_fact_id": producerFactID,
		"seen_up_to_seq":   1,
		"latest_seq":       2,
	})
	if err != nil {
		t.Fatalf("encode freshness hold context: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_transport_audit (
			workspace_id, inbox_event_id, agent_id, action, target, channel_id,
			client_message_id, context_pack, created_at
		) VALUES ($1, $2, $3, 'message_send', $4, $5, 'held-client', $6::jsonb, now() - interval '2 seconds')`,
		testWorkspaceID, got.ID, agentID, target, channelID, holdContext); err != nil {
		t.Fatalf("record freshness hold audit: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_transport_draft (
			workspace_id, agent_id, inbox_event_id, decision_fact_id,
			target, channel_id, content, parts, client_message_id,
			seen_up_to_seq, held_from_seq, held_to_seq, shown_from_seq, shown_to_seq
		) VALUES ($1, $2, $3, $4, $5, $6, 'saved but deliberately not sent', '[]'::jsonb,
		          'held-client', 1, 2, 2, 2, 2)`,
		testWorkspaceID, agentID, got.ID, producerFactID, target, channelID); err != nil {
		t.Fatalf("record freshness draft: %v", err)
	}

	completeReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+got.ID+"/complete", CompleteAgentInboxEventRequest{
		DeliveryID: got.DeliveryID,
		LeaseToken: got.LeaseToken,
		TaskCompleteRequest: TaskCompleteRequest{
			Output:             "suppressed final after freshness hold",
			TransportAttempted: true,
		},
	}, testWorkspaceID, "agent-inbox-held-daemon")
	completeReq = withURLParam(completeReq, "eventId", got.ID)
	completeRec := httptest.NewRecorder()
	testHandler.CompleteAgentInboxEvent(completeRec, completeReq)
	if completeRec.Code != http.StatusOK {
		t.Fatalf("complete held inbox event: status=%d body=%s", completeRec.Code, completeRec.Body.String())
	}
	var completionReceipt struct {
		TerminalOutcome string `json:"terminal_outcome"`
		ResumeUnsafe    bool   `json:"resume_unsafe"`
	}
	if err := json.Unmarshal(completeRec.Body.Bytes(), &completionReceipt); err != nil {
		t.Fatalf("decode held completion receipt: %v body=%s", err, completeRec.Body.String())
	}
	if completionReceipt.TerminalOutcome != "no_reply" || completionReceipt.ResumeUnsafe {
		t.Fatalf("held completion receipt = %+v, want no_reply and resume_unsafe=false", completionReceipt)
	}

	var terminalOutcome string
	if err := testPool.QueryRow(ctx, `SELECT COALESCE(terminal_outcome, '') FROM agent_inbox_event WHERE id = $1`, got.ID).Scan(&terminalOutcome); err != nil {
		t.Fatalf("load abandoned inbox terminal outcome: %v", err)
	}
	if terminalOutcome != "no_reply" {
		t.Fatalf("abandoned inbox terminal outcome = %q, want no_reply", terminalOutcome)
	}

	var draftExists, unresolvedHold bool
	if err := testPool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM agent_transport_draft
			WHERE inbox_event_id = $1 AND target = $2
		)`, got.ID, target).Scan(&draftExists); err != nil {
		t.Fatalf("check abandoned draft: %v", err)
	}
	unresolvedHold = testHandler.inboxEventHasAgentTransportFreshnessHold(ctx, parseUUID(got.ID))
	if draftExists || unresolvedHold {
		t.Fatalf("abandoned draft state = exists:%v unresolved_hold:%v, want false/false", draftExists, unresolvedHold)
	}

	var resolutionAudits, resolutionActivities int
	var resolutionSeconds float64
	if err := testPool.QueryRow(ctx, `
		SELECT count(*), COALESCE(max((context_pack->>'freshness_hold_resolution_seconds')::double precision), 0)
		FROM agent_task_transport_audit
		WHERE inbox_event_id = $1
		  AND action = 'message_send'
		  AND channel_message_id IS NULL
		  AND context_pack->>'freshness_resolution' = 'true'
		  AND context_pack->>'producer_fact_id' = $2
		  AND context_pack->>'outcome' = 'abandoned'`,
		got.ID, producerFactID).Scan(&resolutionAudits, &resolutionSeconds); err != nil {
		t.Fatalf("load abandoned resolution audit: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_activity_event
		WHERE workspace_id = $1
		  AND agent_id = $2
		  AND event_type = 'send_freshness_resolved'
		  AND details->>'producer_fact_id' = $3
		  AND details->>'outcome' = 'abandoned'
		  AND NOT (details ? 'message_id')`,
		testWorkspaceID, agentID, producerFactID).Scan(&resolutionActivities); err != nil {
		t.Fatalf("load abandoned resolution activity: %v", err)
	}
	if resolutionAudits != 1 || resolutionActivities != 1 || resolutionSeconds < 2 {
		t.Fatalf("abandoned resolution = audits:%d activities:%d seconds:%f, want 1/1/>=2", resolutionAudits, resolutionActivities, resolutionSeconds)
	}

	var failureCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_activity_event
		WHERE workspace_id = $1 AND agent_id = $2 AND event_type = 'agent_inbox_failed'
		  AND details->>'inbox_event_id' = $3`, testWorkspaceID, agentID, got.ID).Scan(&failureCount); err != nil {
		t.Fatalf("count abandoned inbox failures: %v", err)
	}
	if failureCount != 0 {
		t.Fatalf("abandoned inbox failure activity = %d, want 0", failureCount)
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
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "[@"+agentName+"](mention://agent/"+agentID+") write hello_world.txt", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("inbox-activity"), 0)
	if err != nil {
		t.Fatalf("insert mention trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, trigger, parseUUID(testUserID))

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

	rows, err := testPool.Query(ctx, `
		SELECT event_kind, event_type, visibility, COALESCE(reason_code, ''), COALESCE(message, ''), details
		FROM agent_activity_event
		WHERE workspace_id = $1
		  AND agent_id = $2
		  AND details->>'inbox_event_id' = $3
		  AND details ? 'seq'
		ORDER BY (details->>'seq')::int ASC`, testWorkspaceID, agentID, got.ID)
	if err != nil {
		t.Fatalf("query activity rows: %v", err)
	}
	defer rows.Close()

	type activityRow struct {
		kind       string
		eventType  string
		visibility string
		reasonCode string
		message    string
		details    map[string]any
	}
	var activity []activityRow
	for rows.Next() {
		var row activityRow
		var raw []byte
		if err := rows.Scan(&row.kind, &row.eventType, &row.visibility, &row.reasonCode, &row.message, &raw); err != nil {
			t.Fatalf("scan activity row: %v", err)
		}
		if err := json.Unmarshal(raw, &row.details); err != nil {
			t.Fatalf("decode activity details: %v", err)
		}
		activity = append(activity, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("activity rows error: %v", err)
	}
	if len(activity) != 9 {
		t.Fatalf("activity rows = %+v, want 9", activity)
	}
	if activity[0].kind != activityKindThinking || activity[0].eventType != "runtime_thinking" || activity[0].visibility != "diagnostic_only" {
		t.Fatalf("thinking row = %+v, want diagnostic thinking/runtime_thinking", activity[0])
	}
	if activity[1].kind != activityKindToolCall || activity[1].eventType != "tool_use" || activity[1].visibility != "user_facing" {
		t.Fatalf("tool row = %+v, want user-facing tool_use", activity[1])
	}
	if activity[1].details["tool"] != "bash" || activity[1].details["raw_tool"] != "terminal" || activity[1].details["tool_target"] != "cat secret" || activity[1].details["summary_kind"] != "command" || activity[1].details["command"] != "cat secret" {
		t.Fatalf("tool details = %+v, want canonical bash/raw terminal/command summary", activity[1].details)
	}
	if strings.Contains(fmt.Sprint(activity[1].details), "/tmp/hello_world.txt") {
		t.Fatalf("tool details leaked path-backed shell command target: %+v", activity[1].details)
	}
	if activity[2].kind != activityKindToolOutput || activity[2].visibility != "diagnostic_only" {
		t.Fatalf("tool result row = %+v, want diagnostic tool_output", activity[2])
	}
	if activity[3].kind != activityKindCustom || activity[3].eventType != "runtime_text" || activity[3].visibility != "diagnostic_only" {
		t.Fatalf("runtime log row = %+v, want diagnostic runtime_text", activity[3])
	}
	if activity[4].kind != activityKindCustom || activity[4].eventType != "runtime_text" || activity[4].visibility != "diagnostic_only" {
		t.Fatalf("runtime text row = %+v, want diagnostic runtime_text", activity[4])
	}
	if activity[5].kind != activityKindCustom || activity[5].eventType != "unmapped_tool_name" || activity[5].visibility != "diagnostic_only" || activity[5].reasonCode != "unmapped_tool_name" {
		t.Fatalf("status-like missing command row = %+v, want diagnostic unmapped gap", activity[5])
	}
	if activity[5].details["unmapped_tool_name"] != "running" || activity[5].details["tool"] != nil || activity[5].details["tool_target"] != nil {
		t.Fatalf("status-like missing command details = %+v, want unmapped running without user-facing tool/target", activity[5].details)
	}
	if activity[5].details["inbox_event_id"] != got.ID || activity[5].details["delivery_id"] != got.DeliveryID || activity[5].details["source_message_id"] != trigger.ID || activity[5].details["seq"] == nil {
		t.Fatalf("status-like missing command source details = %+v, want inbox/delivery/source/seq refs", activity[5].details)
	}
	if activity[6].kind != activityKindToolCall || activity[6].eventType != "tool_use" || activity[6].visibility != "user_facing" {
		t.Fatalf("write file row = %+v, want user-facing tool_use", activity[6])
	}
	writeTarget, _ := activity[6].details["tool_target"].(string)
	if activity[6].details["tool"] != "write_file" || !strings.HasSuffix(writeTarget, "/Code/multica/server/internal/handler/channel_test.go") || activity[6].details["summary_kind"] != "file_path" {
		t.Fatalf("write file details = %+v, want redacted source-backed file path", activity[6].details)
	}
	if activity[7].kind != activityKindToolCall || activity[7].eventType != "tool_use" || activity[7].visibility != "user_facing" {
		t.Fatalf("read file row = %+v, want user-facing tool_use", activity[7])
	}
	if activity[7].details["tool"] != "read_file" || activity[7].details["raw_tool"] != "read" || activity[7].details["tool_target"] != "/tmp/test.go" || activity[7].details["summary_kind"] != "file_path" || activity[7].details["command"] != nil || activity[7].details["path"] != "/tmp/test.go" || activity[7].details["scope"] != "/repo" {
		t.Fatalf("read file details = %+v, want read source facts without invented command", activity[7].details)
	}
	if activity[8].kind != activityKindThinking || activity[8].eventType != "runtime_phase" || activity[8].visibility != "diagnostic_only" || activity[8].message != "" || activity[8].details["phase_status"] != true {
		t.Fatalf("legacy phase row = %+v, want diagnostic-only", activity[8])
	}
}

func TestOutputClaimsFileDeliveryDetectsCreatedArtifacts(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "antigravity created file wording",
			content: "I have created hello_antigravity.txt",
			want:    true,
		},
		{
			name:    "generated artifact wording",
			content: "Generated report.csv for you.",
			want:    true,
		},
		{
			name:    "chinese created file wording",
			content: "已创建 hello_world.txt",
			want:    true,
		},
		{
			name:    "filename without delivery marker",
			content: "hello_world.txt",
			want:    false,
		},
		{
			name:    "marker without filename",
			content: "I have created the file",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := outputClaimsFileDelivery(tt.content); got != tt.want {
				t.Fatalf("outputClaimsFileDelivery(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
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
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "[@"+agentName+"](mention://agent/"+agentID+") send hello_world.txt", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("inbox-artifact"), 0)
	if err != nil {
		t.Fatalf("insert mention trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, trigger, parseUUID(testUserID))

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

	var artifactRows int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_activity_event
		WHERE workspace_id = $1
		  AND agent_id = $2
		  AND event_type = 'artifact_missing'
		  AND details->>'inbox_event_id' = $3`, testWorkspaceID, agentID, got.ID).Scan(&artifactRows); err != nil {
		t.Fatalf("count artifact boundary activity: %v", err)
	}
	if artifactRows != 0 {
		t.Fatalf("artifact_missing rows = %d, want 0 for suppressed final output", artifactRows)
	}

	var outputRows int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_activity_event
		WHERE workspace_id = $1
		  AND agent_id = $2
		  AND event_type = 'message_sent'
		  AND details->>'inbox_event_id' = $3`, testWorkspaceID, agentID, got.ID).Scan(&outputRows); err != nil {
		t.Fatalf("count output rows: %v", err)
	}
	if outputRows != 0 {
		t.Fatalf("message_sent rows = %d, want 0 for missing attachment boundary", outputRows)
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
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
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
		"target": "#" + channelNameForTransportTest(t, channelID),
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

	var terminalOutcome string
	if err := testPool.QueryRow(ctx, `
		SELECT COALESCE(terminal_outcome, '')
		FROM agent_inbox_event
		WHERE id = $1`, got.ID).Scan(&terminalOutcome); err != nil {
		t.Fatalf("load inbox terminal outcome: %v", err)
	}
	if terminalOutcome != "replied" {
		t.Fatalf("inbox terminal outcome = %q, want replied after transport send", terminalOutcome)
	}

	var failureRows int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_activity_event
		WHERE workspace_id = $1
		  AND agent_id = $2
		  AND event_type = 'agent_inbox_failed'
		  AND details->>'inbox_event_id' = $3`, testWorkspaceID, agentID, got.ID).Scan(&failureRows); err != nil {
		t.Fatalf("count inbox failure activity: %v", err)
	}
	if failureRows != 0 {
		t.Fatalf("inbox failure activity rows = %d, want 0 after transport send", failureRows)
	}
}

func TestChannelAgentInboxFailureAfterVisibleTransportSendSettlesAsReplied(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentName := "Inbox Post Send Failure Agent " + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentName, nil)
	runtimeID := handlerTestRuntimeID(t)
	if _, err := testPool.Exec(ctx, `UPDATE agent_runtime SET owner_id = $1 WHERE id = $2`, testUserID, runtimeID); err != nil {
		t.Fatalf("seed runtime owner: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `UPDATE agent_runtime SET owner_id = NULL WHERE id = $1`, runtimeID)
	})
	channelID := seedChannelForTest(t, "agent-inbox-post-send-fail-"+uuid.NewString(), testUserID)
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
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "[@"+agentName+"](mention://agent/"+agentID+") reply before failing", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("post-send-fail"), 0)
	if err != nil {
		t.Fatalf("insert mention trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, trigger, parseUUID(testUserID))

	drainReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "agent-inbox-post-send-fail-daemon")
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
		t.Fatalf("drain response missing inbox task: %s", drainRec.Body.String())
	}
	got := drainResp.Events[0]

	visibleReply := "visible reply before kiro post-send failure " + uuid.NewString()
	sendReq := newRequest(http.MethodPost, "/api/agent/messages/send", map[string]any{
		"target":            "#" + channelNameForTransportTest(t, channelID),
		"content":           visibleReply,
		"client_message_id": "post-send-fail-" + uuid.NewString(),
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
		t.Fatalf("inbox transport send: status=%d body=%s", sendRec.Code, sendRec.Body.String())
	}

	failReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-inbox/events/"+got.ID+"/fail", FailAgentInboxEventRequest{
		DeliveryID:    got.DeliveryID,
		LeaseToken:    got.LeaseToken,
		Error:         "kiro session/prompt failed: session/prompt: Internal error (code=-32603, data=Kiro failed to generate a response)",
		FailureReason: "agent_error.provider_server_error",
		ReasonCode:    "agent_error.provider_server_error",
	}, testWorkspaceID, "agent-inbox-post-send-fail-daemon")
	failReq = withURLParam(failReq, "eventId", got.ID)
	failRec := httptest.NewRecorder()
	testHandler.FailAgentInboxEvent(failRec, failReq)
	if failRec.Code != http.StatusOK {
		t.Fatalf("fail inbox after send: status=%d body=%s", failRec.Code, failRec.Body.String())
	}

	var eventStatus, deliveryStatus, terminalOutcome, terminalDeliveryID, lastError string
	var retryable bool
	var terminalAt pgtype.Timestamptz
	if err := testPool.QueryRow(ctx, `
		SELECT e.status,
		       d.status,
		       COALESCE(e.terminal_outcome, ''),
		       COALESCE(e.terminal_delivery_id::text, ''),
		       COALESCE(e.last_error, ''),
		       e.retryable,
		       e.terminal_at
		FROM agent_inbox_event e
		JOIN agent_event_delivery d ON d.inbox_event_id = e.id
		WHERE e.id = $1 AND d.id = $2`, got.ID, got.DeliveryID).Scan(&eventStatus, &deliveryStatus, &terminalOutcome, &terminalDeliveryID, &lastError, &retryable, &terminalAt); err != nil {
		t.Fatalf("load inbox event: %v", err)
	}
	if eventStatus != "acked" || deliveryStatus != "acked" {
		t.Fatalf("terminal inbox states = event:%q delivery:%q, want acked/acked", eventStatus, deliveryStatus)
	}
	if terminalOutcome != "replied" || terminalDeliveryID != got.DeliveryID || retryable || !terminalAt.Valid {
		t.Fatalf("terminal projection = outcome:%q delivery:%q retryable:%v terminal_at:%v, want replied/%s/not-retryable/timestamp", terminalOutcome, terminalDeliveryID, retryable, terminalAt.Valid, got.DeliveryID)
	}
	if !strings.Contains(lastError, "Kiro failed to generate a response") {
		t.Fatalf("last_error = %q, want retained post-send failure text", lastError)
	}

	var visibleAgentMessages int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1
		  AND author_type = 'agent'
		  AND author_id = $2
		  AND content = $3`, channelID, agentID, visibleReply).Scan(&visibleAgentMessages); err != nil {
		t.Fatalf("count visible agent messages: %v", err)
	}
	if visibleAgentMessages != 1 {
		t.Fatalf("visible agent channel messages = %d, want 1", visibleAgentMessages)
	}

	var failureActivity int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_activity_event
		WHERE workspace_id = $1
		  AND agent_id = $2
		  AND event_type = 'agent_inbox_failed'
		  AND details->>'inbox_event_id' = $3`, testWorkspaceID, agentID, got.ID).Scan(&failureActivity); err != nil {
		t.Fatalf("count failure activity: %v", err)
	}
	if failureActivity != 0 {
		t.Fatalf("agent_inbox_failed activity rows = %d, want 0 after visible reply", failureActivity)
	}
}

func TestChannelAgentInboxTransportBearerTokenThroughMiddleware(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentName := "Inbox Bearer Agent " + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentName, nil)
	runtimeID := handlerTestRuntimeID(t)
	if _, err := testPool.Exec(ctx, `UPDATE agent_runtime SET owner_id = $1 WHERE id = $2`, testUserID, runtimeID); err != nil {
		t.Fatalf("seed runtime owner: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `UPDATE agent_runtime SET owner_id = NULL WHERE id = $1`, runtimeID)
	})
	channelID := seedChannelForTest(t, "agent-inbox-bearer-"+uuid.NewString(), testUserID)
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
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "[@"+agentName+"](mention://agent/"+agentID+") send through bearer token", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("inbox-bearer"), 0)
	if err != nil {
		t.Fatalf("insert mention trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, trigger, parseUUID(testUserID))

	drainReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "agent-inbox-bearer-daemon")
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

	router := chi.NewRouter()
	router.Group(func(r chi.Router) {
		r.Use(middleware.Auth(testHandler.Queries, nil, nil))
		r.Use(middleware.RequireWorkspaceMember(testHandler.Queries))
		r.Post("/api/agent/messages/send", testHandler.AgentTransportSendMessage)
	})

	clientID := "inbox-bearer-" + uuid.NewString()
	body := map[string]any{
		"target": "#" + channelNameForTransportTest(t, channelID),
		"parts": []protocol.MessagePart{{
			Type:      protocol.MessagePartTypeSticker,
			StickerID: "huaji",
		}},
		"client_message_id": clientID,
	}
	sendReq := newRequest(http.MethodPost, "/api/agent/messages/send", body)
	sendReq.Header.Set("Authorization", "Bearer "+got.Task.AuthToken)
	sendReq.Header.Set("X-Workspace-ID", testWorkspaceID)
	sendRec := httptest.NewRecorder()
	router.ServeHTTP(sendRec, sendReq)
	if sendRec.Code != http.StatusCreated {
		t.Fatalf("inbox bearer transport send: status=%d body=%s", sendRec.Code, sendRec.Body.String())
	}
	var sendBody AgentTransportSendResponse
	if err := json.Unmarshal(sendRec.Body.Bytes(), &sendBody); err != nil {
		t.Fatalf("decode inbox bearer transport send: %v", err)
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
		WHERE agent_id = $2 AND action = 'message_send' AND client_message_id = $3`,
		got.ID, agentID, clientID).Scan(&taskAuditRows, &inboxAuditRows); err != nil {
		t.Fatalf("count transport audit rows: %v", err)
	}
	if taskAuditRows != 0 || inboxAuditRows != 1 {
		t.Fatalf("transport audit task rows=%d inbox rows=%d, want 0/1", taskAuditRows, inboxAuditRows)
	}
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
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
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
	testHandler.dispatchChannelAmbientDelivery(ctx, ch, first)

	// Shared handlerTestRuntimeID is reused across tests; cancel leftover pending
	// wakes for other agents so drain returns this ambient observe event.
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_inbox_event e
		SET status = 'cancelled', updated_at = now()
		FROM agent a
		WHERE a.id = e.agent_id
		  AND a.runtime_id = $1
		  AND e.agent_id <> $2
		  AND e.status IN ('pending', 'draining')`, runtimeID, agentID); err != nil {
		t.Fatalf("cancel foreign pending inbox events: %v", err)
	}

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
	if len(got.Messages) != 2 || got.Messages[0].Type != "system" || got.Messages[1].Content != "ordinary ambient one" {
		t.Fatalf("ambient drain messages = %+v, want membership row followed by first ambient message", got.Messages)
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
	testHandler.dispatchChannelAmbientDelivery(ctx, ch, second)

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

func TestChannelHumanMentionDirectsTargetAndOrdinaryWakesOnlyUnmutedBystanders(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	targetID := createHandlerTestAgent(t, "Ambient Target "+uuid.NewString()[:8], nil)
	bystanderID := createHandlerTestAgent(t, "Ambient Bystander "+uuid.NewString()[:8], nil)
	mutedBystanderID := createHandlerTestAgent(t, "Muted Ambient Bystander "+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "ambient-skip-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, muted_at)
		VALUES ($1, $2, 'agent', $3, now()),
		       ($1, $2, 'agent', $4, NULL),
		       ($1, $2, 'agent', $5, now())
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, targetID, bystanderID, mutedBystanderID); err != nil {
		t.Fatalf("seed agent members: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}

	mentionContent := "@target please handle"
	mentionParts := []protocol.MessagePart{{Type: protocol.MessagePartTypeReference, RefType: "mention", RefSubType: "agent", RefID: targetID, Label: "@target"}}
	humanMention, err := testHandler.insertChannelMessageWithParts(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", mentionContent, mentionParts, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert human mention: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, humanMention, parseUUID(testUserID))

	type inboxFact struct {
		agentID      string
		reason       string
		requiresWake bool
		priority     int32
	}
	rows, err := testPool.Query(ctx, `
		SELECT agent_id, reason, requires_wake, priority
		FROM agent_inbox_event
		WHERE channel_id = $1 AND source_message_id = $2
		ORDER BY agent_id`, channelID, humanMention.ID)
	if err != nil {
		t.Fatalf("load mention inbox facts: %v", err)
	}
	defer rows.Close()
	facts := map[string]inboxFact{}
	for rows.Next() {
		var fact inboxFact
		if err := rows.Scan(&fact.agentID, &fact.reason, &fact.requiresWake, &fact.priority); err != nil {
			t.Fatalf("scan mention inbox fact: %v", err)
		}
		if _, duplicate := facts[fact.agentID]; duplicate {
			t.Fatalf("duplicate inbox fact for agent %s", fact.agentID)
		}
		facts[fact.agentID] = fact
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate mention inbox facts: %v", err)
	}
	if len(facts) != 2 {
		t.Fatalf("mention created %d inbox facts, want target + unmuted bystander only: %+v", len(facts), facts)
	}
	if got := facts[targetID]; got.reason != "mention" || !got.requiresWake || got.priority != channelDirectedWakePriority {
		t.Fatalf("target fact = %+v, want directed mention wake", got)
	}
	if got := facts[bystanderID]; got.reason != channelMessageWakeReason || !got.requiresWake || got.priority != channelMessageWakePriority {
		t.Fatalf("bystander fact = %+v, want ordinary coalesced channel wake", got)
	}
	if _, ok := facts[mutedBystanderID]; ok {
		t.Fatalf("muted non-target received inbox fact: %+v", facts[mutedBystanderID])
	}

	var targetCount, bystanderCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE agent_id = $2),
		       count(*) FILTER (WHERE agent_id = $3)
		FROM agent_inbox_event
		WHERE channel_id = $1 AND source_message_id = $4`, channelID, targetID, bystanderID, humanMention.ID).Scan(&targetCount, &bystanderCount); err != nil {
		t.Fatalf("count inbox events: %v", err)
	}
	if targetCount != 1 || bystanderCount != 1 {
		t.Fatalf("per-agent inbox counts target=%d bystander=%d, want 1/1", targetCount, bystanderCount)
	}
}

func TestChannelAgentMentionDoesNotOrdinaryWakeNonTargets(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	authorID := createHandlerTestAgent(t, "Agent Mention Author "+uuid.NewString()[:8], nil)
	targetID := createHandlerTestAgent(t, "Agent Mention Target "+uuid.NewString()[:8], nil)
	bystanderID := createHandlerTestAgent(t, "Agent Mention Bystander "+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "agent-mention-loop-boundary-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3),
		       ($1, $2, 'agent', $4),
		       ($1, $2, 'agent', $5)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, authorID, targetID, bystanderID); err != nil {
		t.Fatalf("seed agent members: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}

	mentionParts := []protocol.MessagePart{{Type: protocol.MessagePartTypeReference, RefType: "mention", RefSubType: "agent", RefID: targetID, Label: "@target"}}
	agentMention, err := testHandler.insertChannelMessageWithParts(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "agent", parseUUID(authorID), "Agent Author", "@target please review", mentionParts, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 1)
	if err != nil {
		t.Fatalf("insert agent mention: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, agentMention, parseUUID(testUserID))

	assertChannelAgentWakeReasonPriority(t, channelID, targetID, agentMention.ID, "mention", channelDirectedWakePriority)
	var nonTargetWakeCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_inbox_event
		WHERE channel_id = $1
		  AND source_message_id = $2
		  AND agent_id IN ($3, $4)
		  AND requires_wake`, channelID, agentMention.ID, authorID, bystanderID).Scan(&nonTargetWakeCount); err != nil {
		t.Fatalf("count agent-authored non-target wakes: %v", err)
	}
	if nonTargetWakeCount != 0 {
		t.Fatalf("agent-authored mention created %d non-target wakes, want 0", nonTargetWakeCount)
	}
}

func TestChannelDirectedIssueMentionCoalescesPendingEvent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Issue Coalesce Agent "+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "issue-coalesce-"+uuid.NewString(), testUserID)
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
	makeTrigger := func(content string) ChannelMessageResponse {
		parts := []protocol.MessagePart{
			{Type: protocol.MessagePartTypeReference, RefType: "mention", RefSubType: "agent", RefID: agentID, Label: "@worker"},
			{Type: protocol.MessagePartTypeReference, RefType: "issue-ref", RefSubType: "issue", RefID: "00000000-0000-0000-0000-000000000999", Label: "LRM-999"},
		}
		msg, err := testHandler.insertChannelMessageWithParts(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", content, parts, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
		if err != nil {
			t.Fatalf("insert mention trigger: %v", err)
		}
		return msg
	}
	first := makeTrigger("@worker please handle LRM-999")
	testHandler.dispatchChannelMessageToAgents(ctx, ch, first, parseUUID(testUserID))
	second := makeTrigger("@worker updated acceptance for LRM-999")
	testHandler.dispatchChannelMessageToAgents(ctx, ch, second, parseUUID(testUserID))

	var count int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_inbox_event
		WHERE channel_id = $1 AND agent_id = $2 AND reason = 'mention' AND requires_wake`, channelID, agentID).Scan(&count); err != nil {
		t.Fatalf("count directed inbox events: %v", err)
	}
	var sourceMessageID pgtype.UUID
	if err := testPool.QueryRow(ctx, `
		SELECT source_message_id
		FROM agent_inbox_event
		WHERE channel_id = $1 AND agent_id = $2 AND reason = 'mention' AND requires_wake`, channelID, agentID).Scan(&sourceMessageID); err != nil {
		t.Fatalf("load directed inbox event: %v", err)
	}
	if count != 1 || uuidToString(sourceMessageID) != second.ID {
		t.Fatalf("coalesced count/source = %d/%s, want 1/%s", count, uuidToString(sourceMessageID), second.ID)
	}
}

func TestChannelDirectedIssueMentionDoesNotCoalesceDrainingEvent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Issue Draining Agent "+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "issue-draining-"+uuid.NewString(), testUserID)
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
	makeTrigger := func(content string) ChannelMessageResponse {
		parts := []protocol.MessagePart{
			{Type: protocol.MessagePartTypeReference, RefType: "mention", RefSubType: "agent", RefID: agentID, Label: "@worker"},
			{Type: protocol.MessagePartTypeReference, RefType: "issue-ref", RefSubType: "issue", RefID: "00000000-0000-0000-0000-000000000998", Label: "LRM-998"},
		}
		msg, err := testHandler.insertChannelMessageWithParts(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", content, parts, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
		if err != nil {
			t.Fatalf("insert mention trigger: %v", err)
		}
		return msg
	}
	first := makeTrigger("@worker please handle LRM-998")
	testHandler.dispatchChannelMessageToAgents(ctx, ch, first, parseUUID(testUserID))
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_inbox_event
		SET status = 'draining'
		WHERE channel_id = $1 AND agent_id = $2 AND reason = 'mention'`, channelID, agentID); err != nil {
		t.Fatalf("mark event draining: %v", err)
	}
	second := makeTrigger("@worker urgent new direction for LRM-998")
	testHandler.dispatchChannelMessageToAgents(ctx, ch, second, parseUUID(testUserID))

	var total, secondEvents int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)::int,
		       count(*) FILTER (WHERE source_message_id = $3)::int
		FROM agent_inbox_event
		WHERE channel_id = $1 AND agent_id = $2 AND reason = 'mention' AND requires_wake`, channelID, agentID, second.ID).Scan(&total, &secondEvents); err != nil {
		t.Fatalf("count directed inbox events: %v", err)
	}
	if total != 2 || secondEvents != 1 {
		t.Fatalf("directed events total/second = %d/%d, want 2/1", total, secondEvents)
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

	var activityReason, activityFailureReason, activityMessage string
	if err := testPool.QueryRow(ctx, `
		SELECT reason_code, details->>'failure_reason', message
		FROM agent_activity_event
		WHERE workspace_id = $1
		  AND agent_id = $2
		  AND event_type = 'agent_inbox_failed'
		  AND details->>'inbox_event_id' = $3
		ORDER BY created_at DESC
		LIMIT 1`, testWorkspaceID, agentID, got.ID).Scan(&activityReason, &activityFailureReason, &activityMessage); err != nil {
		t.Fatalf("load inbox failure activity event: %v", err)
	}
	if activityReason != "provider_auth_required" || activityFailureReason != "agent_error.provider_auth_or_access" || !strings.Contains(activityMessage, "grok not logged in") {
		t.Fatalf("activity event = reason:%q failure:%q message:%q, want provider_auth_required/provider_auth/grok not logged in", activityReason, activityFailureReason, activityMessage)
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

func TestChannelMessageWakeDoesNotSkipObviousNoise(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	withChannelAmbientGateTestConfig(t)
	agentID := createHandlerTestAgent(t, "Ambient Noise Gate "+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "ambient-noise-gate-"+uuid.NewString(), testUserID)
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
		t.Fatalf("noise ordinary message created %d sessions and %d inbox events, want one runnable wake", sessions, events)
	}
	assertChannelAgentInboxEventCounts(t, channelID, agentID, 0, 1)
	assertChannelAgentWakeReasonPriority(t, channelID, agentID, trigger.ID, channelMessageWakeReason, channelMessageWakePriority)
}

func TestChannelAmbientGateDoesNotBlockDirectMention(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	withChannelAmbientGateTestConfig(t)
	agentName := "ambient-direct-gate-" + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentName, nil)
	channelID := seedChannelForTest(t, "ambient-direct-gate-"+uuid.NewString(), testUserID)
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

	assertChannelAgentInboxEventCounts(t, channelID, agentID, 0, 2)
	assertChannelAgentWakeReason(t, channelID, agentID, direct.ID, "mention")
}

func TestChannelMessageWakeSkipsMutedAgentButMentionPierces(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentName := "muted-direct-agent-" + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentName, nil)
	channelID := seedChannelForTest(t, "muted-direct-agent-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, muted_at)
		VALUES ($1, $2, 'agent', $3, now())
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed muted agent member: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}

	ordinary, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "ordinary message while muted", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert ordinary trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, ordinary, parseUUID(testUserID))

	directContent := fmt.Sprintf("@%s please answer even while muted", agentName)
	direct, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", directContent, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert direct trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, direct, parseUUID(testUserID))

	assertChannelAgentInboxEventCounts(t, channelID, agentID, 0, 1)
	assertChannelAgentWakeReason(t, channelID, agentID, direct.ID, "mention")
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
		VALUES ($1, $2, 'agent', $3), ($1, $2, 'agent', $4)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID, otherAgentID); err != nil {
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

func TestChannelMentionedAgentsUsesHandlesOrStructuredIDs(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	handle := "wendy-" + suffix
	secondHandle := handle + "-2"
	displayName := "Wendy"
	agentID := createHandlerTestAgent(t, handle, nil)
	secondAgentID := createHandlerTestAgent(t, secondHandle, nil)
	for _, id := range []string{agentID, secondAgentID} {
		if _, err := testPool.Exec(ctx, `UPDATE agent SET display_name = $2 WHERE id = $1`, id, displayName); err != nil {
			t.Fatalf("set duplicate display_name: %v", err)
		}
	}

	channelID := seedChannelForTest(t, "identity-mentions-"+suffix, testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3), ($1, $2, 'agent', $4)
ON CONFLICT DO NOTHING`,
		channelID, testWorkspaceID, agentID, secondAgentID,
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
		{"bare display name is not routable", "please @Wendy jump in", nil, ""},
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
	agentID := createHandlerTestAgent(t, "Idempotency Group Bot", nil)
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
	agentID := createHandlerTestAgent(t, agentDisplayName, nil)
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
	assertChannelAgentWakeReasonPriority(t, channelID, agentID, mentioned.ID, "mention", channelDirectedWakePriority)
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

func TestChannelThreadReadModelExposesParticipantsAndPendingWake(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentHandle := "thread-contract-helper-" + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentHandle, nil)
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
	if timelineRoot == nil || len(timelineRoot.ThreadParticipants) == 0 || len(timelineRoot.ThreadWakeAnnotations) == 0 {
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
	agentHandle := "thread-state-helper-" + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentHandle, nil)
	channelID := seedChannelForTest(t, "thread-state-model-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}

	replyRoot := dispatchThreadMentionForTest(t, channelID, agentID, "thread-state-reply")
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "agent", parseUUID(agentID), agentHandle, "visible answer", "multica", nil, pgtype.UUID{}, parseUUID(replyRoot.ID), replyRoot.ThreadID, 1); err != nil {
		t.Fatalf("insert visible agent reply: %v", err)
	}
	replyPage, _ := listedThreadForUser(t, channelID, replyRoot.ID, testUserID)
	if got := wakeStateForAgent(t, replyPage.Messages[0], agentID); got.State != "replied" || got.Reason != nil {
		t.Fatalf("reply wake state = %+v, want replied", got)
	}

	rec := sendChannelThreadReplyForTest(t, channelID, replyRoot.ID, testUserID, map[string]any{"content": "@" + agentHandle + " react if done"})
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

func TestChannelThreadReadModelSurfacesInboxTerminalOutcomesAndRetry(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Thread Terminal Helper", nil)
	channelID := seedChannelForTest(t, "thread-terminal-model-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}

	noReplyRoot := dispatchThreadMentionForTest(t, channelID, agentID, "thread-terminal-no-reply-"+uuid.NewString())
	noReplyEventID := latestChannelAgentInboxEventForRootForTest(t, noReplyRoot.ID, agentID)
	noReplyDeliveryID := setAgentInboxTerminalOutcomeForTest(t, noReplyEventID, "no_reply", false)
	noReplyPage, noReplyRaw := listedThreadForUser(t, channelID, noReplyRoot.ID, testUserID)
	noReplyWake := wakeStateForAgent(t, noReplyPage.Messages[0], agentID)
	if noReplyWake.State != "no_reply" || noReplyWake.Reason != nil || noReplyWake.Outcome == nil || *noReplyWake.Outcome != "no_reply" {
		t.Fatalf("no-reply wake = %+v, want terminal no_reply without reason leak", noReplyWake)
	}
	if noReplyWake.Retryable == nil || *noReplyWake.Retryable || noReplyWake.InboxEventID == nil || *noReplyWake.InboxEventID != noReplyEventID || noReplyWake.DeliveryID == nil || *noReplyWake.DeliveryID != noReplyDeliveryID || noReplyWake.TerminalAt == nil {
		t.Fatalf("no-reply terminal metadata = %+v, want non-retryable ids + terminal_at", noReplyWake)
	}
	for _, forbidden := range []string{"reason_code", "failure_reason", "agent_inbox_event", "run_id"} {
		if strings.Contains(noReplyRaw, forbidden) {
			t.Fatalf("no-reply read-model leaked %q: %s", forbidden, noReplyRaw)
		}
	}

	failedRoot := dispatchThreadMentionForTest(t, channelID, agentID, "thread-terminal-failed-"+uuid.NewString())
	failedEventID := latestChannelAgentInboxEventForRootForTest(t, failedRoot.ID, agentID)
	failedDeliveryID := setAgentInboxTerminalOutcomeForTest(t, failedEventID, "failed", true)
	failedPage, failedRaw := listedThreadForUser(t, channelID, failedRoot.ID, testUserID)
	failedWake := wakeStateForAgent(t, failedPage.Messages[0], agentID)
	if failedWake.State != "failed" || failedWake.Outcome == nil || *failedWake.Outcome != "failed" || failedWake.Retryable == nil || !*failedWake.Retryable || failedWake.DeliveryID == nil || *failedWake.DeliveryID != failedDeliveryID {
		t.Fatalf("failed wake = %+v, want retryable terminal failure", failedWake)
	}
	if strings.Contains(failedRaw, "reason_code") || strings.Contains(failedRaw, "failure_reason") || strings.Contains(failedRaw, "run_id") {
		t.Fatalf("failed read-model leaked diagnostics: %s", failedRaw)
	}

	var switchedRuntimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, owner_id, last_seen_at
		)
		VALUES ($1, $2, 'Retry Switched Runtime', 'local', 'test-retry', 'online', 'test runtime', '{}'::jsonb, $3, now())
		RETURNING id
	`, testWorkspaceID, "retry-runtime-"+uuid.NewString(), testUserID).Scan(&switchedRuntimeID); err != nil {
		t.Fatalf("create switched runtime: %v", err)
	}
	var originalRuntimeID string
	if err := testPool.QueryRow(ctx, `SELECT runtime_id FROM agent WHERE id = $1`, agentID).Scan(&originalRuntimeID); err != nil {
		t.Fatalf("load original agent runtime: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `UPDATE agent SET runtime_id = $2 WHERE id = $1`, agentID, originalRuntimeID)
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, switchedRuntimeID)
	})
	if _, err := testPool.Exec(ctx, `UPDATE agent SET runtime_id = $2 WHERE id = $1`, agentID, switchedRuntimeID); err != nil {
		t.Fatalf("switch agent runtime before retry: %v", err)
	}

	retryReq := newRequestAs(testUserID, http.MethodPost, "/api/channels/"+channelID+"/agent-inbox/events/"+failedEventID+"/retry", nil)
	retryReq = withChannelTestWorkspaceCtx(t, retryReq, testUserID)
	retryReq = withURLParams(retryReq, "channelId", channelID, "eventId", failedEventID)
	retryRec := httptest.NewRecorder()
	testHandler.RetryChannelAgentInboxEvent(retryRec, retryReq)
	if retryRec.Code != http.StatusCreated {
		t.Fatalf("retry inbox event: status=%d body=%s", retryRec.Code, retryRec.Body.String())
	}
	var retryResp struct {
		InboxEventID string `json:"inbox_event_id"`
		Status       string `json:"status"`
	}
	if err := json.Unmarshal(retryRec.Body.Bytes(), &retryResp); err != nil {
		t.Fatalf("decode retry response: %v", err)
	}
	if retryResp.InboxEventID == "" || retryResp.InboxEventID == failedEventID || retryResp.Status != "pending" {
		t.Fatalf("retry response = %+v, want new pending inbox event", retryResp)
	}
	var retryRuntimeID string
	if err := testPool.QueryRow(ctx, `
		SELECT s.runtime_id
		FROM agent_inbox_event e
		JOIN agent_session s ON s.id = e.agent_session_id
		WHERE e.id = $1`, retryResp.InboxEventID).Scan(&retryRuntimeID); err != nil {
		t.Fatalf("load retry session runtime: %v", err)
	}
	if retryRuntimeID != switchedRuntimeID {
		t.Fatalf("retry session runtime = %s, want switched runtime %s", retryRuntimeID, switchedRuntimeID)
	}
	retryPage, _ := listedThreadForUser(t, channelID, failedRoot.ID, testUserID)
	retryWake := wakeStateForAgent(t, retryPage.Messages[0], agentID)
	if retryWake.State != "pending" || retryWake.Outcome != nil || retryWake.InboxEventID != nil {
		t.Fatalf("post-retry wake = %+v, want fresh pending new-chain event without terminal metadata", retryWake)
	}
}

func TestChannelActiveTasksSurfacesInboxTerminalOutcomes(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentName := "Active Terminal Helper " + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentName, nil)
	channelID := seedChannelForTest(t, "active-terminal-model-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}

	noReplyRoot := dispatchThreadMentionForTest(t, channelID, agentID, "active-terminal-no-reply-"+uuid.NewString())
	noReplyEventID := latestChannelAgentInboxEventForRootForTest(t, noReplyRoot.ID, agentID)

	req := withURLParam(newRequest(http.MethodGet, "/api/channels/"+channelID+"/active-tasks", nil), "channelId", channelID)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	rec := httptest.NewRecorder()
	testHandler.ListChannelActiveTasks(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list active inbox tasks: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp ChannelActiveTasksResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode active inbox tasks: %v", err)
	}
	if len(resp.Tasks) != 1 {
		t.Fatalf("active inbox tasks = %+v, want one queued inbox row", resp.Tasks)
	}
	got := resp.Tasks[0]
	if got.AgentID != agentID || got.AgentName == "" || got.TaskID != noReplyEventID || got.Status != "queued" {
		t.Fatalf("active inbox task identity = %+v, want agent/queued/inbox event id", got)
	}
	if got.Outcome != nil || got.InboxEventID == nil || *got.InboxEventID != noReplyEventID || got.SourceMessageID == nil || *got.SourceMessageID == noReplyRoot.ID {
		t.Fatalf("active inbox metadata = %+v, want inbox/source ids without terminal outcome", got)
	}

	if _, err := testPool.Exec(ctx, `
		UPDATE agent_inbox_event
		SET status = 'acked', acked_at = now(), updated_at = now()
		WHERE id = $1`, noReplyEventID); err != nil {
		t.Fatalf("ack inbox event without terminal outcome: %v", err)
	}
	rec = httptest.NewRecorder()
	testHandler.ListChannelActiveTasks(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list active tasks after legacy ack: status=%d body=%s", rec.Code, rec.Body.String())
	}
	resp = ChannelActiveTasksResponse{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode active tasks after legacy ack: %v", err)
	}
	if len(resp.Tasks) != 0 {
		t.Fatalf("active tasks after legacy ack = %+v, want no stale working row", resp.Tasks)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_inbox_event
		SET status = 'pending', acked_at = NULL, updated_at = now()
		WHERE id = $1`, noReplyEventID); err != nil {
		t.Fatalf("restore pending inbox event: %v", err)
	}

	noReplyDeliveryID := setAgentInboxTerminalOutcomeForTest(t, noReplyEventID, "no_reply", false)
	rec = httptest.NewRecorder()
	testHandler.ListChannelActiveTasks(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list active terminal tasks: status=%d body=%s", rec.Code, rec.Body.String())
	}
	resp = ChannelActiveTasksResponse{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode active terminal tasks: %v", err)
	}
	if len(resp.Tasks) != 1 {
		t.Fatalf("active terminal tasks = %+v, want one no_reply row", resp.Tasks)
	}
	got = resp.Tasks[0]
	if got.AgentID != agentID || got.AgentName == "" || got.TaskID != noReplyEventID || got.Status != "no_reply" {
		t.Fatalf("active terminal task identity = %+v, want agent/no_reply/inbox event id", got)
	}
	if got.Outcome == nil || *got.Outcome != "no_reply" || got.Retryable == nil || *got.Retryable || got.InboxEventID == nil || *got.InboxEventID != noReplyEventID || got.DeliveryID == nil || *got.DeliveryID != noReplyDeliveryID || got.TerminalAt == nil {
		t.Fatalf("active terminal metadata = %+v, want no_reply ids + non-retryable + terminal_at", got)
	}

	if _, err := testPool.Exec(ctx, `UPDATE agent_inbox_event SET terminal_at = now() - interval '3 minutes' WHERE id = $1`, noReplyEventID); err != nil {
		t.Fatalf("age no_reply terminal row: %v", err)
	}
	rec = httptest.NewRecorder()
	testHandler.ListChannelActiveTasks(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list active tasks after no_reply ttl: status=%d body=%s", rec.Code, rec.Body.String())
	}
	resp = ChannelActiveTasksResponse{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode active tasks after no_reply ttl: %v", err)
	}
	if len(resp.Tasks) != 0 {
		t.Fatalf("active tasks after no_reply ttl = %+v, want no strip rows", resp.Tasks)
	}

	failedRoot := dispatchThreadMentionForTest(t, channelID, agentID, "active-terminal-failed-"+uuid.NewString())
	failedEventID := latestChannelAgentInboxEventForRootForTest(t, failedRoot.ID, agentID)
	failedDeliveryID := setAgentInboxTerminalOutcomeForTest(t, failedEventID, "failed", true)
	if _, err := testPool.Exec(ctx, `UPDATE agent_inbox_event SET terminal_at = now() - interval '3 minutes' WHERE id = $1`, failedEventID); err != nil {
		t.Fatalf("age failed terminal row: %v", err)
	}
	rec = httptest.NewRecorder()
	testHandler.ListChannelActiveTasks(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list failed terminal tasks: status=%d body=%s", rec.Code, rec.Body.String())
	}
	resp = ChannelActiveTasksResponse{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode failed terminal tasks: %v", err)
	}
	if len(resp.Tasks) != 1 {
		t.Fatalf("failed terminal tasks = %+v, want one failed row", resp.Tasks)
	}
	got = resp.Tasks[0]
	if got.Status != "failed" || got.Outcome == nil || *got.Outcome != "failed" || got.Retryable == nil || !*got.Retryable || got.InboxEventID == nil || *got.InboxEventID != failedEventID || got.DeliveryID == nil || *got.DeliveryID != failedDeliveryID {
		t.Fatalf("failed terminal metadata = %+v, want retryable failed ids", got)
	}

	repliedRoot := dispatchThreadMentionForTest(t, channelID, agentID, "active-terminal-replied-"+uuid.NewString())
	repliedEventID := latestChannelAgentInboxEventForRootForTest(t, repliedRoot.ID, agentID)
	setAgentInboxTerminalOutcomeForTest(t, repliedEventID, "replied", false)

	rec = httptest.NewRecorder()
	testHandler.ListChannelActiveTasks(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list active tasks after replied: status=%d body=%s", rec.Code, rec.Body.String())
	}
	resp = ChannelActiveTasksResponse{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode active tasks after replied: %v", err)
	}
	if len(resp.Tasks) != 0 {
		t.Fatalf("active tasks after newer replied = %+v, want no strip rows", resp.Tasks)
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
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
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

func TestChannelThreadPlainReplyWakesAgentFollower(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	participantID := createHandlerTestAgent(t, "Thread Fullstack", nil)
	bystanderID := createHandlerTestAgent(t, "Thread Bystander", nil)
	channelID := seedChannelForTest(t, "thread-followup-agent-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3), ($1, $2, 'agent', $4)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, participantID, bystanderID); err != nil {
		t.Fatalf("seed agent members: %v", err)
	}
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "root", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("followup-agent-root"), 0)
	if err != nil {
		t.Fatalf("insert root: %v", err)
	}
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "agent", parseUUID(participantID), "Thread Fullstack", "agent answer", "multica", nil, pgtype.UUID{}, parseUUID(root.ID), strPtr("followup-agent-root"), 1); err != nil {
		t.Fatalf("insert agent reply: %v", err)
	}
	testHandler.followChannelThreadAgent(ctx, parseUUID(channelID), parseUUID(root.ID), parseUUID(participantID))
	followup, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "hi", "multica", nil, pgtype.UUID{}, parseUUID(root.ID), strPtr("followup-agent-root"), 0)
	if err != nil {
		t.Fatalf("insert follow-up: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}

	testHandler.dispatchChannelThreadReplyMentions(ctx, ch, followup, parseUUID(testUserID))

	assertChannelAgentInboxEventCounts(t, channelID, participantID, 0, 1)
	assertChannelAgentWakeReasonPriority(t, channelID, participantID, followup.ID, "thread_reply", channelThreadReplyPriority)
	assertChannelAgentWakeActivity(t, participantID, followup.ID, "thread_reply")
	assertChannelAgentInboxEventCounts(t, channelID, bystanderID, 1, 0)
}

func TestChannelThreadPlainReplyWakesAgentRootAuthor(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	rootAgentID := createHandlerTestAgent(t, "Thread Root Author "+uuid.NewString()[:8], nil)
	bystanderID := createHandlerTestAgent(t, "Thread Root Bystander "+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "thread-root-agent-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3), ($1, $2, 'agent', $4)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, rootAgentID, bystanderID); err != nil {
		t.Fatalf("seed agent members: %v", err)
	}
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "agent", parseUUID(rootAgentID), "Thread Root Author", "agent root", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("agent-root-thread"), 1)
	if err != nil {
		t.Fatalf("insert agent root: %v", err)
	}

	req := newRequest(http.MethodPost, "/api/channels/"+channelID+"/messages/"+root.ID+"/thread", map[string]string{"content": "following up without mention"})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParams(req, "channelId", channelID, "messageId", root.ID)
	rec := httptest.NewRecorder()
	testHandler.SendChannelMessageThreadReply(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("send thread reply: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var followup ChannelMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &followup); err != nil {
		t.Fatalf("decode thread reply: %v", err)
	}

	assertChannelAgentInboxEventCounts(t, channelID, rootAgentID, 0, 1)
	assertChannelAgentWakeReasonPriority(t, channelID, rootAgentID, followup.ID, "thread_reply", channelThreadReplyPriority)
	assertChannelAgentInboxEventCounts(t, channelID, bystanderID, 1, 0)
}

func TestChannelThreadPlainReplyAfterRootMentionWithoutFollowCreatesAmbientOnly(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	participantID := createHandlerTestAgent(t, "Thread Root Helper", nil)
	bystanderID := createHandlerTestAgent(t, "Thread Root Bystander", nil)
	channelID := seedChannelForTest(t, "thread-root-mention-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3), ($1, $2, 'agent', $4)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, participantID, bystanderID); err != nil {
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
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, participantID); err != nil {
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

	var pendingWake, mentionWake, absorbedAmbient int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE requires_wake AND status IN ('pending', 'draining', 'failed'))::int,
		       count(*) FILTER (WHERE reason = 'mention' AND requires_wake AND status IN ('pending', 'draining', 'failed'))::int,
		       count(*) FILTER (WHERE reason = 'channel_message' AND status = 'acked' AND terminal_outcome = 'no_reply')::int
		FROM agent_inbox_event
		WHERE channel_id = $1 AND agent_id = $2`, channelID, participantID).Scan(&pendingWake, &mentionWake, &absorbedAmbient); err != nil {
		t.Fatalf("count wake/absorb events: %v", err)
	}
	if pendingWake != 1 || mentionWake != 1 {
		t.Fatalf("pending/mention wakes = %d/%d, want 1/1", pendingWake, mentionWake)
	}
	if absorbedAmbient != 1 {
		t.Fatalf("absorbed ambient wakes = %d, want 1", absorbedAmbient)
	}
	assertChannelAgentWakeReason(t, channelID, participantID, followup.ID, "mention")
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
		"Message target for chat transport: #" + ch.Name + ":" + root.ID,
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
	wantTarget := "dm:@" + userHandleForTransportTest(t, testUserID) + ":" + root.ID
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

func assertChannelAgentWakeActivity(t *testing.T, agentID, sourceMessageID, wantReason string) {
	t.Helper()
	ctx := context.Background()
	var eventKind, eventType, reasonCode string
	if err := testPool.QueryRow(ctx, `
		SELECT event_kind, event_type, reason_code
		FROM agent_activity_event
		WHERE agent_id = $1
		  AND reason_code = $2
		  AND details->>'trigger_message_id' = $3
		ORDER BY created_at DESC
		LIMIT 1`, agentID, wantReason, sourceMessageID).Scan(&eventKind, &eventType, &reasonCode); err != nil {
		t.Fatalf("load wake activity event: %v", err)
	}
	if eventKind != activityKindTransport || eventType != "task_dispatched" || reasonCode != wantReason {
		t.Fatalf("wake activity = kind:%q type:%q reason:%q, want transport/task_dispatched/%q", eventKind, eventType, reasonCode, wantReason)
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
			workspace_id, name, description, runtime_mode, runtime_config, runtime_id, max_concurrent_tasks, owner_id, instructions, custom_env, custom_args, mcp_config
		, model) VALUES ($1, 'Offline Channel Agent', '', 'local', '{}'::jsonb, $2, 1, $3, '', '{}'::jsonb, '[]'::jsonb, '[]'::jsonb, 'composer-1.5')
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

ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
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
		SELECT count(*) FROM agent_inbox_event WHERE chat_session_id = $1
	`, chatSessionID).Scan(&queuedCount); err != nil {
		t.Fatalf("count queued tasks: %v", err)
	}
	if queuedCount != 1 {
		t.Fatalf("offline enqueue created %d tasks, want 1 (queued for reconnect)", queuedCount)
	}

	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_inbox_event (agent_id, runtime_id, chat_session_id, status, priority, initiator_user_id)
		VALUES ($1, $2, $3, 'pending', 2, $4)
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
	agentID := createHandlerTestAgent(t, agentHandle, nil)
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
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", triggerContent, "multica", nil, pgtype.UUID{}, parseUUID(root.ID), strPtr("ui-thread"), 0)
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
		t.Fatalf("agent thread participant after first mention count=%d, want 1", agentParticipants)
	}

	var rawContext []byte
	if err := testPool.QueryRow(ctx, `
		SELECT context
		FROM agent_inbox_event
		WHERE channel_id = $1 AND agent_id = $2 AND reason IN ('mention', 'thread_reply')
		ORDER BY created_at DESC
		LIMIT 1`, channelID, agentID).Scan(&rawContext); err != nil {
		t.Fatalf("load thread wake context: %v", err)
	}
	var wake channelWakeContext
	if err := json.Unmarshal(rawContext, &wake); err != nil {
		t.Fatalf("decode thread wake: %v", err)
	}
	if wake.ThreadRootMessageID != root.ID {
		t.Fatalf("wake thread root = %q, want %s", wake.ThreadRootMessageID, root.ID)
	}
	prompt := wake.Prompt
	for _, want := range []string{"Thread context (root message first, then bounded recent replies from this thread only):", "root", triggerContent} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("thread prompt missing %q:\n%s", want, prompt)
		}
	}
	sessionID := ensureLegacyChannelChatBridgeForTest(t, ch, agentID, trigger, prompt)

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

func dispatchThreadMentionForTest(t *testing.T, channelID, agentID, threadID string) ChannelMessageResponse {
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
