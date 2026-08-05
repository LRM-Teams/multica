package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type noticeCaptureWriter struct {
	once sync.Once
	line chan []byte
}

type requestCaptureWriter struct {
	lines chan []byte
}

func (w *requestCaptureWriter) Write(data []byte) (int, error) {
	w.lines <- append([]byte(nil), data...)
	return len(data), nil
}

func newNoticeCaptureWriter() *noticeCaptureWriter {
	return &noticeCaptureWriter{line: make(chan []byte, 1)}
}

func (w *noticeCaptureWriter) Write(data []byte) (int, error) {
	w.once.Do(func() { w.line <- append([]byte(nil), data...) })
	return len(data), nil
}

func testResidentPendingNotice() ResidentPendingNotice {
	return ResidentPendingNotice{
		TotalPending: 2,
		ChangedTargets: []ResidentPendingTarget{
			{Target: "channel:one", PendingCount: 2},
		},
	}
}

func decodeNoticeRequest(t *testing.T, writer *noticeCaptureWriter) map[string]any {
	t.Helper()
	select {
	case raw := <-writer.line:
		var request map[string]any
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Fatalf("decode native Notice request: %v\n%s", err, raw)
		}
		return request
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for native Notice request")
		return nil
	}
}

func decodeCapturedRequest(t *testing.T, lines <-chan []byte) map[string]any {
	t.Helper()
	select {
	case raw := <-lines:
		var request map[string]any
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Fatalf("decode native request: %v\n%s", err, raw)
		}
		return request
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for native request")
		return nil
	}
}

func respondToNoticeRequest(t *testing.T, request map[string]any, handle func(string)) {
	t.Helper()
	id, ok := request["id"].(float64)
	if !ok {
		t.Fatalf("native Notice request id = %#v", request["id"])
	}
	handle(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{}}`, int(id)))
}

func TestCodexPendingNoticeUsesFencedTurnSteer(t *testing.T) {
	writer := newNoticeCaptureWriter()
	client := &codexClient{stdin: writer, pending: make(map[int]*pendingRPC)}
	client.setActiveTurn(true, "turn-7")
	process := &codexAppServerProcess{client: client, threadID: "thread-3"}
	backend := newCodexAppServerBackend(Config{})
	backend.process.Store(process)
	backend.running.Store(true)

	done := make(chan error, 1)
	go func() { done <- backend.AcceptPendingNotice(context.Background(), testResidentPendingNotice()) }()
	request := decodeNoticeRequest(t, writer)
	if request["method"] != "turn/steer" {
		t.Fatalf("Codex Notice method = %#v, want turn/steer", request["method"])
	}
	params, _ := request["params"].(map[string]any)
	if params["threadId"] != "thread-3" || params["expectedTurnId"] != "turn-7" {
		t.Fatalf("Codex Notice fence = %#v", params)
	}
	raw, _ := json.Marshal(params)
	if string(raw) == "" || containsAny(string(raw), "Message body secret", "parts", "attachment") {
		t.Fatalf("Codex Notice leaked concrete Message fields: %s", raw)
	}
	respondToNoticeRequest(t, request, client.handleLine)
	if err := <-done; err != nil {
		t.Fatalf("AcceptPendingNotice: %v", err)
	}
}

func TestGrokPendingNoticeUsesNativeInterject(t *testing.T) {
	writer := newNoticeCaptureWriter()
	client := &hermesClient{stdin: writer, pending: make(map[int]*pendingRPC)}
	process := &grokACPProcess{client: client, sessionID: "session-9"}
	backend := newGrokACPBackend(Config{})
	backend.process.Store(process)
	backend.running.Store(true)

	done := make(chan error, 1)
	go func() { done <- backend.AcceptPendingNotice(context.Background(), testResidentPendingNotice()) }()
	request := decodeNoticeRequest(t, writer)
	if request["method"] != "_x.ai/interject" {
		t.Fatalf("Grok Notice method = %#v, want _x.ai/interject", request["method"])
	}
	params, _ := request["params"].(map[string]any)
	if params["sessionId"] != "session-9" {
		t.Fatalf("Grok Notice session = %#v", params)
	}
	respondToNoticeRequest(t, request, client.handleLine)
	if err := <-done; err != nil {
		t.Fatalf("AcceptPendingNotice: %v", err)
	}
}

func TestCursorPendingNoticeUsesNativeACPFollowUp(t *testing.T) {
	writer := newNoticeCaptureWriter()
	client := &hermesClient{stdin: writer, pending: make(map[int]*pendingRPC)}
	process := &cursorACPProcess{client: client, sessionID: "session-cursor", noticeOpen: true}
	backend := newCursorACPBackend(Config{})
	backend.process.Store(process)
	backend.running.Store(true)

	if err := backend.AcceptPendingNotice(context.Background(), testResidentPendingNotice()); err != nil {
		t.Fatalf("AcceptPendingNotice: %v", err)
	}
	request := decodeNoticeRequest(t, writer)
	if request["method"] != "session/prompt" {
		t.Fatalf("Cursor Notice method = %#v, want session/prompt", request["method"])
	}
	params, _ := request["params"].(map[string]any)
	if params["sessionId"] != "session-cursor" {
		t.Fatalf("Cursor Notice session = %#v", params)
	}
	raw, _ := json.Marshal(params)
	if containsAny(string(raw), "Message body secret", "parts", "attachment") {
		t.Fatalf("Cursor Notice leaked concrete Message fields: %s", raw)
	}
	if process.noticeDone == nil {
		t.Fatal("Cursor Notice completion was not retained by the active turn")
	}
	respondToNoticeRequest(t, request, client.handleLine)
}

func TestCursorPendingNoticeRejectsBeforePrimaryAdmission(t *testing.T) {
	writer := newNoticeCaptureWriter()
	client := &hermesClient{stdin: writer, pending: make(map[int]*pendingRPC)}
	process := &cursorACPProcess{client: client, sessionID: "session-cursor"}
	backend := newCursorACPBackend(Config{})
	backend.process.Store(process)
	backend.running.Store(true)

	if err := backend.AcceptPendingNotice(context.Background(), testResidentPendingNotice()); err == nil {
		t.Fatal("Cursor Notice was accepted before primary prompt admission")
	}
	select {
	case raw := <-writer.line:
		t.Fatalf("Cursor wrote an unsafe early Notice: %s", raw)
	default:
	}
}

func TestCursorResidentTurnWaitsForNativeFollowUp(t *testing.T) {
	writer := &requestCaptureWriter{lines: make(chan []byte, 2)}
	client := &hermesClient{stdin: writer, pending: make(map[int]*pendingRPC)}
	process := &cursorACPProcess{client: client, sessionID: "session-cursor"}
	backend := newCursorACPBackend(Config{})
	backend.process.Store(process)

	session, err := backend.Execute(context.Background(), "primary", ExecOptions{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	primary := decodeCapturedRequest(t, writer.lines)
	if primary["method"] != "session/prompt" {
		t.Fatalf("primary method = %#v", primary["method"])
	}
	if err := backend.AcceptPendingNotice(context.Background(), testResidentPendingNotice()); err != nil {
		t.Fatalf("AcceptPendingNotice: %v", err)
	}
	followUp := decodeCapturedRequest(t, writer.lines)
	respondToNoticeRequest(t, primary, client.handleLine)
	select {
	case result := <-session.Result:
		t.Fatalf("resident turn completed before Cursor follow-up: %+v", result)
	case <-time.After(30 * time.Millisecond):
	}
	respondToNoticeRequest(t, followUp, client.handleLine)
	select {
	case result := <-session.Result:
		if result.Status != "completed" {
			t.Fatalf("resident result = %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("resident turn did not finish after Cursor follow-up")
	}
}

func TestOpenCodePendingNoticeUsesDurableQueuedPrompt(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/session/session-opencode/prompt" {
			t.Errorf("OpenCode Notice request = %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Errorf("decode OpenCode Notice: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"id":"msg_notice","sessionID":"session-opencode","delivery":"queue"}}`))
	}))
	defer server.Close()

	client := newOpenCodeServeClient(server.URL, "opencode", "secret", nil)
	process := &opencodeServeProcess{client: client, sessionID: "session-opencode"}
	backend := newOpenCodeServeBackend(Config{})
	backend.server = process
	backend.running = true
	backend.noticeReady = true
	if err := backend.AcceptPendingNotice(context.Background(), testResidentPendingNotice()); err != nil {
		t.Fatalf("AcceptPendingNotice: %v", err)
	}
	if requestBody["delivery"] != "queue" || requestBody["resume"] != true {
		t.Fatalf("OpenCode Notice admission = %#v", requestBody)
	}
	prompt, _ := requestBody["prompt"].(map[string]any)
	if strings.TrimSpace(fmt.Sprint(prompt["text"])) == "" {
		t.Fatalf("OpenCode Notice prompt = %#v", prompt)
	}
	raw, _ := json.Marshal(requestBody)
	if containsAny(string(raw), "Message body secret", "parts", "attachment") {
		t.Fatalf("OpenCode Notice leaked concrete Message fields: %s", raw)
	}
}

func TestOpenCodePendingNoticeRejectsBeforePrimaryAdmission(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer server.Close()
	client := newOpenCodeServeClient(server.URL, "opencode", "secret", nil)
	backend := newOpenCodeServeBackend(Config{})
	backend.server = &opencodeServeProcess{client: client, sessionID: "session-opencode"}
	backend.running = true

	if err := backend.AcceptPendingNotice(context.Background(), testResidentPendingNotice()); err == nil {
		t.Fatal("OpenCode Notice was accepted before primary prompt admission")
	}
	if called {
		t.Fatal("OpenCode wrote an unsafe early Notice")
	}
}

func TestOpenCodePendingNoticeFencesTerminalRelease(t *testing.T) {
	requestEntered := make(chan struct{})
	allowReceipt := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(requestEntered)
		<-allowReceipt
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"id":"msg_notice","sessionID":"session-opencode","delivery":"queue"}}`))
	}))
	defer server.Close()

	client := newOpenCodeServeClient(server.URL, "opencode", "secret", nil)
	backend := newOpenCodeServeBackend(Config{})
	backend.server = &opencodeServeProcess{client: client, sessionID: "session-opencode"}
	backend.running = true
	backend.noticeReady = true

	acceptDone := make(chan error, 1)
	go func() { acceptDone <- backend.AcceptPendingNotice(context.Background(), testResidentPendingNotice()) }()
	select {
	case <-requestEntered:
	case <-time.After(time.Second):
		t.Fatal("OpenCode Notice did not reach native admission")
	}

	releaseDone := make(chan struct{})
	go func() {
		backend.releaseTurnAdmission()
		close(releaseDone)
	}()
	select {
	case <-releaseDone:
		t.Fatal("OpenCode turn released before Notice admission receipt")
	case <-time.After(30 * time.Millisecond):
	}

	close(allowReceipt)
	if err := <-acceptDone; err != nil {
		t.Fatalf("AcceptPendingNotice: %v", err)
	}
	select {
	case <-releaseDone:
	case <-time.After(time.Second):
		t.Fatal("OpenCode turn did not release after Notice receipt")
	}
	backend.mu.Lock()
	running := backend.running
	backend.mu.Unlock()
	if running {
		t.Fatal("OpenCode turn remained active after fenced release")
	}
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
