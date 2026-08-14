package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestMigration382MaterializesDecisionReferenceEdges(t *testing.T) {
	up, err := os.ReadFile("../../migrations/382_research_decision_reference_edges.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(up)
	for _, required := range []string{
		"research_artifact_materialize_decision_references",
		"research_artifact_scan_research_decision_migration_diagnostics",
		"passport.current_version", "'decision_input_task'", "'decision_input_attempt'",
		"'decision_input_question'", "'decision_input_report'", "'decision_creator_task'",
		"'decision_evaluation'", "'decision_affected_branch'", "'decision_obsolete_task'",
		"research_artifact_scan_session_migration_diagnostics_v381",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("migration 382 missing %q", required)
		}
	}
	if strings.Contains(sql, "DELETE FROM research_artifact_input_reference") {
		t.Error("migration 382 must be append-only")
	}
}
