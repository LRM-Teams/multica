// SPDX-License-Identifier: Apache-2.0

package service

import "strings"

// SkillEvolutionFeatureGates controls the independently deployable stages of
// Graph Navigation v2 and Skill Evolution. Every gate defaults to false; a
// later stage is enabled only when all of its required earlier stages are
// enabled as well.
type SkillEvolutionFeatureGates struct {
	GraphNavigationV2       bool
	PatternConsolidation    bool
	CandidateGeneration     bool
	ShadowEvaluation        bool
	RuntimePromotion        bool
	SpreadsheetSkillProfile bool
}

const (
	graphNavigationV2EnabledEnv         = "MULTICA_GRAPH_NAVIGATION_V2_ENABLED"
	patternConsolidationEnabledEnv      = "MULTICA_PATTERN_CONSOLIDATION_ENABLED"
	skillCandidateGenerationEnabledEnv  = "MULTICA_SKILL_CANDIDATE_GENERATION_ENABLED"
	skillShadowEvaluationEnabledEnv     = "MULTICA_SKILL_SHADOW_EVALUATION_ENABLED"
	skillRuntimePromotionEnabledEnv     = "MULTICA_SKILL_RUNTIME_PROMOTION_ENABLED"
	spreadsheetSkillEvolutionEnabledEnv = "MULTICA_SPREADSHEET_SKILL_EVOLUTION_ENABLED"
)

// LoadSkillEvolutionFeatureGates parses only explicit true/1 values. Invalid
// values fail closed. Dependency normalization prevents an environment typo or
// partial rollout from activating a writer without its required read/audit
// foundations.
func LoadSkillEvolutionFeatureGates(getenv func(string) string) SkillEvolutionFeatureGates {
	gates := SkillEvolutionFeatureGates{
		GraphNavigationV2:       skillEvolutionEnvEnabled(getenv(graphNavigationV2EnabledEnv)),
		PatternConsolidation:    skillEvolutionEnvEnabled(getenv(patternConsolidationEnabledEnv)),
		CandidateGeneration:     skillEvolutionEnvEnabled(getenv(skillCandidateGenerationEnabledEnv)),
		ShadowEvaluation:        skillEvolutionEnvEnabled(getenv(skillShadowEvaluationEnabledEnv)),
		RuntimePromotion:        skillEvolutionEnvEnabled(getenv(skillRuntimePromotionEnabledEnv)),
		SpreadsheetSkillProfile: skillEvolutionEnvEnabled(getenv(spreadsheetSkillEvolutionEnabledEnv)),
	}
	if !gates.GraphNavigationV2 {
		gates.PatternConsolidation = false
	}
	if !gates.PatternConsolidation {
		gates.CandidateGeneration = false
	}
	if !gates.CandidateGeneration {
		gates.ShadowEvaluation = false
	}
	if !gates.ShadowEvaluation {
		gates.RuntimePromotion = false
		gates.SpreadsheetSkillProfile = false
	}
	return gates
}

func skillEvolutionEnvEnabled(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true":
		return true
	default:
		return false
	}
}
