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
