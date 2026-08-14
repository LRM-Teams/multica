package researchrun

import (
	"fmt"
	"math"
	"sort"
)

const ExplorationPortfolioPolicyV1 = "exploration-portfolio-v1"

type ExplorationScore struct {
	DecisionImpact             float64
	UncertaintyReduction       float64
	ExpectedInformationGain    float64
	RequiredQuestionCoverage   float64
	DisputeDiscrimination      float64
	SourceIndependenceGain     float64
	MethodRequirement          float64
	Novelty                    float64
	ExpectedSuccessProbability float64
	DuplicateOverlap           float64
	DependencyProviderRisk     float64
}

type ExplorationCost struct {
	Time  float64
	Token float64
	Tool  float64
	Human float64
}

type ExplorationCandidate struct {
	CandidateID                string
	TargetQuestionID           string
	Purpose                    string
	SourceFamilyKey            string
	MethodKey                  string
	PerspectiveKey             string
	Score                      ExplorationScore
	Cost                       ExplorationCost
	HardConstraintFailures     []string
	DuplicateCandidateIDs      []string
	ProtectsHighImpactTargetID string
	DivergenceProbe            bool
	ReasonableImpactPath       bool
}

type ExplorationBudget struct {
	Total              float64
	IntegrationReserve float64
	ReviewReserve      float64
	ReportReserve      float64
	ExplorationReserve float64
	MaximumConcurrency int
}

type ExplorationCandidateDecision struct {
	CandidateID            string
	Score                  ExplorationScore
	Cost                   ExplorationCost
	HardConstraintFailures []string
	Utility                float64
	TotalCost              float64
	Selected               bool
	Reason                 string
}

type ExplorationPortfolioDecision struct {
	PolicyVersion    string
	Candidates       []ExplorationCandidateDecision
	SelectedIDs      []string
	RegularSpent     float64
	ExplorationSpent float64
}

func SelectExplorationPortfolio(candidates []ExplorationCandidate, budget ExplorationBudget) (ExplorationPortfolioDecision, error) {
	if err := validateExplorationBudget(budget); err != nil {
		return ExplorationPortfolioDecision{}, err
	}
	decision := ExplorationPortfolioDecision{PolicyVersion: ExplorationPortfolioPolicyV1}
	type rankedCandidate struct {
		candidate ExplorationCandidate
		utility   float64
		cost      float64
	}
	ranked := make([]rankedCandidate, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if err := validateExplorationCandidate(candidate); err != nil {
			return ExplorationPortfolioDecision{}, err
		}
		if _, exists := seen[candidate.CandidateID]; exists {
			return ExplorationPortfolioDecision{}, fmt.Errorf("%w: duplicate exploration candidate %q", ErrInvalidContract, candidate.CandidateID)
		}
		seen[candidate.CandidateID] = struct{}{}
		cost := explorationCost(candidate.Cost)
		ranked = append(ranked, rankedCandidate{candidate: candidate, utility: explorationUtility(candidate.Score), cost: cost})
	}
	sort.Slice(ranked, func(i, j int) bool {
		iRatio := ranked[i].utility / math.Max(ranked[i].cost, 0.01)
		jRatio := ranked[j].utility / math.Max(ranked[j].cost, 0.01)
		if iRatio != jRatio {
			return iRatio > jRatio
		}
		return ranked[i].candidate.CandidateID < ranked[j].candidate.CandidateID
	})

	regularAvailable := budget.Total - budget.IntegrationReserve - budget.ReviewReserve - budget.ReportReserve - budget.ExplorationReserve
	selected := make(map[string]ExplorationCandidate)
	reasons := make(map[string]string, len(candidates))
	utilities := make(map[string]float64, len(candidates))
	costs := make(map[string]float64, len(candidates))
	for _, item := range ranked {
		utilities[item.candidate.CandidateID] = item.utility
		costs[item.candidate.CandidateID] = item.cost
		if len(item.candidate.HardConstraintFailures) > 0 {
			reasons[item.candidate.CandidateID] = "hard_constraint_failed"
		}
	}

	// Spend the protected reserve on at least one bounded, plausible probe.
	if budget.ExplorationReserve > 0 {
		for _, item := range ranked {
			candidate := item.candidate
			if reasons[candidate.CandidateID] != "" || !candidate.DivergenceProbe || !candidate.ReasonableImpactPath {
				continue
			}
			if item.cost <= budget.ExplorationReserve && len(selected) < budget.MaximumConcurrency {
				selected[candidate.CandidateID] = candidate
				decision.ExplorationSpent += item.cost
				reasons[candidate.CandidateID] = "selected_exploration_reserve"
				break
			}
		}
	}

	// High-impact conclusions retain an independent verification or
	// counterevidence path before discretionary work consumes the budget.
	protectedTargets := make(map[string]struct{})
	for _, item := range ranked {
		candidate := item.candidate
		if candidate.ProtectsHighImpactTargetID == "" || (candidate.Purpose != "independent_verification" && candidate.Purpose != "counterevidence") {
			continue
		}
		if _, protected := protectedTargets[candidate.ProtectsHighImpactTargetID]; protected || reasons[candidate.CandidateID] != "" {
			continue
		}
		if len(selected) >= budget.MaximumConcurrency || decision.RegularSpent+item.cost > regularAvailable || overlapsSelected(candidate, selected) || lacksParallelDiversity(candidate, selected) {
			continue
		}
		selected[candidate.CandidateID] = candidate
		protectedTargets[candidate.ProtectsHighImpactTargetID] = struct{}{}
		decision.RegularSpent += item.cost
		reasons[candidate.CandidateID] = "selected_high_impact_protection"
	}

	for _, item := range ranked {
		candidate := item.candidate
		if reasons[candidate.CandidateID] != "" || len(selected) >= budget.MaximumConcurrency {
			continue
		}
		if decision.RegularSpent+item.cost > regularAvailable {
			reasons[candidate.CandidateID] = "insufficient_unreserved_budget"
			continue
		}
		if overlapsSelected(candidate, selected) {
			reasons[candidate.CandidateID] = "duplicate_overlap"
			continue
		}
		if lacksParallelDiversity(candidate, selected) {
			reasons[candidate.CandidateID] = "parallel_work_not_distinct"
			continue
		}
		selected[candidate.CandidateID] = candidate
		decision.RegularSpent += item.cost
		reasons[candidate.CandidateID] = "selected_by_policy"
	}

	for _, candidate := range candidates {
		if candidate.ProtectsHighImpactTargetID == "" {
			continue
		}
		if _, ok := selected[candidate.CandidateID]; ok {
			continue
		}
		if !hasHighImpactProtection(candidate.ProtectsHighImpactTargetID, selected) && reasons[candidate.CandidateID] == "" {
			reasons[candidate.CandidateID] = "high_impact_protection_not_selected"
		}
	}

	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.CandidateID)
	}
	sort.Strings(ids)
	for _, id := range ids {
		_, isSelected := selected[id]
		reason := reasons[id]
		if reason == "" {
			reason = "lower_rank_or_concurrency_limit"
		}
		var candidate ExplorationCandidate
		for _, item := range candidates {
			if item.CandidateID == id {
				candidate = item
				break
			}
		}
		decision.Candidates = append(decision.Candidates, ExplorationCandidateDecision{
			CandidateID: id, Score: candidate.Score, Cost: candidate.Cost,
			HardConstraintFailures: append([]string(nil), candidate.HardConstraintFailures...),
			Utility:                utilities[id], TotalCost: costs[id], Selected: isSelected, Reason: reason,
		})
		if isSelected {
			decision.SelectedIDs = append(decision.SelectedIDs, id)
		}
	}
	return decision, nil
}

func explorationUtility(score ExplorationScore) float64 {
	benefit := score.DecisionImpact + score.UncertaintyReduction + score.ExpectedInformationGain + score.RequiredQuestionCoverage + score.DisputeDiscrimination + score.SourceIndependenceGain + score.MethodRequirement + score.Novelty + score.ExpectedSuccessProbability
	return benefit - score.DuplicateOverlap - score.DependencyProviderRisk
}

func explorationCost(cost ExplorationCost) float64 {
	return cost.Time + cost.Token + cost.Tool + cost.Human
}

func validateExplorationBudget(budget ExplorationBudget) error {
	if budget.Total < 0 || budget.IntegrationReserve < 0 || budget.ReviewReserve < 0 || budget.ReportReserve < 0 || budget.ExplorationReserve < 0 || budget.MaximumConcurrency <= 0 {
		return fmt.Errorf("%w: exploration budget values are invalid", ErrInvalidContract)
	}
	if budget.IntegrationReserve+budget.ReviewReserve+budget.ReportReserve+budget.ExplorationReserve > budget.Total {
		return fmt.Errorf("%w: exploration reserves exceed total budget", ErrInvalidContract)
	}
	return nil
}

func validateExplorationCandidate(candidate ExplorationCandidate) error {
	if candidate.CandidateID == "" || candidate.Purpose == "" {
		return fmt.Errorf("%w: exploration candidate identity and purpose are required", ErrInvalidContract)
	}
	values := []float64{candidate.Score.DecisionImpact, candidate.Score.UncertaintyReduction, candidate.Score.ExpectedInformationGain, candidate.Score.RequiredQuestionCoverage, candidate.Score.DisputeDiscrimination, candidate.Score.SourceIndependenceGain, candidate.Score.MethodRequirement, candidate.Score.Novelty, candidate.Score.ExpectedSuccessProbability, candidate.Score.DuplicateOverlap, candidate.Score.DependencyProviderRisk}
	for _, value := range values {
		if math.IsNaN(value) || value < 0 || value > 1 {
			return fmt.Errorf("%w: exploration score components must be in [0,1]", ErrInvalidContract)
		}
	}
	costs := []float64{candidate.Cost.Time, candidate.Cost.Token, candidate.Cost.Tool, candidate.Cost.Human}
	for _, cost := range costs {
		if math.IsNaN(cost) || math.IsInf(cost, 0) || cost < 0 {
			return fmt.Errorf("%w: exploration costs must be finite and non-negative", ErrInvalidContract)
		}
	}
	return nil
}

func overlapsSelected(candidate ExplorationCandidate, selected map[string]ExplorationCandidate) bool {
	for _, duplicateID := range candidate.DuplicateCandidateIDs {
		if _, exists := selected[duplicateID]; exists {
			return true
		}
	}
	for _, selectedCandidate := range selected {
		for _, duplicateID := range selectedCandidate.DuplicateCandidateIDs {
			if duplicateID == candidate.CandidateID {
				return true
			}
		}
	}
	return false
}

func lacksParallelDiversity(candidate ExplorationCandidate, selected map[string]ExplorationCandidate) bool {
	if candidate.TargetQuestionID == "" {
		return false
	}
	for _, other := range selected {
		if other.TargetQuestionID == candidate.TargetQuestionID && other.SourceFamilyKey == candidate.SourceFamilyKey && other.MethodKey == candidate.MethodKey && other.PerspectiveKey == candidate.PerspectiveKey {
			return true
		}
	}
	return false
}

func hasHighImpactProtection(targetID string, selected map[string]ExplorationCandidate) bool {
	for _, candidate := range selected {
		if candidate.ProtectsHighImpactTargetID == targetID && (candidate.Purpose == "independent_verification" || candidate.Purpose == "counterevidence") {
			return true
		}
	}
	return false
}
