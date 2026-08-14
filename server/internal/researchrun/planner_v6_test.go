package researchrun

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const v6ContractHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func validV6PlannerContext() V6PlannerValidationContext {
	return V6PlannerValidationContext{GoalVersion: 3, ContractRevisionHash: v6ContractHash, AuthorizedBranchBudget: .7}
}

func validV6PlannerResult() V6PlannerResult {
	return V6PlannerResult{
		ContractKind: "plan_result", SchemaVersion: 6, ClientRequestID: "10000000-0000-4000-8000-000000000001", Summary: "Investigate competing explanations.",
		Method:     json.RawMessage(`{"decision_question":"Which explanation best fits?"}`),
		Questions:  []QuestionProposal{{ClientKey: "q-root", Kind: QuestionKindDimension, Text: "What explains the outcome?", Required: true, Priority: 1, Impact: 1, Uncertainty: .8, Novelty: .7}},
		Hypotheses: []V6HypothesisSeed{{ClientKey: "h-market", QuestionKey: "q-root", Statement: "Market structure explains it.", Applicability: json.RawMessage(`{"market":"current"}`), ExpectedObservations: []string{"concentration"}, WeakeningConditions: []string{"fragmented supply"}}},
		Branches:   []V6BranchSeed{{ClientKey: "b-market", Objective: "Test market explanation.", EntryConditions: []string{"method accepted"}, ExitConditions: []string{"hypothesis resolved"}, BudgetShare: .7}},
		Edges: []V6InquiryEdgeSeed{
			{ClientKey: "edge-question-hypothesis", From: V6EntityRef{Kind: "question", Key: "q-root"}, To: V6EntityRef{Kind: "hypothesis", Key: "h-market"}, Relation: "decomposes", Rationale: "Candidate explanation."},
			{ClientKey: "edge-hypothesis-branch", From: V6EntityRef{Kind: "hypothesis", Key: "h-market"}, To: V6EntityRef{Kind: "branch", Key: "b-market"}, Relation: "tests", Rationale: "Branch tests hypothesis."},
		},
		Tasks:       []V6PlannerTask{{ClientKey: "discover-market", Kind: "discover", Objective: "Find market evidence.", RequiredCapability: "scout", ExpectedResult: "research_evidence_v6", Priority: .9, Targets: []V6EntityRef{{Kind: "hypothesis", Key: "h-market"}, {Kind: "branch", Key: "b-market"}}}},
		SearchPlans: []V6SearchPlanSeed{{ClientKey: "search-market", Targets: []V6EntityRef{{Kind: "branch", Key: "b-market"}}, Adapter: "web_search_v1", QueryStrategy: "Find independent primary sources.", InclusionCriteria: []string{"direct evidence"}, ExclusionCriteria: []string{"unsourced summary"}, StoppingConditions: []string{"two independent families"}, StrategyVersion: "strategy-v1"}},
	}
}

func TestDecodeAndValidateV6PlannerResult(t *testing.T) {
	raw, err := json.Marshal(validV6PlannerResult())
	if err != nil {
		t.Fatal(err)
	}
	decoded, hash, err := DecodeAndValidateV6PlannerResult(raw, DefaultRunConfig("standard"), validV6PlannerContext())
	if err != nil {
		t.Fatalf("DecodeAndValidateV6PlannerResult: %v", err)
	}
	if decoded.ContractKind != "plan_result" || len(hash) != 64 {
		t.Fatalf("decoded=%+v hash=%q", decoded, hash)
	}
	_, replayHash, err := DecodeAndValidateV6PlannerResult(raw, DefaultRunConfig("standard"), validV6PlannerContext())
	if err != nil || replayHash != hash {
		t.Fatalf("canonical replay hash=%q err=%v, want %q", replayHash, err, hash)
	}
}

func TestDecodeAndValidateV6PlannerResultRejectsUnknownField(t *testing.T) {
	raw, _ := json.Marshal(validV6PlannerResult())
	raw = []byte(strings.Replace(string(raw), `"summary":`, `"future_field":true,"summary":`, 1))
	if _, _, err := DecodeAndValidateV6PlannerResult(raw, DefaultRunConfig("standard"), validV6PlannerContext()); !errors.Is(err, ErrInvalidResult) || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("err=%v want strict unknown-field rejection", err)
	}
}

func TestV6PlannerResultFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*V6PlannerResult, *V6PlannerValidationContext)
	}{
		{name: "invalid assigned Contract", mutate: func(_ *V6PlannerResult, c *V6PlannerValidationContext) { c.ContractRevisionHash = "bad" }},
		{name: "missing method", mutate: func(r *V6PlannerResult, _ *V6PlannerValidationContext) { r.Method = nil }},
		{name: "null required hypotheses", mutate: func(r *V6PlannerResult, _ *V6PlannerValidationContext) { r.Hypotheses = nil }},
		{name: "key pattern", mutate: func(r *V6PlannerResult, _ *V6PlannerValidationContext) { r.Questions[0].ClientKey = "bad key" }},
		{name: "unknown hypothesis question", mutate: func(r *V6PlannerResult, _ *V6PlannerValidationContext) { r.Hypotheses[0].QuestionKey = "q-missing" }},
		{name: "branch budget overflow", mutate: func(r *V6PlannerResult, _ *V6PlannerValidationContext) {
			r.Branches = append(r.Branches, V6BranchSeed{ClientKey: "b-two", Objective: "Second.", EntryConditions: []string{"open"}, ExitConditions: []string{"done"}, BudgetShare: .4})
		}},
		{name: "orphan branch", mutate: func(r *V6PlannerResult, c *V6PlannerValidationContext) {
			c.AuthorizedBranchBudget = 1
			r.Branches = append(r.Branches, V6BranchSeed{ClientKey: "b-orphan", Objective: "Orphan.", EntryConditions: []string{"open"}, ExitConditions: []string{"done"}, BudgetShare: .2})
		}},
		{name: "dependency cycle", mutate: func(r *V6PlannerResult, _ *V6PlannerValidationContext) {
			r.Edges = append(r.Edges, V6InquiryEdgeSeed{ClientKey: "edge-cycle", From: V6EntityRef{Kind: "hypothesis", Key: "h-market"}, To: V6EntityRef{Kind: "question", Key: "q-root"}, Relation: "depends_on", Rationale: "Invalid cycle."})
		}},
		{name: "unknown task target", mutate: func(r *V6PlannerResult, _ *V6PlannerValidationContext) { r.Tasks[0].Targets[0].Key = "h-missing" }},
		{name: "legacy task result", mutate: func(r *V6PlannerResult, _ *V6PlannerValidationContext) {
			r.Tasks[0].ExpectedResult = "research_evidence_v5"
		}},
		{name: "unknown Search Plan target", mutate: func(r *V6PlannerResult, _ *V6PlannerValidationContext) { r.SearchPlans[0].Targets[0].Key = "b-missing" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, context := validV6PlannerResult(), validV6PlannerContext()
			test.mutate(&result, &context)
			if err := result.Validate(DefaultRunConfig("standard"), context); !errors.Is(err, ErrInvalidResult) {
				t.Fatalf("Validate err=%v want ErrInvalidResult", err)
			}
		})
	}
}
