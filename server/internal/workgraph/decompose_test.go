package workgraph

import "testing"

func TestNormalizeDecomposeAcceptsParallelRootsAndJoin(t *testing.T) {
	in := DecomposeInput{Reason: "parallel research then synthesis", Nodes: []IssuePlanNode{
		{TempID: "source-a", Title: "Research A", AssigneeID: "agent-a"},
		{TempID: "source-b", Title: "Research B", AssigneeID: "agent-a"},
		{TempID: "merge", Title: "Synthesize", AssigneeID: "agent-b", DependsOn: []string{"source-a", "source-b"}},
	}}
	if _, err := normalizeDecompose(in); err != nil {
		t.Fatalf("normalizeDecompose() error = %v", err)
	}
}

func TestNormalizeDecomposeRejectsCyclesAndMissingDependencies(t *testing.T) {
	tests := []DecomposeInput{
		{Reason: "cycle", Nodes: []IssuePlanNode{
			{TempID: "a", Title: "A", AssigneeID: "agent", DependsOn: []string{"b"}},
			{TempID: "b", Title: "B", AssigneeID: "agent", DependsOn: []string{"a"}},
		}},
		{Reason: "missing", Nodes: []IssuePlanNode{
			{TempID: "a", Title: "A", AssigneeID: "agent"},
			{TempID: "b", Title: "B", AssigneeID: "agent", DependsOn: []string{"unknown"}},
		}},
	}
	for _, in := range tests {
		if _, err := normalizeDecompose(in); err == nil {
			t.Fatalf("normalizeDecompose(%q) unexpectedly succeeded", in.Reason)
		}
	}
}
