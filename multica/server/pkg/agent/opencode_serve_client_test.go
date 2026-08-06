package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeOpenCodeServeServer is a scripted stand-in for `opencode serve`,
// speaking only the subset of the API opencodeServeClient uses:
// POST /session, POST /session/:id/message, GET /session/:id/message,
// GET /event (SSE), GET /doc (readiness probe).
type fakeOpenCodeServeServer struct {
	mu       sync.Mutex
	sessions map[string]*fakeOpenCodeServeSession
	sseConns []chan string
	// pending holds events published before any SSE subscriber attached.
	// Without this, a POST /message that races ahead of GET /event drops
	// session.idle forever and TestExecuteEndToEndAgainstFakeServeProcess
	// hangs until the package 10m timeout (CI flake on LRM-684 #1754).
	pending []string
	// scriptEvents is called after a message send completes, returning the
	// raw SSE `data: ...` payloads (without the "data:" prefix) to publish
	// for that session, in order.
	scriptEvents func(sessionID string) []string
	// scriptMessages controls what GET /session/:id/message returns.
	scriptMessages func(sessionID string) []opencodeServeMessage
	// eventDelay, when set, holds the /event handler's response for this
	// long before writing headers — lets a test simulate a slow-to-accept
	// SSE subscription against a *real* subprocess, distinct from the fast
	// in-process httptest servers the runEventLoop-level tests use.
	eventDelay time.Duration
	// handshakeMarkerFile, when set, is written the instant the (possibly
	// delayed) /event handshake completes — see eventDelay's doc comment.
	handshakeMarkerFile string
	// eventStatus, when non-zero, overrides /event's response status —
	// lets a test simulate opencode answering with e.g. 503 before a
	// subscription is actually live (see ensureServer's non-2xx handling).
	eventStatus int
}

type fakeOpenCodeServeSession struct{ id string }

func newFakeOpenCodeServeServer() *fakeOpenCodeServeServer {
	return &fakeOpenCodeServeServer{sessions: make(map[string]*fakeOpenCodeServeSession)}
}

func (f *fakeOpenCodeServeServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/global/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/session", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		id := fmt.Sprintf("ses_%d", len(f.sessions)+1)
		f.mu.Lock()
		f.sessions[id] = &fakeOpenCodeServeSession{id: id}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
	})
	mux.HandleFunc("/session/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/session/")
		parts := strings.SplitN(rest, "/", 2)
		sessionID := parts[0]
		if len(parts) == 2 && parts[1] == "message" {
			switch r.Method {
			case http.MethodPost:
				w.WriteHeader(http.StatusOK)
				go func() {
					if f.scriptEvents == nil {
						return
					}
					for _, payload := range f.scriptEvents(sessionID) {
						f.publish(payload)
					}
				}()
				return
			case http.MethodGet:
				var messages []opencodeServeMessage
				if f.scriptMessages != nil {
					messages = f.scriptMessages(sessionID)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(messages)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/event", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if f.eventDelay > 0 {
			select {
			case <-time.After(f.eventDelay):
			case <-r.Context().Done():
				return
			}
		}
		// Written at the instant the handshake actually completes — lets a
		// separate OS process (the real ensureServer subprocess harness in
		// opencode_serve_backend_test.go) observe *whether this happened yet*
		// without racing wall-clock durations against ensureServer's return.
		if f.handshakeMarkerFile != "" {
			_ = os.WriteFile(f.handshakeMarkerFile, []byte("1"), 0o644)
		}
		status := http.StatusOK
		if f.eventStatus != 0 {
			status = f.eventStatus
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(status)
		if status >= 300 {
			return
		}
		ch := make(chan string, 64)
		f.mu.Lock()
		f.sseConns = append(f.sseConns, ch)
		// Drain events that raced ahead of this subscription (POST /message
		// can complete before GET /event registers).
		pending := f.pending
		f.pending = nil
		f.mu.Unlock()
		fmt.Fprintf(w, "data: %s\n\n", `{"type":"server.connected","properties":{}}`)
		flusher.Flush()
		for _, payload := range pending {
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		}
		for {
			select {
			case payload, ok := <-ch:
				if !ok {
					return
				}
				fmt.Fprintf(w, "data: %s\n\n", payload)
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	})
	return mux
}

func (f *fakeOpenCodeServeServer) publish(payload string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sseConns) == 0 {
		f.pending = append(f.pending, payload)
		return
	}
	for _, ch := range f.sseConns {
		select {
		case ch <- payload:
		default:
		}
	}
}

func sessionIdleEvent(sessionID string) string {
	return fmt.Sprintf(`{"type":"session.idle","properties":{"sessionID":%q}}`, sessionID)
}

func sessionErrorEvent(sessionID, message string) string {
	return fmt.Sprintf(`{"type":"session.error","properties":{"sessionID":%q,"error":{"name":"ProviderError","data":{"message":%q}}}}`, sessionID, message)
}

func messagePartDeltaEvent(sessionID, text string) string {
	return fmt.Sprintf(`{"type":"message.part.delta","properties":{"sessionID":%q,"part":{"type":"text","text":%q}}}`, sessionID, text)
}

// messageUpdatedEvent simulates the upstream OpenCode event this client must
// NOT depend on (github.com/anomalyco/opencode#27966: message.updated /
// message.part.updated silently stop being delivered on 1.14.42+). Tests
// that omit this event entirely from the script prove completion still
// works via session.idle alone.
func messageUpdatedEvent(sessionID string) string {
	return fmt.Sprintf(`{"type":"message.updated","properties":{"sessionID":%q}}`, sessionID)
}

// newTestOpenCodeServeClient wires up a client against srv and registers
// its Cleanup so the client's SSE connection is torn down (c.close, which
// closes resp.Body) before the caller's own t.Cleanup(srv.Close) runs.
// t.Cleanup runs LIFO, so callers MUST register srv's cleanup (or defer
// srv.Close()) BEFORE calling this — otherwise httptest.Server.Close()
// blocks forever waiting for this test's deliberately-long-lived SSE
// connection to end on its own.
func newTestOpenCodeServeClient(t *testing.T, srv *httptest.Server) *opencodeServeClient {
	t.Helper()
	c := newOpenCodeServeClient(srv.URL, "opencode", "test-password", slog.Default())
	go c.runEventLoop(func(error) {})
	t.Cleanup(c.close)
	// Wait for the SSE handshake to actually complete (task #49) instead of
	// a fixed sleep — connectedCh closes once the event loop has a live
	// subscription, which is the real fact this helper needs, not a guess
	// at how long that takes.
	select {
	case <-c.connectedCh:
		if c.connectErr != nil {
			t.Fatalf("event loop failed to connect: %v", c.connectErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("event loop did not connect within 5s")
	}
	return c
}

// TestRunTurnCompletesViaSessionIdleWithoutMessageUpdated is the single most
// important test in this file: it reproduces the confirmed upstream SSE
// delivery bug (message.updated/message.part.updated silently dropped) by
// never emitting them, and proves the turn still completes correctly
// because completion is driven by session.idle plus a reconcile poll, not
// by accumulating message.updated events.
func TestRunTurnCompletesViaSessionIdleWithoutMessageUpdated(t *testing.T) {
	fake := newFakeOpenCodeServeServer()
	fake.scriptEvents = func(sessionID string) []string {
		return []string{
			messagePartDeltaEvent(sessionID, "Hello"),
			messagePartDeltaEvent(sessionID, ", world"),
			// Deliberately no message.updated / message.part.updated here.
			sessionIdleEvent(sessionID),
		}
	}
	fake.scriptMessages = func(sessionID string) []opencodeServeMessage {
		return []opencodeServeMessage{{
			ID: "msg_1",
			Parts: []opencodeServeMessagePart{
				{Type: "text", Text: "Hello, world"},
				{Type: "tool", Tool: "read_file", CallID: "call_1", State: &opencodeServeToolState{Status: "completed", Output: "file contents"}},
			},
		}}
	}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	client := newTestOpenCodeServeClient(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sessionID, err := client.createSession(ctx, "")
	if err != nil {
		t.Fatalf("createSession: %v", err)
	}

	var streamed strings.Builder
	var toolMessages []Message
	result, err := client.runTurn(ctx, sessionID, "hi", ExecOptions{}, func(msg Message) {
		if msg.Type == MessageText {
			streamed.WriteString(msg.Content)
		}
		if msg.Type == MessageToolUse || msg.Type == MessageToolResult {
			toolMessages = append(toolMessages, msg)
		}
	})
	if err != nil {
		t.Fatalf("runTurn: %v", err)
	}
	if result.errMsg != "" {
		t.Fatalf("runTurn returned error result: %v", result.errMsg)
	}
	if streamed.String() != "Hello, world" {
		t.Fatalf("streamed text = %q, want %q", streamed.String(), "Hello, world")
	}
	if result.output != "Hello, world" {
		t.Fatalf("result.output (reconciled) = %q, want %q — must come from the GET /session/:id/message poll, not the SSE deltas", result.output, "Hello, world")
	}
	if len(toolMessages) != 2 || toolMessages[0].Type != MessageToolUse || toolMessages[1].Type != MessageToolResult {
		t.Fatalf("tool messages = %+v, want [ToolUse, ToolResult] from the reconciled final message", toolMessages)
	}
}

// TestRunTurnOutputComesFromReconcilePollNotSSEDeltas is the regression test
// Vera's review flagged as missing: it scripts ZERO message.part.delta
// events (simulating dropped/incomplete SSE delivery — the same class of
// upstream bug this adapter is designed to tolerate) while the reconcile
// poll's final message DOES carry the full text. If result.output were
// still derived from accumulated SSE deltas, this would produce an empty
// string; deriving it from the reconciled poll produces the real text.
//
// It also covers the incident this same commit fixes: when SSE delivers no
// text at all, reconcileFinalMessage must report that text via onMessage
// (not just via result.output) — otherwise the daemon's message-reporting
// drain loop never sees it and the reply never gets persisted as a
// chat_message, even though the turn "succeeds." Before that fix, onMessage
// was never called for text on this path at all — this test used to assert
// the callback saw *nothing*, which was actually the bug, not a guarantee.
func TestRunTurnOutputComesFromReconcilePollNotSSEDeltas(t *testing.T) {
	const fullText = "The answer, reconciled from the poll, not the stream."
	fake := newFakeOpenCodeServeServer()
	fake.scriptEvents = func(sessionID string) []string {
		// No message.part.delta at all — only the terminal signal.
		return []string{sessionIdleEvent(sessionID)}
	}
	fake.scriptMessages = func(sessionID string) []opencodeServeMessage {
		return []opencodeServeMessage{{
			ID:    "msg_1",
			Parts: []opencodeServeMessagePart{{Type: "text", Text: fullText}},
		}}
	}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	client := newTestOpenCodeServeClient(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sessionID, err := client.createSession(ctx, "")
	if err != nil {
		t.Fatalf("createSession: %v", err)
	}

	var reported strings.Builder
	result, err := client.runTurn(ctx, sessionID, "hi", ExecOptions{}, func(msg Message) {
		if msg.Type == MessageText {
			reported.WriteString(msg.Content)
		}
	})
	if err != nil {
		t.Fatalf("runTurn: %v", err)
	}
	if result.errMsg != "" {
		t.Fatalf("runTurn returned error result: %v", result.errMsg)
	}
	if reported.String() != fullText {
		t.Fatalf("onMessage-reported text = %q, want %q — the reconcile fallback must report text via onMessage when SSE delivered none, or the reply never gets persisted", reported.String(), fullText)
	}
	if result.output != fullText {
		t.Fatalf("result.output = %q, want %q — final text must come from the reconcile poll even when SSE deltas never arrived", result.output, fullText)
	}
}

// TestRunTurnReconcileDoesNotDuplicateTextAlreadySeenViaSSE is the dedup
// side of the same fix: reconcileFinalMessage runs unconditionally after
// every turn, not only when SSE delivery fails. When SSE already delivered
// the text (the normal, working case), the reconcile fallback must NOT
// report it again via onMessage — otherwise every ordinary reply would be
// persisted twice.
func TestRunTurnReconcileDoesNotDuplicateTextAlreadySeenViaSSE(t *testing.T) {
	const fullText = "Delivered once, live, over SSE."
	fake := newFakeOpenCodeServeServer()
	fake.scriptEvents = func(sessionID string) []string {
		return []string{
			messagePartDeltaEvent(sessionID, fullText),
			sessionIdleEvent(sessionID),
		}
	}
	fake.scriptMessages = func(sessionID string) []opencodeServeMessage {
		// The reconcile poll sees the same final message SSE already
		// streamed — this is the normal case, not a delivery failure.
		return []opencodeServeMessage{{
			ID:    "msg_1",
			Parts: []opencodeServeMessagePart{{Type: "text", Text: fullText}},
		}}
	}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	client := newTestOpenCodeServeClient(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sessionID, err := client.createSession(ctx, "")
	if err != nil {
		t.Fatalf("createSession: %v", err)
	}

	var textCalls int
	var reported strings.Builder
	result, err := client.runTurn(ctx, sessionID, "hi", ExecOptions{}, func(msg Message) {
		if msg.Type == MessageText {
			textCalls++
			reported.WriteString(msg.Content)
		}
	})
	if err != nil {
		t.Fatalf("runTurn: %v", err)
	}
	if result.errMsg != "" {
		t.Fatalf("runTurn returned error result: %v", result.errMsg)
	}
	if textCalls != 1 {
		t.Fatalf("onMessage called with MessageText %d times, want exactly 1 — reconcile must not duplicate text SSE already delivered", textCalls)
	}
	if reported.String() != fullText {
		t.Fatalf("onMessage-reported text = %q, want %q", reported.String(), fullText)
	}
}

func TestRunTurnSurfacesSessionError(t *testing.T) {
	fake := newFakeOpenCodeServeServer()
	fake.scriptEvents = func(sessionID string) []string {
		return []string{sessionErrorEvent(sessionID, "invalid model configuration")}
	}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	client := newTestOpenCodeServeClient(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sessionID, err := client.createSession(ctx, "")
	if err != nil {
		t.Fatalf("createSession: %v", err)
	}

	result, err := client.runTurn(ctx, sessionID, "hi", ExecOptions{}, func(Message) {})
	if err != nil {
		t.Fatalf("runTurn transport error: %v", err)
	}
	if result.errMsg != "invalid model configuration" {
		t.Fatalf("result.errMsg = %q, want %q", result.errMsg, "invalid model configuration")
	}
}

func TestRunTurnTimesOutWithoutTerminalEvent(t *testing.T) {
	fake := newFakeOpenCodeServeServer()
	fake.scriptEvents = func(sessionID string) []string {
		return nil // never emit session.idle or session.error
	}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	client := newTestOpenCodeServeClient(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sessionID, err := client.createSession(ctx, "")
	if err != nil {
		t.Fatalf("createSession: %v", err)
	}

	_, err = client.runTurn(ctx, sessionID, "hi", ExecOptions{Timeout: 200 * time.Millisecond}, func(Message) {})
	if err != errOpenCodeServeTurnTimeout {
		t.Fatalf("runTurn error = %v, want errOpenCodeServeTurnTimeout", err)
	}
}

// TestRunTurnIgnoresMessageUpdatedEvent proves that even when the (buggy or
// fixed-upstream) message.updated event IS delivered, this client does not
// treat it as a completion signal on its own — only session.idle does,
// exercised here by emitting message.updated first, then waiting past a
// short window before session.idle, and confirming the turn only resolves
// once session.idle actually arrives.
func TestRunTurnIgnoresMessageUpdatedEvent(t *testing.T) {
	fake := newFakeOpenCodeServeServer()
	fake.scriptEvents = func(sessionID string) []string {
		return []string{
			messageUpdatedEvent(sessionID),
			messageUpdatedEvent(sessionID),
			sessionIdleEvent(sessionID),
		}
	}
	fake.scriptMessages = func(sessionID string) []opencodeServeMessage {
		return []opencodeServeMessage{{ID: "msg_1", Parts: []opencodeServeMessagePart{{Type: "text"}}}}
	}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	client := newTestOpenCodeServeClient(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sessionID, err := client.createSession(ctx, "")
	if err != nil {
		t.Fatalf("createSession: %v", err)
	}
	result, err := client.runTurn(ctx, sessionID, "hi", ExecOptions{}, func(Message) {})
	if err != nil {
		t.Fatalf("runTurn: %v", err)
	}
	if result.errMsg != "" {
		t.Fatalf("unexpected error result: %v", result.errMsg)
	}
}

// TestWaitReadySurvivesOneHungProbe reproduces the production incident this
// PR fixes: a probe whose connection succeeds but which never writes a
// response never returns from c.http.Do, and with no per-probe deadline that
// single call swallows the entire outer readiness budget — even though the
// server recovers and responds normally moments later. Before
// waitReadyProbeTimeout existed, this test would time out waiting on
// waitReady to return at all (it would block for the full outer context,
// here 5s, then return ctx.Err() — never observing the server's later
// healthy responses). With the per-probe timeout, the hung first probe is
// abandoned after ~waitReadyProbeTimeout and a later retry succeeds well
// within the outer deadline.
func TestWaitReadySurvivesOneHungProbe(t *testing.T) {
	var hits atomic.Int32
	block := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/global/health", func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			// First probe: accept the connection but never write a response,
			// simulating the hang observed in production. Held open until
			// the test itself unblocks it during cleanup, matching how the
			// real incident's hung request was only ever resolved by an
			// external timeout/cancellation, not a server-side response.
			<-block
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(func() {
		close(block)
		srv.Close()
	})

	client := newOpenCodeServeClient(srv.URL, "opencode", "test-password", slog.Default())
	t.Cleanup(client.close)

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.waitReady(ctx); err != nil {
		t.Fatalf("waitReady: %v (a hung first probe must not prevent a later successful retry)", err)
	}
	elapsed := time.Since(start)
	if elapsed >= 5*time.Second {
		t.Fatalf("waitReady took %s — the hung first probe consumed the entire outer deadline instead of being abandoned by the per-probe timeout", elapsed)
	}
	if got := hits.Load(); got < 2 {
		t.Fatalf("expected at least 2 probe attempts (one hung, one that succeeds), got %d", got)
	}
}

// TestRunEventLoopConnectedChClosesOnlyAfterSSEHandshake is the direct
// regression test for task #49's root cause: waitReady only proves the
// health endpoint answers, which says nothing about whether anything is
// subscribed to /event yet. Before connectedCh existed, ensureServer fired
// runEventLoop in a goroutine and returned immediately — a fast-responding
// opencode could emit its turn's SSE events before that goroutine's
// http.Do(req) even returned, silently dropping them for a reader that was
// never there.
//
// This test proves connectedCh is a genuine synchronization point, not
// just "the goroutine started": it delays the /event handler's response
// headers by a controlled amount and asserts connectedCh stays open for the
// entire delay, only closing once the SSE handshake actually completes.
func TestRunEventLoopConnectedChClosesOnlyAfterSSEHandshake(t *testing.T) {
	release := make(chan struct{})
	var headersSent atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/event", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		<-release // simulate a slow-to-accept server
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		headersSent.Store(true)
		flusher.Flush()
		<-r.Context().Done()
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client := newOpenCodeServeClient(srv.URL, "opencode", "test-password", slog.Default())
	t.Cleanup(client.close)
	go client.runEventLoop(func(error) {})

	// While the server is deliberately withholding its response, connectedCh
	// must not have closed yet — otherwise a caller gated on it would
	// proceed to send a turn before anyone is actually listening.
	select {
	case <-client.connectedCh:
		t.Fatal("connectedCh closed before the SSE handshake completed")
	case <-time.After(200 * time.Millisecond):
	}
	if headersSent.Load() {
		t.Fatal("test setup bug: headers already sent despite release channel not yet closed")
	}

	close(release)

	select {
	case <-client.connectedCh:
		if client.connectErr != nil {
			t.Fatalf("connectErr = %v, want nil", client.connectErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("connectedCh did not close after the SSE handshake completed")
	}
}

// TestRunEventLoopConnectedChReportsConnectionFailure proves the failure
// path: if the SSE request can never be established at all (server refuses
// the connection), connectedCh must still close — with connectErr set —
// rather than leaving a caller blocked forever waiting on a signal that
// will never come.
func TestRunEventLoopConnectedChReportsConnectionFailure(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	unreachableURL := srv.URL
	srv.Close() // close immediately so the port refuses new connections

	client := newOpenCodeServeClient(unreachableURL, "opencode", "test-password", slog.Default())
	t.Cleanup(client.close)
	go client.runEventLoop(func(error) {})

	select {
	case <-client.connectedCh:
		if client.connectErr == nil {
			t.Fatal("connectErr = nil, want a connection error against a closed port")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("connectedCh did not close after a failed connection attempt")
	}
}

// TestRunEventLoopConnectedChRejectsNon2xxStatus proves the second door onto
// the same race Parker flagged: opencode can be up and answering /event with
// a non-2xx status (e.g. still starting up, or a routing/auth hiccup) before
// it has actually registered a subscriber. http.Do returns no transport
// error in that case, so treating "Do succeeded" alone as "connected" would
// let a turn proceed against a connection that was never a live SSE stream
// — the exact silent-drop failure mode #49 is about, reached via a
// different door than "never waited at all".
func TestRunEventLoopConnectedChRejectsNon2xxStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/event", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client := newOpenCodeServeClient(srv.URL, "opencode", "test-password", slog.Default())
	t.Cleanup(client.close)
	go client.runEventLoop(func(error) {})

	select {
	case <-client.connectedCh:
		if client.connectErr == nil {
			t.Fatal("connectErr = nil, want an error for a 503 /event response")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("connectedCh did not close after a non-2xx /event response")
	}
}
