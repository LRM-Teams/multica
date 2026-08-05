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

func TestResearchRunV2RejectsPlaceholderReport(t *testing.T) {
	raw := []byte(`{
		"schema_version":2,
		"client_request_id":"report-request-2",
		"summary":"report",
		"report":{
			"content_md":"结论：可行。",
			"structured":{"schema_version":1,"title":"结论","outline":[],"sections":[],"citations":[],"sources":[],"conclusion":"可行。"},
			"claims":[{"claim_key":"claim-1","section_id":"conclusion","anchor_quote":"可行"}]
		},
		"coverage_delta":0,
		"confidence":0.8
	}`)
	_, _, err := DecodeAndValidateResultForVersion("research-run-v2", raw, Task{Kind: TaskKindSynthesize, RequiredCapability: "reporter", ExpectedResult: "research_report_v2"}, DefaultRunConfig("deep"))
	if !errors.Is(err, ErrInvalidResult) || !strings.Contains(err.Error(), "report") {
		t.Fatalf("error=%v, want placeholder report rejection", err)
	}
}

func TestResearchRunV2RequiresCanonicalDeliveryRoles(t *testing.T) {
	result := validPlanResult(t)
	result.SchemaVersion = 2
	result.Plan.Tasks[0].ExpectedResult = "research_evidence_v2"
	result.Plan.Tasks = append(result.Plan.Tasks,
		TaskProposal{ClientKey: "synthesize", Kind: TaskKindSynthesize, Objective: "Write report", RequiredCapability: "lead", ExpectedResult: "research_report_v2", Priority: 0.7},
		TaskProposal{ClientKey: "quality", Kind: TaskKindQualityGate, Objective: "Review report", RequiredCapability: "lead", ExpectedResult: "research_quality_evaluation_v2", Priority: 0.6, DependsOn: []string{"synthesize"}},
		TaskProposal{ClientKey: "citations", Kind: TaskKindCitationAudit, Objective: "Audit citations", RequiredCapability: "lead", ExpectedResult: "research_citation_audit_v2", Priority: 0.6, DependsOn: []string{"synthesize"}},
	)
	raw, _ := json.Marshal(result)
	_, _, err := DecodeAndValidateResultForVersion("research-run-v2", raw, Task{Kind: TaskKindPlan, RequiredCapability: "lead", ExpectedResult: "research_plan_v2"}, DefaultRunConfig("standard"))
	if !errors.Is(err, ErrInvalidResult) || !strings.Contains(err.Error(), "reporter") {
		t.Fatalf("error=%v, want canonical reporter role rejection", err)
	}
}

func TestResearchRunV2RequiresDeliveryTasksInsidePlan(t *testing.T) {
	result := validPlanResult(t)
	result.SchemaVersion = 2
	result.Plan.Tasks[0].ExpectedResult = "research_evidence_v2"
	result.ProposedTasks = []TaskProposal{
		{ClientKey: "synthesize", Kind: TaskKindSynthesize, Objective: "Write report", RequiredCapability: "reporter", ExpectedResult: "research_report_v2", Priority: 0.7},
		{ClientKey: "quality", Kind: TaskKindQualityGate, Objective: "Review report", RequiredCapability: "validator", ExpectedResult: "research_quality_evaluation_v2", Priority: 0.6, DependsOn: []string{"synthesize"}},
		{ClientKey: "citations", Kind: TaskKindCitationAudit, Objective: "Audit citations", RequiredCapability: "validator", ExpectedResult: "research_citation_audit_v2", Priority: 0.6, DependsOn: []string{"synthesize"}},
	}
	raw, _ := json.Marshal(result)
	_, _, err := DecodeAndValidateResultForVersion(OrchestratorVersionV2, raw, Task{Kind: TaskKindPlan, RequiredCapability: "lead", ExpectedResult: "research_plan_v2"}, DefaultRunConfig("standard"))
	if !errors.Is(err, ErrInvalidResult) || !strings.Contains(err.Error(), "v2 plan requires a synthesize") {
		t.Fatalf("error=%v, want plan-local delivery task rejection", err)
	}
}

func TestResearchRunV1ContractRemainsAccepted(t *testing.T) {
	result := validPlanResult(t)
	raw, _ := json.Marshal(result)
	if _, _, err := DecodeAndValidateResultForVersion(OrchestratorVersionV1, raw, Task{Kind: TaskKindPlan}, DefaultRunConfig("standard")); err != nil {
		t.Fatalf("v1 contract changed: %v", err)
	}
}

func TestResearchRunV3RequiresCompleteGeneralResearchMethod(t *testing.T) {
	result := validV3PlanResult(t)
	result.Plan.Method = nil
	raw, _ := json.Marshal(result)
	_, _, err := DecodeAndValidateResultForVersion(OrchestratorVersionV3, raw, Task{
		Kind: TaskKindPlan, RequiredCapability: "lead", ExpectedResult: "research_plan_v3",
	}, DefaultRunConfig("standard"))
	if !errors.Is(err, ErrInvalidResult) || !strings.Contains(err.Error(), "research method") {
		t.Fatalf("error=%v, want missing method rejection", err)
	}

	result = validV3PlanResult(t)
	result.Plan.Method.CounterevidenceStrategy = nil
	raw, _ = json.Marshal(result)
	_, _, err = DecodeAndValidateResultForVersion(OrchestratorVersionV3, raw, Task{
		Kind: TaskKindPlan, RequiredCapability: "lead", ExpectedResult: "research_plan_v3",
	}, DefaultRunConfig("standard"))
	if !errors.Is(err, ErrInvalidResult) || !strings.Contains(err.Error(), "counterevidence_strategy") {
		t.Fatalf("error=%v, want incomplete method rejection", err)
	}
}

func TestResearchRunV3AcceptsNonAcademicMethodAndPinsOlderContracts(t *testing.T) {
	result := validV3PlanResult(t)
	raw, _ := json.Marshal(result)
	decoded, _, err := DecodeAndValidateResultForVersion(OrchestratorVersionV3, raw, Task{
		Kind: TaskKindPlan, RequiredCapability: "lead", ExpectedResult: "research_plan_v3",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("v3 method rejected: %v", err)
	}
	if decoded.Plan.Method.DecisionQuestion != "Which option best satisfies the operating constraints?" {
		t.Fatalf("decoded method=%+v", decoded.Plan.Method)
	}

	v2 := validPlanResult(t)
	v2.SchemaVersion = 2
	v2.Plan.Tasks[0].ExpectedResult = "research_evidence_v2"
	v2.Plan.Tasks = append(v2.Plan.Tasks,
		TaskProposal{ClientKey: "synthesize", Kind: TaskKindSynthesize, Objective: "Write report", RequiredCapability: "reporter", ExpectedResult: "research_report_v2", Priority: 0.7},
		TaskProposal{ClientKey: "quality", Kind: TaskKindQualityGate, Objective: "Review report", RequiredCapability: "validator", ExpectedResult: "research_quality_evaluation_v2", Priority: 0.6, DependsOn: []string{"synthesize"}},
		TaskProposal{ClientKey: "citations", Kind: TaskKindCitationAudit, Objective: "Audit citations", RequiredCapability: "validator", ExpectedResult: "research_citation_audit_v2", Priority: 0.6, DependsOn: []string{"synthesize"}},
	)
	v2Raw, _ := json.Marshal(v2)
	if _, _, err = DecodeAndValidateResultForVersion(OrchestratorVersionV2, v2Raw, Task{
		Kind: TaskKindPlan, RequiredCapability: "lead", ExpectedResult: "research_plan_v2",
	}, DefaultRunConfig("standard")); err != nil {
		t.Fatalf("v2 contract changed: %v", err)
	}

	v2.Plan.Method = result.Plan.Method
	v2Raw, _ = json.Marshal(v2)
	if _, _, err = DecodeAndValidateResultForVersion(OrchestratorVersionV2, v2Raw, Task{
		Kind: TaskKindPlan, RequiredCapability: "lead", ExpectedResult: "research_plan_v2",
	}, DefaultRunConfig("standard")); !errors.Is(err, ErrInvalidResult) || !strings.Contains(err.Error(), "schema_version 3") {
		t.Fatalf("v2 accepted v3 method: %v", err)
	}
}

func TestResearchRunV3RejectsV2TaskResultKinds(t *testing.T) {
	result := validV3PlanResult(t)
	result.Plan.Tasks[0].ExpectedResult = "research_evidence_v2"
	raw, _ := json.Marshal(result)
	_, _, err := DecodeAndValidateResultForVersion(OrchestratorVersionV3, raw, Task{
		Kind: TaskKindPlan, RequiredCapability: "lead", ExpectedResult: "research_plan_v3",
	}, DefaultRunConfig("standard"))
	if !errors.Is(err, ErrInvalidResult) || !strings.Contains(err.Error(), "research_evidence_v3") {
		t.Fatalf("error=%v, want v3 result-kind rejection", err)
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

func validV3PlanResult(t *testing.T) ResultEnvelope {
	t.Helper()
	result := validPlanResult(t)
	result.SchemaVersion = 3
	result.Plan.Method = &MethodProposal{
		DecisionQuestion:        "Which option best satisfies the operating constraints?",
		MethodRationale:         "Compare observed outcomes against explicit constraints and test the assumptions that could reverse the decision.",
		AnalysisMethods:         []string{"Constraint-based comparison", "Risk and sensitivity analysis"},
		EvidenceRequirements:    []string{"Comparable measurements for each option", "Traceable evidence for material risks"},
		CounterevidenceStrategy: []string{"Search for failure cases and evidence that reverses the ranking"},
		StoppingConditions:      []string{"Every required question has verified support and material counterevidence is resolved"},
	}
	result.Plan.Tasks[0].ExpectedResult = "research_evidence_v3"
	result.Plan.Tasks = append(result.Plan.Tasks,
		TaskProposal{ClientKey: "synthesize", Kind: TaskKindSynthesize, Objective: "Write decision report", RequiredCapability: "reporter", ExpectedResult: "research_report_v3", Priority: 0.7, DependsOn: []string{"discover-1"}},
		TaskProposal{ClientKey: "quality", Kind: TaskKindQualityGate, Objective: "Review report", RequiredCapability: "validator", ExpectedResult: "research_quality_evaluation_v3", Priority: 0.6, DependsOn: []string{"synthesize"}},
		TaskProposal{ClientKey: "citations", Kind: TaskKindCitationAudit, Objective: "Audit evidence links", RequiredCapability: "validator", ExpectedResult: "research_citation_audit_v3", Priority: 0.6, DependsOn: []string{"synthesize"}},
	)
	return result
}
