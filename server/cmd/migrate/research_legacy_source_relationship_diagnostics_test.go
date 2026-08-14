package main

import (
	"strings"
	"testing"
)

func TestResearchLegacySourceRelationshipDiagnosticsMigrationContract(t *testing.T) {
	up, down := readMigrationPair(t, "365_research_legacy_source_relationship_diagnostics")
	for _, required := range []string{
		"'mismatched_reference'",
		"'research_legacy_source_payload'",
		"research_artifact_scan_research_legacy_source_migration_diagnostics",
		"'/payload/snapshot_id','source_snapshot'",
		"research_legacy_source_snapshot_payload_guard",
		"research_legacy_source_relationship_diagnostic_refresh",
		"NEW.source_snapshot_id IS NULL",
		"v_payload_value::uuid <> NEW.source_snapshot_id",
	} {
		if !strings.Contains(up, required) {
			t.Fatalf("migration 365 up missing %q", required)
		}
	}
	for _, required := range []string{
		"DROP TRIGGER IF EXISTS research_legacy_source_snapshot_payload_guard",
		"DELETE FROM research_artifact_migration_diagnostic WHERE owner_kind='legacy_source'",
		"'research_graph_node_payload'",
	} {
		if !strings.Contains(down, required) {
			t.Fatalf("migration 365 down missing %q", required)
		}
	}
}
