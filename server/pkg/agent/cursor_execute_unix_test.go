//go:build unix

package agent

import (
	"log/slog"
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

	script := `#!/bin/sh
printf '%s\n' '{"type":"system","subtype":"init","session_id":"sess-tool-call"}'
printf '%s\n' '{"type":"tool_call","subtype":"started","call_id":"call-1","tool_call":{"shellToolCall":{"args":{"command":"pwd"}}},"session_id":"sess-tool-call"}'
printf '%s\n' '{"type":"tool_call","subtype":"completed","call_id":"call-1","tool_call":{"shellToolCall":{"args":{"command":"pwd"},"result":{"success":{"stdout":"/tmp\\n","exitCode":0}}}},"session_id":"sess-tool-call"}'
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"sess-tool-call"}'
`
	messages, result := executeFakeCursorWithMessages(t, script)

	if result.Status != "completed" || result.Output != "done" {
		t.Fatalf("result = %+v", result)
	}
	if len(messages) != 3 {
		t.Fatalf("messages = %+v, want status + tool use + tool result", messages)
	}
	if got := messages[1]; got.Type != MessageToolUse || got.Tool != "shell" || got.CallID != "call-1" || got.Input["command"] != "pwd" {
		t.Fatalf("tool use = %+v", got)
	}
	if got := messages[2]; got.Type != MessageToolResult || got.Tool != "shell" || got.CallID != "call-1" || got.Output == "" {
		t.Fatalf("tool result = %+v", got)
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
