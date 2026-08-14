package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestMigration383MaterializesEvaluationLocalReferenceEdges(t *testing.T) {
	up, err := os.ReadFile("../../migrations/383_research_evaluation_local_reference_edges.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(up)
	for _, required := range []string{
		"research_artifact_scan_research_evaluation_local_diagnostics",
		"research_artifact_materialize_evaluation_local_references",
		`'decision_reviewed_claim'`,
		`'decision_defect_claim'`,
		`'decision_reviewed_report_section'`,
		`'decision_defect_report_section'`,
		"research_artifact_scan_session_migration_diagnostics_v382",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("migration 383 missing %q", required)
		}
	}
	if strings.Contains(sql, "DELETE FROM research_artifact_input_reference") {
		t.Error("migration 383 must be append-only")
	}
}
