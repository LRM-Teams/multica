package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestMigration377MaterializesQuestionReferenceEdges(t *testing.T) {
	up, err := os.ReadFile("../../migrations/377_research_question_reference_edges.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(up)
	for _, required := range []string{
		"research_artifact_materialize_question_references",
		"passport.current_version",
		"passport.entity_kind='question'",
		"'question_parent'",
		"'created_by_task'",
		"'answer_claim'",
		"cyclic_local_reference",
		"question_relationship_migration",
		"research_artifact_scan_session_migration_diagnostics",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("migration 377 missing %q", required)
		}
	}
	if strings.Contains(sql, "DELETE FROM research_artifact_input_reference") {
		t.Error("migration 377 rescan must be append-only")
	}
	down, err := os.ReadFile("../../migrations/377_research_question_reference_edges.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(down), "DELETE FROM research_artifact_input_reference") {
		t.Error("migration 377 down must preserve append-only input-reference history")
	}
}
