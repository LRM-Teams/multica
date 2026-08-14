package researchrun

import (
	"errors"
	"reflect"
	"testing"
)

func steeringImpactFixture() SteeringImpactInput {
	return SteeringImpactInput{
		Entities: []SteeringInquiryRef{
			{Kind: InquiryKindQuestion, ID: "q-changed"}, {Kind: InquiryKindQuestion, ID: "q-retained"},
			{Kind: InquiryKindHypothesis, ID: "h-changed"}, {Kind: InquiryKindHypothesis, ID: "h-dependent"}, {Kind: InquiryKindHypothesis, ID: "h-associated"},
		},
		Branches: []SteeringBranch{{ID: "branch-root"}, {ID: "branch-child", ParentID: "branch-root"}, {ID: "branch-retained"}},
		Edges: []SteeringInquiryEdge{
			{From: SteeringInquiryRef{Kind: InquiryKindQuestion, ID: "q-changed"}, To: SteeringInquiryRef{Kind: InquiryKindHypothesis, ID: "h-changed"}, Relation: InquiryRelationDecomposes},
			{From: SteeringInquiryRef{Kind: InquiryKindHypothesis, ID: "h-dependent"}, To: SteeringInquiryRef{Kind: InquiryKindHypothesis, ID: "h-changed"}, Relation: InquiryRelationDependsOn},
			{From: SteeringInquiryRef{Kind: InquiryKindHypothesis, ID: "h-changed"}, To: SteeringInquiryRef{Kind: InquiryKindHypothesis, ID: "h-associated"}, Relation: InquiryRelationTests},
		},
		Tasks: []SteeringTaskScope{
			{ID: "task-changed", Targets: []SteeringInquiryRef{{Kind: InquiryKindHypothesis, ID: "h-changed"}}},
			{ID: "task-downstream", Targets: []SteeringInquiryRef{{Kind: InquiryKindQuestion, ID: "q-retained"}}, DependsOn: []string{"task-changed"}},
			{ID: "task-retained", Targets: []SteeringInquiryRef{{Kind: InquiryKindHypothesis, ID: "h-associated"}}},
		},
		AffectedRoots: []SteeringInquiryRef{{Kind: InquiryKindQuestion, ID: "q-changed"}, {Kind: InquiryKindBranch, ID: "branch-root"}},
	}
}

func TestComputeSteeringImpactUsesMinimalStructuralClosure(t *testing.T) {
	impact, err := ComputeSteeringImpact(steeringImpactFixture())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(impact.AffectedBranches, []string{"branch-child", "branch-root"}) {
		t.Fatalf("branches=%v", impact.AffectedBranches)
	}
	if !reflect.DeepEqual(impact.AffectedTasks, []string{"task-changed", "task-downstream"}) {
		t.Fatalf("tasks=%v", impact.AffectedTasks)
	}
	if !reflect.DeepEqual(impact.RetainedTasks, []string{"task-retained"}) {
		t.Fatalf("retained=%v", impact.RetainedTasks)
	}
	wantEntities := []SteeringInquiryRef{{Kind: InquiryKindQuestion, ID: "q-changed"}, {Kind: InquiryKindHypothesis, ID: "h-changed"}, {Kind: InquiryKindHypothesis, ID: "h-dependent"}, {Kind: InquiryKindBranch, ID: "branch-child"}, {Kind: InquiryKindBranch, ID: "branch-root"}}
	if !reflect.DeepEqual(impact.AffectedEntities, wantEntities) {
		t.Fatalf("entities=%+v", impact.AffectedEntities)
	}
}

func TestComputeSteeringImpactDoesNotPropagateSemanticAssociationEdges(t *testing.T) {
	fixture := steeringImpactFixture()
	fixture.AffectedRoots = []SteeringInquiryRef{{Kind: InquiryKindHypothesis, ID: "h-changed"}}
	impact, err := ComputeSteeringImpact(fixture)
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range impact.AffectedEntities {
		if entity.ID == "h-associated" {
			t.Fatal("tests edge incorrectly expanded destructive impact")
		}
	}
	if !reflect.DeepEqual(impact.RetainedTasks, []string{"task-retained"}) {
		t.Fatalf("retained=%v", impact.RetainedTasks)
	}
}

func TestComputeSteeringImpactAcceptsBranchEntityWithRequiredParentMetadata(t *testing.T) {
	fixture := steeringImpactFixture()
	fixture.Entities = append(fixture.Entities, SteeringInquiryRef{Kind: InquiryKindBranch, ID: "branch-root"})

	impact, err := ComputeSteeringImpact(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(impact.AffectedBranches, []string{"branch-child", "branch-root"}) {
		t.Fatalf("branches=%v", impact.AffectedBranches)
	}
}

func TestComputeSteeringImpactFailsClosedOnUnknownOrMalformedGraph(t *testing.T) {
	for name, mutate := range map[string]func(*SteeringImpactInput){
		"unknown root": func(in *SteeringImpactInput) {
			in.AffectedRoots = []SteeringInquiryRef{{Kind: InquiryKindQuestion, ID: "missing"}}
		},
		"unknown task target":   func(in *SteeringImpactInput) { in.Tasks[0].Targets[0].ID = "missing" },
		"unknown dependency":    func(in *SteeringImpactInput) { in.Tasks[0].DependsOn = []string{"missing"} },
		"missing branch parent": func(in *SteeringImpactInput) { in.Branches[1].ParentID = "missing" },
		"missing branch metadata": func(in *SteeringImpactInput) {
			in.Entities = append(in.Entities, SteeringInquiryRef{Kind: InquiryKindBranch, ID: "metadata-missing"})
		},
		"branch cycle": func(in *SteeringImpactInput) { in.Branches[0].ParentID = "branch-child" },
		"task cycle":   func(in *SteeringImpactInput) { in.Tasks[0].DependsOn = []string{"task-downstream"} },
	} {
		t.Run(name, func(t *testing.T) {
			fixture := steeringImpactFixture()
			mutate(&fixture)
			if _, err := ComputeSteeringImpact(fixture); !errors.Is(err, ErrInvalidContract) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}
