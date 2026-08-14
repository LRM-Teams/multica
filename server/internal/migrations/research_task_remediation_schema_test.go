package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestMigration369DefinesClosedTaskRemediationRelationshipParser(t *testing.T) {
	up, err := os.ReadFile("../../migrations/369_research_task_remediation_relationship_schema.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(up)
	for _, required := range []string{
		"research_task_remediation_acceptance_criteria",
		"v_client_key NOT LIKE 'control:%'",
		"target_findings",
		"question_id",
		"answer_claim_id",
		"claim_key",
		"evidence_standard_key",
		"evaluation_decision_id",
		"report_id",
		"task_id",
		"attempt_id",
		"cross_scope_reference",
		"dangling_local_key",
		"ambiguous_local_key",
		"goal_version=p_goal_version AND plan_version=p_plan_version",
		"research_task_remediation_relationship_diagnostic_refresh",
		"research_artifact_scan_session_migration_diagnostics",
		"research_artifact_scan_research_task_migration_diagnostics(p_workspace_id,p_session_id,v_owner_id)",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("migration 369 missing %q", required)
		}
	}
}

func TestMigration369LeavesOpaqueAgentCriteriaUninterpreted(t *testing.T) {
	up, err := os.ReadFile("../../migrations/369_research_task_remediation_relationship_schema.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(up)
	boundary := strings.Index(sql, "v_client_key NOT LIKE 'control:%'")
	parser := strings.Index(sql, "v_targets := v_criteria#>'{remediation,target_findings}'")
	if boundary < 0 || parser <= boundary {
		t.Fatal("known control-task discriminator must guard remediation parsing")
	}
}
