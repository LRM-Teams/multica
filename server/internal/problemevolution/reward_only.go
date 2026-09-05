package problemevolution

import (
	"fmt"
	"hash/fnv"
	"sort"
)

// EvaluationPhase values. The search phase is what the evolver gets feedback
// from; blind validation is scored once, at the end, on material the search
// never saw.
const (
	PhaseSearch          = "search"
	PhaseBlindValidation = "blind_validation"
)

// ErrRepairBudgetExhausted means a candidate has already consumed its allowed
// reward-only feedback rounds.
var ErrRepairBudgetExhausted = fmt.Errorf("%w: repair budget exhausted", ErrEventRejected)

// RepairAllowed reports whether another repair round on a parent is permitted.
//
// The bound exists because repeated repair against the same reward signal is
// how a run stops solving the problem and starts guessing the verifier. It is
// enforced at ingestion, not merely advertised in the input.
func RepairAllowed(roundsUsed int, policy FeedbackPolicy) bool {
	maxRounds := policy.MaxRounds
	if maxRounds <= 0 {
		maxRounds = DefaultFeedbackPolicy().MaxRounds
	}
	return roundsUsed < maxRounds
}

// ParentEvaluation is the stored evaluation a feedback bundle is built from.
type ParentEvaluation struct {
	CandidateRef  string
	Score         Score
	FailureClass  string
	ChangeSummary string
	RoundsUsed    int
}

// BuildFeedbackBundle projects parent evaluations into what the next generation
// may see. Every numeric field is passed through the policy projection, and the
// number of numeric fields is capped, so the bundle cannot become a side channel
// wide enough to reconstruct the evaluator.
func BuildFeedbackBundle(parents []ParentEvaluation, policy FeedbackPolicy) FeedbackBundle {
	bundle := FeedbackBundle{Policy: policy}
	weakCounts := make(map[string]int)
	for _, parent := range parents {
		projection := ProjectFeedbackWithPolicy(parent.Score, policy)
		projection = capProjectionFields(projection, policy)
		bundle.Parents = append(bundle.Parents, ParentFeedback{
			CandidateRef:  parent.CandidateRef,
			Projection:    projection,
			FailureClass:  parent.FailureClass,
			ChangeSummary: TruncateFreeText(parent.ChangeSummary),
			RoundsUsed:    parent.RoundsUsed,
			RepairAllowed: RepairAllowed(parent.RoundsUsed, policy),
		})
		for _, dimension := range parent.Score.Dimensions {
			if dimension.Score < 0.5 {
				weakCounts[dimension.DimensionID]++
			}
		}
	}
	// A dimension is only reported as a shared weakness when more than one
	// parent is weak on it; a single candidate's miss is noise, and naming it
	// would spend feedback bandwidth on nothing.
	for dimension, count := range weakCounts {
		if count > 1 {
			bundle.SharedWeakDimensions = append(bundle.SharedWeakDimensions, dimension)
		}
	}
	sort.Strings(bundle.SharedWeakDimensions)
	return bundle
}

// capProjectionFields drops per-dimension detail once the policy's numeric field
// budget is spent, keeping the coarse total that is always allowed.
func capProjectionFields(projection FeedbackProjection, policy FeedbackPolicy) FeedbackProjection {
	budget := policy.MaxNumericFields
	if budget <= 0 {
		budget = DefaultFeedbackPolicy().MaxNumericFields
	}
	// The total itself occupies one field.
	remaining := budget - 1
	if remaining <= 0 || len(projection.Dimensions) == 0 {
		projection.Dimensions = nil
		return projection
	}
	if len(projection.Dimensions) <= remaining {
		return projection
	}
	keys := make([]string, 0, len(projection.Dimensions))
	for key := range projection.Dimensions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	capped := make(map[string]string, remaining)
	for _, key := range keys[:remaining] {
		capped[key] = projection.Dimensions[key]
	}
	projection.Dimensions = capped
	return projection
}

// SeedPair holds the two independent seeds a run uses.
type SeedPair struct {
	Search int64 `json:"search_seed"`
	Blind  int64 `json:"blind_seed"`
}

// DeriveSeeds produces the search and blind-validation seeds for a run.
//
// They must differ: if the final blind check reused the search seed, a run that
// had adapted to the search sample would score well without having solved
// anything, which is the failure mode the blind phase exists to catch.
func DeriveSeeds(runID string) SeedPair {
	search := stableSeed("search:" + runID)
	blind := stableSeed("blind:" + runID)
	if blind == search {
		blind = search ^ 0x5deece66d
	}
	return SeedPair{Search: search, Blind: blind}
}

func stableSeed(input string) int64 {
	digest := fnv.New64a()
	_, _ = digest.Write([]byte(input))
	// Mask the sign bit so the seed is a positive value in both Go and the
	// external evolver's language.
	return int64(digest.Sum64() & 0x7fffffffffffffff)
}

// BlindValidationOutcome is the final, single-shot verdict for a run.
type BlindValidationOutcome struct {
	CandidateRef string  `json:"candidate_id"`
	Score        Score   `json:"score"`
	Seed         int64   `json:"seed"`
	SearchBest   float64 `json:"search_best"`
}

// ValidateBlindOutcome checks that a blind result is usable as a final verdict.
func ValidateBlindOutcome(outcome BlindValidationOutcome, seeds SeedPair) error {
	if outcome.CandidateRef == "" {
		return fmt.Errorf("%w: blind validation needs a candidate", ErrEventRejected)
	}
	if err := outcome.Score.Validate(); err != nil {
		return err
	}
	// A run without pinned seeds has no blind sample to validate against, so
	// there is nothing an outcome could be trusted to mean.
	if seeds.Blind <= 0 || seeds.Search <= 0 {
		return fmt.Errorf("%w: run has no pinned seeds", ErrEventRejected)
	}
	// Blind validation scored with the search seed proves nothing about
	// generalisation, so it is refused rather than recorded with a caveat.
	if outcome.Seed != seeds.Blind {
		return fmt.Errorf("%w: blind validation must use the blind seed", ErrEventRejected)
	}
	return nil
}

// OverfitGap reports how much the search score overstated the blind score. A
// large positive gap is the signal that a run adapted to its search sample.
func OverfitGap(searchBest, blindScore float64) float64 {
	return searchBest - blindScore
}
