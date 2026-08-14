package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestMigration379MaterializesClaimEvidenceReferenceEdges(t *testing.T) {
	up, err := os.ReadFile("../../migrations/379_research_claim_evidence_reference_edges.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(up)
	for _, required := range []string{
		"research_artifact_materialize_claim_references",
		"research_artifact_materialize_evidence_link_references",
		"research_artifact_scan_research_claim_method_diagnostics",
		"passport.current_version",
		"'claim_producer'",
		"'claim_evidence_standard'",
		"'evidence_claim'",
		"'evidence_observation'",
		"'evidence_verifier'",
		"research_artifact_scan_session_migration_diagnostics",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("migration 379 missing %q", required)
		}
	}
	if strings.Contains(sql, "DELETE FROM research_artifact_input_reference") {
		t.Error("migration 379 must be append-only")
	}
	down, err := os.ReadFile("../../migrations/379_research_claim_evidence_reference_edges.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(down), "DELETE FROM research_artifact_input_reference") {
		t.Error("migration 379 down must preserve append-only lineage")
	}
}
