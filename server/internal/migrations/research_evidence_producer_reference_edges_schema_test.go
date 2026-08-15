package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestMigration380MaterializesEvidenceProducerReferenceEdges(t *testing.T) {
	up, err := os.ReadFile("../../migrations/380_research_evidence_producer_reference_edges.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(up)
	for _, required := range []string{
		"research_artifact_materialize_source_snapshot_producer",
		"research_artifact_materialize_observation_complete_references",
		"research_artifact_materialize_observation_producer",
		"research_artifact_materialize_observation_reference",
		"passport.current_version",
		"'source_producer'",
		"'observation_producer'",
		"research_artifact_scan_session_migration_diagnostics_v379",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("migration 380 missing %q", required)
		}
	}
	if strings.Contains(sql, "DELETE FROM research_artifact_input_reference") {
		t.Error("migration 380 must be append-only")
	}
	down, err := os.ReadFile("../../migrations/380_research_evidence_producer_reference_edges.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(down), "DELETE FROM research_artifact_input_reference") {
		t.Error("migration 380 down must preserve append-only lineage")
	}
}
