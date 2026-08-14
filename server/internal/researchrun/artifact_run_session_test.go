package researchrun

import (
	"encoding/json"
	"testing"
)

func TestRunSessionArtifactContentCanonicalizesConfigAndBindsOwner(t *testing.T) {
	t.Parallel()

	first, err := ArtifactContentHash(
		ArtifactKindRunSession,
		runSessionArtifactContent(json.RawMessage(`{
			"created_by":"user-a","goal":"research goal",
			"run_config":{"max_tasks":60,"max_parallel_tasks":5}
		}`)),
	)
	if err != nil {
		t.Fatal(err)
	}
	equivalent, err := ArtifactContentHash(
		ArtifactKindRunSession,
		runSessionArtifactContent(json.RawMessage(`{
			"run_config":{"max_parallel_tasks":5,"max_tasks":60},
			"goal":"research goal","created_by":"user-a"
		}`)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if first != equivalent {
		t.Fatalf("object key order changed hash: %q != %q", first, equivalent)
	}

	otherOwner, err := ArtifactContentHash(
		ArtifactKindRunSession,
		runSessionArtifactContent(json.RawMessage(`{
			"run_config":{"max_parallel_tasks":5,"max_tasks":60},
			"goal":"research goal","created_by":"user-b"
		}`)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if first == otherOwner {
		t.Fatal("session owner change must change content hash")
	}
}
