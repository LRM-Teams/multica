package protocol

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAgentActivityWireCarriesFactsAndBoundedText(t *testing.T) {
	payload := AgentActivityPayload{
		Snapshot: AgentActivitySnapshot{
			AgentID: "agent-1", DaemonInstanceID: "instance-1", ObservedAt: time.Now().UTC(),
			ActivityKind: ActivityKindWorking, DetailKind: "running_command",
		},
		Summary:  AgentActivitySummary{ActivityKind: ActivityKindWorking, DetailKind: "running_command", Label: "Running command..."},
		Timeline: []AgentActivityTimelineRow{{ActivityKind: ActivityKindWorking, DetailKind: "running_command", Title: "Running command", Subtext: "pnpm test", BodyKind: "command"}},
	}
	if err := payload.Validate(); err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{`"snapshot"`, `"activityKind"`, `"summary"`, `"timeline"`, `"bodyKind"`} {
		if !strings.Contains(string(wire), required) {
			t.Fatalf("wire %s missing %s", wire, required)
		}
	}
	for _, removed := range []string{"clientSeq", "producerFactId", "probeId", "entries", "tone", "visibility"} {
		if strings.Contains(string(wire), removed) {
			t.Fatalf("wire %s contains removed field %s", wire, removed)
		}
	}
}

func TestAgentActivityValidationRequiresPresentation(t *testing.T) {
	payload := AgentActivityPayload{Snapshot: AgentActivitySnapshot{
		AgentID: "agent-1", DaemonInstanceID: "instance-1", ObservedAt: time.Now().UTC(),
		ActivityKind: ActivityKindWorking, DetailKind: "model_response_started",
	}}
	if err := payload.Validate(); err == nil {
		t.Fatal("Activity without daemon presentation was accepted")
	}
}

func TestAgentActivityWireCarriesOptionalTiming(t *testing.T) {
	payload := AgentActivityPayload{
		Snapshot: AgentActivitySnapshot{
			AgentID: "agent-1", DaemonInstanceID: "instance-1", ObservedAt: time.Now().UTC(),
			ActivityKind: ActivityKindWorking, DetailKind: "model_response_started",
		},
		Summary: AgentActivitySummary{ActivityKind: ActivityKindWorking, DetailKind: "model_response_started", Label: "Working"},
		Timing:  &AgentActivityTiming{AcceptedAtMS: 100, FirstACPUpdateAtMS: 150, DaemonSentAtMS: 180},
	}
	wire, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded AgentActivityPayload
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Timing == nil || decoded.Timing.AcceptedAtMS != 100 || decoded.Timing.FirstACPUpdateAtMS != 150 || decoded.Timing.DaemonSentAtMS != 180 {
		t.Fatalf("activity timing roundtrip mismatch: %+v", decoded.Timing)
	}
	bare, err := json.Marshal(AgentActivityPayload{Snapshot: payload.Snapshot, Summary: payload.Summary})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bare), `"timing"`) {
		t.Fatalf("wire %s should omit absent timing evidence", bare)
	}
}

func TestWorkspaceReadyValidatesManagedProcessSnapshot(t *testing.T) {
	payload := WorkspaceReadyPayload{
		WorkspaceID: "workspace-1", DaemonInstanceID: "instance-1",
		ActiveCapabilities:     []string{DaemonCapabilityWorkspaceDaemonAgentProcess},
		ProcessSnapshotVersion: AgentProcessSnapshotV1,
		AgentProcesses: []AgentProcessSnapshot{{
			AgentID: "agent-1", RuntimeID: "runtime-1", QueueState: AgentStartQueueRunning,
		}},
	}
	if err := payload.Validate(); err != nil {
		t.Fatal(err)
	}
	payload.AgentProcesses = append(payload.AgentProcesses, payload.AgentProcesses[0])
	if err := payload.Validate(); err == nil {
		t.Fatal("duplicate managed Agent process snapshot was accepted")
	}
}

func TestAgentStatusFailureRequiresInactiveLifecycle(t *testing.T) {
	payload := AgentStatusPayload{
		AgentID: "agent-1", Status: AgentStatusInactive,
		StartFailure: &AgentStartFailure{ReasonCode: "provider_auth_required", RetryClass: AgentStartRetryTerminal},
	}
	if err := payload.Validate(); err != nil {
		t.Fatal(err)
	}
	payload.Status = AgentStatusActive
	if err := payload.Validate(); err == nil {
		t.Fatal("active lifecycle status carried a start failure")
	}
}
