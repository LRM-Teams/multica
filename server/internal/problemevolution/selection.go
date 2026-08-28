package problemevolution

import (
	"math"
	"sort"
)

// Candidate lanes. The platform owns this taxonomy so the graph stays readable
// regardless of what the external evolver calls its internal strategies.
const (
	LaneBaseline  = "baseline"
	LaneDiverse   = "diverse"
	LaneRepair    = "repair"
	LaneChallenge = "challenge"
	LaneCrossover = "crossover"
)

// Lineage relations between candidates.
const (
	RelationDerivedFrom = "derived_from"
	RelationRepairOf    = "repair_of"
	RelationChallengeOf = "challenge_of"
	RelationCrossoverOf = "crossover_of"
	RelationSynthesisOf = "synthesis_of"
)

// SelectionInput is one candidate as the selector sees it.
type SelectionInput struct {
	CandidateRef    string
	Status          string
	Score           *Score
	BehaviorProfile *BehaviorProfile
	Cost            float64
	RuntimeSeconds  float64
}

// SelectionResult is the platform's decision for one generation.
type SelectionResult struct {
	EliteRefs  []string
	PrunedRefs []string
	BestRef    string
}

// LaneForRelation maps a lineage relation onto the lane a child belongs to, so
// a candidate's lane cannot disagree with how it was produced.
func LaneForRelation(relation string) string {
	switch relation {
	case RelationRepairOf:
		return LaneRepair
	case RelationChallengeOf:
		return LaneChallenge
	case RelationCrossoverOf, RelationSynthesisOf:
		return LaneCrossover
	case RelationDerivedFrom:
		return LaneDiverse
	default:
		return LaneBaseline
	}
}

// IsKnownRelation reports whether a lineage relation is part of the taxonomy.
func IsKnownRelation(relation string) bool {
	switch relation {
	case RelationDerivedFrom, RelationRepairOf, RelationChallengeOf,
		RelationCrossoverOf, RelationSynthesisOf:
		return true
	default:
		return false
	}
}

// SelectElite picks the candidates that survive a generation.
//
// Ranking is score first, but ties are broken by behavioral complementarity
// rather than by cost alone: two candidates that fail in the same way are worth
// less to the next generation than two that fail differently, even when their
// totals match. Candidates failing a hard gate are never elite — a fluent wrong
// answer must not seed the next generation.
func SelectElite(candidates []SelectionInput, eliteCount int) SelectionResult {
	if eliteCount <= 0 {
		eliteCount = 1
	}
	scored := make([]SelectionInput, 0, len(candidates))
	result := SelectionResult{}
	for _, candidate := range candidates {
		if candidate.Score == nil || !isSelectableStatus(candidate.Status) {
			result.PrunedRefs = append(result.PrunedRefs, candidate.CandidateRef)
			continue
		}
		if !candidate.Score.HardGatePassed {
			result.PrunedRefs = append(result.PrunedRefs, candidate.CandidateRef)
			continue
		}
		scored = append(scored, candidate)
	}
	sort.SliceStable(scored, func(left, right int) bool {
		leftScore := scored[left].Score.Total
		rightScore := scored[right].Score.Total
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		if scored[left].Cost != scored[right].Cost {
			return scored[left].Cost < scored[right].Cost
		}
		return scored[left].CandidateRef < scored[right].CandidateRef
	})
	if len(scored) == 0 {
		return result
	}
	result.BestRef = scored[0].CandidateRef
	elite := []SelectionInput{scored[0]}
	remaining := scored[1:]
	for len(elite) < eliteCount && len(remaining) > 0 {
		pickIndex := 0
		bestGain := math.Inf(-1)
		for i, candidate := range remaining {
			gain := candidate.Score.Total + complementarity(elite, candidate)
			if gain > bestGain {
				bestGain = gain
				pickIndex = i
			}
		}
		elite = append(elite, remaining[pickIndex])
		remaining = append(remaining[:pickIndex:pickIndex], remaining[pickIndex+1:]...)
	}
	for _, candidate := range elite {
		result.EliteRefs = append(result.EliteRefs, candidate.CandidateRef)
	}
	for _, candidate := range remaining {
		result.PrunedRefs = append(result.PrunedRefs, candidate.CandidateRef)
	}
	return result
}

func isSelectableStatus(status string) bool {
	switch status {
	case "selectable", "elite", "selected":
		return true
	default:
		return false
	}
}

// complementarity scores how differently a candidate behaves from the already
// selected elite set, on the same 0..1 scale as a score so the two can be added.
func complementarity(elite []SelectionInput, candidate SelectionInput) float64 {
	if candidate.BehaviorProfile == nil || len(candidate.BehaviorProfile.Entries) == 0 {
		return 0
	}
	minDistance := math.Inf(1)
	for _, selected := range elite {
		if selected.BehaviorProfile == nil {
			continue
		}
		distance := behaviorDistance(*selected.BehaviorProfile, *candidate.BehaviorProfile)
		if distance < minDistance {
			minDistance = distance
		}
	}
	if math.IsInf(minDistance, 1) {
		return 0
	}
	return minDistance
}

// behaviorDistance is the mean absolute difference over the union of axes,
// which keeps the result within 0..1 for unit-interval profiles.
func behaviorDistance(left, right BehaviorProfile) float64 {
	values := make(map[string][2]float64)
	for _, entry := range left.Entries {
		values[entry.Key] = [2]float64{entry.Value, 0}
	}
	for _, entry := range right.Entries {
		current := values[entry.Key]
		current[1] = entry.Value
		values[entry.Key] = current
	}
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, pair := range values {
		sum += math.Abs(pair[0] - pair[1])
	}
	return sum / float64(len(values))
}
