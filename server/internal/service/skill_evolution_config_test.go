// SPDX-License-Identifier: Apache-2.0

package service

import "testing"

func TestSkillEvolutionFeatureGatesDefaultClosed(t *testing.T) {
	if got := LoadSkillEvolutionFeatureGates(func(string) string { return "" }); got != (SkillEvolutionFeatureGates{}) {
		t.Fatalf("default gates = %+v, want all disabled", got)
	}
}

func TestSkillEvolutionFeatureGatesRequireDependencies(t *testing.T) {
	env := map[string]string{
		graphNavigationV2EnabledEnv:         "true",
		patternConsolidationEnabledEnv:      "1",
		skillCandidateGenerationEnabledEnv:  "TRUE",
		skillShadowEvaluationEnabledEnv:     "true",
		skillRuntimePromotionEnabledEnv:     "1",
		spreadsheetSkillEvolutionEnabledEnv: "true",
	}
	got := LoadSkillEvolutionFeatureGates(func(k string) string { return env[k] })
	want := SkillEvolutionFeatureGates{
		GraphNavigationV2: true, PatternConsolidation: true, CandidateGeneration: true,
		ShadowEvaluation: true, RuntimePromotion: true, SpreadsheetSkillProfile: true,
	}
	if got != want {
		t.Fatalf("all gates = %+v, want %+v", got, want)
	}

	delete(env, patternConsolidationEnabledEnv)
	got = LoadSkillEvolutionFeatureGates(func(k string) string { return env[k] })
	if !got.GraphNavigationV2 || got.PatternConsolidation || got.CandidateGeneration || got.ShadowEvaluation || got.RuntimePromotion || got.SpreadsheetSkillProfile {
		t.Fatalf("missing pattern gate must disable dependents, got %+v", got)
	}
}

func TestSkillEvolutionFeatureGatesInvalidValuesFailClosed(t *testing.T) {
	env := map[string]string{
		graphNavigationV2EnabledEnv:    "yes",
		patternConsolidationEnabledEnv: "on",
	}
	got := LoadSkillEvolutionFeatureGates(func(k string) string { return env[k] })
	if got != (SkillEvolutionFeatureGates{}) {
		t.Fatalf("invalid gate values = %+v, want all disabled", got)
	}
}
