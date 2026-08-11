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
	"github.com/multica-ai/multica/server/internal/daemon"
	"github.com/multica-ai/multica/server/internal/turntransport"
	"github.com/spf13/pflag"
)

func setMessageCredentialProxyEnv(t *testing.T, serverURL string) {
	t.Helper()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(serverURL, "http://"))
	if err != nil {
		t.Fatalf("server port: %v", err)
	}
	t.Setenv("MULTICA_DAEMON_PORT", port)
	t.Setenv("MULTICA_AGENT_ID", "agent-1")
	t.Setenv("MULTICA_WORKSPACE_ID", "ws-1")
}

func TestMessageSendHasNoAgentControlledCursorFlag(t *testing.T) {
	cmd := newMessageSendCmd()
	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		if strings.Contains(strings.ToLower(flag.Name), "seen") {
			t.Errorf("message send exposes cursor flag %q", flag.Name)
		}
	})
	for _, name := range []string{"message", "message-stdin", "message-file", "seen", "client-message-id", "idempotency-key", "output"} {
		if cmd.Flags().Lookup(name) != nil {
			t.Fatalf("message send must not expose --%s", name)
		}
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
	for _, name := range []string{"channel", "output"} {
		if cmd.Flags().Lookup(name) != nil {
			t.Fatalf("message read must not expose --%s", name)
		}
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
		if r.URL.Path != "/credential-proxy/messages/resolve" {
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
	setMessageCredentialProxyEnv(t, srv.URL)

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
	if body["message_id"] != "11111111" || body["agent_id"] != "agent-1" || body["workspace_id"] != "ws-1" {
		t.Fatalf("resolve request body = %#v, want canonical identity and local principal", body)
	}
	for _, legacy := range []string{"task_id", "execution_id", "agent_inbox_event_id", "agent_inbox_delivery_id", "agent_inbox_lease_token"} {
		if _, found := body[legacy]; found {
			t.Fatalf("resolve proxy body leaked %s: %#v", legacy, body)
		}
	}
}

func TestMessageReactUsesCanonicalIdentityWithoutTargetOrCursor(t *testing.T) {
	cmd := newMessageReactCmd()
	for _, name := range []string{"message-id", "emoji", "remove"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("message react is missing --%s", name)
		}
	}
	for _, name := range []string{"target", "client-message-id", "channel", "output"} {
		if cmd.Flags().Lookup(name) != nil {
			t.Errorf("message react must not expose --%s", name)
		}
	}
}

func TestRunAgentMessageReactPostsTargetFreeIdentity(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/credential-proxy/messages/react" {
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
	setMessageCredentialProxyEnv(t, srv.URL)

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
	if body["message_id"] != "11111111" || body["emoji"] != "+1" || body["remove"] != true || body["agent_id"] != "agent-1" || body["workspace_id"] != "ws-1" {
		t.Fatalf("react request body = %#v, want target-free canonical identity", body)
	}
	for _, legacy := range []string{"task_id", "execution_id", "agent_inbox_event_id", "agent_inbox_delivery_id", "agent_inbox_lease_token"} {
		if _, found := body[legacy]; found {
			t.Fatalf("react proxy body leaked %s: %#v", legacy, body)
		}
	}
}

func TestMessageSearchUsesCanonicalFiltersWithoutLegacyChannelFlag(t *testing.T) {
	cmd := newMessageSearchCmd()
	for _, name := range []string{"target", "sender", "sort", "before", "after", "limit", "offset"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("message search missing --%s", name)
		}
	}
	for _, name := range []string{"channel", "output"} {
		if cmd.Flags().Lookup(name) != nil {
			t.Fatalf("message search must not expose --%s", name)
		}
	}
	if err := cmd.Args(cmd, nil); err != nil {
		t.Fatalf("filter-only message search must be accepted: %v", err)
	}
}

func TestRunAgentMessageCheckUsesMachineLocalCredentialProxy(t *testing.T) {
	var body map[string]any
	commitCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/credential-proxy/messages/check":
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode body: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"messages": []map[string]any{{
					"id": "message-1", "target": "channel:one", "seq": 1, "content": "new context",
				}},
				"has_more": true, "remaining": 1, "status": "more", "_coverage_receipt": "receipt-1",
			})
		case "/credential-proxy/messages/coverage/commit":
			commitCalls++
			if got := r.Header.Get(daemon.AgentProxyTokenHeader); got != "map_test-token" {
				t.Errorf("coverage commit token = %q", got)
			}
			var commit map[string]any
			if err := json.NewDecoder(r.Body).Decode(&commit); err != nil {
				t.Errorf("decode coverage commit: %v", err)
			}
			if commit["receipt_id"] != "receipt-1" || len(commit) != 1 {
				t.Errorf("coverage commit = %#v", commit)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "committed"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("server port: %v", err)
	}
	t.Setenv("MULTICA_DAEMON_PORT", port)
	t.Setenv("MULTICA_AGENT_ID", "agent-1")
	tokenFile := filepath.Join(t.TempDir(), "agent-proxy.token")
	if err := os.WriteFile(tokenFile, []byte("map_test-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(daemon.AgentProxyURLEnv, srv.URL)
	t.Setenv(daemon.AgentProxyTokenFileEnv, tokenFile)

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
	if strings.Contains(string(output), "receipt-1") || strings.Contains(string(output), "coverage_receipt") {
		t.Fatalf("message check output leaked internal coverage receipt: %q", output)
	}
	if commitCalls != 1 {
		t.Fatalf("coverage commit calls = %d, want 1", commitCalls)
	}
	if _, ok := body["limit"]; ok {
		t.Fatalf("Agent-controlled limit leaked into request: %+v", body)
	}
}

func TestRunAgentMessageCheckOutputFailureLeavesCoverageUncommitted(t *testing.T) {
	commitCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/credential-proxy/messages/check":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"messages": []map[string]any{{"id": "message-1", "target": "channel:one", "seq": 1, "content": "new context"}},
				"status":   "complete", "_coverage_receipt": "receipt-1",
			})
		case "/credential-proxy/messages/coverage/commit":
			commitCalls++
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "committed"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MULTICA_DAEMON_PORT", port)
	t.Setenv("MULTICA_AGENT_ID", "agent-1")

	outputErr := errors.New("stdout closed")
	err = runAgentMessageCheckWithWriter(messageCoverageFailingWriter{err: outputErr})
	if !errors.Is(err, outputErr) {
		t.Fatalf("message check error = %v, want output failure", err)
	}
	if commitCalls != 0 {
		t.Fatalf("output failure attempted %d coverage commits", commitCalls)
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
	_ = cmd.Flags().Set("around-id", "12345678")
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
		"target": "#one", "around_id": "12345678", "limit": float64(2),
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

func TestPrintAgentTransportOutputHeldReturnsError(t *testing.T) {
	err := printAgentTransportOutput(map[string]any{
		"state":   "held",
		"outcome": "held",
		"reason":  "newer_messages_available",
	})
	if !errors.Is(err, errAgentMessageHeld) {
		t.Fatalf("error = %v, want errAgentMessageHeld", err)
	}
	if code := cli.ExitCodeFor(err); code != cli.ExitGeneric {
		t.Fatalf("ExitCodeFor(held) = %d, want %d", code, cli.ExitGeneric)
	}
}

func TestPrintAgentTransportOutputSuccessReturnsNil(t *testing.T) {
	err := printAgentTransportOutput(map[string]any{
		"created": true,
		"message": map[string]any{"id": "msg-1"},
	})
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
		if r.URL.Path != "/credential-proxy/messages/search" {
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
	setMessageCredentialProxyEnv(t, srv.URL)

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
	for _, legacy := range []string{"task_id", "execution_id", "agent_inbox_event_id", "agent_inbox_delivery_id", "agent_inbox_lease_token"} {
		if _, found := body[legacy]; found {
			t.Fatalf("search proxy body leaked %s: %#v", legacy, body)
		}
	}
}

func TestRunAgentMessageSearchReportsMalformedUpstreamResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()
	setMessageCredentialProxyEnv(t, srv.URL)

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
