package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeGrokACPProcessScript() string {
	return `#!/bin/sh
printf x >> "$GROK_TEST_STARTS"
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
	if b.process != nil {
		b.disposeProcess(b.process)
	}
}

func TestGrokACPBackendRejectsConcurrentTurn(t *testing.T) {
	b := newGrokACPBackend(Config{})
	b.running.Store(true)
	if _, err := b.Execute(context.Background(), "prompt", ExecOptions{}); err == nil || !strings.Contains(err.Error(), ErrGrokACPTurnBusy.Error()) {
		t.Fatalf("concurrent Execute error = %v, want busy error", err)
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
	if b.process == nil || b.process.cmd.Process == nil {
		t.Fatal("first ACP turn completed without a retained child")
	}
	pid := b.process.cmd.Process.Pid

	second, err := b.Execute(ctx, "Reply with exactly: multica-acp-second-ok", ExecOptions{Cwd: workDir})
	if err != nil {
		t.Fatalf("start second ACP turn: %v", err)
	}
	secondResult := <-second.Result
	if secondResult.Status != "completed" {
		t.Fatalf("second ACP turn = %+v", secondResult)
	}
	if b.process == nil || b.process.cmd.Process == nil || b.process.cmd.Process.Pid != pid {
		t.Fatalf("second ACP turn did not reuse child pid %d", pid)
	}
}
