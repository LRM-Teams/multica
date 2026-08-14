package researcheval

import (
	"encoding/json"
	"testing"

	"github.com/multica-ai/multica/server/internal/researchrun"
)

const canonicalArtifactSubjectHash = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func canonicalArtifactFixture() CanonicalArtifactInput {
	return CanonicalArtifactInput{
		SubjectHash: canonicalArtifactSubjectHash,
		Subject:     SubjectInput{Environment: Environment{Documents: []Document{{ID: "doc-1", Family: "official"}, {ID: "doc-2", Family: "independent"}}}},
		Snapshot: researchrun.RunSnapshot{
			Sources: []researchrun.SourceSnapshotView{
				{ID: "source-2", Metadata: json.RawMessage(`{"evaluation_document_id":"doc-2","evaluation_subject_hash":"` + canonicalArtifactSubjectHash + `"}`), VerificationStatus: "verified"},
				{ID: "source-1", Metadata: json.RawMessage(`{"evaluation_document_id":"doc-1","evaluation_subject_hash":"` + canonicalArtifactSubjectHash + `"}`), VerificationStatus: "verified"},
			},
			Observations: []researchrun.Observation{
				{ID: "observation-1", SourceSnapshotID: "source-1", Datum: json.RawMessage(`{"evaluation_fact_key":"fact-a","evaluation_fact_value":"42"}`)},
				{ID: "observation-2", SourceSnapshotID: "source-2", Datum: json.RawMessage(`{"evaluation_fact_key":"fact-a","evaluation_fact_value":"42"}`)},
			},
			Claims: []researchrun.Claim{{ClientKey: "claim-a", Evidence: []researchrun.ClaimEvidence{{ObservationID: "observation-1"}, {ObservationID: "observation-2"}}}},
		},
		ReportClaimKeys: []string{"claim-a"}, ReportMD: "# Report",
	}
}

func TestBuildArtifactFromCanonicalRunMapsLedgerWithoutTextInference(t *testing.T) {
	artifact, err := BuildArtifactFromCanonicalRun(canonicalArtifactFixture())
	if err != nil {
		t.Fatal(err)
	}
	if len(artifact.Sources) != 2 || artifact.Sources[0].DocumentID != "doc-1" || !artifact.Sources[0].Accepted {
		t.Fatalf("sources=%+v", artifact.Sources)
	}
	if len(artifact.Facts) != 1 || artifact.Facts[0].Key != "fact-a" || len(artifact.Facts[0].SourceIDs) != 2 {
		t.Fatalf("facts=%+v", artifact.Facts)
	}
	if len(artifact.Claims) != 1 || !artifact.Claims[0].InReport || len(artifact.Claims[0].FactKeys) != 1 || len(artifact.Claims[0].SourceIDs) != 2 {
		t.Fatalf("claims=%+v", artifact.Claims)
	}
	if artifact.ReportMD != "# Report" {
		t.Fatalf("report=%q", artifact.ReportMD)
	}
	if err = ValidateArtifact(Case{Task: TaskSpec{ID: "case", Mode: ModeFactCheck, Goal: "goal", Language: "en", AllowedTools: []string{"documents"}}, Environment: canonicalArtifactFixture().Subject.Environment}, artifact); err != nil {
		t.Fatalf("canonical artifact does not satisfy the existing grader input contract: %v", err)
	}
}

func TestBuildArtifactFromCanonicalRunRejectsSubjectAndLedgerDrift(t *testing.T) {
	tests := []CanonicalArtifactInput{canonicalArtifactFixture(), canonicalArtifactFixture(), canonicalArtifactFixture(), canonicalArtifactFixture(), canonicalArtifactFixture(), canonicalArtifactFixture()}
	tests[0].Snapshot.Sources[0].Metadata = json.RawMessage(`{"evaluation_document_id":"missing","evaluation_subject_hash":"` + canonicalArtifactSubjectHash + `"}`)
	tests[1].Snapshot.Sources[0].Metadata = json.RawMessage(`{"evaluation_document_id":"doc-2","evaluation_subject_hash":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	tests[2].Snapshot.Observations[0].Datum = json.RawMessage(`{"value":"unkeyed prose"}`)
	tests[3].Snapshot.Observations[1].Datum = json.RawMessage(`{"evaluation_fact_key":"fact-a","evaluation_fact_value":"different"}`)
	tests[4].SubjectHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	tests[5].ReportClaimKeys = []string{"missing-claim"}
	for index, input := range tests {
		if _, err := BuildArtifactFromCanonicalRun(input); err == nil {
			t.Fatalf("case %d unexpectedly accepted", index)
		}
	}
}
