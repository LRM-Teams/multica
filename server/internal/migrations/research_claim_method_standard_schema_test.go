package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestMigration371DefinesClaimMethodStandardParser(t *testing.T) {
	up, err := os.ReadFile("../../migrations/371_research_claim_method_standard_reference.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(up)
	for _, required := range []string{
		"research_claim_method_evidence_standard",
		"decision_kind='research_method'",
		"goal_version=v_goal_version",
		"plan_version=v_plan_version",
		"v_method->'evidence_standards'",
		"standard->>'client_key'=v_key",
		"dangling_local_key",
		"ambiguous_local_key",
		"unknown_schema",
		"research_claim_method_standard_diagnostic_refresh",
		"research_artifact_scan_research_claim_method_diagnostics($1,$2,$3)",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("migration 371 missing %q", required)
		}
	}
}
