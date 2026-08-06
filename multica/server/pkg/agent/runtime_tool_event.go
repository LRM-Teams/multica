package agent

import "time"

// RuntimeToolEventSchemaV1 is the provider-neutral tool lifecycle contract.
// Provider adapters and future hook sources normalize their native payloads to
// this schema before the daemon-facing Message stream is produced.
const RuntimeToolEventSchemaV1 = "runtime-tool-event.v1"

type RuntimeToolEventPhase string

const (
	RuntimeToolEventStarted   RuntimeToolEventPhase = "started"
	RuntimeToolEventCompleted RuntimeToolEventPhase = "completed"
)

// RuntimeToolEvent is an independent runtime fact. It must come from a native
// provider event or a verified runtime hook; final assistant prose is never a
// source for this contract.
type RuntimeToolEvent struct {
	Schema        string                `json:"schema"`
	EventID       string                `json:"event_id"`
	Source        string                `json:"source"`
	ProtocolShape string                `json:"protocol_shape"`
	SessionID     string                `json:"session_id,omitempty"`
	CallID        string                `json:"call_id"`
	Phase         RuntimeToolEventPhase `json:"phase"`
	Tool          string                `json:"tool,omitempty"`
	Input         map[string]any        `json:"input,omitempty"`
	Output        string                `json:"output,omitempty"`
	OccurredAt    time.Time             `json:"occurred_at"`
}

type runtimeToolEventState struct {
	tool      string
	completed bool
	updatedAt time.Time
}

// runtimeToolEventTracker provides a bounded, turn-scoped call_id state
// machine. A tracker belongs to exactly one Backend.Execute invocation, so
// events can never pair across turns. TTL and maxCalls bound long-running turns.
type runtimeToolEventTracker struct {
	states            map[string]runtimeToolEventState
	ttl               time.Duration
	maxCalls          int
	expiredIncomplete int
}

func newRuntimeToolEventTracker(ttl time.Duration, maxCalls int) *runtimeToolEventTracker {
	return &runtimeToolEventTracker{
		states:   make(map[string]runtimeToolEventState),
		ttl:      ttl,
		maxCalls: maxCalls,
	}
}

// accept converts one validated lifecycle fact to the existing daemon-facing
// Message contract. Invalid, duplicate, out-of-order, or mismatched facts are
// rejected with a stable diagnostic reason.
func (t *runtimeToolEventTracker) accept(event RuntimeToolEvent) (Message, bool, string) {
	now := event.OccurredAt
	if now.IsZero() {
		now = time.Now()
	}
	t.prune(now)

	if event.Schema != RuntimeToolEventSchemaV1 {
		return Message{}, false, "unsupported_schema"
	}
	if event.EventID == "" {
		return Message{}, false, "missing_event_id"
	}
	if event.Source == "" || event.ProtocolShape == "" {
		return Message{}, false, "missing_source_identity"
	}
	if event.CallID == "" {
		return Message{}, false, "missing_call_id"
	}

	state, seen := t.states[event.CallID]
	switch event.Phase {
	case RuntimeToolEventStarted:
		if event.Tool == "" {
			return Message{}, false, "missing_tool"
		}
		if seen {
			return Message{}, false, "duplicate_started"
		}
		if t.maxCalls > 0 && len(t.states) >= t.maxCalls {
			return Message{}, false, "state_capacity_exceeded"
		}
		t.states[event.CallID] = runtimeToolEventState{tool: event.Tool, updatedAt: now}
		return Message{
			Type:   MessageToolUse,
			Tool:   event.Tool,
			CallID: event.CallID,
			Input:  event.Input,
		}, true, ""

	case RuntimeToolEventCompleted:
		if !seen {
			return Message{}, false, "orphan_completed"
		}
		if state.completed {
			return Message{}, false, "duplicate_completed"
		}
		if event.Tool != "" && event.Tool != state.tool {
			return Message{}, false, "tool_mismatch"
		}
		state.completed = true
		state.updatedAt = now
		t.states[event.CallID] = state
		// Carry completed Input so the UI can backfill a started-empty
		// tool_use row (Cursor often emits args only on completed).
		return Message{
			Type:   MessageToolResult,
			Tool:   state.tool,
			CallID: event.CallID,
			Input:  event.Input,
			Output: event.Output,
		}, true, ""

	default:
		return Message{}, false, "unsupported_phase"
	}
}

func (t *runtimeToolEventTracker) prune(now time.Time) {
	if t.ttl <= 0 {
		return
	}
	for callID, state := range t.states {
		if now.Sub(state.updatedAt) < t.ttl {
			continue
		}
		if !state.completed {
			t.expiredIncomplete++
		}
		delete(t.states, callID)
	}
}

func (t *runtimeToolEventTracker) finish() (missingCompletion, expiredIncomplete int) {
	for _, state := range t.states {
		if !state.completed {
			missingCompletion++
		}
	}
	return missingCompletion, t.expiredIncomplete
}
