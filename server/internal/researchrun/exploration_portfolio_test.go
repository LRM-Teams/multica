package researchrun

import "testing"

func TestSelectExplorationPortfolioPreservesReservesAndDiversity(t *testing.T) {
	candidates := []ExplorationCandidate{
		{CandidateID: "probe", Purpose: "expansion", DivergenceProbe: true, ReasonableImpactPath: true, Score: ExplorationScore{Novelty: 1, DecisionImpact: .5}, Cost: ExplorationCost{Token: 1}},
		{CandidateID: "verify", TargetQuestionID: "q1", Purpose: "independent_verification", SourceFamilyKey: "official", MethodKey: "audit", PerspectiveKey: "p1", ProtectsHighImpactTargetID: "claim-major", Score: ExplorationScore{DecisionImpact: 1, ExpectedSuccessProbability: 1}, Cost: ExplorationCost{Token: 2}},
		{CandidateID: "same", TargetQuestionID: "q1", Purpose: "expansion", SourceFamilyKey: "official", MethodKey: "audit", PerspectiveKey: "p1", Score: ExplorationScore{DecisionImpact: .9}, Cost: ExplorationCost{Token: 1}},
		{CandidateID: "duplicate", Purpose: "deepening", DuplicateCandidateIDs: []string{"verify"}, Score: ExplorationScore{DecisionImpact: .8}, Cost: ExplorationCost{Token: 1}},
	}
	decision, err := SelectExplorationPortfolio(candidates, ExplorationBudget{Total: 10, IntegrationReserve: 1, ReviewReserve: 1, ReportReserve: 1, ExplorationReserve: 1, MaximumConcurrency: 4})
	if err != nil {
		t.Fatal(err)
	}
	if decision.PolicyVersion != ExplorationPortfolioPolicyV1 || len(decision.SelectedIDs) != 2 || decision.SelectedIDs[0] != "probe" || decision.SelectedIDs[1] != "verify" {
		t.Fatalf("decision=%+v", decision)
	}
	if decision.ExplorationSpent != 1 || decision.RegularSpent != 2 {
		t.Fatalf("spend=%+v", decision)
	}
}

func TestSelectExplorationPortfolioHardConstraintsCannotBeOutscored(t *testing.T) {
	decision, err := SelectExplorationPortfolio([]ExplorationCandidate{
		{CandidateID: "blocked", Purpose: "deepening", HardConstraintFailures: []string{"permission_denied"}, Score: ExplorationScore{DecisionImpact: 1, Novelty: 1, ExpectedInformationGain: 1}, Cost: ExplorationCost{Token: .1}},
		{CandidateID: "allowed", Purpose: "deepening", Score: ExplorationScore{DecisionImpact: .1}, Cost: ExplorationCost{Token: 1}},
	}, ExplorationBudget{Total: 5, IntegrationReserve: 1, ReviewReserve: 1, ReportReserve: 1, MaximumConcurrency: 2})
	if err != nil || len(decision.SelectedIDs) != 1 || decision.SelectedIDs[0] != "allowed" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
}

func TestSelectExplorationPortfolioRejectsReserveOvercommit(t *testing.T) {
	_, err := SelectExplorationPortfolio(nil, ExplorationBudget{Total: 2, IntegrationReserve: 1, ReviewReserve: 1, ReportReserve: 1, MaximumConcurrency: 1})
	if err == nil {
		t.Fatal("expected reserve validation error")
	}
}
