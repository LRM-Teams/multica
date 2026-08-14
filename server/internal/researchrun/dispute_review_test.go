package researchrun

import (
	"reflect"
	"testing"
)

func disputeReviewFixture() DisputeReviewInput {
	return DisputeReviewInput{
		DisputeID: "d1", SubjectArtifactID: "subject", Kind: "method",
		Positions: []DisputeReviewPosition{
			{PositionID: "p1", AuthorAgentID: "a1", ClaimIDs: []string{"c1"}, ScopeHash: "s1"},
			{PositionID: "p2", AuthorAgentID: "a2", ClaimIDs: []string{"c2"}, ScopeHash: "s2"},
		},
	}
}

func TestPlanDisputeReviewCreatesBlindIndependentWork(t *testing.T) {
	tasks, err := PlanDisputeReview(disputeReviewFixture())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 4 {
		t.Fatalf("tasks=%+v", tasks)
	}
	if want := []string{"a1", "a2"}; !reflect.DeepEqual(tasks[0].ExcludedAgentIDs, want) {
		t.Fatalf("excluded=%v want %v", tasks[0].ExcludedAgentIDs, want)
	}
	if want := []string{"c1", "subject"}; !reflect.DeepEqual(tasks[0].VisibleArtifactIDs, want) {
		t.Fatalf("position review visible=%v want %v", tasks[0].VisibleArtifactIDs, want)
	}
	if tasks[0].TargetPositionID != "p1" || tasks[1].TargetPositionID != "p2" {
		t.Fatalf("position tasks=%+v", tasks[:2])
	}
	if tasks[2].RequiredCapability != "methodologist" || tasks[3].Purpose != "collect_evidence_that_distinguishes_positions" {
		t.Fatalf("special tasks=%+v", tasks[2:])
	}
}

func TestBuildAdjudicatorContextOnlyIncludesAcceptedArtifacts(t *testing.T) {
	context, err := BuildAdjudicatorContext(disputeReviewFixture(), []AcceptedDisputeEvidence{
		{EvidenceID: "e2", Accepted: true}, {EvidenceID: "rejected", Accepted: false}, {EvidenceID: "e1", Accepted: true},
	}, []DisputeMethodReview{{ArtifactID: "method-rejected", Accepted: false}, {ArtifactID: "method-ok", Accepted: true}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"e1", "e2"}; !reflect.DeepEqual(context.AcceptedEvidenceIDs, want) {
		t.Fatalf("evidence=%v want %v", context.AcceptedEvidenceIDs, want)
	}
	if want := []string{"method-ok"}; !reflect.DeepEqual(context.AcceptedMethodReviewIDs, want) {
		t.Fatalf("method reviews=%v want %v", context.AcceptedMethodReviewIDs, want)
	}
}

func TestPlanDisputeReviewDoesNotCreateMethodologistForOtherKinds(t *testing.T) {
	input := disputeReviewFixture()
	input.Kind = "logical"
	tasks, err := PlanDisputeReview(input)
	if err != nil || len(tasks) != 3 {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
}
