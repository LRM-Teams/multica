package main

import (
	"strings"
	"testing"
)

func TestResearchRunEventRelationshipSchemaMigrationContract(t *testing.T) {
	up, down := readMigrationPair(t, "367_research_run_event_relationship_schema")
	for _, required := range []string{
		"research_artifact_run_event_relationship_schema_allowed",
		"research_artifact_diagnose_scoped_question_reference",
		"'/payload/question_id'",
		"'/event_type'",
		"'run_event_schema'",
		"'unknown_schema'",
		"research_run_event_relationship_diagnostic_refresh",
	} {
		if !strings.Contains(up, required) {
			t.Fatalf("migration 367 up missing %q", required)
		}
	}
	for _, required := range []string{
		"DROP TRIGGER IF EXISTS research_run_event_relationship_diagnostic_refresh",
		"DROP FUNCTION IF EXISTS research_artifact_run_event_relationship_schema_allowed",
		"CREATE OR REPLACE FUNCTION research_artifact_scan_research_run_event_migration_diagnostics",
	} {
		if !strings.Contains(down, required) {
			t.Fatalf("migration 367 down missing %q", required)
		}
	}
}
