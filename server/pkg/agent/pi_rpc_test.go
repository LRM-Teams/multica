package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakePiRPCProcessScript() string {
	return `#!/bin/sh
printf x >> "$PI_RPC_TEST_STARTS"
while IFS= read -r line; do
  case "$line" in
    *'"type":"prompt"'*)
      printf '{"id":"multica-turn","type":"response","command":"prompt","success":true}\n'
      printf '{"type":"agent_start"}\n'
      printf '{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"Pi reply"}}\n'
      printf '{"type":"agent_end","messages":[{"role":"assistant","model":"test-pi","usage":{"input":2,"output":3}}]}\n'
      ;;
  esac
done
`
}

func TestPiRPCBackendReusesOneChildForCompatibleTurns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pi")
	writeTestExecutable(t, path, []byte(fakePiRPCProcessScript()))
	starts := filepath.Join(dir, "starts")
	sessionPath := filepath.Join(dir, "session.jsonl")
	b := newPiRPCBackend(Config{ExecutablePath: path, Env: map[string]string{"PI_RPC_TEST_STARTS": starts}})
	t.Cleanup(b.Close)
	for _, prompt := range []string{"first", "second"} {
		session, err := b.Execute(context.Background(), prompt, ExecOptions{Cwd: dir, ResumeSessionID: sessionPath})
		if err != nil {
			t.Fatalf("Execute(%q): %v", prompt, err)
		}
		select {
		case got := <-session.Result:
			if got.Status != "completed" || got.Output != "Pi reply" || got.SessionID != sessionPath {
				t.Fatalf("result = %+v", got)
			}
			if usage := got.Usage["test-pi"]; usage.InputTokens != 2 || usage.OutputTokens != 3 {
				t.Fatalf("usage = %+v", got.Usage)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for Pi RPC turn")
		}
	}
	data, err := os.ReadFile(starts)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "x"); got != 1 {
		t.Fatalf("Pi RPC children started = %d, want 1", got)
	}
}

func TestPiRPCBackendRejectsConcurrentTurn(t *testing.T) {
	b := newPiRPCBackend(Config{})
	b.running.Store(true)
	if _, err := b.Execute(context.Background(), "prompt", ExecOptions{}); err == nil || !strings.Contains(err.Error(), ErrPiRPCTurnBusy.Error()) {
		t.Fatalf("concurrent Execute error = %v, want busy error", err)
	}
}
