package protocol

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestWorkspaceRunnerActivityFramesUseRaftWireNames(t *testing.T) {
	observedAt := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	values := []any{
		WorkspaceRunnerReadyPayload{
			WorkspaceID: "workspace-1", DaemonInstanceID: "daemon-instance-1",
			ActiveCapabilities: []string{DaemonCapabilityWorkspaceRunnerAttachment},
		},
		WorkspaceRunnerPingPayload{PingID: "ping-1"},
		WorkspaceRunnerPongPayload{PingID: "ping-1"},
		WorkspaceRunnerAgentStartPayload{AgentID: "agent-1", RuntimeID: "runtime-1", StartDispatchID: "dispatch-1"},
		AgentStartAckPayload{AgentID: "agent-1", LaunchID: "launch-1", StartDispatchID: "dispatch-1", QueueState: AgentStartQueueQueued},
		WorkspaceRunnerAgentStopPayload{AgentID: "agent-1", LaunchID: "launch-1"},
		AgentStatusPayload{AgentID: "agent-1", LaunchID: "launch-1", Status: AgentStatusActive},
		AgentSessionPayload{AgentID: "agent-1", LaunchID: "launch-1", ProviderSessionID: "session-1", RuntimeGeneration: 2},
		AgentActivityPayload{Snapshot: AgentActivitySnapshot{AgentID: "agent-1", LaunchID: "launch-1", DaemonInstanceID: "daemon-instance-1", ClientSequence: 1, ProducerFactID: "fact-1", ObservedAt: observedAt, ActivityKind: ActivityKindWorking}},
		AgentActivityProbePayload{AgentID: "agent-1", LaunchID: "launch-1", ProbeID: "probe-1"},
	}

	var encoded strings.Builder
	for _, value := range values {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal %T: %v", value, err)
		}
		encoded.Write(data)
	}
	wire := encoded.String()
	for _, field := range []string{`"workspaceId"`, `"daemonInstanceId"`, `"startDispatchId"`, `"launchId"`, `"queueState"`, `"clientSequence"`, `"producerFactId"`, `"observedAt"`, `"probeId"`} {
		if !strings.Contains(wire, field) {
			t.Fatalf("runner Activity wire %s does not contain %s", wire, field)
		}
	}
	for _, field := range []string{`"workspace_id"`, `"daemon_instance_id"`, `"start_dispatch_id"`, `"launch_id"`, `"client_sequence"`, `"producer_fact_id"`} {
		if strings.Contains(wire, field) {
			t.Fatalf("runner Activity wire %s contains HTTP field %s", wire, field)
		}
	}
}

func TestWorkspaceRunnerActivityValidationRejectsInvalidBoundaryData(t *testing.T) {
	observedAt := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	validSnapshot := AgentActivitySnapshot{
		AgentID: "agent-1", LaunchID: "launch-1", DaemonInstanceID: "daemon-instance-1",
		ClientSequence: 1, ProducerFactID: "fact-1", ObservedAt: observedAt, ActivityKind: ActivityKindWorking,
	}

	cases := []struct {
		name  string
		value interface{ Validate() error }
	}{
		{name: "missing ready identity", value: WorkspaceRunnerReadyPayload{WorkspaceID: "workspace-1"}},
		{name: "missing hard-cut capability", value: WorkspaceRunnerReadyPayload{WorkspaceID: "workspace-1", DaemonInstanceID: "instance-1"}},
		{name: "unknown start state", value: AgentStartAckPayload{AgentID: "agent-1", LaunchID: "launch-1", StartDispatchID: "dispatch-1", QueueState: "ready"}},
		{name: "negative queue age", value: AgentStartAckPayload{AgentID: "agent-1", LaunchID: "launch-1", StartDispatchID: "dispatch-1", QueueState: AgentStartQueueQueued, QueueAgeMS: -1}},
		{name: "unknown status", value: AgentStatusPayload{AgentID: "agent-1", LaunchID: "launch-1", Status: "online"}},
		{name: "negative generation", value: AgentSessionPayload{AgentID: "agent-1", LaunchID: "launch-1", RuntimeGeneration: -1}},
		{name: "zero sequence", value: AgentActivityPayload{Snapshot: AgentActivitySnapshot{AgentID: "agent-1", LaunchID: "launch-1", DaemonInstanceID: "daemon-instance-1", ProducerFactID: "fact-1", ObservedAt: observedAt, ActivityKind: ActivityKindWorking}}},
		{name: "unknown activity kind", value: AgentActivityPayload{Snapshot: AgentActivitySnapshot{AgentID: "agent-1", LaunchID: "launch-1", DaemonInstanceID: "daemon-instance-1", ClientSequence: 1, ProducerFactID: "fact-1", ObservedAt: observedAt, ActivityKind: "idle"}}},
		{name: "scalar open envelope", value: AgentActivityPayload{Snapshot: validSnapshot, Entries: []AgentActivityEntry{{Kind: "provider_event", Position: 0, Body: json.RawMessage(`"not-an-object"`)}}}},
		{name: "noncontiguous positions", value: AgentActivityPayload{Snapshot: validSnapshot, Entries: []AgentActivityEntry{{Kind: "provider_event", Position: 1, Body: json.RawMessage(`{}`)}}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.value.Validate(); err == nil {
				t.Fatal("Validate() unexpectedly accepted invalid input")
			}
		})
	}
}

func TestWorkspaceRunnerReadyCapabilityValidation(t *testing.T) {
	valid := WorkspaceRunnerReadyPayload{
		WorkspaceID: "workspace-1", DaemonInstanceID: "instance-1",
		ActiveCapabilities: []string{DaemonCapabilityWorkspaceRunnerAttachment, DaemonCapabilityAgentLifecycleActions},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid ready capabilities: %v", err)
	}
	duplicate := valid
	duplicate.ActiveCapabilities = []string{DaemonCapabilityWorkspaceRunnerAttachment, DaemonCapabilityWorkspaceRunnerAttachment}
	if err := duplicate.Validate(); err == nil {
		t.Fatal("duplicate ready capabilities were accepted")
	}
}

func TestAgentActivityOpenEntryEnvelopePreservesUnknownKinds(t *testing.T) {
	payload := AgentActivityPayload{
		Snapshot: AgentActivitySnapshot{
			AgentID: "agent-1", LaunchID: "launch-1", DaemonInstanceID: "daemon-instance-1",
			ClientSequence: 7, ProducerFactID: "fact-1", ObservedAt: time.Now().UTC(), ActivityKind: ActivityKindWorking,
			DetailKind: "future_runtime_detail",
		},
		Entries: []AgentActivityEntry{{
			Kind:     "future_runtime_entry",
			Position: 0,
			Body:     json.RawMessage(`{"future":"shape","nested":{"still":"opaque"}}`),
		}},
	}
	if err := payload.Validate(); err != nil {
		t.Fatalf("open Activity envelope rejected: %v", err)
	}
}
