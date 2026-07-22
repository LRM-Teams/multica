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
	    *'"type":"compact"'*)
	      printf '{"id":"multica-compact","type":"response","command":"compact","success":true,"data":{"summary":"compacted summary","tokensBefore":5000,"tokensAfter":1200}}\n'
	      ;;
	    *'"type":"set_auto_compaction"'*)
	      printf '{"id":"multica-autocompact","type":"response","command":"set_auto_compaction","success":true}\n'
	      ;;
	    *'"type":"get_session_stats"'*)
	      printf '{"id":"multica-stats","type":"response","command":"get_session_stats","success":true,"data":{"tokens":{"input":349000,"output":10000,"cacheRead":2600000,"total":2959000},"cost":3.348,"contextUsage":{"tokens":272000,"contextWindow":607000,"percent":44.8}}}\n'
	      ;;
	    *'"type":"get_state"'*)
	      printf '{"id":"multica-state","type":"response","command":"get_state","success":true,"data":{"autoCompactionEnabled":true}}\n'
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
			if got.RuntimeStats == nil || got.RuntimeStats.ContextPercent == nil || *got.RuntimeStats.ContextPercent != 44.8 || got.RuntimeStats.AutoCompactionEnabled == nil || !*got.RuntimeStats.AutoCompactionEnabled {
				t.Fatalf("runtime stats = %+v", got.RuntimeStats)
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

func TestPiRPCBackendCompact(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pi")
	writeTestExecutable(t, path, []byte(fakePiRPCProcessScript()))
	starts := filepath.Join(dir, "starts")
	sessionPath := filepath.Join(dir, "session.jsonl")
	b := newPiRPCBackend(Config{ExecutablePath: path, Env: map[string]string{"PI_RPC_TEST_STARTS": starts}})
	t.Cleanup(b.Close)

	// Start with one prompt turn to establish the process.
	session, err := b.Execute(context.Background(), "hello", ExecOptions{Cwd: dir, ResumeSessionID: sessionPath})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	<-session.Result

	// Compact between turns.
	result, err := b.Compact(context.Background(), "compact after segment")
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if result.TokensBefore != 5000 || result.TokensAfter != 1200 {
		t.Fatalf("Compact result = %+v", result)
	}
	if result.Summary != "compacted summary" {
		t.Fatalf("Compact summary = %q", result.Summary)
	}

	// A second turn after compaction still works.
	session2, err := b.Execute(context.Background(), "second", ExecOptions{Cwd: dir, ResumeSessionID: sessionPath})
	if err != nil {
		t.Fatalf("Execute after compact: %v", err)
	}
	got := <-session2.Result
	if got.Status != "completed" {
		t.Fatalf("second turn status = %q", got.Status)
	}
}

func TestPiRPCBackendSetAutoCompaction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pi")
	writeTestExecutable(t, path, []byte(fakePiRPCProcessScript()))
	sessionPath := filepath.Join(dir, "session.jsonl")
	b := newPiRPCBackend(Config{ExecutablePath: path})
	t.Cleanup(b.Close)

	session, err := b.Execute(context.Background(), "hello", ExecOptions{Cwd: dir, ResumeSessionID: sessionPath})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	<-session.Result

	if err := b.SetAutoCompaction(context.Background(), false); err != nil {
		t.Fatalf("SetAutoCompaction(false): %v", err)
	}
}

func TestPiRPCBackendRuntimeStats(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pi")
	writeTestExecutable(t, path, []byte(fakePiRPCProcessScript()))
	sessionPath := filepath.Join(dir, "session.jsonl")
	b := newPiRPCBackend(Config{ExecutablePath: path})
	t.Cleanup(b.Close)

	session, err := b.Execute(context.Background(), "hello", ExecOptions{Cwd: dir, ResumeSessionID: sessionPath})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	<-session.Result

	stats, err := b.RuntimeStats(context.Background())
	if err != nil {
		t.Fatalf("RuntimeStats: %v", err)
	}
	if stats == nil || stats.ContextPercent == nil || *stats.ContextPercent != 44.8 {
		t.Fatalf("RuntimeStats = %+v", stats)
	}
}
