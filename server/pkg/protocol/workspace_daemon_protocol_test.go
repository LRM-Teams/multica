package protocol

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestWorkspaceDaemonActivityFramesUseRaftWireNames(t *testing.T) {
	observedAt := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	values := []any{
		WorkspaceReadyPayload{
			WorkspaceID: "workspace-1", DaemonInstanceID: "daemon-instance-1",
			DeviceName: "ubuntu-build-host", OS: "linux", CLIVersion: "0.4.24-alpha.91",
			ActiveCapabilities: []string{DaemonCapabilityWorkspaceDaemonAgentProcess},
			RunningAgents:      []string{"agent-1"},
		},
		WorkspacePingPayload{PingID: "ping-1"},
		WorkspacePongPayload{PingID: "ping-1"},
		AgentStartPayload{AgentID: "agent-1", RuntimeID: "runtime-1"},
		AgentStartAckPayload{AgentID: "agent-1", QueueState: AgentStartQueueQueued},
		AgentStopPayload{AgentID: "agent-1"},
		AgentWorkspaceResetPayload{OperationID: "operation-1", AgentID: "agent-1"},
		AgentWorkspaceResetResultPayload{OperationID: "operation-1", AgentID: "agent-1", Status: AgentResetWorkspaceSucceeded},
		AgentStatusPayload{AgentID: "agent-1", Status: AgentStatusActive},
		AgentSessionPayload{AgentID: "agent-1", ProviderSessionID: "session-1", RuntimeGeneration: 2},
		AgentActivityPayload{Snapshot: AgentActivitySnapshot{AgentID: "agent-1", DaemonInstanceID: "daemon-instance-1", ClientSequence: 1, ProducerFactID: "fact-1", ObservedAt: observedAt, ActivityKind: ActivityKindWorking, DetailKind: "model_response_started"}},
		AgentActivityProbePayload{AgentID: "agent-1", ProbeID: "probe-1"},
		ComputerUpgradePayload{RequestID: "upgrade-1", TargetVersion: "0.4.24-alpha.59"},
		ComputerRestartPayload{RequestID: "restart-1"},
		ComputerUpgradeProgressPayload{RequestID: "upgrade-1", Phase: "staging"},
		ComputerUpgradeDonePayload{RequestID: "upgrade-1", OK: true, NewVersion: "0.4.24-alpha.59"},
		ComputerWorkDigestPayload{
			RequestID: "digest-1",
			Start:     time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC),
			End:       time.Date(2026, time.August, 17, 0, 0, 0, 0, time.UTC),
		},
		ComputerWorkJournalPayload{RequestID: "journal-1", Enabled: true},
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
	for _, field := range []string{`"workspaceId"`, `"daemonInstanceId"`, `"deviceName"`, `"cliVersion"`, `"runningAgents"`, `"queueState"`, `"clientSeq"`, `"producerFactId"`, `"observedAtMs"`, `"probeId"`, `"requestId"`, `"targetVersion"`, `"newVersion"`} {
		if !strings.Contains(wire, field) {
			t.Fatalf("runner Activity wire %s does not contain %s", wire, field)
		}
	}
	for _, field := range []string{`"workspace_id"`, `"daemon_instance_id"`, `"device_name"`, `"cli_version"`, `"client_sequence"`, `"producer_fact_id"`, `"request_id"`, `"target_version"`, `"new_version"`} {
		if strings.Contains(wire, field) {
			t.Fatalf("runner Activity wire %s contains HTTP field %s", wire, field)
		}
	}
}

func TestComputerUpgradePayloadUsesRaftRequestIdentity(t *testing.T) {
	payload := ComputerUpgradePayload{RequestID: "upgrade-1", TargetVersion: "0.4.24-alpha.59"}
	if err := payload.Validate(); err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "operationId") {
		t.Fatalf("Computer upgrade payload restored operation identity: %s", wire)
	}
	if err := (ComputerUpgradePayload{}).Validate(); err == nil {
		t.Fatal("empty Computer upgrade payload was accepted")
	}
}

func TestComputerWorkDigestPayloadRejectsEmptyAndInvertedWindow(t *testing.T) {
	start := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
	valid := ComputerWorkDigestPayload{RequestID: "digest-1", Start: start, End: start.Add(24 * time.Hour)}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (ComputerWorkDigestPayload{Start: start, End: start.Add(time.Hour)}).Validate(); err == nil {
		t.Fatal("work digest payload without requestId was accepted")
	}
	if err := (ComputerWorkDigestPayload{RequestID: "digest-1", Start: start, End: start}).Validate(); err == nil {
		t.Fatal("inverted work digest window was accepted")
	}
}

func TestWorkspaceDaemonStartUsesRaftSessionConfig(t *testing.T) {
	resume, err := json.Marshal(AgentStartPayload{
		AgentID: "agent-1", RuntimeID: "runtime-1", Config: AgentStartConfig{SessionID: "provider-session"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(resume), `"config":{"sessionId":"provider-session"}`) {
		t.Fatalf("resume start lost Raft config.sessionId: %s", resume)
	}
	fresh, err := json.Marshal(AgentStartPayload{
		AgentID: "agent-1", RuntimeID: "runtime-1", Config: AgentStartConfig{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fresh), `"config":{}`) || strings.Contains(string(fresh), `"sessionId"`) {
		t.Fatalf("fresh start must use empty Raft config without sessionId: %s", fresh)
	}
}

func TestAgentActivityPayloadUsesRaftFactOnlyWireEnvelope(t *testing.T) {
	observedAt := time.Date(2026, time.August, 14, 1, 2, 3, 456000000, time.UTC)
	payload := AgentActivityPayload{
		Snapshot: AgentActivitySnapshot{
			AgentID: "agent-1", DaemonInstanceID: "daemon-instance-1",
			ClientSequence: 7, ProducerFactID: "fact-1", ObservedAt: observedAt,
			ActivityKind: ActivityKindWorking, DetailKind: "running_command", ProbeID: "probe-1",
		},
		Detail:      "pnpm test",
		Entries:     []AgentActivityEntry{{Kind: "status", Body: json.RawMessage(`{"activity":"working","detail":"pnpm test","detailKind":"running_command"}`)}},
		IsHeartbeat: true,
	}

	wire, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal Activity fact: %v", err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(wire, &envelope); err != nil {
		t.Fatalf("decode Activity fact envelope: %v", err)
	}
	for _, field := range []string{"agentId", "daemonInstanceId", "clientSeq", "producerFactId", "observedAtMs", "detailKind", "entries", "probeId", "isHeartbeat"} {
		if _, ok := envelope[field]; !ok {
			t.Fatalf("Activity fact wire %s is missing Raft field %q", wire, field)
		}
	}
	for _, forbidden := range []string{"snapshot", "activityKind", "clientSequence", "observedAt", "processInstanceId"} {
		if _, ok := envelope[forbidden]; ok {
			t.Fatalf("Activity fact wire %s contains daemon-owned presentation field %q", wire, forbidden)
		}
	}
	if strings.Contains(string(envelope["entries"]), `"position"`) {
		t.Fatalf("Activity entries must use Raft array order without a position field: %s", envelope["entries"])
	}

	var decoded AgentActivityPayload
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatalf("unmarshal Activity fact: %v", err)
	}
	if decoded.Snapshot.AgentID != payload.Snapshot.AgentID || decoded.Snapshot.DetailKind != payload.Snapshot.DetailKind || decoded.Snapshot.ActivityKind != "" || !decoded.Snapshot.ObservedAt.Equal(observedAt) || decoded.Detail != payload.Detail || !decoded.IsHeartbeat {
		t.Fatalf("decoded Activity fact = %+v, want identities/detail/time without daemon activity kind", decoded.Snapshot)
	}
}

func TestWorkspaceDaemonActivityValidationRejectsInvalidBoundaryData(t *testing.T) {
	observedAt := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	validSnapshot := AgentActivitySnapshot{
		AgentID: "agent-1", DaemonInstanceID: "daemon-instance-1",
		ClientSequence: 1, ProducerFactID: "fact-1", ObservedAt: observedAt, ActivityKind: ActivityKindWorking, DetailKind: "model_response_started",
	}

	cases := []struct {
		name  string
		value interface{ Validate() error }
	}{
		{name: "missing ready identity", value: WorkspaceReadyPayload{WorkspaceID: "workspace-1"}},
		{name: "missing hard-cut capability", value: WorkspaceReadyPayload{WorkspaceID: "workspace-1", DaemonInstanceID: "instance-1"}},
		{name: "unknown start state", value: AgentStartAckPayload{AgentID: "agent-1", QueueState: "ready"}},
		{name: "negative queue age", value: AgentStartAckPayload{AgentID: "agent-1", QueueState: AgentStartQueueQueued, QueueAgeMS: -1}},
		{name: "unknown status", value: AgentStatusPayload{AgentID: "agent-1", Status: "online"}},
		{name: "negative generation", value: AgentSessionPayload{AgentID: "agent-1", RuntimeGeneration: -1}},
		{name: "zero sequence", value: AgentActivityPayload{Snapshot: AgentActivitySnapshot{AgentID: "agent-1", DaemonInstanceID: "daemon-instance-1", ProducerFactID: "fact-1", ObservedAt: observedAt, ActivityKind: ActivityKindWorking, DetailKind: "model_response_started"}}},
		{name: "unknown activity kind", value: AgentActivityPayload{Snapshot: AgentActivitySnapshot{AgentID: "agent-1", DaemonInstanceID: "daemon-instance-1", ClientSequence: 1, ProducerFactID: "fact-1", ObservedAt: observedAt, ActivityKind: "idle", DetailKind: "idle"}}},
		{name: "unknown activity detail kind", value: AgentActivityPayload{Snapshot: AgentActivitySnapshot{AgentID: "agent-1", DaemonInstanceID: "daemon-instance-1", ClientSequence: 1, ProducerFactID: "fact-1", ObservedAt: observedAt, DetailKind: "future_runtime_detail"}}},
		{name: "scalar open envelope", value: AgentActivityPayload{Snapshot: validSnapshot, Entries: []AgentActivityEntry{{Kind: "provider_event", Body: json.RawMessage(`"not-an-object"`)}}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.value.Validate(); err == nil {
				t.Fatal("Validate() unexpectedly accepted invalid input")
			}
		})
	}
}

func TestWorkspaceDaemonReadyCapabilityValidation(t *testing.T) {
	valid := WorkspaceReadyPayload{
		WorkspaceID: "workspace-1", DaemonInstanceID: "instance-1",
		ActiveCapabilities: []string{DaemonCapabilityWorkspaceDaemonAgentProcess, DaemonCapabilityWorkspaceDaemonAgentReset},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid ready capabilities: %v", err)
	}
	duplicate := valid
	duplicate.ActiveCapabilities = []string{DaemonCapabilityWorkspaceDaemonAgentProcess, DaemonCapabilityWorkspaceDaemonAgentProcess}
	if err := duplicate.Validate(); err == nil {
		t.Fatal("duplicate ready capabilities were accepted")
	}
	duplicateAgent := valid
	duplicateAgent.RunningAgents = []string{"agent-1", "agent-1"}
	if err := duplicateAgent.Validate(); err == nil {
		t.Fatal("duplicate running Agent identities were accepted")
	}
}

func TestAgentActivityOpenEntryEnvelopePreservesUnknownKinds(t *testing.T) {
	payload := AgentActivityPayload{
		Snapshot: AgentActivitySnapshot{
			AgentID: "agent-1", DaemonInstanceID: "daemon-instance-1",
			ClientSequence: 7, ProducerFactID: "fact-1", ObservedAt: time.Now().UTC(), ActivityKind: ActivityKindWorking,
			DetailKind: "runtime_progress",
		},
		Entries: []AgentActivityEntry{{
			Kind: "future_runtime_entry",
			Body: json.RawMessage(`{"future":"shape","nested":{"still":"opaque"}}`),
		}},
	}
	if err := payload.Validate(); err != nil {
		t.Fatalf("open Activity envelope rejected: %v", err)
	}
}
