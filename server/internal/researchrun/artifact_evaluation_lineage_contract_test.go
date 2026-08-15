package researchrun

import (
	"os"
	"strings"
	"testing"
)

func TestEvaluationDecisionLocalLineageUsesCanonicalArtifactVersions(t *testing.T) {
	source, err := os.ReadFile("postgres_artifact.go")
	if err != nil {
		t.Fatal(err)
	}
	code := string(source)
	for _, required := range []string{
		`"decision_reviewed_claim"`,
		`"decision_defect_claim"`,
		`"decision_reviewed_report_section"`,
		`"decision_defect_report_section"`,
		"claimIDs[0], ArtifactKindClaim",
		"reportID, ArtifactKindReportRevision",
	} {
		if !strings.Contains(code, required) {
			t.Errorf("evaluation Decision lineage is missing %q", required)
		}
	}
}
