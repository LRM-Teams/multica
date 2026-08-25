package workgraph

import "testing"

func TestNormalizeDecomposeAcceptsParallelRootsAndJoin(t *testing.T) {
	in := DecomposeInput{Reason: "parallel research then synthesis", Nodes: []IssuePlanNode{
		{TempID: "source-a", Title: "Research A", AcceptanceCriteria: []string{"sources recorded"}, AssigneeID: "agent-a"},
		{TempID: "source-b", Title: "Research B", AcceptanceCriteria: []string{"sources recorded"}, AssigneeID: "agent-a"},
		{TempID: "merge", Title: "Synthesize", AcceptanceCriteria: []string{"result reconciled"}, AssigneeID: "agent-b", DependsOn: []string{"source-a", "source-b"}},
	}}
	if _, err := normalizeDecompose(in); err != nil {
		t.Fatalf("normalizeDecompose() error = %v", err)
	}
}

func TestNormalizeDecomposeRejectsCyclesAndMissingDependencies(t *testing.T) {
	tests := []DecomposeInput{
		{Reason: "cycle", Nodes: []IssuePlanNode{
			{TempID: "a", Title: "A", AcceptanceCriteria: []string{"done"}, AssigneeID: "agent", DependsOn: []string{"b"}},
			{TempID: "b", Title: "B", AcceptanceCriteria: []string{"done"}, AssigneeID: "agent", DependsOn: []string{"a"}},
		}},
		{Reason: "missing", Nodes: []IssuePlanNode{
			{TempID: "a", Title: "A", AcceptanceCriteria: []string{"done"}, AssigneeID: "agent"},
			{TempID: "b", Title: "B", AcceptanceCriteria: []string{"done"}, AssigneeID: "agent", DependsOn: []string{"unknown"}},
		}},
	}
	for _, in := range tests {
		if _, err := normalizeDecompose(in); err == nil {
			t.Fatalf("normalizeDecompose(%q) unexpectedly succeeded", in.Reason)
		}
	}
}

func TestNormalizeDecomposeRequiresReasonForDerivedAgent(t *testing.T) {
	in := DecomposeInput{Reason: "isolated implementation", Nodes: []IssuePlanNode{
		{TempID: "a", Title: "A", AcceptanceCriteria: []string{"done"}, AssigneeID: "agent", WorkerMode: WorkerModeDerivedAgent},
		{TempID: "b", Title: "B", AcceptanceCriteria: []string{"done"}, AssigneeID: "agent"},
	}}
	if _, err := normalizeDecompose(in); err == nil {
		t.Fatal("derived agent without clone_reason unexpectedly accepted")
	}
	in.Nodes[0].CloneReason = "independent implementation must not share mutable memory"
	out, err := normalizeDecompose(in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Nodes[1].WorkerMode != WorkerModeReuseAgent {
		t.Fatalf("default worker mode=%q", out.Nodes[1].WorkerMode)
	}
}

func TestNormalizeDecomposeRequiresAcceptanceCriteria(t *testing.T) {
	in := DecomposeInput{Reason: "missing completion contract", Nodes: []IssuePlanNode{
		{TempID: "a", Title: "A", AssigneeID: "agent"},
		{TempID: "b", Title: "B", AcceptanceCriteria: []string{"verified"}, AssigneeID: "agent"},
	}}
	if _, err := normalizeDecompose(in); err == nil {
		t.Fatal("node without acceptance criteria unexpectedly accepted")
	}
}
