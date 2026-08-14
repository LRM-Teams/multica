package researchrun

import (
	"encoding/json"
	"testing"
)

func TestStageEvaluationArtifactContentCanonicalizesFindingsAndBindsOutcome(t *testing.T) {
	t.Parallel()

	passed, err := ArtifactContentHash(
		ArtifactKindStageEvaluation,
		stageEvaluationArtifactContent(json.RawMessage(`{
			"stage":"s2_sources","passed":true,"score":0.8,
			"findings":[{"message":"covered","metadata":{"b":2,"a":1}}]
		}`)),
	)
	if err != nil {
		t.Fatal(err)
	}
	equivalent, err := ArtifactContentHash(
		ArtifactKindStageEvaluation,
		stageEvaluationArtifactContent(json.RawMessage(`{
			"findings":[{"metadata":{"a":1,"b":2},"message":"covered"}],
			"passed":true,"score":0.8,"stage":"s2_sources"
		}`)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if passed != equivalent {
		t.Fatalf("object key order changed hash: %q != %q", passed, equivalent)
	}

	failed, err := ArtifactContentHash(
		ArtifactKindStageEvaluation,
		stageEvaluationArtifactContent(json.RawMessage(`{
			"findings":[{"metadata":{"a":1,"b":2},"message":"covered"}],
			"passed":false,"score":0.8,"stage":"s2_sources"
		}`)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if passed == failed {
		t.Fatal("evaluation outcome change must change content hash")
	}
	if !(ArtifactPolicy{}).EvaluationPrivateKind(ArtifactKindStageEvaluation) {
		t.Fatal("stage evaluation must remain evaluation-private by kind")
	}
}
