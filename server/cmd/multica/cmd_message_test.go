package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestBuildAgentSendPartsIncludesAttachmentParts(t *testing.T) {
	parts := buildAgentSendParts("got-it", "see files", []string{
		"att-1",
		"  att-2  ",
		"",
		"att-1", // duplicates preserved in flag order after appendUniqueStrings; builder itself does not dedupe
	})
	want := []protocol.MessagePart{
		{Type: protocol.MessagePartTypeSticker, StickerID: "got-it"},
		{Type: protocol.MessagePartTypeText, Text: "see files"},
		{Type: protocol.MessagePartTypeAttachment, AttachmentID: "att-1"},
		{Type: protocol.MessagePartTypeAttachment, AttachmentID: "att-2"},
		{Type: protocol.MessagePartTypeAttachment, AttachmentID: "att-1"},
	}
	if len(parts) != len(want) {
		t.Fatalf("parts len = %d, want %d (%+v)", len(parts), len(want), parts)
	}
	for i := range want {
		if parts[i].Type != want[i].Type || parts[i].StickerID != want[i].StickerID ||
			parts[i].Text != want[i].Text || parts[i].AttachmentID != want[i].AttachmentID {
			t.Fatalf("parts[%d] = %+v, want %+v", i, parts[i], want[i])
		}
	}
}

func TestBuildAgentSendPartsAttachmentOnly(t *testing.T) {
	parts := buildAgentSendParts("", "", []string{"att-only"})
	if len(parts) != 1 || parts[0].Type != protocol.MessagePartTypeAttachment || parts[0].AttachmentID != "att-only" {
		t.Fatalf("parts = %+v, want single attachment part", parts)
	}
}

func TestRunAgentMessageSendPostsAttachmentPartsNotIDs(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/messages/send" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"action":  "message_send",
			"created": true,
			"message": map[string]any{"id": "msg-1"},
		})
	}))
	defer srv.Close()

	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "ws-1")
	t.Setenv("MULTICA_TOKEN", "test-token")

	cmd := newMessageSendCmd()
	_ = cmd.Flags().Set("message", "here's the file")
	_ = cmd.Flags().Set("attachment-id", "att-a")
	_ = cmd.Flags().Set("attachment-id", "att-b")
	_ = cmd.Flags().Set("client-message-id", "cli-msg-1")
	if err := runAgentMessageSend(cmd, nil); err != nil {
		t.Fatalf("runAgentMessageSend: %v", err)
	}

	if _, has := body["attachment_ids"]; has {
		t.Fatalf("body still has attachment_ids = %#v; chat send must use parts only", body["attachment_ids"])
	}
	if body["content"] != "here's the file" {
		t.Fatalf("content = %#v, want message text", body["content"])
	}
	rawParts, ok := body["parts"].([]any)
	if !ok {
		t.Fatalf("parts = %#v, want JSON array", body["parts"])
	}
	if len(rawParts) != 3 {
		t.Fatalf("parts len = %d, want 3 (text + 2 attachments): %#v", len(rawParts), rawParts)
	}
	assertPartMap(t, rawParts[0], map[string]any{"type": "text", "text": "here's the file"})
	assertPartMap(t, rawParts[1], map[string]any{"type": "attachment", "attachment_id": "att-a"})
	assertPartMap(t, rawParts[2], map[string]any{"type": "attachment", "attachment_id": "att-b"})
}

func assertPartMap(t *testing.T, got any, want map[string]any) {
	t.Helper()
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("part = %#v, want map", got)
	}
	for k, w := range want {
		if m[k] != w {
			t.Fatalf("part[%q] = %#v, want %#v (full=%#v)", k, m[k], w, m)
		}
	}
}
