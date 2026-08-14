package researchrun

import "fmt"

type V6EvaluationDefectKind string

const (
	V6DefectMethodAdherence     V6EvaluationDefectKind = "method_adherence"
	V6DefectScopeDrift          V6EvaluationDefectKind = "scope_drift"
	V6DefectEvidenceSufficiency V6EvaluationDefectKind = "evidence_sufficiency"
	V6DefectConflictHandling    V6EvaluationDefectKind = "conflict_handling"
	V6DefectCalibration         V6EvaluationDefectKind = "calibration"
	V6DefectDecisionUsability   V6EvaluationDefectKind = "decision_usability"
	V6DefectFreshness           V6EvaluationDefectKind = "freshness"
)

type V6EvaluationDefectPolicy struct {
	DefectID       string
	Kind           V6EvaluationDefectKind
	WireDimension  string
	Blocking       bool
	TargetIDs      []string
	Problem        string
	RequiredChange string
}

func ValidateV6EvaluationDefectPolicy(defect V6EvaluationDefectPolicy) error {
	expected, ok := v6DefectWireDimension(defect.Kind)
	if !ok || defect.DefectID == "" || defect.Problem == "" || defect.RequiredChange == "" || len(defect.TargetIDs) == 0 {
		return fmt.Errorf("%w: V6 evaluation defect is incomplete", ErrInvalidContract)
	}
	if defect.WireDimension != expected {
		return fmt.Errorf("%w: defect %q must map to frozen V6 dimension %q", ErrInvalidContract, defect.Kind, expected)
	}
	return nil
}

func v6DefectWireDimension(kind V6EvaluationDefectKind) (string, bool) {
	switch kind {
	case V6DefectMethodAdherence, V6DefectScopeDrift:
		return "instruction_adherence", true
	case V6DefectEvidenceSufficiency:
		return "factual_grounding", true
	case V6DefectConflictHandling:
		return "contradiction_handling", true
	case V6DefectCalibration:
		return "analytical_depth", true
	case V6DefectDecisionUsability:
		return "coverage", true
	case V6DefectFreshness:
		return "integration_quality", true
	default:
		return "", false
	}
}

type V6ReportInventory struct {
	ClaimIDs   []string
	SectionIDs []string
	InsightIDs []string
}

type V6ReviewCoverage struct {
	ReviewedClaimIDs   []string
	ReviewedSectionIDs []string
	ReviewedInsightIDs []string
	CitationClaimIDs   []string
	CitationSectionIDs []string
}

func ValidateV6ReviewCoverage(report V6ReportInventory, coverage V6ReviewCoverage) error {
	checks := []struct {
		name             string
		required, actual []string
	}{
		{"quality Claims", report.ClaimIDs, coverage.ReviewedClaimIDs},
		{"quality sections", report.SectionIDs, coverage.ReviewedSectionIDs},
		{"quality Insights", report.InsightIDs, coverage.ReviewedInsightIDs},
		{"citation Claims", report.ClaimIDs, coverage.CitationClaimIDs},
		{"citation sections", report.SectionIDs, coverage.CitationSectionIDs},
	}
	for _, check := range checks {
		if !sameV6ReviewSet(check.required, check.actual) {
			return fmt.Errorf("%w: %s do not exactly cover latest report", ErrInvalidContract, check.name)
		}
	}
	return nil
}

func sameV6ReviewSet(required, actual []string) bool {
	if len(required) != len(actual) {
		return false
	}
	set := map[string]struct{}{}
	for _, value := range required {
		if value == "" {
			return false
		}
		set[value] = struct{}{}
	}
	if len(set) != len(required) {
		return false
	}
	for _, value := range actual {
		if _, ok := set[value]; !ok {
			return false
		}
		delete(set, value)
	}
	return len(set) == 0
}
