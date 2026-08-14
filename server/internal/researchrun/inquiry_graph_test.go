package researchrun

import (
	"errors"
	"testing"
)

const (
	graphQuestionID = "10000000-0000-4000-8000-000000000001"
	graphBranchAID  = "20000000-0000-4000-8000-000000000001"
	graphBranchBID  = "20000000-0000-4000-8000-000000000002"
	graphHypothesis = "30000000-0000-4000-8000-000000000001"
)

func validResolvedInquiryGraph() inquiryResolvedGraph {
	return inquiryResolvedGraph{
		Entities: []inquiryEndpoint{
			{Kind: InquiryKindQuestion, ID: graphQuestionID},
			{Kind: InquiryKindBranch, ID: graphBranchAID},
			{Kind: InquiryKindBranch, ID: graphBranchBID},
			{Kind: InquiryKindHypothesis, ID: graphHypothesis},
		},
		Branches: []inquiryResolvedBranch{
			{ID: graphBranchAID, BudgetShare: 0.6},
			{ID: graphBranchBID, ParentID: graphBranchAID, BudgetShare: 0.4},
		},
		Edges: []inquiryEdgeCommand{
			{From: inquiryEndpoint{Kind: InquiryKindQuestion, ID: graphQuestionID}, To: inquiryEndpoint{Kind: InquiryKindHypothesis, ID: graphHypothesis}, Relation: InquiryRelationDecomposes, Rationale: "test"},
			{From: inquiryEndpoint{Kind: InquiryKindHypothesis, ID: graphHypothesis}, To: inquiryEndpoint{Kind: InquiryKindBranch, ID: graphBranchBID}, Relation: InquiryRelationTests, Rationale: "explore"},
		},
		AuthorizedBudgetShare: 1,
	}
}

func TestInquiryModuleValidateResolvedGraph(t *testing.T) {
	if err := (inquiryModule{}).ValidateResolvedGraph(validResolvedInquiryGraph()); err != nil {
		t.Fatalf("ValidateResolvedGraph: %v", err)
	}
}

func TestInquiryModuleValidateResolvedGraphFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*inquiryResolvedGraph)
	}{
		{name: "missing branch", mutate: func(g *inquiryResolvedGraph) { g.Branches = nil }},
		{name: "budget overflow", mutate: func(g *inquiryResolvedGraph) { g.AuthorizedBudgetShare = 0.9 }},
		{name: "unknown parent", mutate: func(g *inquiryResolvedGraph) { g.Branches[1].ParentID = "20000000-0000-4000-8000-000000000099" }},
		{name: "parent cycle", mutate: func(g *inquiryResolvedGraph) { g.Branches[0].ParentID = graphBranchBID }},
		{name: "duplicate entity", mutate: func(g *inquiryResolvedGraph) { g.Entities = append(g.Entities, g.Entities[0]) }},
		{name: "edge outside graph", mutate: func(g *inquiryResolvedGraph) { g.Edges[0].To.ID = "30000000-0000-4000-8000-000000000099" }},
		{name: "duplicate edge", mutate: func(g *inquiryResolvedGraph) { g.Edges = append(g.Edges, g.Edges[0]) }},
		{name: "mixed relation dependency cycle", mutate: func(g *inquiryResolvedGraph) {
			g.Edges = []inquiryEdgeCommand{
				{From: inquiryEndpoint{Kind: InquiryKindQuestion, ID: graphQuestionID}, To: inquiryEndpoint{Kind: InquiryKindHypothesis, ID: graphHypothesis}, Relation: InquiryRelationDecomposes},
				{From: inquiryEndpoint{Kind: InquiryKindHypothesis, ID: graphHypothesis}, To: inquiryEndpoint{Kind: InquiryKindBranch, ID: graphBranchAID}, Relation: InquiryRelationDependsOn},
				{From: inquiryEndpoint{Kind: InquiryKindBranch, ID: graphBranchAID}, To: inquiryEndpoint{Kind: InquiryKindQuestion, ID: graphQuestionID}, Relation: InquiryRelationRefines},
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			graph := validResolvedInquiryGraph()
			tc.mutate(&graph)
			if err := (inquiryModule{}).ValidateResolvedGraph(graph); !errors.Is(err, ErrInvalidContract) {
				t.Fatalf("ValidateResolvedGraph err=%v want ErrInvalidContract", err)
			}
		})
	}
}
