package researchrun

import (
	"encoding/json"
	"testing"
)

func TestProductRoundDecisionArtifactContentCanonicalizesJSONAndBindsDecision(t *testing.T) {
	t.Parallel()

	first, err := ArtifactContentHash(
		ArtifactKindProductRoundDecision,
		productRoundDecisionArtifactContent(json.RawMessage(`{
			"round_number":2,
			"decision":"continue",
			"coverage_gaps":[{"b":2,"a":1}]
		}`)),
	)
	if err != nil {
		t.Fatal(err)
	}
	equivalent, err := ArtifactContentHash(
		ArtifactKindProductRoundDecision,
		productRoundDecisionArtifactContent(json.RawMessage(`{
			"coverage_gaps":[{"a":1,"b":2}],
			"decision":"continue",
			"round_number":2
		}`)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if first != equivalent {
		t.Fatalf("object key order changed hash: %q != %q", first, equivalent)
	}

	stopped, err := ArtifactContentHash(
		ArtifactKindProductRoundDecision,
		productRoundDecisionArtifactContent(json.RawMessage(`{
			"coverage_gaps":[{"a":1,"b":2}],
			"decision":"stop_enough",
			"round_number":2
		}`)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if first == stopped {
		t.Fatal("decision change must change content hash")
	}
}
