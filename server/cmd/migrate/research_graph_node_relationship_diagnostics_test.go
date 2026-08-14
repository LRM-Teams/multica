package main

import (
	"strings"
	"testing"
)

func TestResearchGraphNodeRelationshipDiagnosticsMigrationContract(t *testing.T) {
	up, down := readMigrationPair(t, "364_research_graph_node_relationship_diagnostics")
	for _, required := range []string{
		"'research_graph_node_payload'",
		"research_graph_node_run_event_scoped_fkey",
		"FOREIGN KEY (workspace_id,session_id,run_event_id)",
		"research_artifact_scan_research_graph_node_migration_diagnostics",
		"research_graph_node_relationship_diagnostic_guard",
		"'/payload/source_id','legacy_source'",
		"'/payload/question_id','question'",
		"'/payload/task_id','task'",
		"'/payload/details/question_id','question'",
		"'/payload/details/task_id','task'",
		"'cross_scope_reference'",
		"'unresolved_reference'",
		"'malformed_uuid'",
	} {
		if !strings.Contains(up, required) {
			t.Fatalf("migration 364 up missing %q", required)
		}
	}
	for _, required := range []string{
		"DROP TRIGGER IF EXISTS research_graph_node_relationship_diagnostic_guard",
		"DROP CONSTRAINT IF EXISTS research_graph_node_run_event_scoped_fkey",
		"DELETE FROM research_artifact_migration_diagnostic WHERE owner_kind='graph_node'",
	} {
		if !strings.Contains(down, required) {
			t.Fatalf("migration 364 down missing %q", required)
		}
	}
}
