package problemevolution

import (
	"math"
	"testing"
)

func TestProposeContractIsValidAndDeterministic(t *testing.T) {
	problem := ProblemSpec{
		Statement:   "Prove the bound and report the complexity.",
		Constraints: []string{"Must include a complexity analysis"},
	}
	first := ProposeContract(problem)
	if err := first.Validate(); err != nil {
		t.Fatalf("proposed contract is not valid: %v", err)
	}
	second := ProposeContract(problem)
	if len(first.Dimensions) != len(second.Dimensions) {
		t.Fatal("expected the same problem to propose the same rubric")
	}
	for i := range first.Dimensions {
		if first.Dimensions[i] != second.Dimensions[i] {
			t.Fatalf("dimension %d drifted between proposals", i)
		}
	}
}

func TestProposeContractAddsRigorOnlyForProofProblems(t *testing.T) {
	withProof := ProposeContract(ProblemSpec{Statement: "Prove that the series converges."})
	if !hasDimension(withProof.Dimensions, DimensionRigor) {
		t.Fatal("expected a proof problem to get a rigor dimension")
	}
	withoutProof := ProposeContract(ProblemSpec{Statement: "Summarise the release notes."})
	if hasDimension(withoutProof.Dimensions, DimensionRigor) {
		t.Fatal("expected a summarisation problem to skip the rigor dimension")
	}
}

func TestProposeContractCapsDimensionCount(t *testing.T) {
	constraints := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	contract := ProposeContract(ProblemSpec{Statement: "Prove it.", Constraints: constraints})
	if len(contract.Dimensions) > MaxProposedDimensions {
		t.Fatalf("dimensions = %d, want at most %d", len(contract.Dimensions), MaxProposedDimensions)
	}
}

func TestProposeContractKeepsNonASCIIConstraintsAddressable(t *testing.T) {
	contract := ProposeContract(ProblemSpec{
		Statement:   "解这道题。",
		Constraints: []string{"必须给出复杂度分析"},
	})
	if err := contract.Validate(); err != nil {
		t.Fatalf("proposed contract is not valid: %v", err)
	}
	if len(contract.Dimensions) < 4 {
		t.Fatalf("expected the Chinese constraint to become a dimension, got %d", len(contract.Dimensions))
	}
}

func TestNormalizeWeightsSumsToOne(t *testing.T) {
	normalized := NormalizeWeights(ProposeContract(ProblemSpec{Statement: "Prove it."}))
	var total float64
	for _, dimension := range normalized.Dimensions {
		total += dimension.Weight
	}
	if math.Abs(total-1) > 1e-9 {
		t.Fatalf("weights sum to %v, want 1", total)
	}
}

func TestWeightedTotalFailsHardGateOnWrongAnswer(t *testing.T) {
	contract := ProposeContract(ProblemSpec{Statement: "Prove it."})
	// A clear, complete, well-written wrong answer must not pass: correctness
	// is the hard dimension.
	total, passed := WeightedTotal(contract, map[string]float64{
		DimensionCorrectness:  0.2,
		DimensionCompleteness: 1,
		DimensionRigor:        1,
		DimensionClarity:      1,
	})
	if passed {
		t.Fatal("expected the hard correctness gate to fail")
	}
	if total <= 0 || total > 1 {
		t.Fatalf("total = %v, want within 0..1", total)
	}
}

func TestWeightedTotalClampsOutOfRangeDimensionScores(t *testing.T) {
	contract := ProposeContract(ProblemSpec{Statement: "Summarise it."})
	total, _ := WeightedTotal(contract, map[string]float64{
		DimensionCorrectness:  4,
		DimensionCompleteness: -2,
	})
	if total < 0 || total > 1 {
		t.Fatalf("total = %v, want within 0..1", total)
	}
}
