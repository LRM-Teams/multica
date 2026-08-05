package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/turntransport"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestBuildAgentSendPartsIncludesAttachmentParts(t *testing.T) {
	parts := buildAgentSendParts("got-it", "see files", []string{
		"att-1",
		"  att-2  ",
		"",
		"att-1", // duplicates preserved in flag order after appendUniqueStrings; builder itself does not dedupe
	}, false)
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
	parts := buildAgentSendParts("", "", []string{"att-only"}, false)
	if len(parts) != 1 || parts[0].Type != protocol.MessagePartTypeAttachment || parts[0].AttachmentID != "att-only" {
		t.Fatalf("parts = %+v, want single attachment part", parts)
	}
}

func TestBuildAgentSendPartsMarksVoiceDelivery(t *testing.T) {
	parts := buildAgentSendParts("", "spoken answer", nil, true)
	if len(parts) != 2 || parts[0].Type != protocol.MessagePartTypeText || parts[1].Type != protocol.MessagePartTypeVoice {
		t.Fatalf("parts = %+v, want text followed by voice marker", parts)
	}
}

func TestRunAgentMessageSendVoiceRequiresTranscript(t *testing.T) {
	cmd := newMessageSendCmd()
	_ = cmd.Flags().Set("target", "#multica")
	_ = cmd.Flags().Set("voice", "true")
	if err := runAgentMessageSend(cmd, nil); err == nil || !strings.Contains(err.Error(), "--voice requires message text") {
		t.Fatalf("error = %v, want missing transcript error", err)
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
	_ = cmd.Flags().Set("target", "#multica")
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
	if body["target"] != "#multica" {
		t.Fatalf("target = %#v, want #multica", body["target"])
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

func TestRunAgentMessageSendRecordsAttemptBeforeHTTPFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "transport down", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	attemptPath := filepath.Join(t.TempDir(), "transport-attempt")
	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "ws-1")
	t.Setenv("MULTICA_TOKEN", "test-token")
	t.Setenv(turntransport.AttemptPathEnv, attemptPath)

	cmd := newMessageSendCmd()
	_ = cmd.Flags().Set("target", "#multica")
	_ = cmd.Flags().Set("message", "attempted reply")
	if err := runAgentMessageSend(cmd, nil); err == nil {
		t.Fatal("runAgentMessageSend succeeded against failing transport")
	}
	if _, err := os.Stat(attemptPath); err != nil {
		t.Fatalf("transport attempt marker missing after HTTP failure: %v", err)
	}
}

func TestRunAgentMessageA2AControlPostsOwnerControl(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/messages/a2a-control" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"target": "dm:@peer-agent",
			"control": map[string]any{
				"state":       "active",
				"round_limit": 5,
			},
		})
	}))
	defer srv.Close()

	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "ws-1")
	t.Setenv("MULTICA_TOKEN", "test-token")

	cmd := newMessageA2AControlCmd()
	_ = cmd.Flags().Set("target", "dm:@peer-agent")
	_ = cmd.Flags().Set("action", "grant_rounds")
	_ = cmd.Flags().Set("exchange-id", "exchange-1")
	_ = cmd.Flags().Set("rounds", "2")
	if err := runAgentMessageA2AControl(cmd, nil); err != nil {
		t.Fatalf("runAgentMessageA2AControl: %v", err)
	}
	if body["target"] != "dm:@peer-agent" ||
		body["action"] != "grant_rounds" ||
		body["exchange_id"] != "exchange-1" ||
		body["rounds"] != float64(2) {
		t.Fatalf("A2A control body=%#v", body)
	}
}

func TestRunAgentMessageSendPostsVoiceMarkerAfterTranscript(t *testing.T) {
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
			"message": map[string]any{"id": "msg-voice"},
		})
	}))
	defer srv.Close()

	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "ws-1")
	t.Setenv("MULTICA_TOKEN", "test-token")

	cmd := newMessageSendCmd()
	_ = cmd.Flags().Set("target", "#multica")
	_ = cmd.Flags().Set("message", "spoken answer")
	_ = cmd.Flags().Set("voice", "true")
	if err := runAgentMessageSend(cmd, nil); err != nil {
		t.Fatalf("runAgentMessageSend: %v", err)
	}

	if body["content"] != "spoken answer" {
		t.Fatalf("content = %#v, want spoken answer", body["content"])
	}
	rawParts, ok := body["parts"].([]any)
	if !ok {
		t.Fatalf("parts = %#v, want JSON array", body["parts"])
	}
	if len(rawParts) != 2 {
		t.Fatalf("parts len = %d, want 2 (text + voice): %#v", len(rawParts), rawParts)
	}
	assertPartMap(t, rawParts[0], map[string]any{"type": "text", "text": "spoken answer"})
	assertPartMap(t, rawParts[1], map[string]any{"type": "voice"})
}

func TestRunAgentMessageSendIncludesSeenUpToSeqFromInboxEnv(t *testing.T) {
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
	t.Setenv("MULTICA_AGENT_INBOX_SEQ_TO", "42")

	cmd := newMessageSendCmd()
	_ = cmd.Flags().Set("target", "#multica")
	_ = cmd.Flags().Set("message", "fresh send")
	_ = cmd.Flags().Set("client-message-id", "cli-msg-1")
	if err := runAgentMessageSend(cmd, nil); err != nil {
		t.Fatalf("runAgentMessageSend: %v", err)
	}
	if body["seen_up_to_seq"] != float64(42) {
		t.Fatalf("seen_up_to_seq = %#v, want 42", body["seen_up_to_seq"])
	}
}

func TestAgentMessageSendTextFallbackReportsHeld(t *testing.T) {
	got := agentMessageSendTextFallback(map[string]any{
		"state": "held",
		"contextWindow": map[string]any{
			"olderBoundary": "No older.",
			"newerBoundary": "No newer.",
		},
	})
	if !strings.Contains(got, "Message held by freshness check") {
		t.Fatalf("fallback = %q, want held freshness text", got)
	}
	if !strings.Contains(got, "exits non-zero") || !strings.Contains(got, "Do not automatically retry") {
		t.Fatalf("fallback = %q, want non-zero exit + no-auto-retry guidance", got)
	}
	for _, want := range []string{
		"No older.",
		"No newer.",
		"compose and send a new message",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("fallback = %q, want %q", got, want)
		}
	}
	if strings.Contains(got, "multica message send") {
		t.Fatalf("fallback = %q, must not expose an executable resend command", got)
	}
	if strings.Contains(got, "--abandon-draft") {
		t.Fatalf("fallback = %q, must not invent an abandon command", got)
	}
	got = agentMessageSendTextFallback(map[string]any{"created": true})
	if got != "Message sent." {
		t.Fatalf("fallback = %q, want sent text", got)
	}
}

func TestPrintAgentTransportOutputHeldReturnsError(t *testing.T) {
	cmd := newMessageSendCmd()
	_ = cmd.Flags().Set("output", "json")
	err := printAgentTransportOutput(cmd, map[string]any{
		"state":   "held",
		"outcome": "held",
		"reason":  "newer_messages_available",
	}, agentMessageSendTextFallback(map[string]any{"state": "held"}))
	if !errors.Is(err, errAgentMessageHeld) {
		t.Fatalf("error = %v, want errAgentMessageHeld", err)
	}
	if code := cli.ExitCodeFor(err); code != cli.ExitGeneric {
		t.Fatalf("ExitCodeFor(held) = %d, want %d", code, cli.ExitGeneric)
	}
}

func TestPrintAgentTransportOutputSuccessReturnsNil(t *testing.T) {
	cmd := newMessageSendCmd()
	_ = cmd.Flags().Set("output", "json")
	err := printAgentTransportOutput(cmd, map[string]any{
		"created": true,
		"message": map[string]any{"id": "msg-1"},
	}, "Message sent.")
	if err != nil {
		t.Fatalf("error = %v, want nil for successful send", err)
	}
}

func TestRunAgentMessageCommandsRequireTarget(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "send",
			run: func() error {
				cmd := newMessageSendCmd()
				_ = cmd.Flags().Set("message", "hello")
				return runAgentMessageSend(cmd, nil)
			},
		},
		{
			name: "react",
			run: func() error {
				cmd := newMessageReactCmd()
				_ = cmd.Flags().Set("emoji", "+1")
				return runAgentMessageReact(cmd, nil)
			},
		},
		{
			name: "read",
			run: func() error {
				cmd := newMessageReadCmd()
				return runAgentMessageRead(cmd, nil)
			},
		},
		{
			name: "search",
			run: func() error {
				cmd := newMessageSearchCmd()
				return runAgentMessageSearch(cmd, []string{"needle"})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			const want = "target is required; --target accepts #channel, #channel:<threadId>, dm:@<handle>, or dm:@<handle>:<threadId>"
			if err == nil || err.Error() != want {
				t.Fatalf("error = %v, want %q", err, want)
			}
		})
	}
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
