package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func lookupConversationHandleForUser(t *testing.T, userID, handle string) (*httptest.ResponseRecorder, conversationHandleLookupResponse) {
	t.Helper()
	path := "/api/conversations/lookup?handle=" + url.QueryEscape(handle)
	req := withChannelTestWorkspaceCtx(t, newRequestAs(userID, http.MethodGet, path, nil), userID)
	rec := httptest.NewRecorder()
	testHandler.LookupConversationHandle(rec, req)
	var response conversationHandleLookupResponse
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &response)
	}
	return rec, response
}

func seedChannelMessageWithID(t *testing.T, channelID, messageID, authorID, content string, threadRoot string) {
	t.Helper()
	if threadRoot == "" {
		if _, err := testPool.Exec(context.Background(), `
			INSERT INTO channel_message (id, channel_id, workspace_id, author_type, author_id, author_name, content, source, trigger_depth)
			VALUES ($1, $2, $3, 'user', $4, 'Author', $5, 'multica', 0)`,
			messageID, channelID, testWorkspaceID, authorID, content); err != nil {
			t.Fatalf("seed channel message: %v", err)
		}
		return
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO channel_message (id, channel_id, workspace_id, author_type, author_id, author_name, content, source, thread_root_message_id, trigger_depth)
		VALUES ($1, $2, $3, 'user', $4, 'Author', $5, 'multica', $6, 1)`,
		messageID, channelID, testWorkspaceID, authorID, content, threadRoot); err != nil {
		t.Fatalf("seed thread reply: %v", err)
	}
}

func TestLookupConversationHandleResolvesChannelAndThreadShortID(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	name := "raft-research-" + uuid.NewString()[:8]
	channelID := seedChannelForTest(t, name, testUserID)
	rootID := "a291584b-1111-4000-8000-000000000099"
	replyID := "b391584b-2222-4000-8000-000000000088"
	seedChannelMessageWithID(t, channelID, rootID, testUserID, "root", "")
	seedChannelMessageWithID(t, channelID, replyID, testUserID, "reply", rootID)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM channel_message WHERE id IN ($1, $2)`, rootID, replyID)
	})

	rec, body := lookupConversationHandleForUser(t, testUserID, "#"+name+":a291584b")
	if rec.Code != http.StatusOK || !body.Available || body.Href == nil {
		t.Fatalf("lookup status=%d body=%s", rec.Code, rec.Body.String())
	}
	want := "/" + handlerTestWorkspaceSlug + "/channels/" + channelID + "?message=" + rootID
	if *body.Href != want {
		t.Fatalf("href=%q want %q", *body.Href, want)
	}

	rec, body = lookupConversationHandleForUser(t, testUserID, "#"+name+":b391584b")
	if rec.Code != http.StatusOK || !body.Available || body.Href == nil {
		t.Fatalf("reply lookup status=%d body=%s", rec.Code, rec.Body.String())
	}
	want = "/" + handlerTestWorkspaceSlug + "/channels/" + channelID + "?thread=" + rootID + "&message=" + replyID
	if *body.Href != want {
		t.Fatalf("reply href=%q want %q", *body.Href, want)
	}
}

func TestLookupConversationHandleHidesUnknownAndForeignHandles(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	name := "private-research-" + uuid.NewString()[:8]
	channelID := seedChannelForTest(t, name, testUserID)
	messageID := "c491584b-3333-4000-8000-000000000077"
	seedChannelMessageWithID(t, channelID, messageID, testUserID, "secret", "")
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM channel_message WHERE id = $1`, messageID)
	})

	rec, body := lookupConversationHandleForUser(t, testUserID, "#"+name+":deadbeef")
	if rec.Code != http.StatusOK || body.Available || body.Href != nil {
		t.Fatalf("unknown prefix leaked: status=%d body=%s", rec.Code, rec.Body.String())
	}

	outsider := seedWorkspaceUserForTransportTargetTest(t, "handle-outsider-"+uuid.NewString()[:8])
	rec, body = lookupConversationHandleForUser(t, outsider, "#"+name+":c491584b")
	if rec.Code != http.StatusOK || body.Available || body.Href != nil {
		t.Fatalf("non-member leaked href: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestChannelBareConversationHandleBecomesStructuredMessagePart(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	targetName := "raft-research-" + suffix
	targetChannelID := seedChannelForTest(t, targetName, testUserID)
	sourceChannelID := seedChannelForTest(t, "handle-source-"+suffix, testUserID)
	rootID := "d591584b-4444-4000-8000-000000000066"
	seedChannelMessageWithID(t, targetChannelID, rootID, testUserID, "root", "")
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM channel_message WHERE id = $1`, rootID)
	})
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(sourceChannelID))
	if !found {
		t.Fatal("source channel not found after seed")
	}

	token := "#" + targetName + ":d591584b"
	content := "see " + token + " then #" + targetName
	gotContent, gotParts, err := testHandler.enrichChannelMessageMentions(ctx, ch, content, nil)
	if err != nil {
		t.Fatalf("enrich conversation handle: %v", err)
	}
	if gotContent != content {
		t.Fatalf("content = %q, want unchanged", gotContent)
	}

	var refs []protocol.MessagePart
	for _, part := range gotParts {
		if part.Type == protocol.MessagePartTypeReference && part.RefType == "channel-ref" {
			refs = append(refs, part)
		}
	}
	if len(refs) != 2 {
		t.Fatalf("channel references = %+v, want thread handle + bare #name", refs)
	}

	threadStart, threadEnd := contentUTF16Span(content, strings.Index(content, token), strings.Index(content, token)+len(token))
	if refs[0].RefID != targetChannelID || refs[0].Label != targetName {
		t.Fatalf("thread ref = %+v", refs[0])
	}
	if refs[0].ContentStartUTF16 == nil || refs[0].ContentEndUTF16 == nil ||
		*refs[0].ContentStartUTF16 != threadStart || *refs[0].ContentEndUTF16 != threadEnd {
		t.Fatalf("thread span = [%v,%v) want [%d,%d)", refs[0].ContentStartUTF16, refs[0].ContentEndUTF16, threadStart, threadEnd)
	}
	var params map[string]string
	if err := json.Unmarshal(refs[0].Params, &params); err != nil {
		t.Fatalf("thread params: %v", err)
	}
	if params["message_id"] != rootID || params["thread_id"] != "" {
		t.Fatalf("thread params = %+v, want message_id=%s and no thread_id", params, rootID)
	}

	bare := "#" + targetName
	bareAt := strings.LastIndex(content, bare)
	bareStart, bareEnd := contentUTF16Span(content, bareAt, bareAt+len(bare))
	if refs[1].RefID != targetChannelID || refs[1].Params != nil {
		t.Fatalf("bare ref = %+v", refs[1])
	}
	if refs[1].ContentStartUTF16 == nil || refs[1].ContentEndUTF16 == nil ||
		*refs[1].ContentStartUTF16 != bareStart || *refs[1].ContentEndUTF16 != bareEnd {
		t.Fatalf("bare span = [%v,%v) want [%d,%d)", refs[1].ContentStartUTF16, refs[1].ContentEndUTF16, bareStart, bareEnd)
	}

	unknown := "#" + targetName + ":deadbeef"
	_, unknownParts, err := testHandler.enrichChannelMessageMentions(ctx, ch, "see "+unknown, nil)
	if err != nil {
		t.Fatalf("enrich unknown prefix: %v", err)
	}
	var unknownRefs []protocol.MessagePart
	for _, part := range unknownParts {
		if part.Type == protocol.MessagePartTypeReference && part.RefType == "channel-ref" {
			unknownRefs = append(unknownRefs, part)
		}
	}
	if len(unknownRefs) != 1 || unknownRefs[0].Params != nil {
		t.Fatalf("unknown prefix refs = %+v, want a channel-only #name chip", unknownRefs)
	}
	nameStart, nameEnd := contentUTF16Span("see "+unknown, 4, 4+len("#"+targetName))
	if unknownRefs[0].ContentStartUTF16 == nil || unknownRefs[0].ContentEndUTF16 == nil ||
		*unknownRefs[0].ContentStartUTF16 != nameStart || *unknownRefs[0].ContentEndUTF16 != nameEnd {
		t.Fatalf("unknown prefix span = [%v,%v) want [%d,%d)", unknownRefs[0].ContentStartUTF16, unknownRefs[0].ContentEndUTF16, nameStart, nameEnd)
	}
}
