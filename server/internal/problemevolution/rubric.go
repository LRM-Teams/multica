package problemevolution

import (
	"fmt"
	"regexp"
	"strings"
)

// Dimension ids the proposal uses. They are stable identifiers rather than
// display strings so a rubric stays comparable after the UI is translated.
const (
	DimensionCorrectness  = "correctness"
	DimensionCompleteness = "completeness"
	DimensionRigor        = "rigor"
	DimensionClarity      = "clarity"
)

// MaxProposedDimensions bounds a generated rubric. A rubric with many weak
// dimensions dilutes selection pressure, which is worse than a short one.
const MaxProposedDimensions = 6

var constraintIDPattern = regexp.MustCompile(`[^a-z0-9]+`)

// ProposeContract derives a draft evaluator contract from the problem itself.
//
// It is deliberately deterministic: the same problem always yields the same
// rubric, so a proposal can be reviewed and diffed before it is frozen. It is a
// starting point for the user to edit, not an authority — the frozen contract is
// what scoring is bound to.
func ProposeContract(problem ProblemSpec) EvaluatorContract {
	dimensions := []EvaluatorDimension{
		{
			DimensionID: DimensionCorrectness,
			Name:        "Correctness",
			Criteria:    "The answer is correct for the stated problem, with no unsupported claim.",
			Weight:      2,
			Hard:        true,
		},
		{
			DimensionID: DimensionCompleteness,
			Name:        "Completeness",
			Criteria:    "Every part of the problem statement is addressed.",
			Weight:      1,
		},
	}
	if mentionsProof(problem.Statement) {
		dimensions = append(dimensions, EvaluatorDimension{
			DimensionID: DimensionRigor,
			Name:        "Rigor",
			Criteria:    "Each step follows from the previous one; assumptions are stated.",
			Weight:      1,
		})
	}
	dimensions = append(dimensions, EvaluatorDimension{
		DimensionID: DimensionClarity,
		Name:        "Clarity",
		Criteria:    "A reader can follow the answer without reconstructing missing steps.",
		Weight:      1,
	})
	for _, constraint := range problem.Constraints {
		if len(dimensions) >= MaxProposedDimensions {
			break
		}
		id := constraintDimensionID(constraint)
		if id == "" || hasDimension(dimensions, id) {
			continue
		}
		dimensions = append(dimensions, EvaluatorDimension{
			DimensionID: id,
			Name:        strings.TrimSpace(constraint),
			Criteria:    fmt.Sprintf("Satisfies the stated constraint: %s", strings.TrimSpace(constraint)),
			Weight:      1,
			Hard:        true,
		})
	}
	return EvaluatorContract{
		SchemaVersion: SchemaVersion,
		Kind:          EvaluatorKindBuiltinRubric,
		Dimensions:    dimensions,
		PassThreshold: 0.8,
		Invoke: EvaluatorInvoke{
			Transport:  "cli",
			Command:    []string{"multica", "problem-evolution", "evaluate"},
			InputPath:  DefaultEvaluatorInput,
			OutputPath: DefaultEvaluatorOutput,
		},
	}
}

// NormalizeWeights rewrites dimension weights so they sum to one, keeping their
// relative proportions. Scores are compared across runs, so a rubric whose
// weights sum to three would otherwise be silently on a different scale.
func NormalizeWeights(contract EvaluatorContract) EvaluatorContract {
	var total float64
	for _, dimension := range contract.Dimensions {
		if dimension.Weight > 0 {
			total += dimension.Weight
		}
	}
	if total <= 0 {
		return contract
	}
	normalized := make([]EvaluatorDimension, len(contract.Dimensions))
	copy(normalized, contract.Dimensions)
	for i := range normalized {
		if normalized[i].Weight > 0 {
			normalized[i].Weight = normalized[i].Weight / total
		}
	}
	contract.Dimensions = normalized
	return contract
}

// WeightedTotal recomputes a candidate total from its dimension scores under a
// contract. A hard dimension scoring below the pass threshold fails the gate,
// which is what keeps a high average from hiding a wrong answer.
func WeightedTotal(contract EvaluatorContract, dimensionScores map[string]float64) (total float64, hardGatePassed bool) {
	normalized := NormalizeWeights(contract)
	hardGatePassed = true
	for _, dimension := range normalized.Dimensions {
		score := dimensionScores[dimension.DimensionID]
		if score < 0 {
			score = 0
		}
		if score > 1 {
			score = 1
		}
		total += dimension.Weight * score
		if dimension.Hard && score < contract.PassThreshold {
			hardGatePassed = false
		}
	}
	if total > 1 {
		total = 1
	}
	return total, hardGatePassed
}

func mentionsProof(statement string) bool {
	lowered := strings.ToLower(statement)
	for _, marker := range []string{"prove", "proof", "derive", "证明", "推导"} {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

func constraintDimensionID(constraint string) string {
	lowered := strings.ToLower(strings.TrimSpace(constraint))
	if lowered == "" {
		return ""
	}
	slug := strings.Trim(constraintIDPattern.ReplaceAllString(lowered, "_"), "_")
	if slug == "" {
		// Non-ASCII constraints (e.g. Chinese) slug to nothing; keep them
		// addressable with a stable positional id instead of dropping them.
		return "constraint_" + fmt.Sprintf("%x", len(constraint))
	}
	if len(slug) > 48 {
		slug = slug[:48]
	}
	return "constraint_" + slug
}

func hasDimension(dimensions []EvaluatorDimension, id string) bool {
	for _, dimension := range dimensions {
		if dimension.DimensionID == id {
			return true
		}
	}
	return false
}
