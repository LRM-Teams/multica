package researchrun

import "testing"

func TestResearchMessageArtifactContentCanonicalizesMetaAndBindsTarget(t *testing.T) {
	first, err := ArtifactContentHash(ArtifactKindResearchMessage, researchMessageArtifactContent(
		"agent", "agent-1", "agent-2", "", "body", "chat", []byte(`{"b":2,"a":1}`),
	))
	if err != nil {
		t.Fatal(err)
	}
	second, err := ArtifactContentHash(ArtifactKindResearchMessage, researchMessageArtifactContent(
		"agent", "agent-1", "agent-2", "", "body", "chat", []byte(`{"a":1,"b":2}`),
	))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("canonical message hashes differ: %q != %q", first, second)
	}
	changed, err := ArtifactContentHash(ArtifactKindResearchMessage, researchMessageArtifactContent(
		"agent", "agent-1", "agent-3", "", "body", "chat", []byte(`{"a":1,"b":2}`),
	))
	if err != nil {
		t.Fatal(err)
	}
	if first == changed {
		t.Fatal("different target Agents must not share a message hash")
	}
	lineage, err := ArtifactContentHash(ArtifactKindResearchMessage, researchMessageArtifactContent(
		"agent", "agent-1", "agent-2", "event-1", "body", "chat", []byte(`{"a":1,"b":2}`),
	))
	if err != nil {
		t.Fatal(err)
	}
	if first == lineage {
		t.Fatal("different Run Event lineage must not share a message hash")
	}
}
