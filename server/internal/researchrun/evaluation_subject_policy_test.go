package researchrun

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

const evaluationPolicyHash = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func evaluationPolicyFixture() json.RawMessage {
	return json.RawMessage(`{"evaluation_subject":{"schema_version":"research-eval-subject-v1","subject_hash":"` + evaluationPolicyHash + `","document_ids":["doc-1"]}}`)
}

func evaluationResultFixture() ResultEnvelope {
	return ResultEnvelope{
		Sources: []SourceProposal{{
			ClientKey: "source-1", URL: "https://evaluation.invalid/doc-1", Title: "Controlled document", Publisher: "evaluation",
			SourceClass: "controlled", IndependenceKey: "doc-1", RetrievedAt: time.Now().UTC(), SnapshotText: "The registered value is 42.",
			Metadata: json.RawMessage(`{"evaluation_document_id":"doc-1","evaluation_subject_hash":"` + evaluationPolicyHash + `"}`),
		}},
		Observations: []ObservationProposal{{ClientKey: "observation-1", SourceKey: "source-1", Datum: json.RawMessage(`{"evaluation_fact_key":"registered-value","evaluation_fact_value":"42"}`)}},
	}
}

func TestValidateEvaluationSubjectResultBindsSourcesAndFacts(t *testing.T) {
	if err := validateEvaluationSubjectResult(evaluationPolicyFixture(), evaluationResultFixture()); err != nil {
		t.Fatal(err)
	}
	if err := validateEvaluationSubjectResult(json.RawMessage(`{"prefer_primary":true}`), ResultEnvelope{}); err != nil {
		t.Fatalf("ordinary source policy changed behavior: %v", err)
	}
}

func TestValidateEvaluationSubjectResultRejectsAgentForgedIdentity(t *testing.T) {
	tests := []ResultEnvelope{evaluationResultFixture(), evaluationResultFixture(), evaluationResultFixture(), evaluationResultFixture()}
	tests[0].Sources[0].Metadata = json.RawMessage(`{"evaluation_document_id":"other","evaluation_subject_hash":"` + evaluationPolicyHash + `"}`)
	tests[1].Sources[0].Metadata = json.RawMessage(`{"evaluation_document_id":"doc-1","evaluation_subject_hash":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	tests[2].Sources[0].Metadata = json.RawMessage(`{"evaluation_document_id":"doc-1","evaluation_subject_hash":"` + evaluationPolicyHash + `","oracle":true}`)
	tests[3].Observations[0].Datum = json.RawMessage(`{"value":"42"}`)
	for index, result := range tests {
		if err := validateEvaluationSubjectResult(evaluationPolicyFixture(), result); !errors.Is(err, ErrInvalidResult) {
			t.Fatalf("case %d error=%v", index, err)
		}
	}
}

func TestParseEvaluationSubjectPolicyRejectsMalformedFrozenContract(t *testing.T) {
	policies := []string{
		`{"evaluation_subject":{"schema_version":"future","subject_hash":"` + evaluationPolicyHash + `","document_ids":["doc-1"]}}`,
		`{"evaluation_subject":{"schema_version":"research-eval-subject-v1","subject_hash":"bad","document_ids":["doc-1"]}}`,
		`{"evaluation_subject":{"schema_version":"research-eval-subject-v1","subject_hash":"` + evaluationPolicyHash + `","document_ids":["doc-1"],"oracle":{}}}`,
	}
	for index, policy := range policies {
		if _, _, err := parseEvaluationSubjectPolicy(json.RawMessage(policy)); !errors.Is(err, ErrInvalidContract) {
			t.Fatalf("case %d error=%v", index, err)
		}
	}
}

func TestResultAcceptanceModuleAppliesEvaluationFenceBeforeMaterialization(t *testing.T) {
	store, submission := validResultAcceptanceFixture(t)
	store.contract.SourcePolicy = evaluationPolicyFixture()
	result := validPlanResult(t)
	result.Sources = evaluationResultFixture().Sources
	result.Observations = evaluationResultFixture().Observations
	result.Sources[0].Metadata = json.RawMessage(`{"evaluation_document_id":"forged","evaluation_subject_hash":"` + evaluationPolicyHash + `"}`)
	submission.Raw, _ = json.Marshal(result)

	_, err := (resultAcceptanceModule{store: store}).Accept(t.Context(), submission)
	if !errors.Is(err, ErrInvalidResult) || !strings.Contains(err.Error(), "unapproved document") || store.accepted != nil {
		t.Fatalf("error=%v accepted=%+v", err, store.accepted)
	}
}
