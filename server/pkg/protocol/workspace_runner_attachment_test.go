package protocol

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestWorkspaceRunnerAgentAttachmentEventNames(t *testing.T) {
	got := []string{EventAgentAttach, EventAgentAttached, EventAgentDetach, EventAgentDetached}
	want := []string{"agent:attach", "agent:attached", "agent:detach", "agent:detached"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Attachment event names = %v, want %v", got, want)
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
				AgentID: "agent-1", RuntimeID: "runtime-1", AttachmentGeneration: 2, LifecycleSeq: 3, CorrelationID: "correlation-1",
			},
			new: func() interface{ Validate() error } { return &WorkspaceRunnerAgentAttachPayload{} },
		},
		{
			name: "attached",
			value: WorkspaceRunnerAgentAttachedPayload{
				AgentID: "agent-1", RuntimeID: "runtime-1", AttachmentGeneration: 2, LifecycleSeq: 3, CorrelationID: "correlation-1",
			},
			new: func() interface{ Validate() error } { return &WorkspaceRunnerAgentAttachedPayload{} },
		},
		{
			name: "detach",
			value: WorkspaceRunnerAgentDetachPayload{
				AgentID: "agent-1", RuntimeID: "runtime-1", AttachmentGeneration: 2, LifecycleSeq: 3, CorrelationID: "correlation-2",
			},
			new: func() interface{ Validate() error } { return &WorkspaceRunnerAgentDetachPayload{} },
		},
		{
			name: "detached",
			value: WorkspaceRunnerAgentDetachedPayload{
				AgentID: "agent-1", RuntimeID: "runtime-1", AttachmentGeneration: 2, LifecycleSeq: 3, CorrelationID: "correlation-2",
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
			if want := []string{"agentId", "runtimeId", "attachmentGeneration", "lifecycleSeq", "correlationId"}; !sameJSONFields(fields, want) {
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
		AgentID: "agent-1", RuntimeID: "runtime-1", AttachmentGeneration: 1, LifecycleSeq: 1, CorrelationID: "correlation-1",
	}
	invalid := []WorkspaceRunnerAgentAttachPayload{
		{RuntimeID: valid.RuntimeID, AttachmentGeneration: 1, LifecycleSeq: 1, CorrelationID: valid.CorrelationID},
		{AgentID: valid.AgentID, AttachmentGeneration: 1, LifecycleSeq: 1, CorrelationID: valid.CorrelationID},
		{AgentID: valid.AgentID, RuntimeID: valid.RuntimeID, LifecycleSeq: 1, CorrelationID: valid.CorrelationID},
		{AgentID: valid.AgentID, RuntimeID: valid.RuntimeID, AttachmentGeneration: -1, LifecycleSeq: 1, CorrelationID: valid.CorrelationID},
		{AgentID: valid.AgentID, RuntimeID: valid.RuntimeID, AttachmentGeneration: 1, CorrelationID: valid.CorrelationID},
		{AgentID: valid.AgentID, RuntimeID: valid.RuntimeID, AttachmentGeneration: 1, LifecycleSeq: 1},
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
		AgentID: "agent-1", RuntimeID: "runtime-1", AttachmentGeneration: 1, LifecycleSeq: 1, CorrelationID: "correlation-1",
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

	startRaw, err := json.Marshal(WorkspaceRunnerAgentStartPayload{AgentID: "agent-1", RuntimeID: "runtime-1", StartDispatchID: "dispatch-1"})
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
