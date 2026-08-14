package researchrun

import "testing"

func TestQuestionArtifactContentBindsRelationshipsAndSemanticFacts(t *testing.T) {
	base := questionArtifactContent(
		"parent-1", "task-1", "q-1", "dimension", "What changed?", true,
		0.9, 0.8, 0.7, 0.6, 0.2, 2, 3,
	)
	first, err := ArtifactContentHash(ArtifactKindQuestion, base)
	if err != nil {
		t.Fatal(err)
	}
	changed := questionArtifactContent(
		"parent-2", "task-1", "q-1", "dimension", "What changed?", true,
		0.9, 0.8, 0.7, 0.6, 0.2, 2, 3,
	)
	second, err := ArtifactContentHash(ArtifactKindQuestion, changed)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("different parent relationships must not share a question version hash")
	}
}
