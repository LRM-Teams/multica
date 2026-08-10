package agent

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func cursorACPTestConfig(execPath string, env map[string]string) Config {
	if env == nil {
		env = map[string]string{}
	}
	return Config{ExecutablePath: execPath, Env: env, Logger: slog.Default()}
}

// Fake cursor-agent acp server matching 2026-07-27 live CLI evidence:
// initialize advertises cursor_login; authenticate succeeds; session/new requires
// mcpServers array; permission arrives as session/request_permission.
func fakeCursorACPProcessScript() string {
	return `#!/bin/sh
printf x >> "$CURSOR_ACP_TEST_STARTS"
if [ -n "$CURSOR_ACP_TEST_ARGS" ]; then
  printf '%s\n' "$@" > "$CURSOR_ACP_TEST_ARGS"
fi
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
    *'"initialize"'*)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1,"agentCapabilities":{"loadSession":true,"mcpCapabilities":{"http":true,"sse":true},"promptCapabilities":{"image":true},"sessionCapabilities":{"list":{}}},"authMethods":[{"id":"cursor_login","name":"Cursor Login"}]}}\n' "$id"
      ;;
    *'"authenticate"'*)
      case "$line" in
        *cursor_login*) printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id" ;;
        *) printf '{"jsonrpc":"2.0","id":%s,"error":{"code":-32000,"message":"Authentication required"}}\n' "$id" ;;
      esac
      ;;
    *'"session/new"'*)
      case "$line" in
        *'"mcpServers":['*|*'mcpServers":[]'*)
          printf '{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"cursor-session-1"}}\n' "$id"
          ;;
        *)
          printf '{"jsonrpc":"2.0","id":%s,"error":{"code":-32603,"message":"Internal error","data":[{"path":["mcpServers"],"message":"Invalid input"}]}}\n' "$id"
          ;;
      esac
      ;;
    *'"session/load"'*)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"cursor-session-resumed"}}\n' "$id"
      ;;
    *'"session/prompt"'*)
      # server→client permission callback (live CLI; hermesClient auto-approves)
      printf '%s\n' '{"jsonrpc":"2.0","id":9001,"method":"session/request_permission","params":{"sessionId":"cursor-session-1","toolCall":{"toolCallId":"t1","title":"run"},"options":[{"optionId":"allow-once","name":"Allow","kind":"allow_once"}]}}'
      printf '{"jsonrpc":"2.0","id":%s,"result":{"stopReason":"end_turn"}}\n' "$id"
      ;;
    *'"session/set_model"'*)
      printf '{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"Method not found"}}\n' "$id"
      ;;
  esac
done
`
}

func fakeCursorACPAuthRequiredScript() string {
	return `#!/bin/sh
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
    *'"initialize"'*)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1,"authMethods":[{"id":"cursor_login"}]}}\n' "$id"
      ;;
    *'"authenticate"'*)
      printf '{"jsonrpc":"2.0","id":%s,"error":{"code":-32000,"message":"Authentication required","data":{"message":"Please run agent login first"}}}\n' "$id"
      ;;
  esac
done
`
}

func TestCursorACPBackendStartsAcpSubcommand(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cursor-agent")
	writeTestExecutable(t, path, []byte(fakeCursorACPProcessScript()))
	argsPath := filepath.Join(dir, "args")
	b := newCursorACPBackend(cursorACPTestConfig(path, map[string]string{
		"CURSOR_ACP_TEST_STARTS": filepath.Join(dir, "starts"),
		"CURSOR_ACP_TEST_ARGS":   argsPath,
	}))
	t.Cleanup(b.Close)

	s, err := b.Execute(context.Background(), "first", ExecOptions{Cwd: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	select {
	case got := <-s.Result:
		if got.Status != "completed" {
			t.Fatalf("result = %+v", got)
		}
		if got.SessionID != "cursor-session-1" {
			t.Fatalf("sessionId = %q", got.SessionID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}

	data, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Fields(string(data)), []string{"acp"}; !slices.Equal(got, want) {
		t.Fatalf("cursor argv = %v, want %v", got, want)
	}
}

func TestCursorACPBackendReusesOneChildForCompatibleTurns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cursor-agent")
	writeTestExecutable(t, path, []byte(fakeCursorACPProcessScript()))
	starts := filepath.Join(dir, "starts")
	b := newCursorACPBackend(cursorACPTestConfig(path, map[string]string{
		"CURSOR_ACP_TEST_STARTS": starts,
	}))
	t.Cleanup(b.Close)

	// Pin release-before-publish: hold the first turn's publisher after Result
	// is sent so an immediate second Execute cannot race a late running=false.
	published := make(chan struct{})
	releasePublisher := make(chan struct{})
	var firstPublish sync.Once
	b.afterResultPublishForTest = func() {
		firstPublish.Do(func() {
			close(published)
			<-releasePublisher
		})
	}

	first, err := b.Execute(context.Background(), "first", ExecOptions{Cwd: dir})
	if err != nil {
		t.Fatalf("Execute(first): %v", err)
	}
	select {
	case got := <-first.Result:
		if got.Status != "completed" || got.SessionID != "cursor-session-1" {
			t.Fatalf("first result = %+v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for first result")
	}
	<-published
	if b.running.Load() {
		close(releasePublisher)
		t.Fatal("terminal result published before cursor ACP turn admission was released")
	}

	// Immediate synchronous follow-up — must not get ErrCursorACPTurnBusy.
	second, err := b.Execute(context.Background(), "second", ExecOptions{Cwd: dir})
	if err != nil {
		close(releasePublisher)
		t.Fatalf("Execute(second) immediately after terminal result: %v", err)
	}

	// First turn's deferred release must be inert after explicit pre-publish release.
	close(releasePublisher)
	select {
	case _, ok := <-first.Result:
		if ok {
			t.Fatal("unexpected second terminal result from first turn")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first turn publisher to exit")
	}
	if !b.running.Load() {
		t.Fatal("first turn deferred release clobbered active second-turn admission")
	}
	if _, err := b.Execute(context.Background(), "third", ExecOptions{Cwd: dir}); err == nil || !strings.Contains(err.Error(), ErrCursorACPTurnBusy.Error()) {
		t.Fatalf("overlapping third Execute error = %v, want busy", err)
	}

	select {
	case got := <-second.Result:
		if got.Status != "completed" || got.SessionID != "cursor-session-1" {
			t.Fatalf("second result = %+v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for second result")
	}

	data, err := os.ReadFile(starts)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "x"); got != 1 {
		t.Fatalf("children started = %d, want 1", got)
	}
}

func TestCursorACPBackendRejectsConcurrentTurn(t *testing.T) {
	b := newCursorACPBackend(Config{})
	b.running.Store(true)
	if _, err := b.Execute(context.Background(), "prompt", ExecOptions{}); err == nil || !strings.Contains(err.Error(), ErrCursorACPTurnBusy.Error()) {
		t.Fatalf("concurrent Execute error = %v, want busy", err)
	}
}

func TestCursorACPBackendAuthRequiredFailsClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cursor-agent")
	writeTestExecutable(t, path, []byte(fakeCursorACPAuthRequiredScript()))
	b := newCursorACPBackend(cursorACPTestConfig(path, nil))
	t.Cleanup(b.Close)

	s, err := b.Execute(context.Background(), "hi", ExecOptions{Cwd: dir})
	if err != nil {
		t.Fatalf("Execute returned err=%v, want async result", err)
	}
	select {
	case got := <-s.Result:
		if got.Status != "failed" {
			t.Fatalf("status = %q, want failed", got.Status)
		}
		if !strings.Contains(got.Error, ProviderAuthRequiredMarker) && !strings.Contains(got.Error, "Authentication required") {
			t.Fatalf("error = %q, want auth required marker", got.Error)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
}

func TestCursorACPBackendResumeUsesSessionLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cursor-agent")
	writeTestExecutable(t, path, []byte(fakeCursorACPProcessScript()))
	b := newCursorACPBackend(cursorACPTestConfig(path, map[string]string{
		"CURSOR_ACP_TEST_STARTS": filepath.Join(dir, "starts"),
	}))
	t.Cleanup(b.Close)

	s, err := b.Execute(context.Background(), "resume-me", ExecOptions{
		Cwd: dir, ResumeSessionID: "cursor-session-resumed",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	select {
	case got := <-s.Result:
		if got.Status != "completed" {
			t.Fatalf("result = %+v", got)
		}
		// load returns cursor-session-resumed in fake
		if got.SessionID != "cursor-session-resumed" && got.SessionID != "cursor-session-1" {
			t.Fatalf("sessionId = %q", got.SessionID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
}

func TestCursorACPBackendUnknownSessionFailsAndDisposes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cursor-agent")
	script := `#!/bin/sh
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
    *'"initialize"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1,"authMethods":[{"id":"cursor_login"}]}}\n' "$id" ;;
    *'"authenticate"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id" ;;
    *'"session/new"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"s1"}}\n' "$id" ;;
    *'"session/prompt"'*) printf '{"jsonrpc":"2.0","id":%s,"error":{"code":-32603,"message":"Internal error","data":{"details":"Session s1 not found"}}}\n' "$id" ;;
  esac
done
`
	writeTestExecutable(t, path, []byte(script))
	b := newCursorACPBackend(cursorACPTestConfig(path, nil))
	t.Cleanup(b.Close)

	s, err := b.Execute(context.Background(), "p", ExecOptions{Cwd: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	select {
	case got := <-s.Result:
		if got.Status != "failed" {
			t.Fatalf("status = %q, want failed", got.Status)
		}
		if !strings.Contains(got.Error, "not found") && !strings.Contains(got.Error, "session/prompt") {
			t.Fatalf("error = %q", got.Error)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
	if b.process.Load() != nil {
		t.Fatal("process should be disposed after failed prompt")
	}
}

// TestCursorACPBackendMapsAutoToLiveACPDefault pins Cursor's split model
// vocabulary: the public CLI calls its automatic selection "auto", while the
// live ACP catalog advertises that same choice as modelId "default[]" with the
// display name "Auto". Falling back by omitting set_model is not equivalent —
// Cursor then keeps its configured current model (composer in the live repro).
func TestCursorACPBackendMapsAutoToLiveACPDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cursor-agent")
	requestsPath := filepath.Join(dir, "requests.jsonl")
	script := `#!/bin/sh
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$CURSOR_ACP_TEST_REQUESTS"
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
    *'"initialize"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1,"authMethods":[{"id":"cursor_login"}]}}\n' "$id" ;;
    *'"authenticate"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id" ;;
    *'"session/new"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"s1","models":{"currentModelId":"composer-2.5[fast=true]","availableModels":[{"modelId":"default[]","name":"Auto"},{"modelId":"composer-2.5[fast=true]","name":"composer-2.5"}]}}}\n' "$id" ;;
    *'"session/set_model"'*)
      case "$line" in
        *'"modelId":"default[]"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id" ;;
        *) printf '{"jsonrpc":"2.0","id":%s,"error":{"code":-32602,"message":"Invalid params","data":{"message":"Invalid model value"}}}\n' "$id" ;;
      esac
      ;;
    *'"session/prompt"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{"stopReason":"end_turn"}}\n' "$id" ;;
  esac
done
`
	writeTestExecutable(t, path, []byte(script))
	b := newCursorACPBackend(cursorACPTestConfig(path, map[string]string{
		"CURSOR_ACP_TEST_REQUESTS": requestsPath,
	}))
	t.Cleanup(b.Close)

	s, err := b.Execute(context.Background(), "p", ExecOptions{Cwd: dir, Model: "auto"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	select {
	case got := <-s.Result:
		if got.Status != "completed" {
			t.Fatalf("status = %+v, want completed", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
	requests, err := os.ReadFile(requestsPath)
	if err != nil {
		t.Fatalf("read requests: %v", err)
	}
	if !strings.Contains(string(requests), `"method":"session/set_model"`) ||
		!strings.Contains(string(requests), `"modelId":"default[]"`) {
		t.Fatalf("auto was not mapped to the live ACP default model:\n%s", requests)
	}
	if strings.Contains(string(requests), `"modelId":"auto"`) {
		t.Fatalf("raw CLI alias auto must not be sent to ACP:\n%s", requests)
	}
}

func TestCursorACPBackendFailsWhenConfiguredModelIsMissingFromLiveCatalog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cursor-agent")
	requestsPath := filepath.Join(dir, "requests.jsonl")
	script := `#!/bin/sh
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$CURSOR_ACP_TEST_REQUESTS"
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
    *'"initialize"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1,"authMethods":[{"id":"cursor_login"}]}}\n' "$id" ;;
    *'"authenticate"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id" ;;
    *'"session/new"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"s1","models":{"currentModelId":"composer-2.5[fast=true]","availableModels":[{"modelId":"composer-2.5[fast=true]","name":"composer-2.5"}]}}}\n' "$id" ;;
    *'"session/set_model"'*|*'"session/prompt"'*) printf '{"jsonrpc":"2.0","id":%s,"error":{"code":-32000,"message":"model validation must fail before prompt"}}\n' "$id" ;;
  esac
done
`
	writeTestExecutable(t, path, []byte(script))
	b := newCursorACPBackend(cursorACPTestConfig(path, map[string]string{
		"CURSOR_ACP_TEST_REQUESTS": requestsPath,
	}))
	t.Cleanup(b.Close)

	s, err := b.Execute(context.Background(), "p", ExecOptions{Cwd: dir, Model: "stale-model"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	select {
	case got := <-s.Result:
		if got.Status != "failed" || !strings.Contains(got.Error, `Cursor model "stale-model" is not available`) {
			t.Fatalf("result = %+v, want visible unavailable-model failure", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
	requests, err := os.ReadFile(requestsPath)
	if err != nil {
		t.Fatalf("read requests: %v", err)
	}
	if strings.Contains(string(requests), `"method":"session/set_model"`) || strings.Contains(string(requests), `"method":"session/prompt"`) {
		t.Fatalf("missing configured model must fail before set_model or prompt:\n%s", requests)
	}
}

// TestCursorACPBackendFailsOnInvalidParamsSetModel covers the case where
// the live catalog isn't exposed at all (no `models` field in session/new,
// e.g. an older/different CLI build) so the code falls through to actually
// calling set_model — and the CLI rejects the value with Invalid params
// (-32602), the exact code Frank hit. The turn must fail visibly rather than
// silently running Cursor's current/default model.
func TestCursorACPBackendFailsOnInvalidParamsSetModel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cursor-agent")
	script := `#!/bin/sh
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
    *'"initialize"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1,"authMethods":[{"id":"cursor_login"}]}}\n' "$id" ;;
    *'"authenticate"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id" ;;
    *'"session/new"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"s1"}}\n' "$id" ;;
    *'"session/set_model"'*) printf '{"jsonrpc":"2.0","id":%s,"error":{"code":-32602,"message":"Invalid params","data":{"message":"Invalid model value: auto"}}}\n' "$id" ;;
    *'"session/prompt"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{"stopReason":"end_turn"}}\n' "$id" ;;
  esac
done
`
	writeTestExecutable(t, path, []byte(script))
	b := newCursorACPBackend(cursorACPTestConfig(path, nil))
	t.Cleanup(b.Close)

	s, err := b.Execute(context.Background(), "p", ExecOptions{Cwd: dir, Model: "auto"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	select {
	case got := <-s.Result:
		if got.Status != "failed" || !strings.Contains(got.Error, `Cursor could not apply configured model "auto"`) {
			t.Fatalf("result = %+v, want visible set_model failure", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
	if b.process.Load() != nil {
		t.Fatal("process should be disposed after set_model rejects the configured model")
	}
}

// TestCursorACPBackendStillFailsOnOtherSetModelErrors guards against
// over-widening the tolerance added for the invalid-params case: a set_model
// failure that is neither method-not-found nor invalid-params (e.g. a real
// transport/internal error) must still fail and dispose the session, exactly
// as before this fix.
func TestCursorACPBackendStillFailsOnOtherSetModelErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cursor-agent")
	script := `#!/bin/sh
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
    *'"initialize"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1,"authMethods":[{"id":"cursor_login"}]}}\n' "$id" ;;
    *'"authenticate"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id" ;;
    *'"session/new"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"s1"}}\n' "$id" ;;
    *'"session/set_model"'*) printf '{"jsonrpc":"2.0","id":%s,"error":{"code":-32603,"message":"Internal error"}}\n' "$id" ;;
  esac
done
`
	writeTestExecutable(t, path, []byte(script))
	b := newCursorACPBackend(cursorACPTestConfig(path, nil))
	t.Cleanup(b.Close)

	s, err := b.Execute(context.Background(), "p", ExecOptions{Cwd: dir, Model: "auto"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	select {
	case got := <-s.Result:
		if got.Status != "failed" {
			t.Fatalf("status = %+v, want failed for a genuinely fatal set_model error", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
	if b.process.Load() != nil {
		t.Fatal("process should be disposed after a genuinely fatal set_model error")
	}
}

func TestCursorACPRequireAuthMethod(t *testing.T) {
	if err := cursorACPRequireAuthMethod([]byte(`{"authMethods":[{"id":"cursor_login"}]}`), "cursor_login"); err != nil {
		t.Fatal(err)
	}
	if err := cursorACPRequireAuthMethod([]byte(`{"authMethods":[]}`), "cursor_login"); err == nil {
		t.Fatal("expected missing auth method error")
	}
}

// fakeCursorACPHungTurnScript answers initialize/authenticate/session/new
// normally, then goes silent on session/prompt — the shell's `case` falls
// through with no output and the loop blocks on the next stdin read. This
// simulates a genuinely stuck turn: a real child process, real
// StdoutPipe/StdinPipe, Execute()'s goroutine genuinely blocked reading a
// response that will never arrive — not a stubbed/mocked backend, per the
// design doc's hard requirement that this test exercise the real cmd.Wait()
// contract.
func fakeCursorACPHungTurnScript() string {
	return `#!/bin/sh
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
    *'"initialize"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1,"authMethods":[{"id":"cursor_login"}]}}\n' "$id" ;;
    *'"authenticate"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id" ;;
    *'"session/new"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"cursor-session-1"}}\n' "$id" ;;
    *'"session/prompt"'*)
      # Keep writing so Execute()'s goroutine is genuinely mid-read when
      # ForceKill() interrupts, not idly blocked on an empty pipe — a
      # concurrent Wait() has actual in-flight I/O to race against.
      i=0
      while [ $i -lt 100000 ]; do
        printf '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"cursor-session-1","update":{"kind":"agent_message_chunk","content":{"type":"text","text":"chunk"}}}}\n'
        i=$((i + 1))
      done
      ;;
  esac
done
`
}

// TestCursorACPBackendForceKillInterruptsHungTurn is the concurrency test
// the design doc requires (Parker/Nash, task #62): it uses a real subprocess
// with real StdoutPipe/StdinPipe (not a stub), so it actually exercises
// ForceKill() running on a second goroutine while Execute()'s own goroutine
// is genuinely mid-read against a hung, actively-writing process. It asserts
// the correct behavior: no hang, ForceKill() returns, Execute()'s goroutine
// observes the kill and returns too.
//
// Honesty check on what this does NOT prove: I deliberately reintroduced
// Nash's caught bug (ForceKill calling disposeProcessLocked, which ends in
// cmd.Wait() — a second caller of Wait() while Execute()'s goroutine may
// still be reading, which the Go docs call undefined for StdoutPipe/
// StdinPipe) and ran this test under -race 15+ times. It never failed.
// That's not evidence the bug is safe — Go's os.File already tolerates
// concurrent Read+Close gracefully at the runtime level in the common case,
// so this specific hazard is a documented stdlib *contract* violation, not
// one that reliably manifests as a race-detector-visible or crashing
// failure in a simple reproduction. ForceKill() still follows the
// documented-safe shape (only stdin.Close() + Process.Kill(), no Wait())
// because that's the correct design regardless of whether a test can catch
// the alternative — not because this test demonstrated the alternative
// failing. Don't cite this test as proof the buggy version breaks; cite it
// as proof the correct version doesn't hang or deadlock.
func TestCursorACPBackendForceKillInterruptsHungTurn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cursor-agent")
	writeTestExecutable(t, path, []byte(fakeCursorACPHungTurnScript()))
	b := newCursorACPBackend(cursorACPTestConfig(path, nil))
	t.Cleanup(b.Close)

	s, err := b.Execute(context.Background(), "prompt", ExecOptions{Cwd: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Give the session handshake time to reach session/prompt and start
	// genuinely blocking on a response before we interrupt it.
	time.Sleep(200 * time.Millisecond)

	killErr := make(chan error, 1)
	go func() {
		killErr <- b.ForceKill()
	}()

	select {
	case err := <-killErr:
		if err != nil {
			t.Fatalf("ForceKill: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ForceKill did not return")
	}

	// Execute()'s own goroutine must observe the killed process and return —
	// not hang forever waiting on a response that will now never arrive.
	select {
	case _, ok := <-s.Result:
		_ = ok // either a terminal Result or the channel closing is acceptable
	case <-time.After(3 * time.Second):
		t.Fatal("Execute()'s goroutine did not observe the force-killed process")
	}
}

// fakeCursorACPHungHandshakeScript never responds at all — not even to
// initialize. This simulates a cursor-agent process that started but is
// stuck before completing its auth handshake (e.g. waiting on a login
// flow that will never complete), which is what task #62's real E2E
// verification actually hit: a genuinely stuck agent is far more likely to
// be stuck getting started than stuck mid-conversation.
func fakeCursorACPHungHandshakeScript() string {
	return `#!/bin/sh
while IFS= read -r line; do
  :
done
`
}

// TestCursorACPBackendForceKillDuringHandshakeActuallyKillsNotDeadlock pins
// the real bug found during task #62's live E2E verification (not a
// theoretical one): the previous design had ensureProcess() hold b.mu for
// its entire body, including the blocking initialize/authenticate/
// session/new handshake, while ForceKill() needed that same b.mu just to
// read b.process. If a turn was stuck inside ensureProcess() — the process
// started but never finished its handshake — the two deadlocked:
// ForceKill() could never acquire b.mu, so it never returned, and the
// caller (the daemon's lifecycle operation handler) hung forever with no
// result ever reported. This reproduced live: the restart operation sat at
// status=running indefinitely with the real cursor-agent process never
// actually killed.
//
// The fix (task #62 follow-up) publishes b.process atomically the instant
// cmd.Start() succeeds, before the handshake — so ForceKill() never needs
// any lock a stuck handshake could be holding. The acceptance criterion
// (Parker, explicit): this test must assert the stuck process is actually
// killed and the turn actually terminates — not merely that ForceKill()
// returns a well-formatted error while the underlying agent stays wedged
// forever. An honest error with nothing actually recovered was rejected as
// an interim mitigation (the earlier TryLock approach), not the real fix.
func TestCursorACPBackendForceKillDuringHandshakeActuallyKillsNotDeadlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cursor-agent")
	writeTestExecutable(t, path, []byte(fakeCursorACPHungHandshakeScript()))
	b := newCursorACPBackend(cursorACPTestConfig(path, nil))
	t.Cleanup(b.Close)

	execDone := make(chan struct{})
	var execResult Result
	go func() {
		defer close(execDone)
		// Execute() blocks inside ensureProcess() -> initialize request,
		// which never gets a response — exactly the stuck-handshake case.
		s, err := b.Execute(context.Background(), "prompt", ExecOptions{Cwd: dir})
		if err != nil {
			return
		}
		execResult = <-s.Result
	}()

	// Give Execute() time to actually enter ensureProcess() and start
	// blocking on the initialize handshake before we try to interrupt it.
	time.Sleep(200 * time.Millisecond)

	killErr := make(chan error, 1)
	go func() {
		killErr <- b.ForceKill()
	}()

	select {
	case err := <-killErr:
		if err != nil {
			t.Fatalf("ForceKill during a stuck handshake returned an error: %v — it should actually kill the process, not fail", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ForceKill() deadlocked instead of killing the stuck-handshake process — this is the real bug found in task #62's live verification")
	}

	// The real acceptance criterion: the stuck turn must actually unblock
	// and terminate, not just have ForceKill() return promptly while
	// ensureProcess() stays wedged on the handshake forever.
	select {
	case <-execDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Execute()'s goroutine never returned after ForceKill() during handshake — the process was not actually killed, ensureProcess() is still stuck")
	}
	if execResult.Status != "failed" {
		t.Fatalf("result status = %q, want failed (handshake was force-killed)", execResult.Status)
	}
	// Nash's round-2 review catch: forceKilled must be surfaced even when the
	// kill happens during ensureProcess's own handshake, not just after it —
	// otherwise the daemon can't classify this as a user-initiated restart
	// (reason_code=restarted_by_user) and reports it as a generic crash.
	if !strings.Contains(execResult.Error, AgentForceKilledMarker) {
		t.Fatalf("result error = %q, want it to contain %q", execResult.Error, AgentForceKilledMarker)
	}
}
