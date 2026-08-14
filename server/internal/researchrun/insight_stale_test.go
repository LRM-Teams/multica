package researchrun

import (
	"errors"
	"reflect"
	"testing"
)

func TestPlanInsightInvalidationPropagatesAndChoosesMinimalRoots(t *testing.T) {
	plan, err := PlanInsightInvalidation([]InsightDerivation{
		{InsightID: "i1", InputIDs: []string{"claim-a", "claim-b"}},
		{InsightID: "i2", InputIDs: []string{"claim-a", "claim-c"}},
		{InsightID: "i3", InputIDs: []string{"i1", "i2"}},
		{InsightID: "i4", InputIDs: []string{"i3", "claim-d"}},
		{InsightID: "unrelated", InputIDs: []string{"claim-z"}},
	}, []string{"claim-a", "claim-a"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"i1", "i2", "i3", "i4"}; !reflect.DeepEqual(plan.StaleInsightIDs, want) {
		t.Fatalf("stale=%v want %v", plan.StaleInsightIDs, want)
	}
	if want := []string{"i1", "i2"}; !reflect.DeepEqual(plan.ReintegrationRootIDs, want) {
		t.Fatalf("roots=%v want %v", plan.ReintegrationRootIDs, want)
	}
}

func TestPlanInsightInvalidationAcceptsInsightAsStartingArtifact(t *testing.T) {
	plan, err := PlanInsightInvalidation([]InsightDerivation{
		{InsightID: "i1", InputIDs: []string{"claim-a"}},
		{InsightID: "i2", InputIDs: []string{"i1"}},
		{InsightID: "i3", InputIDs: []string{"i2"}},
	}, []string{"i1"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"i2", "i3"}; !reflect.DeepEqual(plan.StaleInsightIDs, want) {
		t.Fatalf("stale=%v want %v", plan.StaleInsightIDs, want)
	}
	if want := []string{"i2"}; !reflect.DeepEqual(plan.ReintegrationRootIDs, want) {
		t.Fatalf("roots=%v want %v", plan.ReintegrationRootIDs, want)
	}
}

func TestPlanInsightInvalidationRejectsMalformedDAG(t *testing.T) {
	tests := []struct {
		name        string
		derivations []InsightDerivation
	}{
		{name: "cycle", derivations: []InsightDerivation{{InsightID: "i1", InputIDs: []string{"i2"}}, {InsightID: "i2", InputIDs: []string{"i1"}}}},
		{name: "duplicate insight", derivations: []InsightDerivation{{InsightID: "i1", InputIDs: []string{"c1"}}, {InsightID: "i1", InputIDs: []string{"c2"}}}},
		{name: "duplicate input", derivations: []InsightDerivation{{InsightID: "i1", InputIDs: []string{"c1", "c1"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := PlanInsightInvalidation(tt.derivations, []string{"c1"})
			if !errors.Is(err, ErrInvalidContract) {
				t.Fatalf("err=%v want ErrInvalidContract", err)
			}
		})
	}
}
