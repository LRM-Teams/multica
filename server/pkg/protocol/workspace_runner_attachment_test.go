package protocol

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestWorkspaceRunnerAgentAttachmentEventNames(t *testing.T) {
	got := []string{
		EventAgentAttach, EventAgentAttached, EventAgentDetach, EventAgentDetached,
		EventAgentAttachmentReplayReq, EventAgentAttachmentReplayEnd, EventAgentAttachmentReplayAck,
	}
	want := []string{
		"agent:attach", "agent:attached", "agent:detach", "agent:detached",
		"agent:attachment.replay_request", "agent:attachment.replay_end", "agent:attachment.replay_ack",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Attachment event names = %v, want %v", got, want)
	}
}

func TestWorkspaceRunnerAgentAttachmentReplayPayloadRoundTrips(t *testing.T) {
	values := []interface{ Validate() error }{
		WorkspaceRunnerAttachmentReplayRequest{RuntimeCursors: map[string]int64{"runtime-1": 3}},
		WorkspaceRunnerAttachmentReplayEnd{RuntimeCursors: map[string]int64{"runtime-1": 3}},
		WorkspaceRunnerAttachmentReplayAck{RuntimeCursors: map[string]int64{"runtime-1": 3}},
	}
	for _, value := range values {
		if err := value.Validate(); err != nil {
			t.Fatalf("%T Validate(): %v", value, err)
		}
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		if want := []string{"runtimeCursors"}; !sameJSONFields(fields, want) {
			t.Fatalf("%T wire fields = %v, want %v; JSON=%s", value, mapKeys(fields), want, raw)
		}
	}
}

func TestWorkspaceRunnerAgentAttachmentReplayValidation(t *testing.T) {
	invalid := []map[string]int64{
		{"": 1},
		{"runtime-1": -1},
	}
	for _, cursors := range invalid {
		values := []interface{ Validate() error }{
			WorkspaceRunnerAttachmentReplayRequest{RuntimeCursors: cursors},
			WorkspaceRunnerAttachmentReplayEnd{RuntimeCursors: cursors},
			WorkspaceRunnerAttachmentReplayAck{RuntimeCursors: cursors},
		}
		for _, value := range values {
			if err := value.Validate(); err == nil {
				t.Fatalf("%T accepted invalid replay cursors %v", value, cursors)
			}
		}
	}
}

func TestWorkspaceRunnerAgentAttachmentPayloadRoundTrips(t *testing.T) {
	values := []struct {
		name  string
		value interface{ Validate() error }
		new   func() interface{ Validate() error }
	}{
		{
			name: "attach",
			value: WorkspaceRunnerAgentAttachPayload{
				AgentID: "agent-1", RuntimeID: "runtime-1", AttachmentGeneration: 2, LifecycleSeq: 3,
			},
			new: func() interface{ Validate() error } { return &WorkspaceRunnerAgentAttachPayload{} },
		},
		{
			name: "attached",
			value: WorkspaceRunnerAgentAttachedPayload{
				AgentID: "agent-1", RuntimeID: "runtime-1", AttachmentGeneration: 2, LifecycleSeq: 3,
			},
			new: func() interface{ Validate() error } { return &WorkspaceRunnerAgentAttachedPayload{} },
		},
		{
			name: "detach",
			value: WorkspaceRunnerAgentDetachPayload{
				AgentID: "agent-1", RuntimeID: "runtime-1", AttachmentGeneration: 2, LifecycleSeq: 3,
			},
			new: func() interface{ Validate() error } { return &WorkspaceRunnerAgentDetachPayload{} },
		},
		{
			name: "detached",
			value: WorkspaceRunnerAgentDetachedPayload{
				AgentID: "agent-1", RuntimeID: "runtime-1", AttachmentGeneration: 2, LifecycleSeq: 3,
			},
			new: func() interface{ Validate() error } { return &WorkspaceRunnerAgentDetachedPayload{} },
		},
	}
	for _, test := range values {
		t.Run(test.name, func(t *testing.T) {
			if err := test.value.Validate(); err != nil {
				t.Fatalf("Validate(): %v", err)
			}
			raw, err := json.Marshal(test.value)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			if want := []string{"agentId", "runtimeId", "attachmentGeneration", "lifecycleSeq"}; !sameJSONFields(fields, want) {
				t.Fatalf("wire fields = %v, want %v; JSON=%s", mapKeys(fields), want, raw)
			}
			decoded := test.new()
			if err := json.Unmarshal(raw, decoded); err != nil {
				t.Fatal(err)
			}
			if err := decoded.Validate(); err != nil {
				t.Fatalf("round-trip Validate(): %v", err)
			}
		})
	}
}

func TestWorkspaceRunnerAgentAttachmentValidationMatrix(t *testing.T) {
	valid := WorkspaceRunnerAgentAttachPayload{
		AgentID: "agent-1", RuntimeID: "runtime-1", AttachmentGeneration: 1, LifecycleSeq: 1,
	}
	invalid := []WorkspaceRunnerAgentAttachPayload{
		{RuntimeID: valid.RuntimeID, AttachmentGeneration: 1, LifecycleSeq: 1},
		{AgentID: valid.AgentID, AttachmentGeneration: 1, LifecycleSeq: 1},
		{AgentID: valid.AgentID, RuntimeID: valid.RuntimeID, LifecycleSeq: 1},
		{AgentID: valid.AgentID, RuntimeID: valid.RuntimeID, AttachmentGeneration: -1, LifecycleSeq: 1},
		{AgentID: valid.AgentID, RuntimeID: valid.RuntimeID, AttachmentGeneration: 1},
	}
	for index, attach := range invalid {
		values := []interface{ Validate() error }{
			attach,
			WorkspaceRunnerAgentAttachedPayload(attach),
			WorkspaceRunnerAgentDetachPayload(attach),
			WorkspaceRunnerAgentDetachedPayload(attach),
		}
		for _, value := range values {
			if err := value.Validate(); err == nil {
				t.Fatalf("case %d: %T accepted invalid payload %+v", index, value, value)
			}
		}
	}
}

func TestWorkspaceRunnerAgentAttachAndStartPayloadsRejectCrossDecode(t *testing.T) {
	attachRaw, err := json.Marshal(WorkspaceRunnerAgentAttachPayload{
		AgentID: "agent-1", RuntimeID: "runtime-1", AttachmentGeneration: 1, LifecycleSeq: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	var start WorkspaceRunnerAgentStartPayload
	if err := json.Unmarshal(attachRaw, &start); err != nil {
		t.Fatal(err)
	}
	if err := start.Validate(); err == nil {
		t.Fatalf("start payload accepted attach JSON: %s", attachRaw)
	}

	startRaw, err := json.Marshal(WorkspaceRunnerAgentStartPayload{AgentID: "agent-1", RuntimeID: "runtime-1", LaunchID: "launch-1"})
	if err != nil {
		t.Fatal(err)
	}
	var attach WorkspaceRunnerAgentAttachPayload
	if err := json.Unmarshal(startRaw, &attach); err != nil {
		t.Fatal(err)
	}
	if err := attach.Validate(); err == nil {
		t.Fatalf("attach payload accepted start JSON: %s", startRaw)
	}
}

func sameJSONFields(fields map[string]json.RawMessage, want []string) bool {
	if len(fields) != len(want) {
		return false
	}
	for _, field := range want {
		if _, found := fields[field]; !found {
			return false
		}
	}
	return true
}

func mapKeys(fields map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	return keys
}
