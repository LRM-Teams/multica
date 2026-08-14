package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestMigration374MaterializesReportSourceReferenceEdges(t *testing.T) {
	up, err := os.ReadFile("../../migrations/374_research_report_source_reference_edges.up.sql")
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
		"duplicate_local_key",
		"unresolved_reference",
		"v_diagnostics>0",
		"research_artifact_scan_session_migration_diagnostics",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("migration 374 missing %q", required)
		}
	}
	down, err := os.ReadFile("../../migrations/374_research_report_source_reference_edges.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(down), "purpose='report_source_migration'") {
		t.Error("migration 374 down must preserve production report-source edges")
	}
}
