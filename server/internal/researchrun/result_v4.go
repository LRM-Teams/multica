package researchrun

import (
	"fmt"
	"strings"
)

const maxIndependentSourcesPerStandard = 8

func (r *ResultEnvelope) validateV4(task Task, cfg RunConfig) error {
	if r.SchemaVersion != 4 {
		return fmt.Errorf("%w: research-run-v4 requires schema_version 4", ErrInvalidResult)
	}
	if err := validateV4TaskContract(task); err != nil {
		return err
	}
	if err := validateV4TaskContracts(*r); err != nil {
		return err
	}

	legacy := cloneResultForV3(*r)
	legacy.SchemaVersion = 3
	legacyTask := task
	legacyTask.ExpectedResult = translateResultKind(task.ExpectedResult, "_v4", "_v3")
	if err := legacy.validateV3(legacyTask, cfg); err != nil {
		return err
	}

	standards := map[string]EvidenceStandard{}
	if task.Kind == TaskKindPlan || task.Kind == TaskKindReplan {
		if r.Plan == nil || r.Plan.Method == nil {
			return fmt.Errorf("%w: v4 plan requires a research method", ErrInvalidResult)
		}
		var err error
		standards, err = validateEvidenceStandards(r.Plan.Method.EvidenceStandards)
		if err != nil {
			return err
		}
		if requiresCounterevidenceTask(standards) && !planContainsTaskKind(*r.Plan, TaskKindCounterSearch) {
			return fmt.Errorf("%w: v4 plan requires a counter_search task for its evidence standards", ErrInvalidResult)
		}
	}

	for _, source := range r.Sources {
		if err := validateEvidenceTraits("source "+source.ClientKey, source.EvidenceTraits); err != nil {
			return err
		}
	}
	for _, claim := range r.Claims {
		if err := validateKey("claim.evidence_standard_key", claim.EvidenceStandardKey); err != nil {
			return err
		}
		if len(standards) > 0 {
			if _, ok := standards[claim.EvidenceStandardKey]; !ok {
				return fmt.Errorf("%w: claim %q references unknown evidence standard %q", ErrInvalidResult, claim.ClientKey, claim.EvidenceStandardKey)
			}
		}
		for _, evidence := range claim.Evidence {
			if evidence.Directness <= 0 || !unitInterval(evidence.Directness) {
				return fmt.Errorf("%w: claim %q evidence directness must be in (0,1]", ErrInvalidResult, claim.ClientKey)
			}
			if evidence.MethodFit <= 0 || !unitInterval(evidence.MethodFit) {
				return fmt.Errorf("%w: claim %q evidence method_fit must be in (0,1]", ErrInvalidResult, claim.ClientKey)
			}
		}
	}
	return nil
}

func validateEvidenceStandards(values []EvidenceStandard) (map[string]EvidenceStandard, error) {
	if len(values) == 0 || len(values) > maxResultItems {
		return nil, fmt.Errorf("%w: method.evidence_standards requires between 1 and %d items", ErrInvalidResult, maxResultItems)
	}
	standards := make(map[string]EvidenceStandard, len(values))
	for _, standard := range values {
		if err := validateKey("evidence standard client_key", standard.ClientKey); err != nil {
			return nil, err
		}
		if _, duplicate := standards[standard.ClientKey]; duplicate {
			return nil, fmt.Errorf("%w: duplicate evidence standard %q", ErrInvalidResult, standard.ClientKey)
		}
		if strings.TrimSpace(standard.Purpose) == "" || len(standard.Purpose) > maxTaskObjectiveBytes {
			return nil, fmt.Errorf("%w: evidence standard %q purpose is invalid", ErrInvalidResult, standard.ClientKey)
		}
		if standard.MinimumIndependentSources < 1 || standard.MinimumIndependentSources > maxIndependentSourcesPerStandard {
			return nil, fmt.Errorf("%w: evidence standard %q minimum_independent_sources must be in [1,%d]", ErrInvalidResult, standard.ClientKey, maxIndependentSourcesPerStandard)
		}
		if err := validateEvidenceTraits("evidence standard "+standard.ClientKey, standard.RequiredSourceTraits); err != nil {
			return nil, err
		}
		for name, value := range map[string]float64{
			"minimum_strength":   standard.MinimumStrength,
			"minimum_directness": standard.MinimumDirectness,
			"minimum_method_fit": standard.MinimumMethodFit,
		} {
			if value <= 0 || !unitInterval(value) {
				return nil, fmt.Errorf("%w: evidence standard %q %s must be in (0,1]", ErrInvalidResult, standard.ClientKey, name)
			}
		}
		standards[standard.ClientKey] = standard
	}
	return standards, nil
}

func validateEvidenceTraits(owner string, traits []string) error {
	if len(traits) == 0 || len(traits) > maxResultItems {
		return fmt.Errorf("%w: %s requires evidence_traits", ErrInvalidResult, owner)
	}
	seen := map[string]struct{}{}
	for _, trait := range traits {
		trait = strings.TrimSpace(trait)
		if !capabilityPattern.MatchString(trait) {
			return fmt.Errorf("%w: %s has invalid evidence trait %q", ErrInvalidResult, owner, trait)
		}
		if _, duplicate := seen[trait]; duplicate {
			return fmt.Errorf("%w: %s has duplicate evidence trait %q", ErrInvalidResult, owner, trait)
		}
		seen[trait] = struct{}{}
	}
	return nil
}

func validateV4TaskContract(task Task) error {
	capability, expected, fixed := v4TaskContract(task.Kind)
	if fixed && task.RequiredCapability != capability {
		return fmt.Errorf("%w: assigned %s task requires capability %q", ErrInvalidResult, task.Kind, capability)
	}
	if expected != "" && task.ExpectedResult != expected {
		return fmt.Errorf("%w: assigned %s task requires expected_result %q", ErrInvalidResult, task.Kind, expected)
	}
	return nil
}

func validateV4TaskContracts(result ResultEnvelope) error {
	tasks := append([]TaskProposal(nil), result.ProposedTasks...)
	if result.Plan != nil {
		tasks = append(tasks, result.Plan.Tasks...)
	}
	for _, task := range tasks {
		capability, expected, fixed := v4TaskContract(task.Kind)
		if fixed && task.RequiredCapability != capability {
			return fmt.Errorf("%w: %s task %q requires capability %q", ErrInvalidResult, task.Kind, task.ClientKey, capability)
		}
		if expected != "" && task.ExpectedResult != expected {
			return fmt.Errorf("%w: %s task %q requires expected_result %q", ErrInvalidResult, task.Kind, task.ClientKey, expected)
		}
	}
	translated := cloneResultForV3(result)
	return validateV3TaskContracts(translated)
}

func v4TaskContract(kind TaskKind) (string, string, bool) {
	capability, expected, fixed := v3TaskContract(kind)
	if expected == "" {
		return capability, "", fixed
	}
	return capability, translateResultKind(expected, "_v3", "_v4"), fixed
}

func cloneResultForV3(result ResultEnvelope) ResultEnvelope {
	clone := result
	clone.Plan = clonePlanForV3(result.Plan)
	clone.ProposedTasks = translateTaskResultKinds(result.ProposedTasks, "_v4", "_v3")
	clone.Sources = append([]SourceProposal(nil), result.Sources...)
	for i := range clone.Sources {
		clone.Sources[i].EvidenceTraits = nil
	}
	clone.Claims = append([]ClaimProposal(nil), result.Claims...)
	for i := range clone.Claims {
		clone.Claims[i].EvidenceStandardKey = ""
		clone.Claims[i].Evidence = append([]EvidenceProposal(nil), result.Claims[i].Evidence...)
		for j := range clone.Claims[i].Evidence {
			clone.Claims[i].Evidence[j].Directness = 0
			clone.Claims[i].Evidence[j].MethodFit = 0
		}
	}
	return clone
}

func clonePlanForV3(plan *PlanProposal) *PlanProposal {
	if plan == nil {
		return nil
	}
	clone := *plan
	if plan.Method != nil {
		method := *plan.Method
		method.EvidenceStandards = nil
		clone.Method = &method
	}
	clone.Tasks = translateTaskResultKinds(plan.Tasks, "_v4", "_v3")
	return &clone
}

func translateTaskResultKinds(tasks []TaskProposal, from, to string) []TaskProposal {
	translated := append([]TaskProposal(nil), tasks...)
	for i := range translated {
		translated[i].ExpectedResult = translateResultKind(translated[i].ExpectedResult, from, to)
	}
	return translated
}

func translateResultKind(value, from, to string) string {
	if strings.HasSuffix(value, from) {
		return strings.TrimSuffix(value, from) + to
	}
	return value
}

func requiresCounterevidenceTask(standards map[string]EvidenceStandard) bool {
	for _, standard := range standards {
		if standard.CounterevidenceRequired {
			return true
		}
	}
	return false
}

func planContainsTaskKind(plan PlanProposal, kind TaskKind) bool {
	for _, task := range plan.Tasks {
		if task.Kind == kind {
			return true
		}
	}
	return false
}

func hasEvidenceFitnessFields(result ResultEnvelope) bool {
	if result.Plan != nil && result.Plan.Method != nil && len(result.Plan.Method.EvidenceStandards) > 0 {
		return true
	}
	for _, source := range result.Sources {
		if len(source.EvidenceTraits) > 0 {
			return true
		}
	}
	for _, claim := range result.Claims {
		if claim.EvidenceStandardKey != "" {
			return true
		}
		for _, evidence := range claim.Evidence {
			if evidence.Directness != 0 || evidence.MethodFit != 0 {
				return true
			}
		}
	}
	return false
}
