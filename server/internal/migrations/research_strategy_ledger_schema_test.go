package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestMigration354DefinesDurableStrategyLedgerAndRunPin(t *testing.T) {
	up, err := os.ReadFile("../../migrations/354_research_strategy_ledger.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(up)
	for _, required := range []string{
		"CREATE TABLE research_strategy_version",
		"CREATE TABLE research_strategy_evaluation",
		"CREATE TABLE research_strategy_promotion_decision",
		"CREATE TABLE research_strategy_pointer",
		"CREATE TABLE research_run_strategy_assignment",
		"workspace_research_strategy_initialize",
		"research_session_strategy_pin",
		"research_run_strategy_assignment_append_only",
		"research_strategy_pointer_transition_guard",
		"research_validate_strategy_actor",
		"research_strategy_decision_approval_guard",
		"research_strategy_decision_effect_guard",
		"UNIQUE (workspace_id, request_key)",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("migration 354 missing %q", required)
		}
	}
}

func TestMigration354DownDropsRunPinBeforeStrategyLedger(t *testing.T) {
	down, err := os.ReadFile("../../migrations/354_research_strategy_ledger.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(down)
	assignment := strings.Index(sql, "DROP TABLE IF EXISTS research_run_strategy_assignment")
	pointer := strings.Index(sql, "DROP TABLE IF EXISTS research_strategy_pointer")
	version := strings.Index(sql, "DROP TABLE IF EXISTS research_strategy_version")
	if assignment < 0 || pointer <= assignment || version <= pointer {
		t.Fatal("down migration does not remove dependents before Strategy versions")
	}
}
