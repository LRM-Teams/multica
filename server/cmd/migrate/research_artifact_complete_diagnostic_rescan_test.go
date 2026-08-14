package main

import (
	"strings"
	"testing"
)

func TestResearchArtifactCompleteDiagnosticRescanMigrationContract(t *testing.T) {
	up, down := readMigrationPair(t, "366_research_artifact_complete_diagnostic_rescan")
	for _, scanner := range []string{
		"research_artifact_scan_research_message_migration_diagnostics",
		"research_artifact_scan_research_decision_migration_diagnostics",
		"research_artifact_scan_research_report_migration_diagnostics",
		"research_artifact_scan_research_run_event_migration_diagnostics",
		"research_artifact_scan_research_graph_node_migration_diagnostics",
		"research_artifact_scan_research_legacy_source_migration_diagnostics",
	} {
		if !strings.Contains(up, scanner) {
			t.Fatalf("migration 366 must invoke %s", scanner)
		}
	}
	for _, optional := range []string{
		"research_artifact_scan_research_graph_node_migration_diagnostics(uuid,uuid,uuid)",
		"research_artifact_scan_research_legacy_source_migration_diagnostics(uuid,uuid,uuid)",
	} {
		if !strings.Contains(up, "to_regprocedure(\n    '"+optional+"'") {
			t.Fatalf("migration 366 must safely discover optional later parser %s", optional)
		}
	}
	for _, scanner := range []string{
		"research_artifact_scan_research_message_migration_diagnostics",
		"research_artifact_scan_research_decision_migration_diagnostics",
		"research_artifact_scan_research_run_event_migration_diagnostics",
	} {
		if !strings.Contains(down, scanner) {
			t.Fatalf("migration 366 down missing legacy scanner %s", scanner)
		}
	}
	for _, added := range []string{
		"research_artifact_scan_research_report_migration_diagnostics",
		"research_artifact_scan_research_graph_node_migration_diagnostics",
		"research_artifact_scan_research_legacy_source_migration_diagnostics",
	} {
		if strings.Contains(down, added) {
			t.Fatalf("migration 366 down retained added scanner %s", added)
		}
	}
}
