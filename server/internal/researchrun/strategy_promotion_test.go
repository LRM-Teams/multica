package researchrun

import "testing"

func promotionFixture() StrategyPromotionInput {
	return StrategyPromotionInput{Current: StrategyEvaluation{StrategyVersion: "v1", CorpusVersion: "c1", ModeScores: map[string]float64{"market": .8, "monitoring": .7}}, Candidate: StrategyEvaluation{StrategyVersion: "v2", CorpusVersion: "c1", SeedCount: 5, HistoricalReplayCount: 10, DeterministicInvariantsPassed: true, ModeScores: map[string]float64{"market": .81, "monitoring": .7}, Cost: 8, Latency: 9}, MinimumSeeds: 3, MinimumHistoricalReplays: 5, MaximumCost: 10, MaximumLatency: 10, ApproverUserID: "user", EvaluationRunID: "eval-1"}
}

func TestEvaluateStrategyPromotionRequiresEvidenceNonRegressionAndApproval(t *testing.T) {
	input := promotionFixture()
	decision, err := EvaluateStrategyPromotion(input)
	if err != nil || !decision.Promoted || decision.PreviousVersion != "v1" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
	input = promotionFixture()
	input.Candidate.ModeScores["monitoring"] = .6
	decision, _ = EvaluateStrategyPromotion(input)
	if decision.Promoted || decision.Reason != "research_mode_regressed:monitoring" {
		t.Fatalf("decision=%+v", decision)
	}
	input = promotionFixture()
	input.ApproverUserID = ""
	decision, _ = EvaluateStrategyPromotion(input)
	if decision.Promoted || decision.Reason != "promotion_approval_missing" {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestEvaluateStrategyPromotionRejectsMissingSafetyReplayAndComparableCorpus(t *testing.T) {
	input := promotionFixture()
	input.Candidate.DeterministicInvariantsPassed = false
	decision, _ := EvaluateStrategyPromotion(input)
	if decision.Reason != "safety_invariant_failed" {
		t.Fatalf("decision=%+v", decision)
	}
	input = promotionFixture()
	input.Candidate.HistoricalReplayCount = 0
	decision, _ = EvaluateStrategyPromotion(input)
	if decision.Reason != "insufficient_historical_replay" {
		t.Fatalf("decision=%+v", decision)
	}
	input = promotionFixture()
	input.Candidate.CorpusVersion = "other"
	decision, _ = EvaluateStrategyPromotion(input)
	if decision.Reason != "corpus_version_mismatch" {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestAssignStrategyToRunNeverMutatesStartedRun(t *testing.T) {
	assignment, err := AssignStrategyToRun(StrategyAssignment{RunID: "run", StrategyVersion: "v1", Started: true}, "v2")
	if err != nil || assignment.StrategyVersion != "v1" {
		t.Fatalf("assignment=%+v err=%v", assignment, err)
	}
	assignment, err = AssignStrategyToRun(StrategyAssignment{RunID: "new"}, "v2")
	if err != nil || assignment.StrategyVersion != "v2" || !assignment.Started {
		t.Fatalf("assignment=%+v err=%v", assignment, err)
	}
}

func TestRollbackStrategyRetainsProblemVersionAsPrevious(t *testing.T) {
	decision, err := RollbackStrategy("v2", "v1", "quality_threshold_breached")
	if err != nil || decision.NewVersion != "v1" || decision.PreviousVersion != "v2" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
}
