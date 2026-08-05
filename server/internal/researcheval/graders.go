package researcheval

import (
	"context"
	"fmt"
)

type FactConflictGrader struct{}

func (FactConflictGrader) Name() string { return "fact_conflict_v1" }

func (FactConflictGrader) Grade(_ context.Context, evaluationCase Case, artifact Artifact) (Grade, error) {
	facts := map[string]ArtifactFact{}
	for _, fact := range artifact.Facts {
		facts[fact.Key] = fact
	}
	conflicts := map[string]ArtifactConflict{}
	for _, conflict := range artifact.Conflicts {
		conflicts[conflict.Key] = conflict
	}
	total := len(evaluationCase.Oracle.RequiredFacts) + len(evaluationCase.Oracle.ForbiddenFactKeys) + len(evaluationCase.Oracle.RequiredConflicts)
	earned := 0
	findings := []Finding{}
	for _, expected := range evaluationCase.Oracle.RequiredFacts {
		actual, exists := facts[expected.Key]
		if exists && actual.Value == expected.Value {
			earned++
			continue
		}
		findings = append(findings, Finding{Code: "required_fact_missing", Message: fmt.Sprintf("fact %q does not match the hidden expected value", expected.Key)})
	}
	for _, key := range evaluationCase.Oracle.ForbiddenFactKeys {
		if _, exists := facts[key]; !exists {
			earned++
			continue
		}
		findings = append(findings, Finding{Code: "forbidden_fact_accepted", Message: fmt.Sprintf("forbidden fact %q was accepted", key)})
	}
	for _, expected := range evaluationCase.Oracle.RequiredConflicts {
		actual, exists := conflicts[expected.Key]
		if exists && actual.Type == expected.Type {
			earned++
			continue
		}
		findings = append(findings, Finding{Code: "required_conflict_missing", Message: fmt.Sprintf("conflict %q/%s was not detected", expected.Key, expected.Type)})
	}
	return scoredGrade(earned, total, findings), nil
}

type TraceabilityGrader struct{}

func (TraceabilityGrader) Name() string { return "traceability_v1" }

func (TraceabilityGrader) Grade(_ context.Context, evaluationCase Case, artifact Artifact) (Grade, error) {
	sources := map[string]ArtifactSource{}
	for _, source := range artifact.Sources {
		sources[source.DocumentID] = source
	}
	facts := map[string]ArtifactFact{}
	for _, fact := range artifact.Facts {
		facts[fact.Key] = fact
	}
	claims := map[string]ArtifactClaim{}
	for _, claim := range artifact.Claims {
		claims[claim.Key] = claim
	}
	total := len(evaluationCase.Oracle.RequiredFacts) + len(evaluationCase.Oracle.RequiredReportClaims)
	earned := 0
	findings := []Finding{}
	for _, expected := range evaluationCase.Oracle.RequiredFacts {
		fact, exists := facts[expected.Key]
		if exists && containsAll(fact.SourceIDs, expected.RequiredSourceIDs) && allAcceptedSources(fact.SourceIDs, sources) {
			earned++
			continue
		}
		findings = append(findings, Finding{Code: "fact_provenance_missing", Message: fmt.Sprintf("fact %q is missing required accepted provenance", expected.Key)})
	}
	for _, expected := range evaluationCase.Oracle.RequiredReportClaims {
		claim, exists := claims[expected.Key]
		if exists && claim.InReport && containsAll(claim.FactKeys, expected.RequiredFactKeys) && allAcceptedSources(claim.SourceIDs, sources) {
			earned++
			continue
		}
		findings = append(findings, Finding{Code: "report_claim_untraceable", Message: fmt.Sprintf("report claim %q is absent or untraceable", expected.Key)})
	}
	return scoredGrade(earned, total, findings), nil
}

type SourceDisciplineGrader struct{}

func (SourceDisciplineGrader) Name() string { return "source_discipline_v1" }

func (SourceDisciplineGrader) Grade(_ context.Context, evaluationCase Case, artifact Artifact) (Grade, error) {
	accepted := map[string]ArtifactSource{}
	acceptedByFamily := map[string]int{}
	for _, source := range artifact.Sources {
		if source.Accepted {
			accepted[source.DocumentID] = source
			acceptedByFamily[source.Family]++
		}
	}
	total := len(evaluationCase.Oracle.ForbiddenDocumentIDs) + len(evaluationCase.Oracle.MaxAcceptedPerFamily)
	earned := 0
	findings := []Finding{}
	for _, id := range evaluationCase.Oracle.ForbiddenDocumentIDs {
		if _, exists := accepted[id]; !exists {
			earned++
			continue
		}
		findings = append(findings, Finding{Code: "forbidden_source_accepted", Message: fmt.Sprintf("forbidden document %q was accepted", id)})
	}
	for family, maximum := range evaluationCase.Oracle.MaxAcceptedPerFamily {
		if acceptedByFamily[family] <= maximum {
			earned++
			continue
		}
		findings = append(findings, Finding{Code: "source_family_overcounted", Message: fmt.Sprintf("family %q accepted %d sources, maximum %d", family, acceptedByFamily[family], maximum)})
	}
	return scoredGrade(earned, total, findings), nil
}

func scoredGrade(earned, total int, findings []Finding) Grade {
	score := 1.0
	if total > 0 {
		score = float64(earned) / float64(total)
	}
	return Grade{Score: score, Passed: earned == total, Metrics: map[string]float64{
		"criteria_met": float64(earned), "criteria_total": float64(total),
	}, Findings: findings}
}

func containsAll(actual, required []string) bool {
	set := map[string]struct{}{}
	for _, value := range actual {
		set[value] = struct{}{}
	}
	for _, value := range required {
		if _, exists := set[value]; !exists {
			return false
		}
	}
	return true
}

func allAcceptedSources(ids []string, sources map[string]ArtifactSource) bool {
	if len(ids) == 0 {
		return false
	}
	for _, id := range ids {
		source, exists := sources[id]
		if !exists || !source.Accepted {
			return false
		}
	}
	return true
}
