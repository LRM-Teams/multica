package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestMigration370DefinesEvaluationLocalReferenceParser(t *testing.T) {
	up, err := os.ReadFile("../../migrations/370_research_evaluation_decision_local_references.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(up)
	for _, required := range []string{
		"research_decision_evaluation_local_references",
		"v_kind NOT IN ('quality_gate','citation_audit')",
		"/outcome/reviewed_claim_keys",
		"/outcome/reviewed_section_ids",
		"/claim_keys",
		"/section_ids",
		"research_report_claim link",
		"claim.client_key=v_value",
		"report.structured->'sections'",
		"dangling_local_key",
		"ambiguous_local_key",
		"unknown_schema",
		"research_decision_z_evaluation_local_diagnostic_refresh",
		"research_artifact_scan_session_migration_diagnostics",
		"research_artifact_scan_research_evaluation_local_diagnostics(p_workspace_id,p_session_id,v_owner_id)",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("migration 370 missing %q", required)
		}
	}
}
