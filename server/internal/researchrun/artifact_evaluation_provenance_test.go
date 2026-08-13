package researchrun

import "testing"

func TestEvaluationDecisionArtifactContentHashesCanonicalPersistedFacts(t *testing.T) {
	first, err := ArtifactContentHash(ArtifactKindEvaluationDecision,
		evaluationDecisionArtifactContent(
			TaskKindQualityGate, "agent-1", 2, 3,
			[]byte(`{"report_id":"report-1","task_id":"task-1","task_kind":"quality_gate"}`),
			[]byte(`{"coverage":0.9,"passed":true}`), "reviewed all claims",
		))
	if err != nil {
		t.Fatal(err)
	}
	second, err := ArtifactContentHash(ArtifactKindEvaluationDecision,
		evaluationDecisionArtifactContent(
			TaskKindQualityGate, "agent-1", 2, 3,
			[]byte(`{"task_kind":"quality_gate","task_id":"task-1","report_id":"report-1"}`),
			[]byte(`{"passed":true,"coverage":0.9}`), "reviewed all claims",
		))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("canonical evaluation hashes differ: %q != %q", first, second)
	}
	changed, err := ArtifactContentHash(ArtifactKindEvaluationDecision,
		evaluationDecisionArtifactContent(
			TaskKindCitationAudit, "agent-1", 2, 3,
			[]byte(`{"report_id":"report-1","task_id":"task-1","task_kind":"citation_audit"}`),
			[]byte(`{"coverage":0.9,"passed":true}`), "reviewed all claims",
		))
	if err != nil {
		t.Fatal(err)
	}
	if first == changed {
		t.Fatal("quality and citation decisions must not share a version hash")
	}
}
