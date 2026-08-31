package main

import (
	"bytes"
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

type messageFailingWriter struct{ err error }

func (w messageFailingWriter) Write([]byte) (int, error) { return 0, w.err }

func setMessageCredentialProxyEnv(t *testing.T, serverURL string) {
	t.Helper()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(serverURL, "http://"))
	if err != nil {
		t.Fatalf("server port: %v", err)
	}
	t.Setenv("MULTICA_DAEMON_PORT", port)
	t.Setenv("MULTICA_AGENT_ID", "agent-1")
	t.Setenv("MULTICA_WORKSPACE_ID", "ws-1")
	setTestAgentProxyToken(t)
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

	withNoteWrite := newMessageSendCmd()
	_ = withNoteWrite.Flags().Set("target", "#one")
	_ = withNoteWrite.Flags().Set("send-draft", "true")
	_ = withNoteWrite.Flags().Set("note-write", "true")
	if err := runAgentMessageSend(withNoteWrite, nil); err == nil || !strings.Contains(err.Error(), "does not accept --note-write") {
		t.Fatalf("send-draft note-write error = %v", err)
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
	var agentHeader string
	ackCalls := 0
	checkCalls := 0
	items := []map[string]any{
		{"itemId": "message:message-1:1", "message": map[string]any{"id": "message-1", "target": "channel:one", "seq": 1, "content": "new context"}},
		{"itemId": "message:message-2:2", "message": map[string]any{"id": "message-2", "target": "channel:one", "seq": 2, "content": "final context"}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/inbox":
			checkCalls++
			agentHeader = r.Header.Get("X-Agent-ID")
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
		case "/inbox/ack":
			ackCalls++
			var ack map[string]any
			if err := json.NewDecoder(r.Body).Decode(&ack); err != nil {
				t.Errorf("decode inbox ACK: %v", err)
			}
			if _, ok := ack["itemId"].(string); !ok || len(ack) != 1 {
				t.Errorf("inbox ACK = %#v", ack)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "itemId": ack["itemId"]})
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
	if err := os.WriteFile(tokenFile, []byte("mpt_test-token"), 0o600); err != nil {
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
	if agentHeader != "agent-1" {
		t.Fatalf("inbox agent scope = %q", agentHeader)
	}
	if checkCalls != 1 {
		t.Fatalf("inbox snapshot calls = %d, want 1", checkCalls)
	}
	for _, legacy := range []string{"task_id", "agent_inbox_event_id", "agent_inbox_delivery_id", "agent_inbox_lease_token"} {
		if _, found := body[legacy]; found {
			t.Fatalf("Credential Proxy body leaked %s: %+v", legacy, body)
		}
	}
	for _, want := range []string{"channel:one", "new context", "final context"} {
		if !strings.Contains(string(output), want) {
			t.Errorf("output %q missing %q", output, want)
		}
	}
	if ackCalls != 2 {
		t.Fatalf("inbox ACK calls = %d, want 2", ackCalls)
	}
	if _, ok := body["limit"]; ok {
		t.Fatalf("Agent-controlled limit leaked into request: %+v", body)
	}
}

func TestRunAgentMessageCheckCommitsCoverageBeforeOutput(t *testing.T) {
	ackCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/inbox":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{{"itemId": "message:message-1:1", "message": map[string]any{"id": "message-1", "target": "channel:one", "seq": 1, "content": "new context"}}},
			})
		case "/inbox/ack":
			ackCalls++
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "acknowledged"})
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
	t.Setenv(daemon.AgentProxyURLEnv, srv.URL)
	tokenFile := filepath.Join(t.TempDir(), "agent-proxy.token")
	if err := os.WriteFile(tokenFile, []byte("mpt_test-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(daemon.AgentProxyTokenFileEnv, tokenFile)

	outputErr := errors.New("stdout closed")
	err = runAgentMessageCheckWithWriter(messageFailingWriter{err: outputErr})
	if !errors.Is(err, outputErr) {
		t.Fatalf("message check error = %v, want output failure", err)
	}
	if ackCalls != 0 {
		t.Fatalf("output failure attempted %d inbox ACKs, want 0", ackCalls)
	}
}

func TestRunAgentMessageReadUsesMachineLocalCredentialProxy(t *testing.T) {
	ackCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/inbox":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"itemId": "message:message-1:1", "message": map[string]any{"id": "message-1", "target": "channel:one", "seq": 1, "content": "read context"}}}})
		case "/inbox/ack":
			ackCalls++
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "acknowledged"})
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
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-1")
	tokenFile := filepath.Join(t.TempDir(), "agent-proxy.token")
	if err := os.WriteFile(tokenFile, []byte("mpt_read-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(daemon.AgentProxyURLEnv, srv.URL)
	t.Setenv(daemon.AgentProxyTokenFileEnv, tokenFile)

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
	var printed map[string]any
	if err := json.Unmarshal(output, &printed); err != nil || printed["target"] != "#one" {
		t.Fatalf("output = %q, want JSON read result (err=%v)", output, err)
	}
	if _, leaked := printed["revision"]; leaked {
		t.Fatalf("message read output leaked inbox revision: %#v", printed)
	}
	if ackCalls != 0 {
		t.Fatalf("empty read inbox ACK calls = %d, want 0", ackCalls)
	}
}

func TestRunAgentMessageReadOutputFailureLeavesCoverageUncommitted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/inbox":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	setMessageCredentialProxyEnv(t, srv.URL)

	cmd := newMessageReadCmd()
	_ = cmd.Flags().Set("target", "#one")
	outputErr := errors.New("stdout closed")
	err := runAgentMessageReadWithWriter(cmd, messageFailingWriter{err: outputErr})
	if !errors.Is(err, outputErr) {
		t.Fatalf("message read error = %v, want output failure", err)
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
	setTestAgentProxyToken(t)

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

func TestRunAgentMessageSendPostsNoteWriteFlagWithoutParts(t *testing.T) {
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
			"action": "message_send", "created": true,
			"message": map[string]any{"id": "msg-1"},
		})
	}))
	defer srv.Close()
	setMessageCredentialProxyEnv(t, srv.URL)

	cmd := newMessageSendCmd()
	_ = cmd.Flags().Set("target", "#multica")
	_ = cmd.Flags().Set("note-write", "true")
	_ = cmd.Flags().Set("note-page-id", "11111111-1111-1111-1111-111111111111")
	cmd.SetIn(strings.NewReader("proposed body"))
	if err := runAgentMessageSend(cmd, nil); err != nil {
		t.Fatalf("runAgentMessageSend: %v", err)
	}
	if _, has := body["parts"]; has {
		t.Fatalf("Proxy request must not contain caller-built parts: %#v", body["parts"])
	}
	if body["note_write"] != true {
		t.Fatalf("note_write = %#v, want true", body["note_write"])
	}
	if body["note_page_id"] != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("note_page_id = %#v", body["note_page_id"])
	}
}

func TestRunAgentMessageSendRejectsNotePageIDWithoutNoteWrite(t *testing.T) {
	cmd := newMessageSendCmd()
	_ = cmd.Flags().Set("target", "#multica")
	_ = cmd.Flags().Set("note-page-id", "11111111-1111-1111-1111-111111111111")
	cmd.SetIn(strings.NewReader("proposed body"))
	if err := runAgentMessageSend(cmd, nil); err == nil || !strings.Contains(err.Error(), "requires --note-write") {
		t.Fatalf("error = %v, want --note-page-id requires --note-write", err)
	}
}

func TestRunAgentMessageSendCommitsHeldCoverageAfterVisibleOutput(t *testing.T) {
	ackCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/credential-proxy/messages/send":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"action": "message_send", "state": "held", "outcome": "held",
				"heldMessages": []map[string]any{{"id": "message-1", "target": "channel:one", "seq": 1, "content": "new context"}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	setMessageCredentialProxyEnv(t, srv.URL)
	tokenFile := filepath.Join(t.TempDir(), "agent-proxy.token")
	if err := os.WriteFile(tokenFile, []byte("mpt_send-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(daemon.AgentProxyURLEnv, srv.URL)
	t.Setenv(daemon.AgentProxyTokenFileEnv, tokenFile)

	cmd := newMessageSendCmd()
	_ = cmd.Flags().Set("target", "#one")
	cmd.SetIn(strings.NewReader("draft reply"))
	var output bytes.Buffer
	err := runAgentMessageSendWithWriter(cmd, &output)
	if !errors.Is(err, errAgentMessageHeld) {
		t.Fatalf("send error = %v, want held result after commit", err)
	}
	if ackCalls != 0 {
		t.Fatalf("held send inbox ACK calls = %d, want 0", ackCalls)
	}
	var visible map[string]any
	if err := json.Unmarshal(output.Bytes(), &visible); err != nil {
		t.Fatalf("visible output = %q: %v", output.String(), err)
	}
	if visible["state"] != "held" || visible["outcome"] != "held" {
		t.Fatalf("visible held output = %#v", visible)
	}
}

func TestRunAgentMessageSendOutputFailureLeavesHeldCoverageUncommitted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/credential-proxy/messages/send":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"action": "message_send", "state": "held", "outcome": "held",
				"heldMessages": []map[string]any{{"id": "message-1", "target": "channel:one", "seq": 1, "content": "new context"}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	setMessageCredentialProxyEnv(t, srv.URL)

	cmd := newMessageSendCmd()
	_ = cmd.Flags().Set("target", "#one")
	cmd.SetIn(strings.NewReader("draft reply"))
	outputErr := errors.New("stdout closed")
	err := runAgentMessageSendWithWriter(cmd, messageFailingWriter{err: outputErr})
	if !errors.Is(err, outputErr) {
		t.Fatalf("send error = %v, want output failure", err)
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
