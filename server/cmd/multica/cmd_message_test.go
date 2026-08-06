package main

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/turntransport"
	"github.com/spf13/pflag"
)

func TestMessageSendHasNoAgentControlledCursorFlag(t *testing.T) {
	cmd := newMessageSendCmd()
	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		if strings.Contains(strings.ToLower(flag.Name), "seen") {
			t.Errorf("message send exposes cursor flag %q", flag.Name)
		}
	})
	if cmd.Flags().Lookup("client-message-id") != nil || cmd.Flags().Lookup("idempotency-key") != nil {
		t.Fatal("message send exposes an Agent-controlled idempotency flag")
	}
}

func TestRunAgentMessageSendDraftPathRejectsReplacementPayloadAndNormalAnyway(t *testing.T) {
	withAttachment := newMessageSendCmd()
	_ = withAttachment.Flags().Set("target", "#one")
	_ = withAttachment.Flags().Set("send-draft", "true")
	_ = withAttachment.Flags().Set("attachment-id", "attachment-1")
	if err := runAgentMessageSend(withAttachment, nil); err == nil || !strings.Contains(err.Error(), "does not accept --attachment-id") {
		t.Fatalf("send-draft replacement error = %v", err)
	}

	normalAnyway := newMessageSendCmd()
	_ = normalAnyway.Flags().Set("target", "#one")
	_ = normalAnyway.Flags().Set("anyway", "true")
	if err := runAgentMessageSend(normalAnyway, nil); err == nil || !strings.Contains(err.Error(), "only valid with --send-draft") {
		t.Fatalf("normal --anyway error = %v", err)
	}
}

func TestMessageReadUsesOnlyCanonicalTargetFlag(t *testing.T) {
	cmd := newMessageReadCmd()
	if cmd.Flags().Lookup("target") == nil {
		t.Fatal("message read is missing --target")
	}
	if cmd.Flags().Lookup("channel") != nil {
		t.Fatal("message read must not expose legacy --channel")
	}
}

func TestMessageResolveAcceptsOnlyOneIdentityWithoutGenericFlags(t *testing.T) {
	cmd := newMessageResolveCmd()
	if err := cmd.Args(cmd, []string{"11111111-2222-3333-4444-555555555555"}); err != nil {
		t.Fatalf("resolve one full id: %v", err)
	}
	if err := cmd.Args(cmd, nil); err == nil {
		t.Fatal("resolve must require one message identity")
	}
	if err := cmd.Args(cmd, []string{"one", "two"}); err == nil {
		t.Fatal("resolve must reject more than one message identity")
	}
	for _, name := range []string{"target", "output", "channel"} {
		if cmd.Flags().Lookup(name) != nil {
			t.Errorf("resolve must not expose --%s", name)
		}
	}
}

func TestRunAgentMessageResolvePostsOneIdentity(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/messages/resolve" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"action": "message_resolve", "message": map[string]any{"id": "11111111-2222-3333-4444-555555555555"},
		})
	}))
	defer srv.Close()
	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "ws-1")
	t.Setenv("MULTICA_TOKEN", "test-token")

	readOut, writeOut, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	oldOut := os.Stdout
	os.Stdout = writeOut
	err = runAgentMessageResolve(newMessageResolveCmd(), []string{"11111111"})
	writeOut.Close()
	os.Stdout = oldOut
	_, readErr := io.ReadAll(readOut)
	readOut.Close()
	if err != nil {
		t.Fatalf("runAgentMessageResolve: %v", err)
	}
	if readErr != nil {
		t.Fatalf("read stdout: %v", readErr)
	}
	if len(body) != 1 || body["message_id"] != "11111111" {
		t.Fatalf("resolve request body = %#v, want only message_id", body)
	}
}

func TestMessageReactUsesCanonicalIdentityWithoutTargetOrCursor(t *testing.T) {
	cmd := newMessageReactCmd()
	for _, name := range []string{"message-id", "emoji", "remove", "output"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("message react is missing --%s", name)
		}
	}
	for _, name := range []string{"target", "client-message-id", "channel"} {
		if cmd.Flags().Lookup(name) != nil {
			t.Errorf("message react must not expose --%s", name)
		}
	}
}

func TestRunAgentMessageReactPostsTargetFreeIdentity(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/messages/react" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"action": "message_react", "channel_id": "channel-1", "message_id": "11111111-2222-3333-4444-555555555555", "emoji": "👍", "removed": true,
		})
	}))
	defer srv.Close()
	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "ws-1")
	t.Setenv("MULTICA_TOKEN", "test-token")

	readOut, writeOut, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	oldOut := os.Stdout
	os.Stdout = writeOut
	cmd := newMessageReactCmd()
	_ = cmd.Flags().Set("message-id", "11111111")
	_ = cmd.Flags().Set("emoji", "+1")
	_ = cmd.Flags().Set("remove", "true")
	err = runAgentMessageReact(cmd, nil)
	writeOut.Close()
	os.Stdout = oldOut
	_, readErr := io.ReadAll(readOut)
	readOut.Close()
	if err != nil {
		t.Fatalf("runAgentMessageReact: %v", err)
	}
	if readErr != nil {
		t.Fatalf("read stdout: %v", readErr)
	}
	if len(body) != 3 || body["message_id"] != "11111111" || body["emoji"] != "+1" || body["remove"] != true {
		t.Fatalf("react request body = %#v, want target-free canonical identity", body)
	}
}

func TestMessageSearchUsesCanonicalFiltersWithoutLegacyChannelFlag(t *testing.T) {
	cmd := newMessageSearchCmd()
	for _, name := range []string{"target", "sender", "sort", "before", "after", "limit", "offset"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("message search missing --%s", name)
		}
	}
	if cmd.Flags().Lookup("channel") != nil {
		t.Fatal("message search must not expose legacy --channel")
	}
	if err := cmd.Args(cmd, nil); err != nil {
		t.Fatalf("filter-only message search must be accepted: %v", err)
	}
}

func TestRunAgentMessageCheckUsesMachineLocalCredentialProxy(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/credential-proxy/messages/check" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"messages": []map[string]any{{
				"id": "message-1", "target": "channel:one", "seq": 1, "content": "new context",
			}},
			"has_more": true, "remaining": 1, "status": "more",
		})
	}))
	defer srv.Close()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("server port: %v", err)
	}
	t.Setenv("MULTICA_DAEMON_PORT", port)
	t.Setenv("MULTICA_AGENT_ID", "agent-1")

	readOut, writeOut, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	oldOut := os.Stdout
	os.Stdout = writeOut
	err = runAgentMessageCheck(newMessageCheckCmd(), nil)
	writeOut.Close()
	os.Stdout = oldOut
	output, readErr := io.ReadAll(readOut)
	readOut.Close()
	if err != nil {
		t.Fatalf("runAgentMessageCheck: %v", err)
	}
	if readErr != nil {
		t.Fatalf("read stdout: %v", readErr)
	}
	if body["agent_id"] != "agent-1" {
		t.Fatalf("Credential Proxy body = %+v", body)
	}
	for _, legacy := range []string{"task_id", "agent_inbox_event_id", "agent_inbox_delivery_id", "agent_inbox_lease_token"} {
		if _, found := body[legacy]; found {
			t.Fatalf("Credential Proxy body leaked %s: %+v", legacy, body)
		}
	}
	for _, want := range []string{"channel:one", "new context", "run `multica message check` again"} {
		if !strings.Contains(string(output), want) {
			t.Errorf("output %q missing %q", output, want)
		}
	}
	if _, ok := body["limit"]; ok {
		t.Fatalf("Agent-controlled limit leaked into request: %+v", body)
	}
}

func TestRunAgentMessageReadUsesMachineLocalCredentialProxy(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/credential-proxy/messages/read" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"action": "message_read", "target": "#one", "messages": []any{}, "limit": 2,
		})
	}))
	defer srv.Close()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("server port: %v", err)
	}
	t.Setenv("MULTICA_DAEMON_PORT", port)
	t.Setenv("MULTICA_AGENT_ID", "agent-1")
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-1")

	cmd := newMessageReadCmd()
	_ = cmd.Flags().Set("target", "#one")
	_ = cmd.Flags().Set("around", "123")
	_ = cmd.Flags().Set("limit", "2")
	readOut, writeOut, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	oldOut := os.Stdout
	os.Stdout = writeOut
	err = runAgentMessageRead(cmd, nil)
	writeOut.Close()
	os.Stdout = oldOut
	output, readErr := io.ReadAll(readOut)
	readOut.Close()
	if err != nil {
		t.Fatalf("runAgentMessageRead: %v", err)
	}
	if readErr != nil {
		t.Fatalf("read stdout: %v", readErr)
	}
	for field, want := range map[string]any{
		"agent_id": "agent-1", "workspace_id": "workspace-1",
		"target": "#one", "around": "123", "limit": float64(2),
	} {
		if got := body[field]; got != want {
			t.Errorf("Credential Proxy body[%q] = %#v, want %#v (body=%+v)", field, got, want, body)
		}
	}
	if _, ok := body["seen_up_to_seq"]; ok {
		t.Fatalf("Agent-controlled cursor leaked into request: %+v", body)
	}
	var printed map[string]any
	if err := json.Unmarshal(output, &printed); err != nil || printed["target"] != "#one" {
		t.Fatalf("output = %q, want JSON read result (err=%v)", output, err)
	}
}

func TestRunAgentMessageSendRequiresNonEmptyStdin(t *testing.T) {
	cmd := newMessageSendCmd()
	_ = cmd.Flags().Set("target", "#multica")
	cmd.SetIn(strings.NewReader(" \n\t "))
	if err := runAgentMessageSend(cmd, nil); err == nil || !strings.Contains(err.Error(), "required on stdin") {
		t.Fatalf("error = %v, want missing stdin content error", err)
	}
}

func TestRunAgentMessageSendPostsOpaqueAttachmentIDsInOrder(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/credential-proxy/messages/send" {
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

	t.Setenv("MULTICA_WORKSPACE_ID", "ws-1")
	t.Setenv("MULTICA_AGENT_ID", "agent-1")
	_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("server port: %v", err)
	}
	t.Setenv("MULTICA_DAEMON_PORT", port)

	cmd := newMessageSendCmd()
	_ = cmd.Flags().Set("target", "#multica")
	_ = cmd.Flags().Set("attachment-id", "att-a")
	_ = cmd.Flags().Set("attachment-id", "att-b")
	cmd.SetIn(strings.NewReader("here's the file"))
	if err := runAgentMessageSend(cmd, nil); err != nil {
		t.Fatalf("runAgentMessageSend: %v", err)
	}

	if _, has := body["parts"]; has {
		t.Fatalf("Proxy request must not contain caller-built parts: %#v", body["parts"])
	}
	if body["target"] != "#multica" {
		t.Fatalf("target = %#v, want #multica", body["target"])
	}
	if body["content"] != "here's the file" {
		t.Fatalf("content = %#v, want message text", body["content"])
	}
	for _, legacy := range []string{"task_id", "agent_inbox_event_id", "agent_inbox_delivery_id", "agent_inbox_lease_token"} {
		if _, found := body[legacy]; found {
			t.Fatalf("Proxy request leaked %s: %+v", legacy, body)
		}
	}
	attachmentIDs, ok := body["attachment_ids"].([]any)
	if !ok {
		t.Fatalf("attachment_ids = %#v, want JSON array", body["attachment_ids"])
	}
	if len(attachmentIDs) != 2 || attachmentIDs[0] != "att-a" || attachmentIDs[1] != "att-b" {
		t.Fatalf("attachment_ids = %#v, want ordered opaque ids", attachmentIDs)
	}
}

func TestRunAgentMessageSendDoesNotRecordTurnAttempt(t *testing.T) {
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
	cmd.SetIn(strings.NewReader("attempted reply"))
	if err := runAgentMessageSend(cmd, nil); err == nil {
		t.Fatal("runAgentMessageSend succeeded against failing transport")
	}
	if _, err := os.Stat(attemptPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("chat send must not record a turn attempt, stat error = %v", err)
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
				cmd.SetIn(strings.NewReader("hello"))
				return runAgentMessageSend(cmd, nil)
			},
		},
		{
			name: "read",
			run: func() error {
				cmd := newMessageReadCmd()
				return runAgentMessageRead(cmd, nil)
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

func TestRunAgentMessageSearchPostsCanonicalFiltersAndPermitsFilterOnly(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/messages/search" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"action": "message_search", "query": "", "sort": "oldest", "results": []any{}, "total": 0,
		})
	}))
	defer srv.Close()
	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "ws-1")
	t.Setenv("MULTICA_TOKEN", "test-token")

	cmd := newMessageSearchCmd()
	_ = cmd.Flags().Set("target", "#multica")
	_ = cmd.Flags().Set("sender", "user:00000000-0000-4000-8000-000000000001")
	_ = cmd.Flags().Set("sort", "oldest")
	_ = cmd.Flags().Set("before", "2026-01-03T03:04:05Z")
	_ = cmd.Flags().Set("after", "2026-01-02T03:04:05Z")
	_ = cmd.Flags().Set("limit", "2")
	_ = cmd.Flags().Set("offset", "4")

	readOut, writeOut, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	oldOut := os.Stdout
	os.Stdout = writeOut
	err = runAgentMessageSearch(cmd, nil)
	writeOut.Close()
	os.Stdout = oldOut
	_, readErr := io.ReadAll(readOut)
	readOut.Close()
	if err != nil {
		t.Fatalf("runAgentMessageSearch: %v", err)
	}
	if readErr != nil {
		t.Fatalf("read stdout: %v", readErr)
	}
	if body["query"] != "" || body["target"] != "#multica" || body["sender"] != "user:00000000-0000-4000-8000-000000000001" || body["sort"] != "oldest" {
		t.Fatalf("search body = %#v", body)
	}
	if body["before"] != "2026-01-03T03:04:05Z" || body["after"] != "2026-01-02T03:04:05Z" || body["limit"] != float64(2) || body["offset"] != float64(4) {
		t.Fatalf("search filters = %#v", body)
	}
	if _, ok := body["channel"]; ok {
		t.Fatalf("legacy channel field leaked into search body: %#v", body)
	}
}

func TestRunAgentMessageSearchReportsMalformedUpstreamResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()
	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "ws-1")
	t.Setenv("MULTICA_TOKEN", "test-token")

	err := runAgentMessageSearch(newMessageSearchCmd(), []string{"needle"})
	if err == nil || !strings.Contains(err.Error(), "search messages") {
		t.Fatalf("error = %v, want malformed search response context", err)
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
