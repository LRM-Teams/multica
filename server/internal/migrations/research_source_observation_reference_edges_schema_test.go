package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestMigration376MaterializesSourceObservationReferenceEdges(t *testing.T) {
	up, err := os.ReadFile("../../migrations/376_research_source_observation_reference_edges.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(up)
	for _, required := range []string{
		"research_artifact_materialize_legacy_source_reference",
		"research_artifact_materialize_observation_reference",
		"research_artifact_scan_research_legacy_source_migration_diagnostics",
		"passport.current_version",
		"passport.entity_kind='source_snapshot'",
		"'projects','source_projection_migration'",
		"'observes','observation_migration'",
		"unresolved_reference",
		"research_artifact_scan_session_migration_diagnostics",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("migration 376 missing %q", required)
		}
	}
	if strings.Contains(sql, "DELETE FROM research_artifact_input_reference") {
		t.Error("migration 376 rescan must be append-only")
	}
	down, err := os.ReadFile("../../migrations/376_research_source_observation_reference_edges.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(down), "DELETE FROM research_artifact_input_reference") {
		t.Error("migration 376 down must preserve append-only input-reference history")
	}
}
