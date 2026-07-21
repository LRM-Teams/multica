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
	if !ok {
		return []cursorDecodedToolEvent{{event: event, reason: "unsupported_payload"}}
	}
	if phase != RuntimeToolEventStarted && phase != RuntimeToolEventCompleted {
		return []cursorDecodedToolEvent{{event: event, reason: "unsupported_subtype"}}
	}
	return []cursorDecodedToolEvent{{event: event}}
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
