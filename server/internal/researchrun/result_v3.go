package researchrun

import (
	"fmt"
	"strings"
)

func (r *ResultEnvelope) validateV3(task Task, cfg RunConfig) error {
	if r.SchemaVersion != 3 {
		return fmt.Errorf("%w: research-run-v3 requires schema_version 3", ErrInvalidResult)
	}
	if hasEvidenceFitnessFields(*r) {
		return fmt.Errorf("%w: evidence fitness fields require schema_version 4", ErrInvalidResult)
	}
	if err := validateV3TaskContract(task); err != nil {
		return err
	}
	if err := validateV3TaskContracts(*r); err != nil {
		return err
	}
	if task.Kind != TaskKindPlan && task.Kind != TaskKindReplan && r.Plan != nil {
		return fmt.Errorf("%w: plan is only valid for plan and replan tasks", ErrInvalidResult)
	}

	// V3 deliberately inherits the complete V2 evidence, report, attribution,
	// and review contract. Translate only versioned result-kind identifiers in
	// a copy so the immutable V2 validator remains authoritative for that part.
	legacy := *r
	legacy.SchemaVersion = 2
	legacy.Plan = clonePlanForV2(r.Plan)
	legacy.ProposedTasks = translateTaskResultsForV2(r.ProposedTasks)
	legacyTask := task
	legacyTask.ExpectedResult = translateResultKindForV2(task.ExpectedResult)
	if err := legacy.validateV2(legacyTask, cfg); err != nil {
		return err
	}

	if task.Kind == TaskKindPlan || task.Kind == TaskKindReplan {
		if r.Plan == nil || r.Plan.Method == nil {
			return fmt.Errorf("%w: v3 plan requires a research method", ErrInvalidResult)
		}
		if err := validateMethodProposal(*r.Plan.Method); err != nil {
			return err
		}
		if err := validateV3PlanMethodLists(*r.Plan); err != nil {
			return err
		}
	}
	return nil
}

func validateV3PlanMethodLists(plan PlanProposal) error {
	for name, values := range map[string][]string{
		"inclusion_criteria": plan.InclusionCriteria,
		"exclusion_criteria": plan.ExclusionCriteria,
		"source_strategy":    plan.SourceStrategy,
		"uncertainties":      plan.Uncertainties,
		"planning_risks":     plan.PlanningRisks,
	} {
		if len(values) == 0 {
			return fmt.Errorf("%w: v3 plan requires %s", ErrInvalidResult, name)
		}
	}
	return nil
}

func validateMethodProposal(method MethodProposal) error {
	for name, value := range map[string]string{
		"method.decision_question": method.DecisionQuestion,
		"method.method_rationale":  method.MethodRationale,
	} {
		if strings.TrimSpace(value) == "" || len(value) > maxTaskObjectiveBytes {
			return fmt.Errorf("%w: %s is required and must not exceed %d bytes", ErrInvalidResult, name, maxTaskObjectiveBytes)
		}
	}
	for name, values := range map[string][]string{
		"method.analysis_methods":         method.AnalysisMethods,
		"method.evidence_requirements":    method.EvidenceRequirements,
		"method.counterevidence_strategy": method.CounterevidenceStrategy,
		"method.stopping_conditions":      method.StoppingConditions,
	} {
		if len(values) == 0 {
			return fmt.Errorf("%w: %s requires at least one item", ErrInvalidResult, name)
		}
		if err := validateStringList(name, values); err != nil {
			return err
		}
	}
	return nil
}

func validateV3TaskContract(task Task) error {
	capability, expected, fixed := v3TaskContract(task.Kind)
	if fixed && task.RequiredCapability != capability {
		return fmt.Errorf("%w: assigned %s task requires capability %q", ErrInvalidResult, task.Kind, capability)
	}
	if expected != "" && task.ExpectedResult != expected {
		return fmt.Errorf("%w: assigned %s task requires expected_result %q", ErrInvalidResult, task.Kind, expected)
	}
	return nil
}

func validateV3TaskContracts(result ResultEnvelope) error {
	tasks := append([]TaskProposal(nil), result.ProposedTasks...)
	if result.Plan != nil {
		tasks = append(tasks, result.Plan.Tasks...)
	}
	for _, task := range tasks {
		capability, expected, fixed := v3TaskContract(task.Kind)
		if fixed && task.RequiredCapability != capability {
			return fmt.Errorf("%w: %s task %q requires capability %q", ErrInvalidResult, task.Kind, task.ClientKey, capability)
		}
		if expected != "" && task.ExpectedResult != expected {
			return fmt.Errorf("%w: %s task %q requires expected_result %q", ErrInvalidResult, task.Kind, task.ClientKey, expected)
		}
	}
	translated := result
	translated.Plan = clonePlanForV2(result.Plan)
	translated.ProposedTasks = translateTaskResultsForV2(result.ProposedTasks)
	return validateV2TaskContracts(translated)
}

func v3TaskContract(kind TaskKind) (string, string, bool) {
	capability, expected, fixed := v2TaskContract(kind)
	if expected == "" {
		return capability, "", fixed
	}
	return capability, strings.TrimSuffix(expected, "_v2") + "_v3", fixed
}

func clonePlanForV2(plan *PlanProposal) *PlanProposal {
	if plan == nil {
		return nil
	}
	clone := *plan
	clone.Method = nil
	clone.Tasks = translateTaskResultsForV2(plan.Tasks)
	return &clone
}

func translateTaskResultsForV2(tasks []TaskProposal) []TaskProposal {
	translated := append([]TaskProposal(nil), tasks...)
	for i := range translated {
		translated[i].ExpectedResult = translateResultKindForV2(translated[i].ExpectedResult)
	}
	return translated
}

func translateResultKindForV2(value string) string {
	if strings.HasSuffix(value, "_v3") {
		return strings.TrimSuffix(value, "_v3") + "_v2"
	}
	return value
}
