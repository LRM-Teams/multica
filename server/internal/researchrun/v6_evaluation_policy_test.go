package researchrun

import "testing"

func TestValidateV6EvaluationDefectPolicyMapsToFrozenDimensions(t *testing.T) {
	defect := V6EvaluationDefectPolicy{DefectID: "d1", Kind: V6DefectFreshness, WireDimension: "integration_quality", Blocking: true, TargetIDs: []string{"insight-1"}, Problem: "stale integration", RequiredChange: "reintegrate"}
	if err := ValidateV6EvaluationDefectPolicy(defect); err != nil {
		t.Fatal(err)
	}
	defect.WireDimension = "freshness"
	if err := ValidateV6EvaluationDefectPolicy(defect); err == nil {
		t.Fatal("expected invented wire dimension rejection")
	}
}

func TestValidateV6ReviewCoverageRequiresExactLatestReportCoverage(t *testing.T) {
	report := V6ReportInventory{ClaimIDs: []string{"c1", "c2"}, SectionIDs: []string{"s1"}, InsightIDs: []string{"i1"}}
	coverage := V6ReviewCoverage{ReviewedClaimIDs: []string{"c2", "c1"}, ReviewedSectionIDs: []string{"s1"}, ReviewedInsightIDs: []string{"i1"}, CitationClaimIDs: []string{"c1", "c2"}, CitationSectionIDs: []string{"s1"}}
	if err := ValidateV6ReviewCoverage(report, coverage); err != nil {
		t.Fatal(err)
	}
	coverage.CitationClaimIDs = []string{"c1"}
	if err := ValidateV6ReviewCoverage(report, coverage); err == nil {
		t.Fatal("expected incomplete citation coverage rejection")
	}
	coverage.CitationClaimIDs = []string{"c1", "c2"}
	coverage.ReviewedInsightIDs = nil
	if err := ValidateV6ReviewCoverage(report, coverage); err == nil {
		t.Fatal("expected incomplete Insight review rejection")
	}
}
