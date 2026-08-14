package main

import (
	"strings"
	"testing"
)

func TestResearchDecisionRelationshipSchemaMigrationContract(t *testing.T) {
	up, down := readMigrationPair(t, "368_research_decision_relationship_schema")
	for _, required := range []string{
		"research_artifact_decision_relationship_schema_allowed",
		"research_artifact_diagnose_scoped_decision_reference",
		"'/decision_kind'",
		"'/inputs/question_id'",
		"'/outcome/created_by_task_id'",
		"'/outcome/task_id'",
		"'/outcome/attempt_id'",
		"'/outcome/question_id'",
		"'/outcome/report_id'",
		"'/outcome/evaluation_decision_id'",
		"research_artifact_diagnose_decision_reference_array",
		"'/inputs/affected_branch_ids'",
		"'/outcome/impacted_branch_ids'",
		"'/outcome/obsolete_branch_ids'",
		"'/outcome/obsolete_task_ids'",
		"'/outcome/cancel_running_task_ids'",
		"'/outcome/retained_running_task_ids'",
		"research_decision_relationship_diagnostic_refresh",
	} {
		if !strings.Contains(up, required) {
			t.Fatalf("migration 368 up missing %q", required)
		}
	}
	for _, required := range []string{
		"DROP TRIGGER IF EXISTS research_decision_relationship_diagnostic_refresh",
		"DROP FUNCTION IF EXISTS research_artifact_decision_relationship_schema_allowed",
		"CREATE OR REPLACE FUNCTION research_artifact_scan_research_decision_migration_diagnostics",
	} {
		if !strings.Contains(down, required) {
			t.Fatalf("migration 368 down missing %q", required)
		}
	}
}
