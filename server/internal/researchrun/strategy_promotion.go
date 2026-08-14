package researchrun

import "fmt"

type StrategyEvaluation struct {
	StrategyVersion               string
	CorpusVersion                 string
	SeedCount                     int
	HistoricalReplayCount         int
	DeterministicInvariantsPassed bool
	ModeScores                    map[string]float64
	Cost                          float64
	Latency                       float64
}

type StrategyPromotionInput struct {
	Current                  StrategyEvaluation
	Candidate                StrategyEvaluation
	MinimumSeeds             int
	MinimumHistoricalReplays int
	MaximumCost              float64
	MaximumLatency           float64
	ApproverUserID           string
	EvaluationRunID          string
}

type StrategyPromotionDecision struct {
	Promoted        bool
	NewVersion      string
	PreviousVersion string
	Reason          string
}

func EvaluateStrategyPromotion(input StrategyPromotionInput) (StrategyPromotionDecision, error) {
	decision := StrategyPromotionDecision{NewVersion: input.Candidate.StrategyVersion, PreviousVersion: input.Current.StrategyVersion}
	if input.Current.StrategyVersion == "" || input.Candidate.StrategyVersion == "" || input.Current.CorpusVersion == "" || input.Candidate.CorpusVersion == "" || input.MinimumSeeds <= 1 || input.MinimumHistoricalReplays <= 0 || input.EvaluationRunID == "" {
		return decision, fmt.Errorf("%w: Strategy Promotion input is incomplete", ErrInvalidContract)
	}
	if input.Current.CorpusVersion != input.Candidate.CorpusVersion {
		decision.Reason = "corpus_version_mismatch"
		return decision, nil
	}
	if input.Candidate.SeedCount < input.MinimumSeeds {
		decision.Reason = "insufficient_seed_count"
		return decision, nil
	}
	if input.Candidate.HistoricalReplayCount < input.MinimumHistoricalReplays {
		decision.Reason = "insufficient_historical_replay"
		return decision, nil
	}
	if !input.Candidate.DeterministicInvariantsPassed {
		decision.Reason = "safety_invariant_failed"
		return decision, nil
	}
	if input.Candidate.Cost > input.MaximumCost || input.Candidate.Latency > input.MaximumLatency {
		decision.Reason = "cost_or_latency_limit_exceeded"
		return decision, nil
	}
	for mode, baseline := range input.Current.ModeScores {
		candidate, exists := input.Candidate.ModeScores[mode]
		if !exists || candidate < baseline {
			decision.Reason = "research_mode_regressed:" + mode
			return decision, nil
		}
	}
	if input.ApproverUserID == "" {
		decision.Reason = "promotion_approval_missing"
		return decision, nil
	}
	decision.Promoted, decision.Reason = true, "offline_evaluation_and_approval_passed"
	return decision, nil
}

type StrategyAssignment struct {
	RunID           string
	StrategyVersion string
	Started         bool
}

func AssignStrategyToRun(existing StrategyAssignment, defaultVersion string) (StrategyAssignment, error) {
	if existing.RunID == "" || defaultVersion == "" {
		return StrategyAssignment{}, fmt.Errorf("%w: Run and default Strategy are required", ErrInvalidContract)
	}
	if existing.Started {
		if existing.StrategyVersion == "" {
			return StrategyAssignment{}, fmt.Errorf("%w: started Run lacks pinned Strategy", ErrInvalidContract)
		}
		return existing, nil
	}
	existing.StrategyVersion, existing.Started = defaultVersion, true
	return existing, nil
}

func RollbackStrategy(currentVersion, previousVersion, reason string) (StrategyPromotionDecision, error) {
	if currentVersion == "" || previousVersion == "" || reason == "" || currentVersion == previousVersion {
		return StrategyPromotionDecision{}, fmt.Errorf("%w: Strategy rollback requires current, previous, and reason", ErrInvalidContract)
	}
	return StrategyPromotionDecision{Promoted: true, NewVersion: previousVersion, PreviousVersion: currentVersion, Reason: "rollback:" + reason}, nil
}
