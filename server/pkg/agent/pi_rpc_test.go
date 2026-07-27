package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func fakePiRPCProcessScript() string {
	return `#!/bin/sh
	printf x >> "$PI_RPC_TEST_STARTS"
	turn=0
	while IFS= read -r line; do
	  case "$line" in
	    *'"type":"prompt"'*)
	      turn=$((turn + 1))
	      printf '{"id":"multica-turn","type":"response","command":"prompt","success":true}\n'
	      printf '{"type":"agent_start"}\n'
	      printf '{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"Pi reply"}}\n'
	      if [ "$turn" -eq 2 ] && [ -n "$PI_RPC_TEST_SECOND_STARTED" ]; then
	        : > "$PI_RPC_TEST_SECOND_STARTED"
	        while [ ! -f "$PI_RPC_TEST_RELEASE_SECOND" ]; do sleep 0.01; done
	      fi
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
	secondStarted := filepath.Join(dir, "second-started")
	releaseSecond := filepath.Join(dir, "release-second")
	b := newPiRPCBackend(Config{ExecutablePath: path, Env: map[string]string{
		"PI_RPC_TEST_STARTS":         starts,
		"PI_RPC_TEST_SECOND_STARTED": secondStarted,
		"PI_RPC_TEST_RELEASE_SECOND": releaseSecond,
	}})
	t.Cleanup(b.Close)

	published := make(chan struct{})
	releasePublisher := make(chan struct{})
	var firstPublish sync.Once
	b.afterResultPublishForTest = func() {
		firstPublish.Do(func() {
			close(published)
			<-releasePublisher
		})
	}

	first, err := b.Execute(context.Background(), "first", ExecOptions{Cwd: dir, ResumeSessionID: sessionPath})
	if err != nil {
		t.Fatalf("Execute(%q): %v", "first", err)
	}
	waitPiRPCResult(t, first, sessionPath)
	<-published
	if b.running.Load() {
		close(releasePublisher)
		t.Fatal("terminal result published before Pi RPC turn admission was released")
	}

	second, err := b.Execute(context.Background(), "second", ExecOptions{Cwd: dir, ResumeSessionID: sessionPath})
	if err != nil {
		t.Fatalf("Execute(%q) immediately after terminal result: %v", "second", err)
	}
	waitForPiRPCTestPath(t, secondStarted)

	// Let turn 1's publisher return while turn 2 owns admission. Its deferred
	// fallback must be inert after the explicit pre-publication release;
	// otherwise it clears turn 2's flag and admits an overlapping third turn.
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
	if _, err := b.Execute(context.Background(), "third", ExecOptions{Cwd: dir, ResumeSessionID: sessionPath}); err == nil || !strings.Contains(err.Error(), ErrPiRPCTurnBusy.Error()) {
		t.Fatalf("overlapping third Execute error = %v, want busy error", err)
	}

	if err := os.WriteFile(releaseSecond, []byte("release"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitPiRPCResult(t, second, sessionPath)

	data, err := os.ReadFile(starts)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "x"); got != 1 {
		t.Fatalf("Pi RPC children started = %d, want 1", got)
	}
}

func waitForPiRPCTestPath(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitPiRPCResult(t *testing.T, session *Session, wantSessionID string) {
	t.Helper()
	select {
	case got := <-session.Result:
		if got.Status != "completed" || got.Output != "Pi reply" || got.SessionID != wantSessionID {
			t.Fatalf("result = %+v", got)
		}
		if usage := got.Usage["test-pi"]; usage.InputTokens != 2 || usage.OutputTokens != 3 {
			t.Fatalf("usage = %+v", got.Usage)
		}
		if got.RuntimeStats == nil || got.RuntimeStats.ContextPercent == nil || *got.RuntimeStats.ContextPercent != 44.8 || got.RuntimeStats.AutoCompactionEnabled == nil || !*got.RuntimeStats.AutoCompactionEnabled {
			t.Fatalf("runtime stats = %+v", got.RuntimeStats)
		}
		return
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Pi RPC turn")
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
