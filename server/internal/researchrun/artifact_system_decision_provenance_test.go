package researchrun

import "testing"

func TestDecisionArtifactContentCanonicalizesPersistedJSON(t *testing.T) {
	first, err := ArtifactContentHash(ArtifactKindEvaluationDecision, decisionArtifactContent(
		"information_gain", "system", "", 2, 3,
		[]byte(`{"task_id":"task-1","before":1}`), []byte(`{"low_gain":false,"gain":0.4}`), "measured",
	))
	if err != nil {
		t.Fatal(err)
	}
	second, err := ArtifactContentHash(ArtifactKindEvaluationDecision, decisionArtifactContent(
		"information_gain", "system", "", 2, 3,
		[]byte(`{"before":1,"task_id":"task-1"}`), []byte(`{"gain":0.4,"low_gain":false}`), "measured",
	))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("canonical decision hashes differ: %q != %q", first, second)
	}
	changed, err := ArtifactContentHash(ArtifactKindEvaluationDecision, decisionArtifactContent(
		"budget_exhausted", "system", "", 2, 3,
		[]byte(`{"before":1,"task_id":"task-1"}`), []byte(`{"gain":0.4,"low_gain":false}`), "measured",
	))
	if err != nil {
		t.Fatal(err)
	}
	if first == changed {
		t.Fatal("different decision kinds must not share a version hash")
	}
}
