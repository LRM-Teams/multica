package agent

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func fakeGrokACPProcessScript() string {
	return `#!/bin/sh
printf x >> "$GROK_TEST_STARTS"
if [ -n "$GROK_TEST_ARGS" ]; then
  printf '%s\n' "$@" > "$GROK_TEST_ARGS"
fi
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
    *'"initialize"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1,"agentCapabilities":{"mcpCapabilities":{}}}}\n' "$id" ;;
    *'"session/new"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"grok-session"}}\n' "$id" ;;
    *'"session/prompt"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id" ;;
  esac
done
`
}

func TestGrokACPBackendStartsIsolatedAlwaysApprovedProcess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "grok")
	writeTestExecutable(t, path, []byte(fakeGrokACPProcessScript()))
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	argsPath := filepath.Join(dir, "args")
	b := newGrokACPBackend(Config{ExecutablePath: path, Env: map[string]string{
		"GROK_HOME": dir, "GROK_TEST_STARTS": filepath.Join(dir, "starts"), "GROK_TEST_ARGS": argsPath,
	}})
	t.Cleanup(b.Close)

	s, err := b.Execute(context.Background(), "first", ExecOptions{Cwd: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := <-s.Result; got.Status != "completed" {
		t.Fatalf("result = %+v", got)
	}

	data, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Fields(string(data)), []string{"agent", "--no-leader", "--always-approve", "stdio"}; !slices.Equal(got, want) {
		t.Fatalf("grok argv = %v, want %v", got, want)
	}
}

func TestGrokACPBackendReusesOneChildForCompatibleTurns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "grok")
	writeTestExecutable(t, path, []byte(fakeGrokACPProcessScript()))
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	starts := filepath.Join(dir, "starts")
	b := newGrokACPBackend(Config{ExecutablePath: path, Env: map[string]string{
		"GROK_HOME": dir, "GROK_TEST_STARTS": starts,
	}})
	for _, prompt := range []string{"first", "second"} {
		s, err := b.Execute(context.Background(), prompt, ExecOptions{Cwd: dir})
		if err != nil {
			t.Fatalf("Execute(%q): %v", prompt, err)
		}
		select {
		case got := <-s.Result:
			if got.Status != "completed" || got.SessionID != "grok-session" {
				t.Fatalf("result = %+v", got)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for ACP turn")
		}
	}
	data, err := os.ReadFile(starts)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "x"); got != 1 {
		t.Fatalf("grok children started = %d, want 1", got)
	}
	if p := b.process.Load(); p != nil {
		b.disposeProcess(p)
	}
}

func TestGrokACPBackendRejectsConcurrentTurn(t *testing.T) {
	b := newGrokACPBackend(Config{})
	b.running.Store(true)
	if _, err := b.Execute(context.Background(), "prompt", ExecOptions{}); err == nil || !strings.Contains(err.Error(), ErrGrokACPTurnBusy.Error()) {
		t.Fatalf("concurrent Execute error = %v, want busy error", err)
	}
}

func TestGrokACPBackendFailedToolFrameFailsTurnAndDisposesProcess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "grok")
	script := `#!/bin/sh
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
    *'"initialize"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1,"agentCapabilities":{"mcpCapabilities":{}}}}\n' "$id" ;;
    *'"session/new"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"poisoned-session"}}\n' "$id" ;;
    *'"session/prompt"'*)
      printf '%s\n' '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"poisoned-session","update":{"sessionUpdate":"tool_call_update","toolCallId":"tool-1","status":"pending","kind":"execute","title":"terminal: pwd","rawInput":{"command":"pwd"}}}}'
      printf '%s\n' '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"poisoned-session","update":{"sessionUpdate":"tool_call_update","toolCallId":"tool-1","status":"failed","content":[{"type":"content","content":{"type":"text","text":"Failed to request permission from user: unknown permission option for tool ` + "`run_terminal_command`" + `"}}]},"_meta":{"updateParams":{"toolCallId":"tool-1","status":"Failed"}}}}'
      printf '{"jsonrpc":"2.0","id":%s,"result":{"stopReason":"end_turn"}}\n' "$id"
      ;;
  esac
done
`
	writeTestExecutable(t, path, []byte(script))
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := newGrokACPBackend(Config{ExecutablePath: path, Env: map[string]string{"GROK_HOME": dir}})
	t.Cleanup(b.Close)

	session, err := b.Execute(context.Background(), "run pwd", ExecOptions{Cwd: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	result := <-session.Result
	if result.Status != "failed" {
		t.Fatalf("status = %q, want failed (result=%+v)", result.Status, result)
	}
	const original = "Failed to request permission from user: unknown permission option for tool `run_terminal_command`"
	if result.Error != original {
		t.Fatalf("error = %q, want exact original provider error %q", result.Error, original)
	}
	if result.SessionID != "poisoned-session" {
		t.Fatalf("session id = %q, want poisoned-session", result.SessionID)
	}
	if b.process.Load() != nil {
		t.Fatal("failed tool turn retained poisoned Grok process")
	}
}

// fakeGrokACPHungHandshakeScript never responds to anything, including
// initialize — simulating a grok process that started but is stuck before
// completing its handshake. Mirrors
// fakeCursorACPHungHandshakeScript/TestCursorACPBackendForceKillDuringHandshakeActuallyKillsNotDeadlock:
// grokACPBackend had the identical b.mu-across-the-whole-handshake shape as
// cursorACPBackend before the task #62 follow-up fix (confirmed by Vera's
// cross-check), so it needs the same acceptance test.
func fakeGrokACPHungHandshakeScript() string {
	return `#!/bin/sh
while IFS= read -r line; do
  :
done
`
}

// TestGrokACPBackendForceKillDuringHandshakeActuallyKillsNotDeadlock is
// grok's counterpart to
// TestCursorACPBackendForceKillDuringHandshakeActuallyKillsNotDeadlock. See
// that test's doc comment for the full deadlock background; the shape here
// is identical (ensureProcess held b.mu across the whole handshake,
// ForceKill needed the same b.mu just to read b.process). Asserts the real
// acceptance criterion: ForceKill() during a stuck handshake must actually
// kill the process and let the turn terminate, not just return an error.
func TestGrokACPBackendForceKillDuringHandshakeActuallyKillsNotDeadlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "grok")
	writeTestExecutable(t, path, []byte(fakeGrokACPHungHandshakeScript()))
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := newGrokACPBackend(Config{ExecutablePath: path, Env: map[string]string{"GROK_HOME": dir}})
	t.Cleanup(b.Close)

	execDone := make(chan struct{})
	var execResult Result
	go func() {
		defer close(execDone)
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
		t.Fatal("ForceKill() deadlocked instead of killing the stuck-handshake process")
	}

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

// TestGrokACPBackendLiveTwoTurns is an opt-in provider proof. It is kept out
// of normal CI because it consumes the authenticated Grok account; run it only
// with explicit operator approval via MULTICA_RUN_GROK_ACP_LIVE=1.
func TestGrokACPBackendLiveTwoTurns(t *testing.T) {
	if os.Getenv("MULTICA_RUN_GROK_ACP_LIVE") != "1" {
		t.Skip("set MULTICA_RUN_GROK_ACP_LIVE=1 to run authenticated Grok ACP proof")
	}

	b := newGrokACPBackend(Config{})
	t.Cleanup(b.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	workDir := t.TempDir()

	first, err := b.Execute(ctx, "Reply with exactly: multica-acp-first-ok", ExecOptions{Cwd: workDir})
	if err != nil {
		t.Fatalf("start first ACP turn: %v", err)
	}
	firstResult := <-first.Result
	if firstResult.Status != "completed" {
		t.Fatalf("first ACP turn = %+v", firstResult)
	}
	p := b.process.Load()
	if p == nil || p.cmd.Process == nil {
		t.Fatal("first ACP turn completed without a retained child")
	}
	pid := p.cmd.Process.Pid

	second, err := b.Execute(ctx, "Reply with exactly: multica-acp-second-ok", ExecOptions{Cwd: workDir})
	if err != nil {
		t.Fatalf("start second ACP turn: %v", err)
	}
	secondResult := <-second.Result
	if secondResult.Status != "completed" {
		t.Fatalf("second ACP turn = %+v", secondResult)
	}
	p2 := b.process.Load()
	if p2 == nil || p2.cmd.Process == nil || p2.cmd.Process.Pid != pid {
		t.Fatalf("second ACP turn did not reuse child pid %d", pid)
	}
}
