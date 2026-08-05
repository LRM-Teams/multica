package researchrun

import (
	"fmt"
	"strings"
)

const (
	evaluationDefectBlocking     = "blocking"
	evaluationDefectAdvisory     = "advisory"
	maxEvaluationDefects         = 16
	maxEvaluationDefectTargets   = 64
	maxEvaluationDefectTextBytes = 1024
)

func (r *ResultEnvelope) validateV5(task Task, cfg RunConfig) error {
	if r.SchemaVersion != 5 {
		return fmt.Errorf("%w: research-run-v5 requires schema_version 5", ErrInvalidResult)
	}
	if err := validateV5TaskContract(task); err != nil {
		return err
	}
	if err := validateV5TaskContracts(*r); err != nil {
		return err
	}
	if r.Evaluation != nil {
		if err := validateEvaluationDefectsV5(r.Evaluation); err != nil {
			return err
		}
	}

	legacy := cloneResultForV4(*r)
	legacy.SchemaVersion = 4
	legacyTask := task
	legacyTask.ExpectedResult = translateResultKind(task.ExpectedResult, "_v5", "_v4")
	return legacy.validateV4(legacyTask, cfg)
}

func validateEvaluationDefectsV5(evaluation *EvaluationProposal) error {
	if len(evaluation.Defects) > maxEvaluationDefects {
		return fmt.Errorf("%w: evaluation defects exceed %d items", ErrInvalidResult, maxEvaluationDefects)
	}
	seen := make(map[string]struct{}, len(evaluation.Defects))
	dimensions := make(map[string]struct{}, len(evaluationDimensionNames))
	for _, dimension := range evaluationDimensionNames {
		dimensions[dimension] = struct{}{}
	}
	summaries := make([]string, 0, len(evaluation.Defects))
	blocking := 0
	for _, defect := range evaluation.Defects {
		if err := validateKey("evaluation defect client_key", defect.ClientKey); err != nil {
			return err
		}
		if _, duplicate := seen[defect.ClientKey]; duplicate {
			return fmt.Errorf("%w: duplicate evaluation defect %q", ErrInvalidResult, defect.ClientKey)
		}
		seen[defect.ClientKey] = struct{}{}
		if _, ok := dimensions[defect.Dimension]; !ok {
			return fmt.Errorf("%w: evaluation defect %q has invalid dimension %q", ErrInvalidResult, defect.ClientKey, defect.Dimension)
		}
		switch defect.Severity {
		case evaluationDefectBlocking:
			blocking++
		case evaluationDefectAdvisory:
		default:
			return fmt.Errorf("%w: evaluation defect %q has invalid severity %q", ErrInvalidResult, defect.ClientKey, defect.Severity)
		}
		for name, value := range map[string]string{
			"problem": defect.Problem, "required_change": defect.RequiredChange,
		} {
			if substantiveRuneCount(value) < minimumReviewRationaleCharacters || len(value) > maxEvaluationDefectTextBytes {
				return fmt.Errorf("%w: evaluation defect %q %s is not substantive or exceeds %d bytes", ErrInvalidResult, defect.ClientKey, name, maxEvaluationDefectTextBytes)
			}
		}
		if len(defect.ClaimKeys) == 0 && len(defect.SectionIDs) == 0 {
			return fmt.Errorf("%w: evaluation defect %q must target a report Claim or section", ErrInvalidResult, defect.ClientKey)
		}
		if err := validateEvaluationDefectKeys(defect.ClientKey, "claim_key", defect.ClaimKeys); err != nil {
			return err
		}
		if err := validateEvaluationDefectKeys(defect.ClientKey, "section_id", defect.SectionIDs); err != nil {
			return err
		}
		summaries = append(summaries, strings.TrimSpace(defect.Problem))
	}
	if evaluation.Passed && len(evaluation.Defects) > 0 {
		return fmt.Errorf("%w: passing V5 evaluation cannot contain defects", ErrInvalidResult)
	}
	if !evaluation.Passed && blocking == 0 {
		return fmt.Errorf("%w: failed V5 evaluation requires a blocking defect", ErrInvalidResult)
	}
	if len(evaluation.Findings) > 0 && !sameOrderedStrings(evaluation.Findings, summaries) {
		return fmt.Errorf("%w: evaluation findings must exactly match ordered defect problems", ErrInvalidResult)
	}
	evaluation.Findings = summaries
	return nil
}

func validateEvaluationDefectKeys(defectKey, field string, values []string) error {
	if len(values) > maxEvaluationDefectTargets {
		return fmt.Errorf("%w: evaluation defect %q %s exceeds %d items", ErrInvalidResult, defectKey, field, maxEvaluationDefectTargets)
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if err := validateKey("evaluation defect "+field, value); err != nil {
			return err
		}
		if _, duplicate := seen[value]; duplicate {
			return fmt.Errorf("%w: evaluation defect %q has duplicate %s %q", ErrInvalidResult, defectKey, field, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateEvaluationDefectsAgainstReport(evaluation EvaluationProposal, claimKeys, sectionIDs []string, minimumScore float64) error {
	claims := make(map[string]struct{}, len(claimKeys))
	sections := make(map[string]struct{}, len(sectionIDs))
	for _, key := range claimKeys {
		claims[key] = struct{}{}
	}
	for _, id := range sectionIDs {
		sections[id] = struct{}{}
	}
	blockingDimensions := map[string]bool{}
	for _, defect := range evaluation.Defects {
		for _, key := range defect.ClaimKeys {
			if _, ok := claims[key]; !ok {
				return fmt.Errorf("%w: evaluation defect %q targets unknown report Claim %q", ErrInvalidResult, defect.ClientKey, key)
			}
		}
		for _, id := range defect.SectionIDs {
			if _, ok := sections[id]; !ok {
				return fmt.Errorf("%w: evaluation defect %q targets unknown report section %q", ErrInvalidResult, defect.ClientKey, id)
			}
		}
		if defect.Severity == evaluationDefectBlocking {
			blockingDimensions[defect.Dimension] = true
		}
	}
	scores := evaluationDimensionScores(evaluation)
	belowFloor := 0
	for _, dimension := range evaluationDimensionNames {
		if scores[dimension] >= minimumScore {
			if blockingDimensions[dimension] {
				return fmt.Errorf("%w: blocking defect dimension %q is not below the %.2f score floor", ErrInvalidResult, dimension, minimumScore)
			}
			continue
		}
		belowFloor++
		if !blockingDimensions[dimension] {
			return fmt.Errorf("%w: below-floor dimension %q requires a blocking defect", ErrInvalidResult, dimension)
		}
	}
	if evaluation.Passed && belowFloor > 0 {
		return fmt.Errorf("%w: passing evaluation has %d dimensions below the %.2f score floor", ErrInvalidResult, belowFloor, minimumScore)
	}
	if !evaluation.Passed && belowFloor == 0 {
		return fmt.Errorf("%w: failed evaluation requires a dimension below the %.2f score floor", ErrInvalidResult, minimumScore)
	}
	return nil
}

func evaluationDimensionScores(evaluation EvaluationProposal) map[string]float64 {
	return map[string]float64{
		"factual_grounding":      evaluation.FactualGrounding,
		"coverage":               evaluation.Coverage,
		"analytical_depth":       evaluation.AnalyticalDepth,
		"source_quality":         evaluation.SourceQuality,
		"contradiction_handling": evaluation.ContradictionHandling,
		"instruction_adherence":  evaluation.InstructionAdherence,
		"readability":            evaluation.Readability,
	}
}

func sameOrderedStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if strings.TrimSpace(left[i]) != strings.TrimSpace(right[i]) {
			return false
		}
	}
	return true
}

func validateV5TaskContract(task Task) error {
	capability, expected, fixed := v5TaskContract(task.Kind)
	if fixed && task.RequiredCapability != capability {
		return fmt.Errorf("%w: assigned %s task requires capability %q", ErrInvalidResult, task.Kind, capability)
	}
	if expected != "" && task.ExpectedResult != expected {
		return fmt.Errorf("%w: assigned %s task requires expected_result %q", ErrInvalidResult, task.Kind, expected)
	}
	return nil
}

func validateV5TaskContracts(result ResultEnvelope) error {
	tasks := append([]TaskProposal(nil), result.ProposedTasks...)
	if result.Plan != nil {
		tasks = append(tasks, result.Plan.Tasks...)
	}
	for _, task := range tasks {
		capability, expected, fixed := v5TaskContract(task.Kind)
		if fixed && task.RequiredCapability != capability {
			return fmt.Errorf("%w: %s task %q requires capability %q", ErrInvalidResult, task.Kind, task.ClientKey, capability)
		}
		if expected != "" && task.ExpectedResult != expected {
			return fmt.Errorf("%w: %s task %q requires expected_result %q", ErrInvalidResult, task.Kind, task.ClientKey, expected)
		}
	}
	return nil
}

func v5TaskContract(kind TaskKind) (string, string, bool) {
	capability, expected, fixed := v4TaskContract(kind)
	return capability, translateResultKind(expected, "_v4", "_v5"), fixed
}

func cloneResultForV4(result ResultEnvelope) ResultEnvelope {
	clone := result
	clone.ProposedTasks = translateTaskResultKinds(result.ProposedTasks, "_v5", "_v4")
	if result.Plan != nil {
		plan := *result.Plan
		plan.Tasks = translateTaskResultKinds(result.Plan.Tasks, "_v5", "_v4")
		clone.Plan = &plan
	}
	if result.Evaluation != nil {
		evaluation := *result.Evaluation
		evaluation.Defects = nil
		clone.Evaluation = &evaluation
	}
	return clone
}

func hasStructuredEvaluationDefects(result ResultEnvelope) bool {
	return result.Evaluation != nil && len(result.Evaluation.Defects) > 0
}
