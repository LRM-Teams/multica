package researchrun

import (
	"errors"
	"testing"
)

func validInquiryBatch() inquiryBatchIntent {
	low, high := 0.2, 0.8
	return inquiryBatchIntent{
		ExistingEndpoints: []inquiryKeyRef{
			{Kind: InquiryKindQuestion, Key: "question.root"},
			{Kind: InquiryKindClaim, Key: "claim.one"},
		},
		Hypotheses: []inquiryHypothesisIntent{{
			ClientKey: "hypothesis.one", QuestionKey: "question.root", Statement: "A testable statement",
			ExpectedObservations: []string{"Expected observation"}, WeakeningConditions: []string{"Weakening condition"},
			ConfidenceLow: &low, ConfidenceHigh: &high,
		}},
		Branches: []inquiryBranchIntent{
			{ClientKey: "branch.root", Objective: "Investigate", EntryConditions: []string{"Start"}, ExitConditions: []string{"Finish"}, BudgetShare: 0.5},
			{ClientKey: "branch.child", ParentBranchKey: "branch.root", Objective: "Deepen", EntryConditions: []string{"Evidence"}, ExitConditions: []string{"Resolved"}, BudgetShare: 0.25},
		},
		Insights: []inquiryInsightIntent{{
			ClientKey: "insight.one", Title: "Synthesis", Summary: "A new combined explanation",
			Inputs:   []inquiryKeyRef{{Kind: InquiryKindHypothesis, Key: "hypothesis.one"}, {Kind: InquiryKindClaim, Key: "claim.one"}},
			Relation: "integrates", SemanticValue: "new_explanation",
		}},
		Edges: []inquiryEdgeIntent{{
			ClientKey: "edge.one",
			From:      inquiryKeyRef{Kind: InquiryKindQuestion, Key: "question.root"},
			To:        inquiryKeyRef{Kind: InquiryKindHypothesis, Key: "hypothesis.one"},
			Relation:  InquiryRelationDecomposes, Rationale: "Makes the question testable",
		}},
	}
}

func TestInquiryModuleValidateBatchAcceptsForwardReferences(t *testing.T) {
	if err := (inquiryModule{}).ValidateBatch(validInquiryBatch()); err != nil {
		t.Fatalf("ValidateBatch: %v", err)
	}
}

func TestInquiryModuleValidateBatchFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*inquiryBatchIntent)
	}{
		{name: "invalid key", mutate: func(b *inquiryBatchIntent) { b.Hypotheses[0].ClientKey = "bad key" }},
		{name: "duplicate key", mutate: func(b *inquiryBatchIntent) { b.Hypotheses = append(b.Hypotheses, b.Hypotheses[0]) }},
		{name: "unknown question", mutate: func(b *inquiryBatchIntent) { b.Hypotheses[0].QuestionKey = "question.missing" }},
		{name: "self parent", mutate: func(b *inquiryBatchIntent) { b.Branches[1].ParentBranchKey = "branch.child" }},
		{name: "unknown insight input", mutate: func(b *inquiryBatchIntent) { b.Insights[0].Inputs[1].Key = "claim.missing" }},
		{name: "duplicate insight input", mutate: func(b *inquiryBatchIntent) { b.Insights[0].Inputs[1] = b.Insights[0].Inputs[0] }},
		{name: "unknown edge endpoint", mutate: func(b *inquiryBatchIntent) { b.Edges[0].To.Key = "hypothesis.missing" }},
		{name: "reserved dispute", mutate: func(b *inquiryBatchIntent) {
			b.ExistingEndpoints = append(b.ExistingEndpoints, inquiryKeyRef{Kind: InquiryKindDispute, Key: "dispute.one"})
		}},
		{name: "reversed confidence", mutate: func(b *inquiryBatchIntent) {
			low, high := 0.9, 0.1
			b.Hypotheses[0].ConfidenceLow, b.Hypotheses[0].ConfidenceHigh = &low, &high
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			batch := validInquiryBatch()
			tc.mutate(&batch)
			if err := (inquiryModule{}).ValidateBatch(batch); !errors.Is(err, ErrInvalidContract) {
				t.Fatalf("ValidateBatch err=%v want ErrInvalidContract", err)
			}
		})
	}
}
