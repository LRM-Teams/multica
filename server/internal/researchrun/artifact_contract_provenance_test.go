package researchrun

import "testing"

func TestContractRevisionArtifactContentCanonicalizesJSONFacts(t *testing.T) {
	first, err := ArtifactContentHash(ArtifactKindContractRevision, contractRevisionArtifactContent(
		2, "goal", []byte(`{"b":2,"a":1}`), "team", "fresh", "zh",
		[]byte(`{"verified":true,"domains":["a"]}`), []byte(`{"max_tasks":10,"seconds":30}`), "user-1", "steer",
	))
	if err != nil {
		t.Fatal(err)
	}
	second, err := ArtifactContentHash(ArtifactKindContractRevision, contractRevisionArtifactContent(
		2, "goal", []byte(`{"a":1,"b":2}`), "team", "fresh", "zh",
		[]byte(`{"domains":["a"],"verified":true}`), []byte(`{"seconds":30,"max_tasks":10}`), "user-1", "steer",
	))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("canonical contract hashes differ: %q != %q", first, second)
	}
	changed, err := ArtifactContentHash(ArtifactKindContractRevision, contractRevisionArtifactContent(
		2, "changed", []byte(`{"a":1,"b":2}`), "team", "fresh", "zh",
		[]byte(`{"domains":["a"],"verified":true}`), []byte(`{"seconds":30,"max_tasks":10}`), "user-1", "steer",
	))
	if err != nil {
		t.Fatal(err)
	}
	if first == changed {
		t.Fatal("different contract goals must not share a version hash")
	}
}
