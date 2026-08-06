//go:build unix

package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCursorExecuteStopsAfterTerminalResult(t *testing.T) {
	t.Parallel()

	script := `#!/bin/sh
printf '%s\n' '{"type":"system","subtype":"init","session_id":"sess-terminal"}'
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"sess-terminal"}'
sleep 10
`
	result := executeFakeCursor(t, script)

	if result.Status != "completed" {
		t.Fatalf("status = %q, want completed; error=%q", result.Status, result.Error)
	}
	if result.Output != "done" {
		t.Fatalf("output = %q, want done", result.Output)
	}
	if result.SessionID != "sess-terminal" {
		t.Fatalf("session id = %q, want sess-terminal", result.SessionID)
	}
}

func TestCursorExecuteReportsCurrentUsageUnderInitModel(t *testing.T) {
	t.Parallel()

	script := `#!/bin/sh
printf '%s\n' '{"type":"system","subtype":"init","session_id":"sess-usage","model":"composer-2.5-fast"}'
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"sess-usage","usage":{"inputTokens":26640,"outputTokens":40,"cacheReadTokens":467,"cacheWriteTokens":12}}'
`
	result := executeFakeCursor(t, script)

	want := TokenUsage{InputTokens: 26640, OutputTokens: 40, CacheReadTokens: 467, CacheWriteTokens: 12}
	if got := result.Usage["composer-2.5-fast"]; got != want {
		t.Fatalf("usage = %+v, want %+v; all=%+v", got, want, result.Usage)
	}
}

func TestCursorExecuteStopsAfterTerminalErrorResult(t *testing.T) {
	t.Parallel()

	script := `#!/bin/sh
printf '%s\n' '{"type":"system","subtype":"init","session_id":"sess-terminal-error"}'
printf '%s\n' '{"type":"result","subtype":"error","is_error":true,"result":"failed hard","session_id":"sess-terminal-error"}'
sleep 10
`
	result := executeFakeCursor(t, script)

	if result.Status != "failed" {
		t.Fatalf("status = %q, want failed; error=%q", result.Status, result.Error)
	}
	if result.Error != "failed hard" {
		t.Fatalf("error = %q, want failed hard", result.Error)
	}
	if result.Output != "failed hard" {
		t.Fatalf("output = %q, want failed hard", result.Output)
	}
	if result.SessionID != "sess-terminal-error" {
		t.Fatalf("session id = %q, want sess-terminal-error", result.SessionID)
	}
}

func TestCursorExecuteCurrentToolCallStreamShape(t *testing.T) {
	t.Parallel()

	fixturePath, err := filepath.Abs(filepath.Join("testdata", "cursor-tool-calls-2026-07-17.jsonl"))
	if err != nil {
		t.Fatalf("fixture path: %v", err)
	}
	fixture, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	assertCursorLiveToolCallFixture(t, fixture)

	script := fmt.Sprintf("#!/bin/sh\ncat %q\n", fixturePath)
	messages, result := executeFakeCursorWithMessages(t, script)

	if result.Status != "completed" || result.Output != "done" {
		t.Fatalf("result = %+v", result)
	}
	if result.SessionID != "session-live-sanitized" {
		t.Fatalf("session id = %q, want session-live-sanitized", result.SessionID)
	}
	if len(messages) != 19 {
		t.Fatalf("message count = %d, want status + 18 live tool events", len(messages))
	}

	wantStarted := map[string]int{"shell": 3, "read_file": 3, "edit_file": 3}
	wantCompleted := map[string]int{"shell": 3, "read_file": 3, "edit_file": 3}
	gotStarted := make(map[string]int)
	gotCompleted := make(map[string]int)
	for _, message := range messages[1:] {
		switch message.Type {
		case MessageToolUse:
			gotStarted[message.Tool]++
		case MessageToolResult:
			gotCompleted[message.Tool]++
		default:
			t.Fatalf("unexpected message in live fixture: %+v", message)
		}
	}
	for tool, want := range wantStarted {
		if gotStarted[tool] != want || gotCompleted[tool] != wantCompleted[tool] {
			t.Fatalf("tool %q counts: started=%d completed=%d, want %d/%d", tool, gotStarted[tool], gotCompleted[tool], want, wantCompleted[tool])
		}
	}
}

func assertCursorLiveToolCallFixture(t *testing.T, fixture []byte) {
	t.Helper()

	tracker := newRuntimeToolEventTracker(time.Hour, 32)
	scanner := bufio.NewScanner(bytes.NewReader(fixture))
	accepted := 0
	for scanner.Scan() {
		var event cursorStreamEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("decode fixture line: %v", err)
		}
		if event.Type != "tool_call" {
			continue
		}
		decoded := decodeCursorToolEvents(&event, time.Unix(int64(accepted+1), 0))
		if len(decoded) != 1 || decoded[0].reason != "" {
			t.Fatalf("decode call %q = %+v, want one accepted event", event.CallID, decoded)
		}
		if _, ok, reason := tracker.accept(decoded[0].event); !ok {
			t.Fatalf("tracker rejected call %q subtype %q: %s", event.CallID, event.Subtype, reason)
		}
		accepted++
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan fixture: %v", err)
	}
	if accepted != 18 {
		t.Fatalf("accepted events = %d, want 18", accepted)
	}
	if missing, expired := tracker.finish(); missing != 0 || expired != 0 {
		t.Fatalf("tracker finish: missing=%d expired=%d, want 0/0", missing, expired)
	}
}

func TestCursorExecuteCurrentToolCallLifecycleRejectsInvalidPairs(t *testing.T) {
	t.Parallel()

	script := `#!/bin/sh
printf '%s\n' '{"type":"system","subtype":"init","session_id":"sess-tool-lifecycle"}'
printf '%s\n' '{"type":"tool_call","subtype":"completed","call_id":"orphan","tool_call":{"shellToolCall":{"args":{"command":"false"},"result":{"success":{"exitCode":1}}}}}'
printf '%s\n' '{"type":"tool_call","subtype":"started","call_id":"call-1","tool_call":{"shellToolCall":{"args":{"command":"pwd"}}}}'
printf '%s\n' '{"type":"tool_call","subtype":"started","call_id":"call-1","tool_call":{"readToolCall":{"args":{"path":"secret.txt"}}}}'
printf '%s\n' '{"type":"tool_call","subtype":"completed","call_id":"call-1","tool_call":{"shellToolCall":{"args":{"command":"pwd"},"result":{"success":{"stdout":"/tmp\\n","exitCode":0}}}}}'
printf '%s\n' '{"type":"tool_call","subtype":"completed","call_id":"call-1","tool_call":{"shellToolCall":{"args":{"command":"pwd"},"result":{"success":{"stdout":"duplicate"}}}}}'
printf '%s\n' '{"type":"tool_call","subtype":"started","call_id":"call-2","tool_call":{"writeToolCall":{"args":{"path":"unfinished.txt"}}}}'
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"sess-tool-lifecycle"}'
`
	messages, result := executeFakeCursorWithMessages(t, script)

	if result.Status != "completed" {
		t.Fatalf("result = %+v", result)
	}
	if len(messages) != 4 {
		t.Fatalf("messages = %+v, want status + one paired call + one real unmatched start", messages)
	}
	if messages[1].Type != MessageToolUse || messages[1].CallID != "call-1" || messages[1].Tool != "shell" {
		t.Fatalf("first accepted tool event = %+v", messages[1])
	}
	if messages[2].Type != MessageToolResult || messages[2].CallID != "call-1" || messages[2].Tool != "shell" {
		t.Fatalf("paired tool result = %+v", messages[2])
	}
	if messages[3].Type != MessageToolUse || messages[3].CallID != "call-2" || messages[3].Tool != "write_file" {
		t.Fatalf("real unmatched started event = %+v", messages[3])
	}
}

func executeFakeCursor(t *testing.T, script string) Result {
	t.Helper()
	_, result := executeFakeCursorWithMessages(t, script)
	return result
}

func executeFakeCursorWithMessages(t *testing.T, script string) ([]Message, Result) {
	t.Helper()

	fakePath := filepath.Join(t.TempDir(), "cursor-agent")
	writeTestExecutable(t, fakePath, []byte(script))

	backend, err := New("cursor", Config{ExecutablePath: fakePath, Logger: slog.Default()})
	if err != nil {
		t.Fatalf("New(cursor): %v", err)
	}
	session, err := backend.Execute(t.Context(), "hello", ExecOptions{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var messages []Message
	for message := range session.Messages {
		messages = append(messages, message)
	}
	result := <-session.Result
	if result.Status == "timeout" {
		t.Fatalf("cursor backend timed out instead of stopping after terminal result; error=%q", result.Error)
	}
	return messages, result
}
