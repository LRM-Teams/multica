package agent

import (
	"encoding/json"
	"strings"
	"time"
)

const (
	cursorToolEventSource             = "cursor_native_stream"
	cursorCurrentToolCallShape        = "cursor.tool_call.v1"
	cursorLegacyDirectToolUseShape    = "cursor.tool_use.legacy"
	cursorLegacyDirectToolResultShape = "cursor.tool_result.legacy"
	cursorLegacyAssistantToolUseShape = "cursor.assistant_tool_use.legacy"
)

type cursorDecodedToolEvent struct {
	event  RuntimeToolEvent
	reason string
	// Diag-only (not Activity facts). Set on completed tool_call decode.
	inputEnrich    string // args_path | result_path | result_miss | ""
	resultKeyShape string // key tree of result payload, no values
}

type cursorToolEventDecoder func(*cursorStreamEvent, time.Time) []cursorDecodedToolEvent

// cursorToolEventDecoders is the explicit provider-shape registry. Supporting
// a new Cursor protocol shape requires a named decoder and a raw contract
// fixture instead of adding fallback field guesses to the execution loop.
var cursorToolEventDecoders = map[string]cursorToolEventDecoder{
	"tool_use":    decodeCursorLegacyToolUse,
	"tool_result": decodeCursorLegacyToolResult,
	"tool_call":   decodeCursorCurrentToolCall,
}

func decodeCursorToolEvents(evt *cursorStreamEvent, occurredAt time.Time) []cursorDecodedToolEvent {
	decoder, ok := cursorToolEventDecoders[evt.Type]
	if !ok {
		return nil
	}
	return decoder(evt, occurredAt)
}

func decodeCursorCurrentToolCall(evt *cursorStreamEvent, occurredAt time.Time) []cursorDecodedToolEvent {
	phase := RuntimeToolEventPhase(evt.Subtype)
	tool, input, result, ok := parseCursorToolCall(evt.ToolCall)
	// #103: writeToolCall (and some read) completed frames often omit args
	// and only put the path on result.success.path. Backfill needs Input to
	// carry path, so enrich when args are empty/missing path.
	var enrichSrc string
	input, enrichSrc = enrichCursorToolCallInputFromResult(input, result)
	event := RuntimeToolEvent{
		Schema:        RuntimeToolEventSchemaV1,
		EventID:       cursorToolEventID(evt.SessionID, evt.CallID, phase),
		Source:        cursorToolEventSource,
		ProtocolShape: cursorCurrentToolCallShape,
		SessionID:     strings.TrimSpace(evt.SessionID),
		CallID:        strings.TrimSpace(evt.CallID),
		Phase:         phase,
		Tool:          tool,
		Input:         input,
		Output:        cursorToolCallResultText(result),
		OccurredAt:    occurredAt,
	}
	decoded := cursorDecodedToolEvent{event: event}
	if phase == RuntimeToolEventCompleted {
		decoded.inputEnrich = enrichSrc
		decoded.resultKeyShape = cursorResultKeyShape(result)
	}
	if !ok {
		decoded.reason = "unsupported_payload"
		return []cursorDecodedToolEvent{decoded}
	}
	if phase != RuntimeToolEventStarted && phase != RuntimeToolEventCompleted {
		decoded.reason = "unsupported_subtype"
		return []cursorDecodedToolEvent{decoded}
	}
	return []cursorDecodedToolEvent{decoded}
}

// enrichCursorToolCallInputFromResult fills Input.path from the Cursor
// result payload when args were empty. Observed shapes:
//
//	{"success":{"path":"/abs/file", ...}}
//	{"path":"/abs/file"}
//
// Does not overwrite an existing non-empty path on args.
// Second return is a dig label: args_path | result_path | result_miss.
func enrichCursorToolCallInputFromResult(input map[string]any, result json.RawMessage) (map[string]any, string) {
	if pathFromMap(input) != "" {
		return input, "args_path"
	}
	path := cursorToolCallResultPath(result)
	if path == "" {
		return input, "result_miss"
	}
	out := make(map[string]any, len(input)+1)
	for k, v := range input {
		out[k] = v
	}
	out["path"] = path
	return out, "result_path"
}

// cursorResultKeyShape returns a compact key tree of the result payload
// (types only, no string values) for #103 dig when path enrichment misses.
func cursorResultKeyShape(result json.RawMessage) string {
	if len(result) == 0 || string(result) == "null" {
		return "<empty>"
	}
	var root any
	if err := json.Unmarshal(result, &root); err != nil {
		return "<invalid_json>"
	}
	return cursorKeyShapeValue(root, 0)
}

func cursorKeyShapeValue(v any, depth int) string {
	if depth > 3 || v == nil {
		return "..."
	}
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		// insertion-sort for stable log grepping without importing sort in hot path concerns
		for i := 0; i < len(keys); i++ {
			for j := i + 1; j < len(keys); j++ {
				if keys[j] < keys[i] {
					keys[i], keys[j] = keys[j], keys[i]
				}
			}
		}
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			nested := cursorKeyShapeValue(t[k], depth+1)
			switch t[k].(type) {
			case map[string]any:
				parts = append(parts, k+"={"+nested+"}")
			case []any:
				parts = append(parts, k+":arr")
			case string:
				parts = append(parts, k+":str")
			case float64:
				parts = append(parts, k+":num")
			case bool:
				parts = append(parts, k+":bool")
			case nil:
				parts = append(parts, k+":null")
			default:
				parts = append(parts, k+":"+nested)
			}
		}
		return strings.Join(parts, ",")
	case []any:
		return "arr"
	default:
		return ""
	}
}

func pathFromMap(m map[string]any) string {
	if m == nil {
		return ""
	}
	for _, key := range []string{"path", "file_path", "filepath", "filePath", "file", "filename", "absolute_path", "absolutePath"} {
		if v, ok := m[key].(string); ok {
			if p := strings.TrimSpace(v); p != "" {
				return p
			}
		}
	}
	return ""
}

func cursorToolCallResultPath(result json.RawMessage) string {
	if len(result) == 0 || string(result) == "null" {
		return ""
	}
	var root any
	if err := json.Unmarshal(result, &root); err != nil {
		return ""
	}
	return cursorResultPathValue(root, 0)
}

func cursorResultPathValue(v any, depth int) string {
	if depth > 4 || v == nil {
		return ""
	}
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	if p := pathFromMap(m); p != "" {
		return p
	}
	for _, key := range []string{"success", "result", "data", "value"} {
		if nested, ok := m[key]; ok {
			if p := cursorResultPathValue(nested, depth+1); p != "" {
				return p
			}
		}
	}
	return ""
}

func decodeCursorLegacyToolUse(evt *cursorStreamEvent, occurredAt time.Time) []cursorDecodedToolEvent {
	var input map[string]any
	if len(evt.Parameters) > 0 {
		if err := json.Unmarshal(evt.Parameters, &input); err != nil {
			event := newCursorLegacyToolEvent(evt, cursorLegacyDirectToolUseShape, RuntimeToolEventStarted, occurredAt)
			return []cursorDecodedToolEvent{{event: event, reason: "invalid_input"}}
		}
	}
	event := newCursorLegacyToolEvent(evt, cursorLegacyDirectToolUseShape, RuntimeToolEventStarted, occurredAt)
	event.Tool = strings.TrimSpace(evt.ToolName)
	event.Input = input
	return []cursorDecodedToolEvent{{event: event}}
}

func decodeCursorLegacyToolResult(evt *cursorStreamEvent, occurredAt time.Time) []cursorDecodedToolEvent {
	event := newCursorLegacyToolEvent(evt, cursorLegacyDirectToolResultShape, RuntimeToolEventCompleted, occurredAt)
	event.Output = evt.Output
	return []cursorDecodedToolEvent{{event: event}}
}

func decodeCursorAssistantToolEvents(evt *cursorStreamEvent, occurredAt time.Time) []cursorDecodedToolEvent {
	if len(evt.Message) == 0 {
		return nil
	}
	var message cursorAssistantMessage
	if err := json.Unmarshal(evt.Message, &message); err != nil {
		return []cursorDecodedToolEvent{{
			event: RuntimeToolEvent{
				Schema:        RuntimeToolEventSchemaV1,
				EventID:       cursorToolEventID(evt.SessionID, "", "invalid"),
				Source:        cursorToolEventSource,
				ProtocolShape: cursorLegacyAssistantToolUseShape,
				SessionID:     strings.TrimSpace(evt.SessionID),
				OccurredAt:    occurredAt,
			},
			reason: "invalid_assistant_message",
		}}
	}

	var decoded []cursorDecodedToolEvent
	for _, block := range message.Content {
		if block.Type != "tool_use" {
			continue
		}
		decoded = append(decoded, decodeCursorAssistantToolBlock(evt.SessionID, block, occurredAt))
	}
	return decoded
}

func decodeCursorAssistantToolBlock(sessionID string, block cursorContentBlock, occurredAt time.Time) cursorDecodedToolEvent {
	event := RuntimeToolEvent{
		Schema:        RuntimeToolEventSchemaV1,
		EventID:       cursorToolEventID(sessionID, block.ID, RuntimeToolEventStarted),
		Source:        cursorToolEventSource,
		ProtocolShape: cursorLegacyAssistantToolUseShape,
		SessionID:     strings.TrimSpace(sessionID),
		CallID:        strings.TrimSpace(block.ID),
		Phase:         RuntimeToolEventStarted,
		Tool:          strings.TrimSpace(block.Name),
		OccurredAt:    occurredAt,
	}
	if len(block.Input) > 0 {
		if err := json.Unmarshal(block.Input, &event.Input); err != nil {
			return cursorDecodedToolEvent{event: event, reason: "invalid_input"}
		}
	}
	return cursorDecodedToolEvent{event: event}
}

func newCursorLegacyToolEvent(evt *cursorStreamEvent, shape string, phase RuntimeToolEventPhase, occurredAt time.Time) RuntimeToolEvent {
	return RuntimeToolEvent{
		Schema:        RuntimeToolEventSchemaV1,
		EventID:       cursorToolEventID(evt.SessionID, evt.ToolID, phase),
		Source:        cursorToolEventSource,
		ProtocolShape: shape,
		SessionID:     strings.TrimSpace(evt.SessionID),
		CallID:        strings.TrimSpace(evt.ToolID),
		Phase:         phase,
		OccurredAt:    occurredAt,
	}
}

func cursorToolEventID(sessionID, callID string, phase RuntimeToolEventPhase) string {
	return strings.Join([]string{
		cursorToolEventSource,
		strings.TrimSpace(sessionID),
		strings.TrimSpace(callID),
		string(phase),
	}, ":")
}

type cursorToolEventDiagnostics struct {
	acceptedByShape map[string]int
	droppedByReason map[string]int
}

func newCursorToolEventDiagnostics() *cursorToolEventDiagnostics {
	return &cursorToolEventDiagnostics{
		acceptedByShape: make(map[string]int),
		droppedByReason: make(map[string]int),
	}
}

func (d *cursorToolEventDiagnostics) accepted(shape string) {
	d.acceptedByShape[shape]++
}

func (d *cursorToolEventDiagnostics) dropped(reason string) {
	d.droppedByReason[reason]++
}


// classifyWriteInputEnrich maps decode+tracker outcome to Parker/Barry tags:
// result_path | started_fallback | none (plus args_path when started args already had path).
func classifyWriteInputEnrich(decoded cursorDecodedToolEvent, message Message) string {
	finalPath := pathFromMap(message.Input)
	if finalPath == "" {
		return "none"
	}
	// completed event already carried path after enrich
	if pathFromMap(decoded.event.Input) != "" {
		if decoded.inputEnrich == "result_path" {
			return "result_path"
		}
		if decoded.inputEnrich == "args_path" {
			return "args_path"
		}
		return "result_path"
	}
	// completed Input empty but tracker filled from started
	return "started_fallback"
}
