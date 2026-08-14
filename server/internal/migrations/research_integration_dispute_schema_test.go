package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestMigration350DefinesIntegrationAndDisputeCanonicalGraph(t *testing.T) {
	up, err := os.ReadFile("../../migrations/350_research_integration_dispute_graph.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(up)
	for _, required := range []string{"CREATE TABLE research_integration_round", "CREATE TABLE research_integration_contribution", "CREATE TABLE research_insight_derivation", "CREATE TABLE research_dispute", "CREATE TABLE research_dispute_position", "CREATE TABLE research_deliberation", "CREATE TABLE research_deliberation_turn", "research_insight_derivation_input_guard", "research_dispute_subject_guard", "FOREIGN KEY (workspace_id,session_id"} {
		if !strings.Contains(sql, required) {
			t.Fatalf("migration 350 missing %q", required)
		}
	}
}

func TestMigration350DownRemovesGraphInDependencyOrder(t *testing.T) {
	down, err := os.ReadFile("../../migrations/350_research_integration_dispute_graph.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(down)
	turn := strings.Index(sql, "DROP TABLE IF EXISTS research_deliberation_turn")
	deliberation := strings.Index(sql, "DROP TABLE IF EXISTS research_deliberation;")
	dispute := strings.Index(sql, "DROP TABLE IF EXISTS research_dispute;")
	if turn < 0 || deliberation <= turn || dispute <= deliberation {
		t.Fatalf("down migration dependency order is unsafe")
	}
}
