package researchrun

import (
	"fmt"
	"sort"
)

// InsightDerivation records the canonical inputs used to derive an Insight.
// Inputs may name Claims or other Insights; only Insight IDs appear as keys in
// the graph passed to PlanInsightInvalidation.
type InsightDerivation struct {
	InsightID string
	InputIDs  []string
}

// InsightInvalidationPlan is a deterministic mutation plan. StaleInsightIDs
// contains the complete affected transitive closure. ReintegrationRootIDs is
// the smallest work frontier: affected Insights that directly consume an
// invalidated artifact, before their affected ancestors are recomputed.
type InsightInvalidationPlan struct {
	StaleInsightIDs      []string
	ReintegrationRootIDs []string
}

// PlanInsightInvalidation propagates invalid canonical artifacts through the
// Insight Derivation DAG without mutating historical derivations.
func PlanInsightInvalidation(derivations []InsightDerivation, invalidArtifactIDs []string) (InsightInvalidationPlan, error) {
	byID := make(map[string]InsightDerivation, len(derivations))
	consumers := make(map[string][]string)
	for _, derivation := range derivations {
		if derivation.InsightID == "" {
			return InsightInvalidationPlan{}, fmt.Errorf("%w: insight id is required", ErrInvalidContract)
		}
		if _, exists := byID[derivation.InsightID]; exists {
			return InsightInvalidationPlan{}, fmt.Errorf("%w: duplicate insight %q", ErrInvalidContract, derivation.InsightID)
		}
		byID[derivation.InsightID] = derivation
		seenInputs := make(map[string]struct{}, len(derivation.InputIDs))
		for _, inputID := range derivation.InputIDs {
			if inputID == "" {
				return InsightInvalidationPlan{}, fmt.Errorf("%w: insight %q has an empty input", ErrInvalidContract, derivation.InsightID)
			}
			if inputID == derivation.InsightID {
				return InsightInvalidationPlan{}, fmt.Errorf("%w: insight %q depends on itself", ErrInvalidContract, derivation.InsightID)
			}
			if _, exists := seenInputs[inputID]; exists {
				return InsightInvalidationPlan{}, fmt.Errorf("%w: insight %q repeats input %q", ErrInvalidContract, derivation.InsightID, inputID)
			}
			seenInputs[inputID] = struct{}{}
			consumers[inputID] = append(consumers[inputID], derivation.InsightID)
		}
	}

	if err := validateInsightDerivationAcyclic(byID); err != nil {
		return InsightInvalidationPlan{}, err
	}

	invalid := make(map[string]struct{}, len(invalidArtifactIDs))
	queue := make([]string, 0, len(invalidArtifactIDs))
	for _, artifactID := range invalidArtifactIDs {
		if artifactID == "" {
			return InsightInvalidationPlan{}, fmt.Errorf("%w: invalid artifact id is required", ErrInvalidContract)
		}
		if _, exists := invalid[artifactID]; !exists {
			invalid[artifactID] = struct{}{}
			queue = append(queue, artifactID)
		}
	}

	affected := make(map[string]struct{})
	roots := make(map[string]struct{})
	for head := 0; head < len(queue); head++ {
		inputID := queue[head]
		for _, insightID := range consumers[inputID] {
			if _, isDirectInput := invalid[inputID]; isDirectInput {
				roots[insightID] = struct{}{}
			}
			if _, exists := affected[insightID]; exists {
				continue
			}
			affected[insightID] = struct{}{}
			queue = append(queue, insightID)
		}
	}

	plan := InsightInvalidationPlan{
		StaleInsightIDs:      sortedInsightIDs(affected),
		ReintegrationRootIDs: sortedInsightIDs(roots),
	}
	return plan, nil
}

func validateInsightDerivationAcyclic(byID map[string]InsightDerivation) error {
	const (
		unvisited = iota
		visiting
		visited
	)
	state := make(map[string]int, len(byID))
	var visit func(string) error
	visit = func(insightID string) error {
		if state[insightID] == visiting {
			return fmt.Errorf("%w: insight derivation cycle at %q", ErrInvalidContract, insightID)
		}
		if state[insightID] == visited {
			return nil
		}
		state[insightID] = visiting
		for _, inputID := range byID[insightID].InputIDs {
			if _, isInsight := byID[inputID]; isInsight {
				if err := visit(inputID); err != nil {
					return err
				}
			}
		}
		state[insightID] = visited
		return nil
	}
	for insightID := range byID {
		if err := visit(insightID); err != nil {
			return err
		}
	}
	return nil
}

func sortedInsightIDs(ids map[string]struct{}) []string {
	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}
