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
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestAgentTransportSendMessageIdempotentAndSuppressesFinalOutput(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)
	clientID := "transport-" + uuid.NewString()
	content := "hello via transport " + uuid.NewString()

	first := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            target,
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
	assertAgentMessageSentActivityText(t, firstBody.Message.ID, content)

	second := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            target,
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
	assertAgentMessageSentActivityCount(t, firstBody.Message.ID, 1)

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

func TestAgentTransportSendFreshnessHoldSavesDraftAndDoesNotWriteMessage(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)
	seen, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "seen before compose "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("seed seen message: %v", err)
	}
	newerText := "newer during compose " + uuid.NewString()
	newer, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", newerText, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("seed newer message: %v", err)
	}

	draftContent := "held draft " + uuid.NewString()
	clientID := "transport-held-" + uuid.NewString()
	heldRec := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            target,
		"content":           draftContent,
		"client_message_id": clientID,
		"seen_up_to_seq":    seen.Seq,
	})
	if heldRec.Code != http.StatusOK {
		t.Fatalf("freshness hold send: status=%d body=%s", heldRec.Code, heldRec.Body.String())
	}
	var held AgentTransportSendHeldResponse
	if err := json.Unmarshal(heldRec.Body.Bytes(), &held); err != nil {
		t.Fatalf("decode held response: %v", err)
	}
	if held.State != "held" || held.Outcome != "held" || held.Subtype != "freshness" || held.Reason != "newer_messages_available" {
		t.Fatalf("held envelope mismatch: %+v", held)
	}
	if held.SeenUpToSeq != seen.Seq || held.LatestSeq != newer.Seq || held.NewMessageCount != 1 || len(held.HeldMessages) != 1 || held.HeldMessages[0].ID != newer.ID {
		t.Fatalf("held context mismatch: seen=%d newer=%s body=%+v", seen.Seq, newer.ID, held)
	}
	assertNoChannelMessageContent(t, channelID, draftContent)
	assertAgentTransportDraftContent(t, agentID, target, draftContent)
	assertAgentTransportFreshnessHoldActivity(t, taskID, target, 1)
	assertAgentTransportVisibleOutputAuditCount(t, taskID, 0)

	revised := "revised held draft " + uuid.NewString()
	secondHeld := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            target,
		"content":           revised,
		"client_message_id": "transport-held-" + uuid.NewString(),
		"seen_up_to_seq":    seen.Seq,
	})
	if secondHeld.Code != http.StatusOK {
		t.Fatalf("second freshness hold send: status=%d body=%s", secondHeld.Code, secondHeld.Body.String())
	}
	var secondBody AgentTransportSendHeldResponse
	if err := json.Unmarshal(secondHeld.Body.Bytes(), &secondBody); err != nil {
		t.Fatalf("decode second held response: %v", err)
	}
	if len(secondBody.HeldMessages) != 0 || secondBody.OmittedMessageCount != 1 {
		t.Fatalf("second hold should omit previously reviewed blockers: %+v", secondBody)
	}
	assertAgentTransportDraftContent(t, agentID, target, revised)
}

func TestAgentTransportSendDraftSendsSavedDraftAndClearsDraft(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)
	seen, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "draft send seen "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("seed seen message: %v", err)
	}
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "draft send newer "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0); err != nil {
		t.Fatalf("seed newer message: %v", err)
	}
	content := "saved draft content " + uuid.NewString()
	clientID := "transport-draft-" + uuid.NewString()
	held := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            target,
		"content":           content,
		"client_message_id": clientID,
		"seen_up_to_seq":    seen.Seq,
	})
	if held.Code != http.StatusOK {
		t.Fatalf("freshness hold send: status=%d body=%s", held.Code, held.Body.String())
	}

	sent := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":     target,
		"send_draft": true,
	})
	if sent.Code != http.StatusCreated {
		t.Fatalf("send saved draft: status=%d body=%s", sent.Code, sent.Body.String())
	}
	var body AgentTransportSendResponse
	if err := json.Unmarshal(sent.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode draft send: %v", err)
	}
	if body.Message.Content != content || body.Message.ClientMessageID == nil || *body.Message.ClientMessageID != clientID {
		t.Fatalf("draft send message mismatch: %+v", body.Message)
	}
	assertAgentTransportDraftMissing(t, agentID, target)
	assertAgentTransportVisibleOutputAuditCount(t, taskID, 1)
}

func TestAgentTransportSendDraftRebuildsMentionForCurrentDestinationMembers(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, _ := createChannelCompletionTask(t, "group")
	senderID := agentIDForTask(t, taskID)
	targetChannelID := seedChannelForTest(t, "transport-draft-destination-"+uuid.NewString(), testUserID)
	sharedDisplayName := "Draft Destination " + uuid.NewString()
	oldTargetID := createHandlerTestAgent(t, "draft-old-"+uuid.NewString(), nil)
	newTargetID := createHandlerTestAgent(t, "draft-new-"+uuid.NewString(), nil)
	if _, err := testPool.Exec(ctx, `
		UPDATE agent
		SET display_name = $2
		WHERE id = ANY($1::uuid[])
	`, []string{oldTargetID, newTargetID}, sharedDisplayName); err != nil {
		t.Fatalf("set duplicate display names: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES
			($1, $2, 'agent', $3),
			($1, $2, 'agent', $4)
	`, targetChannelID, testWorkspaceID, senderID, oldTargetID); err != nil {
		t.Fatalf("seed destination members: %v", err)
	}
	seen, err := testHandler.insertChannelMessage(ctx, parseUUID(targetChannelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "seen before held destination draft", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("seed seen destination message: %v", err)
	}
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(targetChannelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "newer destination message", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0); err != nil {
		t.Fatalf("seed newer destination message: %v", err)
	}
	target := "#" + channelNameForTransportTest(t, targetChannelID)
	content := "please @" + sharedDisplayName + " review the held draft"
	clientID := "transport-draft-members-" + uuid.NewString()
	held := agentTransportSendForTest(t, taskID, senderID, map[string]any{
		"target":            target,
		"content":           content,
		"client_message_id": clientID,
		"seen_up_to_seq":    seen.Seq,
	})
	if held.Code != http.StatusOK {
		t.Fatalf("hold destination draft: status=%d body=%s", held.Code, held.Body.String())
	}
	if _, err := testPool.Exec(ctx, `
		DELETE FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'agent' AND member_id = $3
	`, targetChannelID, testWorkspaceID, oldTargetID); err != nil {
		t.Fatalf("remove original destination member: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
	`, targetChannelID, testWorkspaceID, newTargetID); err != nil {
		t.Fatalf("add current destination member: %v", err)
	}

	sent := agentTransportSendForTest(t, taskID, senderID, map[string]any{
		"target":     target,
		"send_draft": true,
	})
	if sent.Code != http.StatusCreated {
		t.Fatalf("send held destination draft: status=%d body=%s", sent.Code, sent.Body.String())
	}
	var body AgentTransportSendResponse
	if err := json.Unmarshal(sent.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode held draft response: %v", err)
	}
	start := strings.Index(content, "@")
	startUTF16, endUTF16 := contentUTF16Span(content, start, start+len("@"+sharedDisplayName))
	assertSingleMentionReferenceForTest(t, body.Message.Parts, newTargetID, startUTF16, endUTF16)
	for _, part := range body.Message.Parts {
		if part.Type == protocol.MessagePartTypeReference && part.RefType == "mention" && part.RefID == oldTargetID {
			t.Fatalf("draft retained removed destination member: %+v", part)
		}
	}
}

func TestAgentTransportSendMessageStickerOnlyAndWithText(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)

	stickerOnlyID := "transport-sticker-" + uuid.NewString()
	stickerOnly := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target": target,
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
	if stickerOnlyBody.Message.Parts[0].Alt == "" {
		t.Fatalf("sticker-only alt is empty: %+v", stickerOnlyBody.Message.Parts[0])
	}
	assertAgentMessageSentActivityText(t, stickerOnlyBody.Message.ID, stickerOnlyBody.Message.Parts[0].Alt)

	explanation := "这个问题是因为 transport sticker test " + uuid.NewString()
	combinedID := "transport-combined-" + uuid.NewString()
	combined := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":  target,
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
	target := "#" + channelNameForTransportTest(t, channelID)

	ownedAttachmentID := seedUnboundAgentAttachmentForTest(t, agentID, "agent-file.png")
	otherAgentID := uuid.NewString()
	foreignAttachmentID := seedUnboundAgentAttachmentForTest(t, otherAgentID, "not-mine.png")

	clientID := "transport-attachment-" + uuid.NewString()
	resp := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":  target,
		"content": "here's the file",
		"parts": []protocol.MessagePart{
			{Type: protocol.MessagePartTypeText, Text: "here's the file"},
			{Type: protocol.MessagePartTypeAttachment, AttachmentID: ownedAttachmentID},
			{Type: protocol.MessagePartTypeAttachment, AttachmentID: foreignAttachmentID},
		},
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
	if len(body.Message.Parts) < 2 {
		t.Fatalf("message parts = %+v, want text + attachment parts", body.Message.Parts)
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

func TestAgentTransportSendMessageAttachmentOnlyActivityText(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)
	attachmentID := seedUnboundAgentAttachmentForTest(t, agentID, "activity-report.pdf")

	resp := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target": target,
		"parts": []protocol.MessagePart{{
			Type:         protocol.MessagePartTypeAttachment,
			AttachmentID: attachmentID,
		}},
		"client_message_id": "transport-attachment-only-" + uuid.NewString(),
	})
	if resp.Code != http.StatusCreated {
		t.Fatalf("attachment-only transport send: status=%d body=%s", resp.Code, resp.Body.String())
	}
	var body AgentTransportSendResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode attachment-only send: %v", err)
	}
	if body.Message.Content != "" || len(body.Message.Attachments) != 1 || body.Message.Attachments[0].Filename != "activity-report.pdf" {
		t.Fatalf("attachment-only message = %+v, want one linked activity-report.pdf attachment and empty content", body.Message)
	}
	if len(body.Message.Parts) != 1 || body.Message.Parts[0].Type != protocol.MessagePartTypeAttachment || body.Message.Parts[0].AttachmentID != attachmentID {
		t.Fatalf("attachment-only parts = %+v, want one attachment part for %s", body.Message.Parts, attachmentID)
	}
	assertAgentMessageSentActivityText(t, body.Message.ID, "Sent attachment: activity-report.pdf")
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

func TestAgentTransportSendThreadReplyFollowsAgentForPlainFollowup(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	threadID := "transport-follow-" + uuid.NewString()
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "transport follow root", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, &threadID, 0)
	if err != nil {
		t.Fatalf("insert root: %v", err)
	}

	target := "#" + channelNameForTransportTest(t, channelID) + ":" + root.ID
	resp := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            target,
		"content":           "agent reply that should follow thread",
		"client_message_id": "transport-follow-" + uuid.NewString(),
	})
	if resp.Code != http.StatusCreated {
		t.Fatalf("transport thread send: status=%d body=%s", resp.Code, resp.Body.String())
	}

	var followed bool
	var wakeState string
	if err := testPool.QueryRow(ctx, `
		SELECT followed_at IS NOT NULL, wake_state
		FROM thread_participant
		WHERE root_message_id = $1
		  AND member_type = 'agent'
		  AND member_id = $2`, root.ID, agentID).Scan(&followed, &wakeState); err != nil {
		t.Fatalf("load transport thread participant: %v", err)
	}
	if !followed || wakeState != "active" {
		t.Fatalf("transport thread participant = followed:%v wake_state:%q, want true/active", followed, wakeState)
	}

	req := newRequest(http.MethodPost, "/api/channels/"+channelID+"/messages/"+root.ID+"/thread", map[string]string{"content": "plain human follow-up"})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParams(req, "channelId", channelID, "messageId", root.ID)
	rec := httptest.NewRecorder()
	testHandler.SendChannelMessageThreadReply(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("send human thread reply: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var followup ChannelMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &followup); err != nil {
		t.Fatalf("decode human thread reply: %v", err)
	}

	assertChannelAgentInboxEventCounts(t, channelID, agentID, 0, 1)
	assertChannelAgentWakeReason(t, channelID, agentID, followup.ID, "thread_reply")
}

func TestAgentTransportReadSearchAndReactAudit(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)
	needle := "needle transport search " + uuid.NewString()
	seeded, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", needle, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("seed channel message: %v", err)
	}
	systemNotice := "system transport notice " + uuid.NewString()
	systemMsg, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "system", pgtype.UUID{}, "system", systemNotice, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("seed system channel message: %v", err)
	}

	readRec := agentTransportReadForTest(t, taskID, agentID, map[string]any{"target": target, "limit": 5})
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
	if !transportMessagesContainType(readBody.Messages, systemMsg.ID, systemNotice, "system") {
		t.Fatalf("read messages did not include system message %s: %+v", systemMsg.ID, readBody.Messages)
	}

	searchRec := agentTransportSearchForTest(t, taskID, agentID, map[string]any{
		"target": target,
		"query":  "needle transport search",
		"limit":  10,
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
		"target":            target,
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

func TestAgentTransportRequiresExplicitTarget(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	visible := "missing explicit target should not send " + uuid.NewString()

	sendRec := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"content":           visible,
		"client_message_id": "missing-target-" + uuid.NewString(),
	})
	if sendRec.Code != http.StatusBadRequest {
		t.Fatalf("send without target: status=%d body=%s", sendRec.Code, sendRec.Body.String())
	}
	assertNoChannelMessageContent(t, channelID, visible)

	readRec := agentTransportReadForTest(t, taskID, agentID, map[string]any{"limit": 5})
	if readRec.Code != http.StatusBadRequest {
		t.Fatalf("read without target: status=%d body=%s", readRec.Code, readRec.Body.String())
	}

	searchRec := agentTransportSearchForTest(t, taskID, agentID, map[string]any{
		"query": "needle",
		"limit": 5,
	})
	if searchRec.Code != http.StatusBadRequest {
		t.Fatalf("search without target: status=%d body=%s", searchRec.Code, searchRec.Body.String())
	}

	reactRec := agentTransportReactForTest(t, taskID, agentID, map[string]any{
		"message_id":        uuid.NewString(),
		"emoji":             "+1",
		"client_message_id": "missing-target-react-" + uuid.NewString(),
	})
	if reactRec.Code != http.StatusBadRequest {
		t.Fatalf("react without target: status=%d body=%s", reactRec.Code, reactRec.Body.String())
	}
}

func TestAgentTransportDMThreadTarget(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, _ := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	humanHandle := userHandleForTransportTest(t, testUserID)
	dmChannel, ok := testHandler.ensureAgentHumanDMChannel(ctx, parseUUID(testWorkspaceID), parseUUID(agentID), parseUUID(testUserID))
	if !ok {
		t.Fatal("create agent-human DM channel")
	}
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(dmChannel.ID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), humanHandle, "dm thread root "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert dm root: %v", err)
	}

	clientID := "dm-thread-" + uuid.NewString()
	rec := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            "dm:@" + humanHandle + ":" + root.ID,
		"content":           "dm thread reply " + uuid.NewString(),
		"client_message_id": clientID,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("send to dm thread target: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body AgentTransportSendResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode dm thread response: %v", err)
	}
	if body.Message.ChannelID != dmChannel.ID {
		t.Fatalf("message channel_id=%s, want dm channel %s", body.Message.ChannelID, dmChannel.ID)
	}
	if body.Message.ThreadRootMessageID == nil || *body.Message.ThreadRootMessageID != root.ID {
		t.Fatalf("message thread_root_message_id=%v, want %s", body.Message.ThreadRootMessageID, root.ID)
	}
}

func TestAgentTransportUnfollowDMThreadTarget(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, _ := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	humanHandle := userHandleForTransportTest(t, testUserID)
	dmChannel, ok := testHandler.ensureAgentHumanDMChannel(ctx, parseUUID(testWorkspaceID), parseUUID(agentID), parseUUID(testUserID))
	if !ok {
		t.Fatal("create agent-human DM channel")
	}
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(dmChannel.ID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), humanHandle, "dm unfollow root "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert dm root: %v", err)
	}
	testHandler.followChannelThreadAgent(ctx, parseUUID(dmChannel.ID), parseUUID(root.ID), parseUUID(agentID))

	rec := agentTransportUnfollowThreadForTest(t, taskID, agentID, map[string]any{
		"target": "dm:@" + humanHandle + ":" + root.ID,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("unfollow dm thread target: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body AgentTransportThreadUnfollowResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode unfollow response: %v", err)
	}
	if body.Action != agentTransportActionThreadUnfollow || body.ChannelID != dmChannel.ID || body.MessageID != root.ID {
		t.Fatalf("unfollow response = %+v, want dm channel %s root %s", body, dmChannel.ID, root.ID)
	}

	var followedAt pgtype.Timestamptz
	var wakeState string
	if err := testPool.QueryRow(ctx, `
		SELECT followed_at, wake_state
		FROM thread_participant
		WHERE root_message_id = $1 AND member_type = 'agent' AND member_id = $2`,
		root.ID, agentID).Scan(&followedAt, &wakeState); err != nil {
		t.Fatalf("load agent thread participant: %v", err)
	}
	if followedAt.Valid {
		t.Fatalf("agent followed_at still set after unfollow: %+v", followedAt)
	}
	if wakeState != "no_wake" {
		t.Fatalf("agent wake_state=%q, want no_wake", wakeState)
	}

	var eventRows int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1
		  AND thread_root_message_id = $2
		  AND author_type = 'system'
		  AND content LIKE '%unfollowed this thread%'`,
		dmChannel.ID, root.ID).Scan(&eventRows); err != nil {
		t.Fatalf("count thread unfollow system event: %v", err)
	}
	if eventRows != 1 {
		t.Fatalf("thread unfollow system event rows = %d, want 1", eventRows)
	}
	assertAgentTransportAuditCount(t, taskID, agentTransportActionThreadUnfollow, 1)
}

func TestAgentTransportRejectsNonRaftTargetForms(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	for _, target := range []string{
		uuid.NewString(),
		"#workspace:channel:" + uuid.NewString(),
		"dm:@" + userHandleForTransportTest(t, testUserID) + ":thread:" + uuid.NewString(),
	} {
		t.Run(target, func(t *testing.T) {
			content := "rejected non-raft target " + uuid.NewString()
			rec := agentTransportSendForTest(t, taskID, agentID, map[string]any{
				"target":            target,
				"content":           content,
				"client_message_id": "bad-target-" + uuid.NewString(),
			})
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("target %q: status=%d body=%s", target, rec.Code, rec.Body.String())
			}
			assertNoChannelMessageContent(t, channelID, content)
		})
	}
}

func TestAgentTransportReadThreadIncludesSystemReplies(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	threadID := "thread-system-" + uuid.NewString()
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "thread root for system read", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr(threadID), 0)
	if err != nil {
		t.Fatalf("seed thread root: %v", err)
	}
	systemNotice := "thread system notice " + uuid.NewString()
	systemReply, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "system", pgtype.UUID{}, "system", systemNotice, "multica", nil, parseUUID(root.ID), parseUUID(root.ID), strPtr(threadID), 0)
	if err != nil {
		t.Fatalf("seed thread system reply: %v", err)
	}
	var channelName string
	if err := testPool.QueryRow(ctx, `SELECT name FROM channel WHERE id = $1`, channelID).Scan(&channelName); err != nil {
		t.Fatalf("load channel name: %v", err)
	}

	readRec := agentTransportReadForTest(t, taskID, agentID, map[string]any{
		"target": "#" + channelName + ":" + root.ID,
		"limit":  5,
	})
	if readRec.Code != http.StatusOK {
		t.Fatalf("transport thread read: status=%d body=%s", readRec.Code, readRec.Body.String())
	}
	var readBody AgentTransportReadResponse
	if err := json.Unmarshal(readRec.Body.Bytes(), &readBody); err != nil {
		t.Fatalf("decode transport thread read: %v", err)
	}
	if !transportMessagesContainType(readBody.Messages, systemReply.ID, systemNotice, "system") {
		t.Fatalf("thread read messages did not include system reply %s: %+v", systemReply.ID, readBody.Messages)
	}
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

// TestAgentTransportAutoRetryStripsArealProxyFromChildContext verifies D9: a
// retry child does NOT inherit the parent's areal_proxy RL-session config.
// CreateRetryTask copies the parent's context verbatim, so the retry path strips
// areal_proxy (keeping other keys + the chat session_id/work_dir resume pointers)
// so the child opens a fresh areal session at its own session-open chokepoint.
func TestAgentTransportAutoRetryStripsArealProxyFromChildContext(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	taskID, _ := createChannelCompletionTask(t, "group")
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_task_queue
		SET status = 'failed', failure_reason = 'runtime_offline', attempt = 1, max_attempts = 3,
		    context = '{"areal_proxy":{"session_id":"sess-parent","api_key":"key-parent"},"squad_id":"squad-9"}'::jsonb
		WHERE id = $1`, taskID); err != nil {
		t.Fatalf("seed failed parent with areal_proxy context: %v", err)
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
	// Re-fetch: MaybeRetryFailedTask returns the pre-strip in-memory row; the
	// strip is a DB UPDATE inside createRetryTaskWithPendingWakeTransfer's tx.
	reloaded, err := testHandler.Queries.GetAgentTask(ctx, child.ID)
	if err != nil {
		t.Fatalf("load retry child: %v", err)
	}
	var ctxMap map[string]json.RawMessage
	if err := json.Unmarshal(reloaded.Context, &ctxMap); err != nil {
		t.Fatalf("unmarshal child context: %v", err)
	}
	if _, ok := ctxMap["areal_proxy"]; ok {
		t.Errorf("retry child inherited areal_proxy (D9 violation); context=%s", string(reloaded.Context))
	}
	if _, ok := ctxMap["squad_id"]; !ok {
		t.Errorf("retry child lost squad_id; want it preserved, context=%s", string(reloaded.Context))
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

func agentTransportUnfollowThreadForTest(t *testing.T, taskID, agentID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	req := agentTransportRequest(t, http.MethodPost, "/api/agent/threads/unfollow", taskID, agentID, body)
	rec := httptest.NewRecorder()
	testHandler.AgentTransportUnfollowThread(rec, req)
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

func channelNameForTransportTest(t *testing.T, channelID string) string {
	t.Helper()
	var name string
	if err := testPool.QueryRow(context.Background(), `SELECT name FROM channel WHERE id = $1`, channelID).Scan(&name); err != nil {
		t.Fatalf("load channel name: %v", err)
	}
	return name
}

func userHandleForTransportTest(t *testing.T, userID string) string {
	t.Helper()
	var name string
	if err := testPool.QueryRow(context.Background(), `SELECT name FROM "user" WHERE id = $1`, userID).Scan(&name); err != nil {
		t.Fatalf("load user name: %v", err)
	}
	return name
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

func assertAgentTransportVisibleOutputAuditCount(t *testing.T, taskID string, want int) {
	t.Helper()
	var got int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM agent_task_transport_audit
		WHERE task_id = $1 AND action IN ('message_send', 'message_react') AND channel_message_id IS NOT NULL`, taskID).Scan(&got); err != nil {
		t.Fatalf("count visible transport audit: %v", err)
	}
	if got != want {
		t.Fatalf("visible transport audit count=%d, want %d", got, want)
	}
}

func assertAgentTransportDraftContent(t *testing.T, agentID, target, want string) {
	t.Helper()
	var got string
	if err := testPool.QueryRow(context.Background(), `
		SELECT content
		FROM agent_transport_draft
		WHERE workspace_id = $1 AND agent_id = $2 AND target = $3`,
		testWorkspaceID, agentID, target).Scan(&got); err != nil {
		t.Fatalf("load transport draft: %v", err)
	}
	if got != want {
		t.Fatalf("draft content = %q, want %q", got, want)
	}
}

func assertAgentTransportDraftMissing(t *testing.T, agentID, target string) {
	t.Helper()
	var exists bool
	if err := testPool.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1
			FROM agent_transport_draft
			WHERE workspace_id = $1 AND agent_id = $2 AND target = $3
		)`, testWorkspaceID, agentID, target).Scan(&exists); err != nil {
		t.Fatalf("check transport draft: %v", err)
	}
	if exists {
		t.Fatalf("transport draft still exists for agent=%s target=%s", agentID, target)
	}
}

func assertAgentTransportFreshnessHoldActivity(t *testing.T, taskID, target string, newMessages int) {
	t.Helper()
	var statusCount, detailCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM agent_activity_event
		WHERE task_id = $1
		  AND event_type = 'send_freshness_hold'
		  AND message = 'Send held by freshness check'
		  AND target_slug = $2
		  AND details->>'new_message_count' = $3`, taskID, target, fmt.Sprint(newMessages)).Scan(&statusCount); err != nil {
		t.Fatalf("count freshness hold status activity: %v", err)
	}
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM agent_activity_event
		WHERE task_id = $1
		  AND event_type = 'send_freshness_hold_detail'
		  AND target_slug = $2
		  AND message LIKE 'target: % / new messages: % newer / decision: local hold; review the newer context before retrying'`,
		taskID, target).Scan(&detailCount); err != nil {
		t.Fatalf("count freshness hold detail activity: %v", err)
	}
	if statusCount != 1 || detailCount != 1 {
		t.Fatalf("freshness hold activity status=%d detail=%d, want 1/1", statusCount, detailCount)
	}
}

func assertAgentMessageSentActivityText(t *testing.T, messageID, want string) {
	t.Helper()
	var got string
	if err := testPool.QueryRow(context.Background(), `
		SELECT message
		FROM agent_activity_event
		WHERE event_type = 'message_sent'
		  AND details->>'message_id' = $1
		ORDER BY created_at DESC
		LIMIT 1`, messageID).Scan(&got); err != nil {
		t.Fatalf("load message_sent activity for message %s: %v", messageID, err)
	}
	if got != want {
		t.Fatalf("message_sent activity text = %q, want %q", got, want)
	}
	if strings.Contains(got, "Agent sent a visible message") {
		t.Fatalf("message_sent activity leaked legacy machine phrase: %q", got)
	}
}

func assertAgentMessageSentActivityCount(t *testing.T, messageID string, want int) {
	t.Helper()
	var got int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM agent_activity_event
		WHERE event_type = 'message_sent'
		  AND details->>'message_id' = $1`, messageID).Scan(&got); err != nil {
		t.Fatalf("count message_sent activity for message %s: %v", messageID, err)
	}
	if got != want {
		t.Fatalf("message_sent activity count for message %s = %d, want %d", messageID, got, want)
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

func transportMessagesContainType(messages []ChannelMessageResponse, id, content, typ string) bool {
	for _, msg := range messages {
		if msg.ID == id && msg.Content == content && msg.Type == typ {
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
