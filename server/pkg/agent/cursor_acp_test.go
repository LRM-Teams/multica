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
	if b.process != nil {
		t.Fatal("process should be disposed after failed prompt")
	}
}

// TestCursorACPBackendSkipsSetModelWhenNotInLiveCatalog is the regression
// test for the "Invalid model value: auto" bug: a stale/wrong configured
// model must never fail the whole session. Here session/new's live catalog
// doesn't include the configured model at all, so set_model must never even
// be called (the fake CLI fails hard if it sees one) — the session still
// starts successfully with the CLI's own default.
func TestCursorACPBackendSkipsSetModelWhenNotInLiveCatalog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cursor-agent")
	script := `#!/bin/sh
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
    *'"initialize"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1,"authMethods":[{"id":"cursor_login"}]}}\n' "$id" ;;
    *'"authenticate"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id" ;;
    *'"session/new"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"s1","models":{"currentModelId":"composer-2","availableModels":[{"modelId":"composer-2","name":"Composer 2"}]}}}\n' "$id" ;;
    *'"session/set_model"'*) printf '{"jsonrpc":"2.0","id":%s,"error":{"code":-32000,"message":"set_model should never be called for a model absent from the live catalog"}}\n' "$id" ;;
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
		if got.Status != "completed" {
			t.Fatalf("status = %+v, want completed (set_model must be skipped, not fail the session)", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
}

// TestCursorACPBackendDegradesOnInvalidParamsSetModel covers the case where
// the live catalog isn't exposed at all (no `models` field in session/new,
// e.g. an older/different CLI build) so the code falls through to actually
// calling set_model — and the CLI rejects the value with Invalid params
// (-32602), the exact code Frank hit. The session must still succeed rather
// than being disposed.
func TestCursorACPBackendDegradesOnInvalidParamsSetModel(t *testing.T) {
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
		if got.Status != "completed" {
			t.Fatalf("status = %+v, want completed (invalid-params set_model must degrade, not fail the session)", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
	if b.process == nil {
		t.Fatal("process should still be alive/reusable after a degraded set_model")
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
	if b.process != nil {
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
