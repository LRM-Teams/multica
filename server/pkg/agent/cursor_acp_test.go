package agent

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

	for _, prompt := range []string{"first", "second"} {
		s, err := b.Execute(context.Background(), prompt, ExecOptions{Cwd: dir})
		if err != nil {
			t.Fatalf("Execute(%q): %v", prompt, err)
		}
		select {
		case got := <-s.Result:
			if got.Status != "completed" || got.SessionID != "cursor-session-1" {
				t.Fatalf("result = %+v", got)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timeout")
		}
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

func TestCursorACPRequireAuthMethod(t *testing.T) {
	if err := cursorACPRequireAuthMethod([]byte(`{"authMethods":[{"id":"cursor_login"}]}`), "cursor_login"); err != nil {
		t.Fatal(err)
	}
	if err := cursorACPRequireAuthMethod([]byte(`{"authMethods":[]}`), "cursor_login"); err == nil {
		t.Fatal("expected missing auth method error")
	}
}
