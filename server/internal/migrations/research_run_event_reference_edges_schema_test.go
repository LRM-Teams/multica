package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestMigration381MaterializesRunEventReferenceEdges(t *testing.T) {
	up, err := os.ReadFile("../../migrations/381_research_run_event_reference_edges.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(up)
	for _, required := range []string{
		"research_artifact_materialize_run_event_references",
		"research_artifact_scan_research_run_event_migration_diagnostics",
		"passport.current_version",
		"'event_task'",
		"'event_attempt'",
		"'event_question'",
		"'event_report'",
		"research_artifact_scan_session_migration_diagnostics_v380",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("migration 381 missing %q", required)
		}
	}
	if strings.Contains(sql, "DELETE FROM research_artifact_input_reference") {
		t.Error("migration 381 must be append-only")
	}
}
