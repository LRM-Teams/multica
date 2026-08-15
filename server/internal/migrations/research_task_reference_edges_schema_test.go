package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestMigration378MaterializesTaskReferenceEdges(t *testing.T) {
	up, err := os.ReadFile("../../migrations/378_research_task_reference_edges.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(up)
	for _, required := range []string{
		"research_artifact_materialize_task_references",
		"research_artifact_scan_research_task_migration_diagnostics",
		"passport.current_version",
		"'task_question'",
		"'task_parent'",
		"'task_dependency'",
		"'remediation_question'",
		"'remediation_answer_claim'",
		"'remediation_evaluation'",
		"'remediation_report'",
		"'remediation_task'",
		"'remediation_attempt'",
		"'remediation_claim_key'",
		"'remediation_evidence_standard'",
		"cyclic_local_reference",
		"research_artifact_scan_session_migration_diagnostics",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("migration 378 missing %q", required)
		}
	}
	if strings.Contains(sql, "DELETE FROM research_artifact_input_reference") {
		t.Error("migration 378 must be append-only")
	}
	down, err := os.ReadFile("../../migrations/378_research_task_reference_edges.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(down), "DELETE FROM research_artifact_input_reference") {
		t.Error("migration 378 down must preserve append-only lineage")
	}
}
