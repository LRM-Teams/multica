package agent

import (
	"encoding/json"
	"testing"
	"time"
)

// Prod shape from #103 dig: writeToolCall completed with empty/missing args
// and path only on result.success.path.
func TestEnrichWriteToolCallCompletedInputFromResultPath(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{
		"type":"tool_call",
		"subtype":"completed",
		"call_id":"call-write-empty-args",
		"session_id":"sess",
		"tool_call":{
			"writeToolCall":{
				"result":{"success":{"path":"/tmp/prod-write.txt","linesAdded":1,"message":"Wrote contents"}}
			},
			"hookAdditionalContexts":[],
			"toolCallId":"call-write-empty-args",
			"startedAtMs":"1",
			"completedAtMs":"2"
		}
	}`)
	var evt cursorStreamEvent
	if err := json.Unmarshal(raw, &evt); err != nil {
		t.Fatal(err)
	}
	decoded := decodeCursorToolEvents(&evt, time.Unix(100, 0))
	if len(decoded) != 1 || decoded[0].reason != "" {
		t.Fatalf("decoded=%+v", decoded)
	}
	ev := decoded[0].event
	if ev.Tool != "write_file" {
		t.Fatalf("tool=%q want write_file", ev.Tool)
	}
	if ev.Phase != RuntimeToolEventCompleted {
		t.Fatalf("phase=%q", ev.Phase)
	}
	if path, _ := ev.Input["path"].(string); path != "/tmp/prod-write.txt" {
		t.Fatalf("Input.path=%v want /tmp/prod-write.txt (full Input=%v)", ev.Input["path"], ev.Input)
	}
}

func TestWriteToolCallCompletedBackfillViaTracker(t *testing.T) {
	t.Parallel()
	// started with empty args (prod write often starts empty)
	started := RuntimeToolEvent{
		Schema: RuntimeToolEventSchemaV1, EventID: "e1", Source: cursorToolEventSource,
		ProtocolShape: cursorCurrentToolCallShape, CallID: "c1",
		Phase: RuntimeToolEventStarted, Tool: "write_file",
		Input: map[string]any{}, OccurredAt: time.Unix(1, 0),
	}
	// completed: path only via result enrichment
	completedRaw := json.RawMessage(`{"writeToolCall":{"result":{"success":{"path":"/ws/foo.go"}}},"toolCallId":"c1","startedAtMs":"1","completedAtMs":"2"}`)
	tool, input, result, ok := parseCursorToolCall(completedRaw)
	if !ok || tool != "write_file" {
		t.Fatalf("parse ok=%v tool=%q", ok, tool)
	}
	input, src := enrichCursorToolCallInputFromResult(input, result)
	if src != "result_path" {
		t.Fatalf("enrich src=%q want result_path", src)
	}
	completed := RuntimeToolEvent{
		Schema: RuntimeToolEventSchemaV1, EventID: "e2", Source: cursorToolEventSource,
		ProtocolShape: cursorCurrentToolCallShape, CallID: "c1",
		Phase: RuntimeToolEventCompleted, Tool: "write_file",
		Input: input, OccurredAt: time.Unix(2, 0),
	}

	tr := newRuntimeToolEventTracker(0, 0)
	if _, ok, reason := tr.accept(started); !ok {
		t.Fatalf("started: %s", reason)
	}
	msg, ok, reason := tr.accept(completed)
	if !ok {
		t.Fatalf("completed: %s", reason)
	}
	if msg.Type != MessageToolResult {
		t.Fatalf("type=%q", msg.Type)
	}
	if path, _ := msg.Input["path"].(string); path != "/ws/foo.go" {
		t.Fatalf("tool_result Input.path=%v want /ws/foo.go", msg.Input["path"])
	}
}

func TestEnrichDoesNotOverwriteExistingPath(t *testing.T) {
	t.Parallel()
	in := map[string]any{"path": "/from/args"}
	out, src := enrichCursorToolCallInputFromResult(in, json.RawMessage(`{"success":{"path":"/from/result"}}`))
	if src != "args_path" {
		t.Fatalf("src=%q want args_path", src)
	}
	if out["path"] != "/from/args" {
		t.Fatalf("got %v", out["path"])
	}
}
