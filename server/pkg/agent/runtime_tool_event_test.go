package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRuntimeToolEventJSONContract(t *testing.T) {
	t.Parallel()

	event := RuntimeToolEvent{
		Schema:        RuntimeToolEventSchemaV1,
		EventID:       "cursor:session-1:call-1:started",
		Source:        "cursor_native_stream",
		ProtocolShape: "cursor.tool_call.v1",
		SessionID:     "session-1",
		CallID:        "call-1",
		Phase:         RuntimeToolEventStarted,
		Tool:          "shell",
		Input:         map[string]any{"command": "pwd"},
		OccurredAt:    time.Unix(100, 0).UTC(),
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, field := range []string{`"schema":"runtime-tool-event.v1"`, `"event_id":`, `"protocol_shape":`, `"call_id":`, `"phase":"started"`, `"occurred_at":`} {
		if !strings.Contains(string(raw), field) {
			t.Fatalf("JSON %s missing %s", raw, field)
		}
	}
}

func TestRuntimeToolEventTrackerLifecycle(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0)
	tracker := newRuntimeToolEventTracker(time.Minute, 8)
	started := RuntimeToolEvent{
		Schema:        RuntimeToolEventSchemaV1,
		EventID:       "event-1-started",
		Source:        "test",
		ProtocolShape: "test.v1",
		CallID:        "call-1",
		Phase:         RuntimeToolEventStarted,
		Tool:          "bash",
		Input:         map[string]any{"command": "pwd"},
		OccurredAt:    now,
	}
	message, ok, reason := tracker.accept(started)
	if !ok || reason != "" || message.Type != MessageToolUse || message.Tool != "bash" || message.CallID != "call-1" {
		t.Fatalf("started = (%+v, %v, %q)", message, ok, reason)
	}

	completed := started
	completed.EventID = "event-1-completed"
	completed.Phase = RuntimeToolEventCompleted
	completed.Output = "/tmp"
	completed.OccurredAt = now.Add(time.Second)
	message, ok, reason = tracker.accept(completed)
	if !ok || reason != "" || message.Type != MessageToolResult || message.Tool != "bash" || message.Output != "/tmp" {
		t.Fatalf("completed = (%+v, %v, %q)", message, ok, reason)
	}
	if command, _ := message.Input["command"].(string); command != "pwd" {
		t.Fatalf("completed Input = %#v, want command=pwd (LRM-689 backfill carrier)", message.Input)
	}

	if missing, expired := tracker.finish(); missing != 0 || expired != 0 {
		t.Fatalf("finish = (%d, %d), want (0, 0)", missing, expired)
	}
}

func TestRuntimeToolEventTrackerCompletedCarriesInputWhenStartedEmpty(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0)
	tracker := newRuntimeToolEventTracker(time.Minute, 8)
	started := RuntimeToolEvent{
		Schema:        RuntimeToolEventSchemaV1,
		EventID:       "event-empty-started",
		Source:        "test",
		ProtocolShape: "test.v1",
		CallID:        "call-empty",
		Phase:         RuntimeToolEventStarted,
		Tool:          "shell",
		OccurredAt:    now,
	}
	message, ok, reason := tracker.accept(started)
	if !ok || reason != "" || message.Type != MessageToolUse || len(message.Input) != 0 {
		t.Fatalf("started = (%+v, %v, %q)", message, ok, reason)
	}

	completed := RuntimeToolEvent{
		Schema:        RuntimeToolEventSchemaV1,
		EventID:       "event-empty-completed",
		Source:        "test",
		ProtocolShape: "test.v1",
		CallID:        "call-empty",
		Phase:         RuntimeToolEventCompleted,
		Tool:          "shell",
		Input:         map[string]any{"command": "ls -la"},
		Output:        "ok",
		OccurredAt:    now.Add(time.Second),
	}
	message, ok, reason = tracker.accept(completed)
	if !ok || reason != "" || message.Type != MessageToolResult {
		t.Fatalf("completed = (%+v, %v, %q)", message, ok, reason)
	}
	if command, _ := message.Input["command"].(string); command != "ls -la" {
		t.Fatalf("completed Input = %#v, want command backfill carrier", message.Input)
	}
}

func TestRuntimeToolEventTrackerRejectsInvalidOrdering(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0)
	base := RuntimeToolEvent{
		Schema:        RuntimeToolEventSchemaV1,
		EventID:       "event-1",
		Source:        "test",
		ProtocolShape: "test.v1",
		CallID:        "call-1",
		Tool:          "bash",
		OccurredAt:    now,
	}

	tests := []struct {
		name string
		run  func(*runtimeToolEventTracker) string
		want string
	}{
		{
			name: "orphan completed",
			run: func(tracker *runtimeToolEventTracker) string {
				event := base
				event.Phase = RuntimeToolEventCompleted
				_, _, reason := tracker.accept(event)
				return reason
			},
			want: "orphan_completed",
		},
		{
			name: "duplicate started",
			run: func(tracker *runtimeToolEventTracker) string {
				event := base
				event.Phase = RuntimeToolEventStarted
				_, _, _ = tracker.accept(event)
				_, _, reason := tracker.accept(event)
				return reason
			},
			want: "duplicate_started",
		},
		{
			name: "tool mismatch",
			run: func(tracker *runtimeToolEventTracker) string {
				started := base
				started.Phase = RuntimeToolEventStarted
				_, _, _ = tracker.accept(started)
				completed := base
				completed.Phase = RuntimeToolEventCompleted
				completed.Tool = "read_file"
				_, _, reason := tracker.accept(completed)
				return reason
			},
			want: "tool_mismatch",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.run(newRuntimeToolEventTracker(time.Minute, 8)); got != tc.want {
				t.Fatalf("reason = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRuntimeToolEventTrackerBoundsTurnState(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0)
	tracker := newRuntimeToolEventTracker(time.Second, 1)
	first := RuntimeToolEvent{
		Schema:        RuntimeToolEventSchemaV1,
		EventID:       "event-1",
		Source:        "test",
		ProtocolShape: "test.v1",
		CallID:        "call-1",
		Phase:         RuntimeToolEventStarted,
		Tool:          "bash",
		OccurredAt:    now,
	}
	if _, ok, reason := tracker.accept(first); !ok || reason != "" {
		t.Fatalf("first event rejected: ok=%v reason=%q", ok, reason)
	}

	second := first
	second.EventID = "event-2"
	second.CallID = "call-2"
	if _, ok, reason := tracker.accept(second); ok || reason != "state_capacity_exceeded" {
		t.Fatalf("capacity event = ok=%v reason=%q", ok, reason)
	}

	second.OccurredAt = now.Add(2 * time.Second)
	if _, ok, reason := tracker.accept(second); !ok || reason != "" {
		t.Fatalf("event after TTL prune rejected: ok=%v reason=%q", ok, reason)
	}
	if missing, expired := tracker.finish(); missing != 1 || expired != 1 {
		t.Fatalf("finish = (%d, %d), want (1, 1)", missing, expired)
	}
}
