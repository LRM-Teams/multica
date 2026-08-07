package workgraph

import (
	"encoding/json"
	"errors"
	"testing"
)

func validCreateInput() CreateInput {
	return CreateInput{
		WorkspaceID:    "11111111-1111-1111-1111-111111111111",
		AnchorKind:     AnchorChannelGoal,
		AnchorID:       "22222222-2222-2222-2222-222222222222",
		Admission:      AdmissionGraph,
		Reason:         "parallel delivery",
		ActorType:      "agent",
		ActorID:        "33333333-3333-3333-3333-333333333333",
		IdempotencyKey: "44444444-4444-4444-4444-444444444444",
		Nodes: []NodeSpec{
			{TempID: "backend", IssueID: "55555555-5555-5555-5555-555555555555", Role: "worker"},
			{TempID: "verify", IssueID: "66666666-6666-6666-6666-666666666666", Role: "verifier", ContextPolicy: "blind", DependsOn: []string{"backend"}},
		},
	}
}

func TestNormalizeCreateRejectsCyclesAndUnknownDependencies(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*CreateInput)
	}{
		{"cycle", func(in *CreateInput) { in.Nodes[0].DependsOn = []string{"verify"} }},
		{"unknown dependency", func(in *CreateInput) { in.Nodes[1].DependsOn = []string{"missing"} }},
		{"duplicate temp id", func(in *CreateInput) { in.Nodes[1].TempID = "backend" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := validCreateInput()
			tc.mutate(&in)
			if _, err := normalizeCreate(in); !errors.Is(err, ErrInvalidGraph) {
				t.Fatalf("err=%v want ErrInvalidGraph", err)
			}
		})
	}
}

func TestNormalizeCreateDefaultsBoundedContextAndBudgets(t *testing.T) {
	in, err := normalizeCreate(validCreateInput())
	if err != nil {
		t.Fatal(err)
	}
	if in.Nodes[0].ContextPolicy != "bounded" {
		t.Fatalf("context=%q", in.Nodes[0].ContextPolicy)
	}
	if string(in.Nodes[0].Budget) != `{}` || string(in.BudgetPolicy) != `{}` {
		t.Fatalf("defaults budget=%s policy=%s", in.Nodes[0].Budget, in.BudgetPolicy)
	}
}

func TestCreateDigestExcludesDeliveryIdentityButIncludesPlan(t *testing.T) {
	a := validCreateInput()
	b := validCreateInput()
	b.IdempotencyKey = "77777777-7777-7777-7777-777777777777"
	b.ActorID = "88888888-8888-8888-8888-888888888888"
	if digestCreate(a) != digestCreate(b) {
		t.Fatal("delivery identity changed semantic digest")
	}
	b.Nodes[0].Budget = json.RawMessage(`{"tokens":10}`)
	if digestCreate(a) == digestCreate(b) {
		t.Fatal("plan change did not change digest")
	}
}
