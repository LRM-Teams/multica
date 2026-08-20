package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type blockingClaudeNoticeWriter struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *blockingClaudeNoticeWriter) Write(data []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	return len(data), nil
}

func (w *blockingClaudeNoticeWriter) Close() error { return nil }

func fakeClaudeResidentScript() string {
	return `#!/bin/sh
	turn=0
	while IFS= read -r line; do
	  printf '%s\n' "$line" >> "$CLAUDE_RESIDENT_TEST_INPUT"
	  turn=$((turn + 1))
	  if [ "$turn" -eq 1 ]; then
	    printf '{"type":"system","subtype":"init","session_id":"claude-session-1"}\n'
	    printf '{"type":"assistant","session_id":"claude-session-1","message":{"role":"assistant","model":"claude-test","content":[{"type":"tool_use","id":"tool-1","name":"Bash","input":{"command":"true"}}]}}\n'
	    printf '{"type":"user","session_id":"claude-session-1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-1","content":"ok"}]}}\n'
	  elif [ "$turn" -eq 2 ]; then
	    printf '{"type":"assistant","session_id":"claude-session-1","message":{"role":"assistant","model":"claude-test","content":[{"type":"text","text":"noticed"}]}}\n'
	    printf '{"type":"result","session_id":"claude-session-1","result":"noticed","is_error":false}\n'
	  else
	    printf '{"type":"assistant","session_id":"claude-session-1","message":{"role":"assistant","model":"claude-test","content":[{"type":"text","text":"handoff"}]}}\n'
	    printf '{"type":"result","session_id":"claude-session-1","result":"handoff","is_error":false}\n'
	  fi
	done
	`
}

func TestClaudeStreamJSONResidentGatesNoticeAndReusesProcessForIdleHandoff(t *testing.T) {
	dir := t.TempDir()
	executable := filepath.Join(dir, "claude")
	writeTestExecutable(t, executable, []byte(fakeClaudeResidentScript()))
	inputPath := filepath.Join(dir, "input.jsonl")
	backend := newClaudeStreamJSONBackend(Config{ExecutablePath: executable, Env: map[string]string{
		"CLAUDE_RESIDENT_TEST_INPUT": inputPath,
	}})
	t.Cleanup(backend.Close)

	if err := backend.AcceptPendingNotice(context.Background(), testResidentPendingNotice()); err == nil {
		t.Fatal("Notice succeeded without an active turn")
	}
	session, err := backend.Execute(context.Background(), "initial concrete body", ExecOptions{Cwd: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	foundBoundary := false
	deadline := time.After(2 * time.Second)
	for !foundBoundary {
		select {
		case message := <-session.Messages:
			foundBoundary = message.Type == MessageToolResult && message.CallID == "tool-1"
		case <-deadline:
			t.Fatal("timed out waiting for Claude tool-result boundary")
		}
	}
	if err := backend.AcceptPendingNotice(context.Background(), testResidentPendingNotice()); err != nil {
		t.Fatalf("AcceptPendingNotice at tool boundary: %v", err)
	}
	select {
	case result := <-session.Result:
		if result.Status != "completed" || result.Output != "noticed" || result.SessionID != "claude-session-1" {
			t.Fatalf("first result = %+v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Claude result")
	}

	acceptance, err := backend.AcceptMessageBatch(context.Background(), []ResidentMessage{{
		ID: "message-8", Target: "channel:one", Seq: 8, Content: "idle concrete body",
		PartsJSON: json.RawMessage(`[{"type":"text","text":"idle concrete body"}]`),
	}})
	if err != nil {
		t.Fatalf("AcceptMessageBatch: %v", err)
	}
	select {
	case err := <-acceptance.Done:
		if err != nil {
			t.Fatalf("idle handoff completion: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for idle handoff completion")
	}
	var sawHandoff bool
	for message := range acceptance.Messages {
		sawHandoff = sawHandoff || message.Type == MessageText && message.Content == "handoff"
	}
	if !sawHandoff {
		t.Fatal("Claude idle handoff omitted resident lifecycle events")
	}

	raw, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatalf("read Claude inputs: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 3 {
		t.Fatalf("Claude input count = %d, want one initial, one Notice, one idle handoff\n%s", len(lines), raw)
	}
	if !strings.Contains(lines[1], "Content-free Inbox Notice") || strings.Contains(lines[1], "idle concrete body") {
		t.Fatalf("Claude Notice input is not content-free: %s", lines[1])
	}
	if !strings.Contains(lines[2], "idle concrete body") {
		t.Fatalf("Claude idle input missing concrete body: %s", lines[2])
	}
}

func TestNewClaudeStreamJSONBackendImplementsCanonicalResidentInterfaces(t *testing.T) {
	backend := NewClaudeStreamJSONBackend(Config{})
	if _, ok := backend.(ResidentMessageInput); !ok {
		t.Fatal("Claude stream-json backend must implement idle Message input")
	}
	if _, ok := backend.(ResidentPendingNoticeInput); !ok {
		t.Fatal("Claude stream-json backend must implement Pending Notice input")
	}
	if _, ok := backend.(ResidentRuntimeForceKillable); !ok {
		t.Fatal("Claude stream-json backend must implement force kill")
	}
}

func TestClaudePendingNoticeWriteFencesTerminalResult(t *testing.T) {
	writer := &blockingClaudeNoticeWriter{entered: make(chan struct{}), release: make(chan struct{})}
	turn := &claudeStreamJSONTurn{
		started: time.Now(), completed: make(chan struct{}), resCh: make(chan Result, 1), usage: make(map[string]TokenUsage),
	}
	process := &claudeStreamJSONProcess{
		stdin: writer, turn: turn, sessionID: "session-1", noticeReady: true,
		outstandingTool: make(map[string]struct{}), readerDone: make(chan struct{}),
	}
	backend := newClaudeStreamJSONBackend(Config{})
	backend.process.Store(process)
	backend.running.Store(true)

	noticeDone := make(chan error, 1)
	go func() { noticeDone <- backend.AcceptPendingNotice(context.Background(), testResidentPendingNotice()) }()
	select {
	case <-writer.entered:
	case <-time.After(time.Second):
		t.Fatal("Notice did not reach the native write")
	}

	resultDone := make(chan struct{})
	go func() {
		backend.handleProcessMessage(process, &claudeBackend{}, claudeSDKMessage{
			Type: "result", SessionID: "session-1", ResultText: "done",
		})
		close(resultDone)
	}()
	select {
	case <-resultDone:
		t.Fatal("terminal result crossed the in-flight Notice write fence")
	case <-time.After(30 * time.Millisecond):
	}

	close(writer.release)
	if err := <-noticeDone; err != nil {
		t.Fatalf("AcceptPendingNotice: %v", err)
	}
	select {
	case <-resultDone:
	case <-time.After(time.Second):
		t.Fatal("terminal result did not resume after Notice receipt")
	}
	if backend.running.Load() {
		t.Fatal("terminal result did not release canonical turn admission")
	}
}
