package researchrun

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDecodeAndValidateResultAcceptsEvidencePlan(t *testing.T) {
	result := validPlanResult(t)
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	decoded, hash, err := DecodeAndValidateResult(raw, Task{Kind: TaskKindPlan}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("DecodeAndValidateResult: %v", err)
	}
	if decoded.ClientRequestID != result.ClientRequestID || len(hash) != 64 {
		t.Fatalf("decoded=%+v hash=%q", decoded, hash)
	}
}

func TestDecodeAndValidateResultRejectsUnknownAndTrailingJSON(t *testing.T) {
	task := Task{Kind: TaskKindDiscover}
	cfg := DefaultRunConfig("standard")
	for name, raw := range map[string]string{
		"unknown field": `{"schema_version":1,"client_request_id":"request-1","summary":"","coverage_delta":0,"confidence":0.5,"unexpected":true}`,
		"second value":  `{"schema_version":1,"client_request_id":"request-1","summary":"","coverage_delta":0,"confidence":0.5} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := DecodeAndValidateResult([]byte(raw), task, cfg); !errors.Is(err, ErrInvalidResult) {
				t.Fatalf("error=%v, want ErrInvalidResult", err)
			}
		})
	}
}

func TestDecodeAndValidateResultRequiresExactQuoteSnapshot(t *testing.T) {
	result := ResultEnvelope{
		SchemaVersion:   1,
		ClientRequestID: "request-quote",
		Summary:         "evidence",
		Sources: []SourceProposal{{
			ClientKey: "source-1", URL: "https://example.com/paper", Title: "Paper",
			Publisher: "Example", SourceClass: "paper", IndependenceKey: "example-paper",
			RetrievedAt: time.Now().UTC(), SnapshotText: "The measured value was 42.",
		}},
		Observations:  []ObservationProposal{{ClientKey: "observation-1", SourceKey: "source-1", Quote: "measured value was 43"}},
		CoverageDelta: 0.1,
		Confidence:    0.7,
	}
	raw, _ := json.Marshal(result)
	if _, _, err := DecodeAndValidateResult(raw, Task{Kind: TaskKindDiscover}, DefaultRunConfig("standard")); !errors.Is(err, ErrInvalidResult) || !strings.Contains(err.Error(), "absent from source snapshot") {
		t.Fatalf("error=%v, want exact-quote rejection", err)
	}
}

func TestDecodeAndValidateResultRejectsCyclicTaskGraph(t *testing.T) {
	result := validPlanResult(t)
	result.Plan.Tasks = []TaskProposal{
		{ClientKey: "task-a", QuestionKey: "question-1", Kind: TaskKindDiscover, Objective: "A", RequiredCapability: "scout", ExpectedResult: "research_evidence_v1", Priority: 0.8, DependsOn: []string{"task-b"}},
		{ClientKey: "task-b", QuestionKey: "question-1", Kind: TaskKindVerify, Objective: "B", RequiredCapability: "validator", ExpectedResult: "research_evidence_v1", Priority: 0.8, DependsOn: []string{"task-a"}},
	}
	raw, _ := json.Marshal(result)
	if _, _, err := DecodeAndValidateResult(raw, Task{Kind: TaskKindPlan}, DefaultRunConfig("standard")); !errors.Is(err, ErrInvalidResult) || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("error=%v, want cycle rejection", err)
	}
}

func TestCanonicalURLNormalizesWithoutLosingQuery(t *testing.T) {
	got, err := CanonicalURL("HTTPS://Example.COM:443/path?q=1#fragment")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://example.com/path?q=1" {
		t.Fatalf("CanonicalURL=%q", got)
	}
}

func TestMeasuredInformationGainUsesOnlyNewNormalizedEvidence(t *testing.T) {
	if got := measuredInformationGain(AcceptResultOutcome{SourcesCreated: 1, ObservationsCreated: 2, ClaimsCreated: 1}); got != 0.06 {
		t.Fatalf("measuredInformationGain=%v, want 0.06", got)
	}
	if got := measuredInformationGain(AcceptResultOutcome{SourcesCreated: 100}); got != 1 {
		t.Fatalf("capped measuredInformationGain=%v, want 1", got)
	}
}

func validPlanResult(t *testing.T) ResultEnvelope {
	t.Helper()
	return ResultEnvelope{
		SchemaVersion:   1,
		ClientRequestID: "plan-request-1",
		Summary:         "plan",
		Plan: &PlanProposal{
			Questions: []QuestionProposal{{
				ClientKey: "question-1", Kind: QuestionKindDimension, Text: "What does the evidence show?",
				Required: true, Priority: 1, Impact: 1, Uncertainty: 0.8, Novelty: 0.7,
			}},
			Tasks: []TaskProposal{{
				ClientKey: "discover-1", QuestionKey: "question-1", Kind: TaskKindDiscover,
				Objective: "Find primary evidence", RequiredCapability: "scout",
				ExpectedResult: "research_evidence_v1", Priority: 1,
			}},
			InclusionCriteria: []string{"Directly relevant evidence"},
			ExclusionCriteria: []string{"Unverifiable summaries"},
			SourceStrategy:    []string{"Start with primary sources"},
			Uncertainties:     []string{"Evidence may be incomplete"},
			PlanningRisks:     []string{"Source access can fail"},
		},
		CoverageDelta: 0,
		Confidence:    0.7,
	}
}
