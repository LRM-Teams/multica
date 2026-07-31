package handler

import "testing"

func TestProductRoundBudgetForTier(t *testing.T) {
	cases := []struct {
		tier string
		want int32
	}{
		{researchDepthShallow, 2},
		{"SHALLOW", 2},
		{researchDepthStandard, 5},
		{"", 5},
		{"weird", 5},
		{researchDepthDeep, 10},
	}
	for _, tc := range cases {
		if got := productRoundBudgetForTier(tc.tier); got != tc.want {
			t.Fatalf("tier %q: got %d want %d", tc.tier, got, tc.want)
		}
	}
}

func TestResolveProductRoundDecisionHardCap(t *testing.T) {
	cases := []struct {
		name      string
		requested string
		round     int32
		budget    int32
		want      string
		forced    bool
	}{
		{"continue under cap", researchDecisionContinue, 3, 5, researchDecisionContinue, false},
		{"continue at cap forced", researchDecisionContinue, 5, 5, researchDecisionStopBudget, true},
		{"continue over cap forced", researchDecisionContinue, 6, 5, researchDecisionStopBudget, true},
		{"stop_enough at cap ok", researchDecisionStopEnough, 5, 5, researchDecisionStopEnough, false},
		{"stop_budget explicit", researchDecisionStopBudget, 2, 5, researchDecisionStopBudget, false},
		{"shallow continue at 2", researchDecisionContinue, 2, 2, researchDecisionStopBudget, true},
		{"deep continue at 9", researchDecisionContinue, 9, 10, researchDecisionContinue, false},
		{"invalid", "maybe", 1, 5, "", false},
	}
	for _, tc := range cases {
		got, forced := resolveProductRoundDecision(tc.requested, tc.round, tc.budget)
		if got != tc.want || forced != tc.forced {
			t.Fatalf("%s: got (%q,%v) want (%q,%v)", tc.name, got, forced, tc.want, tc.forced)
		}
	}
}

func TestNormalizeResearchDepthTier(t *testing.T) {
	if normalizeResearchDepthTier("deep") != researchDepthDeep {
		t.Fatal("deep")
	}
	if normalizeResearchDepthTier("shallow") != researchDepthShallow {
		t.Fatal("shallow")
	}
	if normalizeResearchDepthTier("") != researchDepthStandard {
		t.Fatal("default standard")
	}
}
