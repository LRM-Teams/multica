package researchrun

import (
	"encoding/json"
	"testing"
)

func TestRunEventArtifactContentHashesCanonicalPersistedFacts(t *testing.T) {
	first := RunEvent{
		Sequence: 3, Type: "task_ready", IdempotencyKey: "task:ready:3",
		ActorType: "agent", ActorID: "00000000-0000-0000-0000-000000000001",
		Payload: json.RawMessage(`{"task_id":"task-1","priority":0.9}`),
	}
	second := first
	second.Payload = json.RawMessage(`{"priority":0.9,"task_id":"task-1"}`)

	firstHash, err := ArtifactContentHash(ArtifactKindRunEvent, runEventArtifactContent(first))
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := ArtifactContentHash(ArtifactKindRunEvent, runEventArtifactContent(second))
	if err != nil {
		t.Fatal(err)
	}
	if firstHash != secondHash {
		t.Fatalf("canonical event hashes differ: %q != %q", firstHash, secondHash)
	}

	second.Sequence++
	changedHash, err := ArtifactContentHash(ArtifactKindRunEvent, runEventArtifactContent(second))
	if err != nil {
		t.Fatal(err)
	}
	if firstHash == changedHash {
		t.Fatal("different event sequences must not share a version hash")
	}
}
