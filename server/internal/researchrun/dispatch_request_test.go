package researchrun

import (
	"encoding/json"
	"testing"
)

func TestHashDispatchRequestUsesSemanticJSONAndExcludesHashField(t *testing.T) {
	left := DispatchRequest{
		Run:       Run{SessionID: "session-1"},
		Task:      Task{ID: "task-1", AcceptanceCriteria: json.RawMessage(`{ "b": 2, "a": 1 }`)},
		AttemptID: "attempt-1", AgentID: "agent-1", Prompt: "prompt", Key: "key-1",
	}
	right := left
	right.Task.AcceptanceCriteria = json.RawMessage(`{"a":1,"b":2}`)
	right.RequestHash = "caller-supplied-value-is-not-part-of-the-fingerprint"
	// These fields are intentionally outside the immutable external mutation.
	// Future canonical model growth must not invalidate committed V1 outbox rows.
	right.Run.LastError = "new diagnostic field value"
	right.Task.Objective = "changed canonical description already captured by prompt"
	leftHash, err := HashDispatchRequest(left)
	if err != nil {
		t.Fatal(err)
	}
	rightHash, err := HashDispatchRequest(right)
	if err != nil {
		t.Fatal(err)
	}
	if leftHash != rightHash || len(leftHash) != 64 {
		t.Fatalf("left=%q right=%q", leftHash, rightHash)
	}
}

func TestHashDispatchRequestIncludesFrozenExecutionTarget(t *testing.T) {
	base := DispatchRequest{
		Run:  Run{SessionID: "session-1", WorkspaceID: "workspace-1"},
		Task: Task{ID: "task-1"}, AttemptID: "attempt-1", AgentID: "agent-1",
		Target: ExecutionTarget{Adapter: "agent_inbox", AgentID: "agent-1", RuntimeID: "runtime-1", Provider: "openai", Model: "gpt-5.4", ConfigFingerprint: "config-a"},
		Prompt: "frozen", Key: "dispatch-1",
	}
	left, err := HashDispatchRequest(base)
	if err != nil {
		t.Fatal(err)
	}
	base.Target.Model = "gpt-5.5"
	right, err := HashDispatchRequest(base)
	if err != nil {
		t.Fatal(err)
	}
	if left == right {
		t.Fatal("execution target change did not change request hash")
	}
}

func TestHashDispatchRequestHistoricalPayloadHashIsUnchangedWithoutTarget(t *testing.T) {
	request := DispatchRequest{Run: Run{SessionID: "session-1"}, Task: Task{ID: "task-1"}, AttemptID: "attempt-1", AgentID: "agent-1", Prompt: "frozen prompt", Key: "dispatch-1"}
	got, err := HashDispatchRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	const historical = "7bcc490a3d9c945b3ccee37f573631a5385eb09f7981c0d881ebddea45c5d2e0"
	if got != historical {
		t.Fatalf("historical hash changed: got %s want %s", got, historical)
	}
}
