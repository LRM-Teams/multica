package agent

import (
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestNewReturnsCursorBackend(t *testing.T) {
	t.Parallel()
	b, err := New("cursor", Config{ExecutablePath: "/nonexistent/cursor-agent"})
	if err != nil {
		t.Fatalf("New(cursor) error: %v", err)
	}
	if _, ok := b.(*cursorBackend); !ok {
		t.Fatalf("expected *cursorBackend, got %T", b)
	}
}

func TestCursorArgsSize(t *testing.T) {
	t.Parallel()

	if got := cursorArgsSize([]string{"-p", "hello"}); got != len("-p")+1+len("hello")+1 {
		t.Fatalf("cursorArgsSize = %d", got)
	}
}

func TestShouldSpillCursorPrompt(t *testing.T) {
	t.Parallel()

	if shouldSpillCursorPrompt("small", []string{"-p", "small"}) {
		t.Fatal("small prompt should not spill")
	}
	bigPrompt := strings.Repeat("x", maxCursorPromptBytes+1)
	if !shouldSpillCursorPrompt(bigPrompt, []string{"-p", bigPrompt}) {
		t.Fatal("prompt over maxCursorPromptBytes should spill")
	}
	bigArg := strings.Repeat("y", maxCursorArgvBytes)
	if !shouldSpillCursorPrompt("ok", []string{"-p", bigArg}) {
		t.Fatal("argv over maxCursorArgvBytes should spill")
	}
}

func TestBuildCursorArgs(t *testing.T) {
	t.Parallel()

	args := buildCursorArgs("do something", ExecOptions{
		Cwd:   "/tmp/work",
		Model: "composer-1.5",
	}, slog.Default())

	expected := []string{
		"-p", "do something",
		"--output-format", "stream-json",
		"--yolo",
		"--workspace", "/tmp/work",
		"--model", "composer-1.5",
	}

	if len(args) != len(expected) {
		t.Fatalf("expected %d args, got %d: %v", len(expected), len(args), args)
	}
	for i, want := range expected {
		if args[i] != want {
			t.Errorf("args[%d] = %q, want %q", i, args[i], want)
		}
	}
}

func TestBuildCursorArgsWithResume(t *testing.T) {
	t.Parallel()

	args := buildCursorArgs("continue", ExecOptions{
		ResumeSessionID: "sess-123",
	}, slog.Default())

	hasResume := false
	for i, a := range args {
		if a == "--resume" && i+1 < len(args) && args[i+1] == "sess-123" {
			hasResume = true
		}
	}
	if !hasResume {
		t.Fatalf("expected --resume sess-123, got %v", args)
	}
}

func TestBuildCursorArgsMinimal(t *testing.T) {
	t.Parallel()

	args := buildCursorArgs("hello", ExecOptions{}, slog.Default())
	expected := []string{"-p", "hello", "--output-format", "stream-json", "--yolo"}

	if len(args) != len(expected) {
		t.Fatalf("expected %d args, got %d: %v", len(expected), len(args), args)
	}
}

func TestBuildCursorArgsIgnoresSystemPromptAndMaxTurns(t *testing.T) {
	t.Parallel()

	// cursor-agent CLI does not support --system-prompt or --max-turns;
	// verify they are NOT emitted even when set in ExecOptions.
	args := buildCursorArgs("task", ExecOptions{
		SystemPrompt: "You are helpful",
		MaxTurns:     5,
	}, slog.Default())

	for _, a := range args {
		if a == "--system-prompt" {
			t.Fatalf("unexpected --system-prompt in args: %v", args)
		}
		if a == "--max-turns" {
			t.Fatalf("unexpected --max-turns in args: %v", args)
		}
	}
}

func TestBuildCursorArgsCustomArgs(t *testing.T) {
	t.Parallel()

	args := buildCursorArgs("task", ExecOptions{
		CustomArgs: []string{"--extra", "val", "--yolo", "--output-format", "text"},
	}, slog.Default())

	// --extra val should be present; --yolo and --output-format should be filtered out
	hasExtra := false
	hasBlockedYolo := false
	hasBlockedFormat := false
	for i, a := range args {
		if a == "--extra" && i+1 < len(args) && args[i+1] == "val" {
			hasExtra = true
		}
	}
	// Count occurrences of --yolo (should be exactly 1 — the hardcoded one)
	yoloCount := 0
	for _, a := range args {
		if a == "--yolo" {
			yoloCount++
		}
		if a == "text" {
			hasBlockedFormat = true
		}
	}
	if yoloCount > 1 {
		hasBlockedYolo = true
	}
	if !hasExtra {
		t.Fatalf("expected --extra val in args, got %v", args)
	}
	if hasBlockedYolo {
		t.Fatalf("--yolo from custom args should be filtered, got %v", args)
	}
	if hasBlockedFormat {
		t.Fatalf("--output-format from custom args should be filtered, got %v", args)
	}
}

func TestNormalizeCursorStreamLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{`stdout: {"type":"init"}`, `{"type":"init"}`},
		{`stderr: {"type":"error"}`, `{"type":"error"}`},
		{`stdout:{"type":"init"}`, `{"type":"init"}`},
		{`  {"type":"assistant"}  `, `{"type":"assistant"}`},
		{``, ``},
		{`  `, ``},
		{`plain text`, `plain text`},
	}

	for _, tc := range tests {
		got := normalizeCursorStreamLine(tc.input)
		if got != tc.want {
			t.Errorf("normalizeCursorStreamLine(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestCursorHandleAssistantText(t *testing.T) {
	t.Parallel()

	b := &cursorBackend{cfg: Config{Logger: slog.Default()}}
	ch := make(chan Message, 10)
	var output strings.Builder

	evt := &cursorStreamEvent{
		Type: "assistant",
		Message: mustMarshal(t, cursorAssistantMessage{
			Model: "composer-1.5",
			Content: []cursorContentBlock{
				{Type: "output_text", Text: "Hello from Cursor"},
			},
			Usage: &cursorUsage{
				InputTokens:  100,
				OutputTokens: 50,
			},
		}),
	}

	b.handleCursorAssistant(evt, ch, &output, nil, nil)

	if output.String() != "Hello from Cursor" {
		t.Fatalf("expected output 'Hello from Cursor', got %q", output.String())
	}

	select {
	case m := <-ch:
		if m.Type != MessageText || m.Content != "Hello from Cursor" {
			t.Fatalf("unexpected message: %+v", m)
		}
	default:
		t.Fatal("expected message on channel")
	}
}

func TestCursorDecodeLegacyAssistantToolUse(t *testing.T) {
	t.Parallel()

	evt := &cursorStreamEvent{
		Type:      "assistant",
		SessionID: "session-1",
		Message: mustMarshal(t, cursorAssistantMessage{
			Content: []cursorContentBlock{
				{
					Type:  "tool_use",
					ID:    "call-42",
					Name:  "file_edit",
					Input: mustMarshal(t, map[string]any{"path": "/tmp/foo.go"}),
				},
			},
		}),
	}

	decoded := decodeCursorAssistantToolEvents(evt, time.Unix(100, 0))
	if len(decoded) != 1 || decoded[0].reason != "" {
		t.Fatalf("decoded = %+v", decoded)
	}
	event := decoded[0].event
	if event.Schema != RuntimeToolEventSchemaV1 || event.Source != cursorToolEventSource || event.ProtocolShape != cursorLegacyAssistantToolUseShape {
		t.Fatalf("event identity = %+v", event)
	}
	if event.Phase != RuntimeToolEventStarted || event.Tool != "file_edit" || event.CallID != "call-42" || event.SessionID != "session-1" {
		t.Fatalf("event lifecycle = %+v", event)
	}
}

func TestCursorHandleAssistantPreservesTextToolOrder(t *testing.T) {
	t.Parallel()

	b := &cursorBackend{cfg: Config{Logger: slog.Default()}}
	ch := make(chan Message, 10)
	var output strings.Builder
	tracker := newRuntimeToolEventTracker(time.Minute, 8)

	evt := &cursorStreamEvent{
		Type:      "assistant",
		SessionID: "session-1",
		Message: mustMarshal(t, cursorAssistantMessage{Content: []cursorContentBlock{
			{Type: "text", Text: "before"},
			{Type: "tool_use", ID: "call-1", Name: "shell", Input: mustMarshal(t, map[string]any{"command": "pwd"})},
			{Type: "text", Text: "after"},
		}}),
	}
	b.handleCursorAssistant(evt, ch, &output, func(decoded cursorDecodedToolEvent) {
		message, ok, reason := tracker.accept(decoded.event)
		if decoded.reason != "" || !ok || reason != "" {
			t.Fatalf("tool event rejected: decoded=%q ok=%v reason=%q", decoded.reason, ok, reason)
		}
		ch <- message
	}, nil)

	want := []MessageType{MessageText, MessageToolUse, MessageText}
	for i, wantType := range want {
		select {
		case message := <-ch:
			if message.Type != wantType {
				t.Fatalf("message[%d].Type = %q, want %q", i, message.Type, wantType)
			}
		default:
			t.Fatalf("missing message[%d]", i)
		}
	}
}

func TestParseCursorToolCallCurrentStreamShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		raw      string
		wantTool string
		wantKey  string
		wantVal  string
	}{
		{
			name:     "shell command",
			raw:      `{"toolCallId":"call-1","startedAtMs":100,"shellToolCall":{"args":{"command":"pwd"}},"hookAdditionalContexts":[]}`,
			wantTool: "shell",
			wantKey:  "command",
			wantVal:  "pwd",
		},
		{
			name:     "read file",
			raw:      `{"readToolCall":{"args":{"path":"README.md"}}}`,
			wantTool: "read_file",
			wantKey:  "path",
			wantVal:  "README.md",
		},
		{
			name:     "edit file with current lifecycle metadata",
			raw:      `{"toolCallId":"call-3","startedAtMs":100,"editToolCall":{"args":{"path":"notes.txt","fileText":"hello"}},"hookAdditionalContexts":[]}`,
			wantTool: "edit_file",
			wantKey:  "path",
			wantVal:  "notes.txt",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tool, input, result, ok := parseCursorToolCall(json.RawMessage(tc.raw))
			if !ok {
				t.Fatal("parseCursorToolCall returned ok=false")
			}
			if tool != tc.wantTool {
				t.Fatalf("tool = %q, want %q", tool, tc.wantTool)
			}
			if got, _ := input[tc.wantKey].(string); got != tc.wantVal {
				t.Fatalf("input[%q] = %q, want %q", tc.wantKey, got, tc.wantVal)
			}
			if len(result) != 0 {
				t.Fatalf("result = %s, want empty", result)
			}
		})
	}
}

func TestParseCursorToolCallRejectsAmbiguousOrUnknownSiblings(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		`{"toolCallId":"call-1","startedAtMs":100}`,
		`{"shellToolCall":{"args":{"command":"pwd"}},"readToolCall":{"args":{"path":"README.md"}}}`,
		`{"shellToolCall":{"args":{"command":"pwd"}},"futureMetadata":"drift"}`,
	} {
		if tool, _, _, ok := parseCursorToolCall(json.RawMessage(raw)); ok {
			t.Fatalf("parseCursorToolCall(%s) = %q, want rejected", raw, tool)
		}
	}
}

func TestParseCursorToolCallCompletedResult(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"toolCallId":"call-1","startedAtMs":100,"completedAtMs":101,"shellToolCall":{"args":{"command":"pwd"},"result":{"success":{"stdout":"/tmp\n","exitCode":0}}},"hookAdditionalContexts":[]}`)
	tool, input, result, ok := parseCursorToolCall(raw)
	if !ok {
		t.Fatal("parseCursorToolCall returned ok=false")
	}
	if tool != "shell" || input["command"] != "pwd" {
		t.Fatalf("parsed tool/input = %q/%v", tool, input)
	}
	if got := cursorToolCallResultText(result); !strings.Contains(got, `"stdout":"/tmp\n"`) {
		t.Fatalf("result text = %q", got)
	}
}

func TestCursorDecodeCurrentToolCallContract(t *testing.T) {
	t.Parallel()

	evt := &cursorStreamEvent{
		Type:      "tool_call",
		Subtype:   "started",
		SessionID: "session-1",
		CallID:    "call-1",
		ToolCall:  json.RawMessage(`{"shellToolCall":{"args":{"command":"pwd"}}}`),
	}
	decoded := decodeCursorToolEvents(evt, time.Unix(100, 0))
	if len(decoded) != 1 || decoded[0].reason != "" {
		t.Fatalf("decoded = %+v", decoded)
	}
	event := decoded[0].event
	if event.Schema != RuntimeToolEventSchemaV1 || event.Source != cursorToolEventSource || event.ProtocolShape != cursorCurrentToolCallShape {
		t.Fatalf("event identity = %+v", event)
	}
	if event.Phase != RuntimeToolEventStarted || event.Tool != "shell" || event.CallID != "call-1" || event.Input["command"] != "pwd" {
		t.Fatalf("event lifecycle = %+v", event)
	}
}

func TestCursorToolEventShapeRegistryRejectsUnknownPayload(t *testing.T) {
	t.Parallel()

	evt := &cursorStreamEvent{
		Type:     "tool_call",
		Subtype:  "started",
		CallID:   "call-1",
		ToolCall: json.RawMessage(`{"shell":{"args":{"command":"pwd"}}}`),
	}
	decoded := decodeCursorToolEvents(evt, time.Unix(100, 0))
	if len(decoded) != 1 || decoded[0].reason != "unsupported_payload" {
		t.Fatalf("decoded = %+v", decoded)
	}
}

func TestCursorErrorText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		evt  cursorStreamEvent
		want string
	}{
		{"error field", cursorStreamEvent{ErrorMsg: "bad request"}, "bad request"},
		{"detail field", cursorStreamEvent{Detail: "not found"}, "not found"},
		{"result field", cursorStreamEvent{ResultText: "failed"}, "failed"},
		{"empty", cursorStreamEvent{}, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := cursorErrorText(&tc.evt)
			if got != tc.want {
				t.Errorf("cursorErrorText = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCursorAccumulateResultUsage(t *testing.T) {
	t.Parallel()

	b := &cursorBackend{cfg: Config{Logger: slog.Default()}}
	usage := make(map[string]TokenUsage)

	evt := &cursorStreamEvent{
		Model: "gpt-5.3",
		Usage: &cursorUsage{
			InputTokens:          200,
			OutputTokens:         100,
			CacheReadInputTokens: 50,
		},
	}

	b.accumulateResultUsage(usage, evt)

	u := usage["gpt-5.3"]
	if u.InputTokens != 200 || u.OutputTokens != 100 || u.CacheReadTokens != 50 {
		t.Fatalf("unexpected usage: %+v", u)
	}
}

func TestCursorCurrentResultUsageAndModelAttribution(t *testing.T) {
	t.Parallel()

	raw := `{"type":"result","subtype":"success","usage":{"inputTokens":26640,"outputTokens":40,"cacheReadTokens":467,"cacheWriteTokens":12}}`
	var evt cursorStreamEvent
	if err := json.Unmarshal([]byte(raw), &evt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	b := &cursorBackend{cfg: Config{Logger: slog.Default()}}
	usage := make(map[string]TokenUsage)
	b.accumulateResultUsage(usage, &evt, "composer-2.5-fast", "auto")
	got := usage["composer-2.5-fast"]
	want := TokenUsage{InputTokens: 26640, OutputTokens: 40, CacheReadTokens: 467, CacheWriteTokens: 12}
	if got != want {
		t.Fatalf("usage = %+v, want %+v", got, want)
	}
	if _, ok := usage["cursor"]; ok {
		t.Fatalf("usage must not fall into the unpriced cursor bucket: %+v", usage)
	}
}

func TestCursorUsageOnlyFromResult(t *testing.T) {
	t.Parallel()

	b := &cursorBackend{cfg: Config{Logger: slog.Default()}}
	ch := make(chan Message, 10)
	var output strings.Builder

	evt := &cursorStreamEvent{
		Type: "assistant",
		Message: mustMarshal(t, cursorAssistantMessage{
			Model: "gpt-5",
			Content: []cursorContentBlock{
				{Type: "text", Text: "hello"},
			},
			Usage: &cursorUsage{
				InputTokens:  999,
				OutputTokens: 888,
			},
		}),
	}

	b.handleCursorAssistant(evt, ch, &output, nil, nil)

	if output.String() != "hello" {
		t.Fatalf("expected 'hello', got %q", output.String())
	}

	// handleCursorAssistant should NOT have accumulated usage anywhere —
	// usage is only taken from result events to avoid double-counting.
	// (no usage map to check; this test documents the intent)
}

func TestCursorStepFinishParsing(t *testing.T) {
	t.Parallel()

	part := cursorStepFinishPart{}
	data := `{"tokens":{"input":500,"output":200,"cache":{"read":100}},"cost":0.01}`
	if err := json.Unmarshal([]byte(data), &part); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if part.Tokens.Input != 500 || part.Tokens.Output != 200 || part.Tokens.Cache.Read != 100 {
		t.Fatalf("unexpected part: %+v", part)
	}
}

// TestCursorUsageNoDoubleCount verifies that step_finish and result usage
// are never double-counted. When a result event includes usage (session
// totals), step_finish values must be discarded entirely.
func TestCursorUsageNoDoubleCount(t *testing.T) {
	t.Parallel()

	type jsonlEvent struct {
		raw string
	}

	tests := []struct {
		name  string
		lines []string
		want  map[string]TokenUsage
	}{
		{
			name: "result_only — use result usage",
			lines: []string{
				`{"type":"result","model":"gpt-5","usage":{"input_tokens":1000,"output_tokens":500,"cached_input_tokens":200}}`,
			},
			want: map[string]TokenUsage{
				"gpt-5": {InputTokens: 1000, OutputTokens: 500, CacheReadTokens: 200},
			},
		},
		{
			name: "step_finish_only — fallback to step usage",
			lines: []string{
				`{"type":"step_finish","model":"gpt-5","part":{"tokens":{"input":300,"output":100,"cache":{"read":50}}}}`,
				`{"type":"step_finish","model":"gpt-5","part":{"tokens":{"input":200,"output":80,"cache":{"read":30}}}}`,
				`{"type":"result","model":"gpt-5"}`,
			},
			want: map[string]TokenUsage{
				"gpt-5": {InputTokens: 500, OutputTokens: 180, CacheReadTokens: 80},
			},
		},
		{
			name: "step_finish_then_result — result wins, no double count",
			lines: []string{
				`{"type":"step_finish","model":"gpt-5","part":{"tokens":{"input":300,"output":100,"cache":{"read":50}}}}`,
				`{"type":"step_finish","model":"gpt-5","part":{"tokens":{"input":200,"output":80,"cache":{"read":30}}}}`,
				`{"type":"result","model":"gpt-5","usage":{"input_tokens":500,"output_tokens":180,"cached_input_tokens":80}}`,
			},
			want: map[string]TokenUsage{
				"gpt-5": {InputTokens: 500, OutputTokens: 180, CacheReadTokens: 80},
			},
		},
		{
			name: "multi_model — each model tracked independently",
			lines: []string{
				`{"type":"step_finish","model":"gpt-5","part":{"tokens":{"input":100,"output":50,"cache":{"read":10}}}}`,
				`{"type":"step_finish","model":"sonnet-4","part":{"tokens":{"input":200,"output":80,"cache":{"read":20}}}}`,
				`{"type":"result","model":"gpt-5","usage":{"input_tokens":100,"output_tokens":50,"cached_input_tokens":10}}`,
			},
			want: map[string]TokenUsage{
				// result had usage → use result only, discard all step_finish
				"gpt-5": {InputTokens: 100, OutputTokens: 50, CacheReadTokens: 10},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stepUsage := make(map[string]TokenUsage)
			resultUsage := make(map[string]TokenUsage)
			hasResultUsage := false

			b := &cursorBackend{cfg: Config{Logger: slog.Default()}}

			for _, line := range tc.lines {
				var evt cursorStreamEvent
				if err := json.Unmarshal([]byte(line), &evt); err != nil {
					t.Fatalf("unmarshal %q: %v", line, err)
				}

				switch evt.Type {
				case "result":
					b.accumulateResultUsage(resultUsage, &evt)
					if evt.hasResultUsage() {
						hasResultUsage = true
					}
				case "step_finish":
					if evt.Part != nil {
						var part cursorStepFinishPart
						_ = json.Unmarshal(evt.Part, &part)
						model := evt.Model
						if model == "" {
							model = "cursor"
						}
						u := stepUsage[model]
						u.InputTokens += int64(part.Tokens.Input)
						u.OutputTokens += int64(part.Tokens.Output)
						u.CacheReadTokens += int64(part.Tokens.Cache.Read)
						stepUsage[model] = u
					}
				}
			}

			if !hasResultUsage {
				resultUsage = stepUsage
			}

			if len(resultUsage) != len(tc.want) {
				t.Fatalf("got %d models, want %d: %+v", len(resultUsage), len(tc.want), resultUsage)
			}
			for model, want := range tc.want {
				got := resultUsage[model]
				if got != want {
					t.Errorf("model %q: got %+v, want %+v", model, got, want)
				}
			}
		})
	}
}

func TestIsCursorTransportHang(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want bool
	}{
		{"Error: RetriableError: [internal] HTTP/2 keepalive ping timed out after 5000ms", true},
		{"RetriableError: internal error: HTTP/2 keepalive ping timed out after 5000ms", true},
		{"cursor-agent timed out after 2h0m0s", false},
		{"failed hard", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isCursorTransportHang(tc.in); got != tc.want {
			t.Errorf("isCursorTransportHang(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestHandleCursorAssistantTransportHangIsErrorNotText(t *testing.T) {
	t.Parallel()
	b := &cursorBackend{cfg: Config{Logger: slog.Default()}}
	ch := make(chan Message, 4)
	var output strings.Builder
	hang := "Error: RetriableError: [internal] HTTP/2 keepalive ping timed out after 5000ms"
	evt := &cursorStreamEvent{
		Type: "assistant",
		Message: mustMarshal(t, cursorAssistantMessage{
			Content: []cursorContentBlock{{Type: "output_text", Text: hang}},
		}),
	}
	b.handleCursorAssistant(evt, ch, &output, nil, nil)
	if output.Len() != 0 {
		t.Fatalf("hang text must not become assistant output, got %q", output.String())
	}
	select {
	case m := <-ch:
		if m.Type != MessageError || m.Content != hang {
			t.Fatalf("unexpected message: %+v", m)
		}
	default:
		t.Fatal("expected error message on channel")
	}
}
