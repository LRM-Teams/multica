package researcheval

import (
	"encoding/json"
	"strings"
	"testing"
)

func subjectFixture() SubjectInput {
	return SubjectInput{
		Task: TaskSpec{ID: "case-1", Mode: ModeFactCheck, Goal: "Verify the controlled facts", Language: "en", AllowedTools: []string{"documents", "documents"}, Tags: []string{"regression"}},
		Environment: Environment{
			Documents: []Document{
				{ID: "doc-b", Family: "independent", Title: "B", Traits: []string{"primary"}, Content: "second"},
				{ID: "doc-a", Family: "official", Title: "A", Traits: []string{"record"}, Content: "first"},
			},
			Faults: []Fault{{Kind: "retrieval_failure", TargetID: "doc-b", Trigger: "first_read"}},
		},
	}
}

func TestSealSubjectInputIsStableAndContainsNoOracle(t *testing.T) {
	first, err := SealSubjectInput(subjectFixture(), 7)
	if err != nil {
		t.Fatal(err)
	}
	reordered := subjectFixture()
	reordered.Environment.Documents[0], reordered.Environment.Documents[1] = reordered.Environment.Documents[1], reordered.Environment.Documents[0]
	second, err := SealSubjectInput(reordered, 7)
	if err != nil {
		t.Fatal(err)
	}
	if first.SubjectHash != second.SubjectHash {
		t.Fatalf("equivalent subjects produced different hashes: %q != %q", first.SubjectHash, second.SubjectHash)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "oracle") {
		t.Fatalf("sealed subject leaked Oracle surface: %s", encoded)
	}
	decoded, err := DecodeSealedSubjectInput(encoded)
	if err != nil || decoded.SubjectHash != first.SubjectHash {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
}

func TestDecodeSealedSubjectInputRejectsOracleUnknownFieldsAndTampering(t *testing.T) {
	sealed, err := SealSubjectInput(subjectFixture(), 3)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(sealed)
	withOracle := strings.TrimSuffix(string(encoded), "}") + `,"oracle":{"required_facts":[]}}`
	if _, err = DecodeSealedSubjectInput([]byte(withOracle)); err == nil {
		t.Fatal("expected Oracle field rejection")
	}
	sealed.Input.Task.Goal = "tampered"
	tampered, _ := json.Marshal(sealed)
	if _, err = DecodeSealedSubjectInput(tampered); err == nil {
		t.Fatal("expected content/hash mismatch rejection")
	}
}

func TestSealSubjectInputRejectsInvalidControlledEnvironment(t *testing.T) {
	tests := []SubjectInput{subjectFixture(), subjectFixture(), subjectFixture(), subjectFixture()}
	tests[0].Task.AllowedTools = nil
	tests[1].Environment.Documents[1].ID = "doc-b"
	tests[2].Environment.Faults[0].TargetID = "missing"
	tests[3].Environment.Documents[0].Content = strings.Repeat("x", maximumDocumentBytes+1)
	for index, input := range tests {
		if _, err := SealSubjectInput(input, 1); err == nil {
			t.Fatalf("case %d unexpectedly accepted", index)
		}
	}
}
