package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestMigration373MaterializesMessageMatchReferenceEdges(t *testing.T) {
	up, err := os.ReadFile("../../migrations/373_research_message_match_reference_edges.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(up)
	for _, required := range []string{
		"research_artifact_materialize_message_match_references",
		"passport.current_version",
		"passport.entity_kind=p_target_kind",
		"match_utterance",
		"match_primary_anchor",
		"match_candidate",
		"match_decision",
		"duplicate_local_key",
		"unresolved_reference",
		"WITH ORDINALITY",
		"purpose,ordinal",
		"v_diagnostics>0",
		"research_artifact_scan_session_migration_diagnostics",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("migration 373 missing %q", required)
		}
	}
}
