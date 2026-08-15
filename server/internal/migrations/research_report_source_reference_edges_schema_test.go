package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestMigration375MaterializesReportSourceReferenceEdges(t *testing.T) {
	up, err := os.ReadFile("../../migrations/375_research_report_source_reference_edges.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(up)
	for _, required := range []string{
		"research_artifact_materialize_report_source_references",
		"research_artifact_scan_research_report_migration_diagnostics",
		"passport.current_version",
		"passport.entity_kind='legacy_source'",
		"relation='report_source'",
		"report_source_migration",
		"WITH ORDINALITY",
		"unresolved_reference",
		"v_diagnostics>0",
		"research_artifact_scan_session_migration_diagnostics",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("migration 375 missing %q", required)
		}
	}
	down, err := os.ReadFile("../../migrations/375_research_report_source_reference_edges.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(down), "DELETE FROM research_artifact_input_reference") {
		t.Error("migration 375 down must preserve append-only input-reference history")
	}
	if strings.Contains(sql, "DELETE FROM research_artifact_input_reference") {
		t.Error("migration 375 rescan must be append-only")
	}
}
