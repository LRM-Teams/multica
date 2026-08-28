package problemevolution

import (
	"errors"
	"testing"
)

func scoreWithDimensions(total float64, dimensions map[string]float64) Score {
	score := Score{
		SchemaVersion:  SchemaVersion,
		Total:          total,
		Scale:          ScaleUnitInterval,
		HardGatePassed: true,
	}
	for id, value := range dimensions {
		score.Dimensions = append(score.Dimensions, ScoreDimension{
			DimensionID: id,
			Score:       value,
			Weight:      1 / float64(len(dimensions)),
		})
	}
	return score
}

func TestBuildFeedbackBundleNeverExposesExactTotals(t *testing.T) {
	policy := DefaultFeedbackPolicy()
	bundle := BuildFeedbackBundle([]ParentEvaluation{
		{CandidateRef: "c1", Score: scoreWithDimensions(0.83, map[string]float64{"correctness": 0.9})},
	}, policy)
	if len(bundle.Parents) != 1 {
		t.Fatalf("parents = %d, want 1", len(bundle.Parents))
	}
	projection := bundle.Parents[0].Projection
	// An exact score handed back every round is a gradient toward the verifier.
	if projection.Total != nil {
		t.Fatalf("projection exposed an exact total: %+v", projection)
	}
	if projection.TotalBucket == "" {
		t.Fatal("projection is missing the bucketed total")
	}
}

func TestBuildFeedbackBundleHonoursExactBandwidthWhenChosen(t *testing.T) {
	policy := DefaultFeedbackPolicy()
	policy.Bandwidth = FeedbackBandwidthExact
	bundle := BuildFeedbackBundle([]ParentEvaluation{
		{CandidateRef: "c1", Score: scoreWithDimensions(0.42, nil)},
	}, policy)
	if bundle.Parents[0].Projection.Total == nil {
		t.Fatal("exact bandwidth did not include the total")
	}
}

func TestBuildFeedbackBundleCapsNumericFields(t *testing.T) {
	policy := DefaultFeedbackPolicy()
	policy.MaxNumericFields = 3
	bundle := BuildFeedbackBundle([]ParentEvaluation{
		{CandidateRef: "c1", Score: scoreWithDimensions(0.5, map[string]float64{
			"a": 0.1, "b": 0.2, "c": 0.3, "d": 0.4, "e": 0.5,
		})},
	}, policy)
	dimensions := bundle.Parents[0].Projection.Dimensions
	// The total occupies one field, so two dimensions fit in a budget of three.
	if len(dimensions) != 2 {
		t.Fatalf("dimensions = %d, want 2 under a budget of 3: %v", len(dimensions), dimensions)
	}
}

func TestBuildFeedbackBundleReportsOnlySharedWeakDimensions(t *testing.T) {
	bundle := BuildFeedbackBundle([]ParentEvaluation{
		{CandidateRef: "c1", Score: scoreWithDimensions(0.4, map[string]float64{"rigor": 0.2, "coverage": 0.9})},
		{CandidateRef: "c2", Score: scoreWithDimensions(0.4, map[string]float64{"rigor": 0.3, "coverage": 0.8})},
	}, DefaultFeedbackPolicy())
	if len(bundle.SharedWeakDimensions) != 1 || bundle.SharedWeakDimensions[0] != "rigor" {
		t.Fatalf("shared weak dimensions = %v, want [rigor]", bundle.SharedWeakDimensions)
	}
}

func TestBuildFeedbackBundleAdvertisesRepairBudget(t *testing.T) {
	policy := DefaultFeedbackPolicy()
	bundle := BuildFeedbackBundle([]ParentEvaluation{
		{CandidateRef: "fresh", Score: scoreWithDimensions(0.5, nil), RoundsUsed: 0},
		{CandidateRef: "spent", Score: scoreWithDimensions(0.5, nil), RoundsUsed: policy.MaxRounds},
	}, policy)
	byRef := map[string]ParentFeedback{}
	for _, parent := range bundle.Parents {
		byRef[parent.CandidateRef] = parent
	}
	if !byRef["fresh"].RepairAllowed {
		t.Fatal("a parent with rounds left was reported as unrepairable")
	}
	if byRef["spent"].RepairAllowed {
		t.Fatal("a parent past its repair budget was reported as repairable")
	}
}

func TestRepairAllowedUsesPolicyBound(t *testing.T) {
	policy := FeedbackPolicy{MaxRounds: 2}
	if !RepairAllowed(1, policy) {
		t.Fatal("expected a second round to be allowed")
	}
	if RepairAllowed(2, policy) {
		t.Fatal("expected the third round to be refused")
	}
	// A missing bound must fall back to the default, not to unlimited repair.
	if RepairAllowed(99, FeedbackPolicy{}) {
		t.Fatal("an unset policy allowed unlimited repair")
	}
}

func TestDeriveSeedsSplitsSearchFromBlindValidation(t *testing.T) {
	seeds := DeriveSeeds("run-1")
	if seeds.Search == seeds.Blind {
		t.Fatal("search and blind validation share a seed")
	}
	if seeds.Search <= 0 || seeds.Blind <= 0 {
		t.Fatalf("seeds must be positive: %+v", seeds)
	}
	// Determinism is what makes a rerun comparable to the original run.
	if DeriveSeeds("run-1") != seeds {
		t.Fatal("seed derivation is not deterministic")
	}
	if DeriveSeeds("run-2").Search == seeds.Search {
		t.Fatal("two runs derived the same search seed")
	}
}

func TestValidateBlindOutcomeRejectsSearchSeed(t *testing.T) {
	seeds := DeriveSeeds("run-1")
	outcome := BlindValidationOutcome{
		CandidateRef: "c1",
		Score:        scoreWithDimensions(0.7, map[string]float64{"correctness": 0.7}),
		Seed:         seeds.Search,
	}
	// Blind validation on the search sample proves nothing about
	// generalisation, so it is refused rather than stored with a caveat.
	if err := ValidateBlindOutcome(outcome, seeds); err == nil {
		t.Fatal("blind validation with the search seed was accepted")
	}
	outcome.Seed = seeds.Blind
	if err := ValidateBlindOutcome(outcome, seeds); err != nil {
		t.Fatalf("blind validation with the blind seed was rejected: %v", err)
	}
}

func TestValidateBlindOutcomeRequiresCandidateAndValidScore(t *testing.T) {
	seeds := DeriveSeeds("run-1")
	if err := ValidateBlindOutcome(BlindValidationOutcome{Seed: seeds.Blind}, seeds); err == nil {
		t.Fatal("a blind outcome without a candidate was accepted")
	}
	bad := BlindValidationOutcome{
		CandidateRef: "c1",
		Score:        Score{SchemaVersion: SchemaVersion, Total: 5, Scale: ScaleUnitInterval},
		Seed:         seeds.Blind,
	}
	if err := ValidateBlindOutcome(bad, seeds); err == nil {
		t.Fatal("an out-of-range blind score was accepted")
	}
}

func TestOverfitGapMeasuresSearchOverstatement(t *testing.T) {
	if gap := OverfitGap(0.9, 0.4); gap < 0.49 || gap > 0.51 {
		t.Fatalf("gap = %v, want ~0.5", gap)
	}
	if gap := OverfitGap(0.5, 0.6); gap >= 0 {
		t.Fatalf("gap = %v, want a negative gap when blind validation is higher", gap)
	}
}

func TestRepairBudgetErrorIsAnEventRejection(t *testing.T) {
	// The daemon distinguishes a rejected event from a server failure by this
	// sentinel, so a budget refusal must not surface as a 500.
	if !errors.Is(ErrRepairBudgetExhausted, ErrEventRejected) {
		t.Fatal("repair budget error is not an event rejection")
	}
}
