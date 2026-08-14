package migrations

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMigration353BuildsScopedAppendOnlySearchCorpusLineage(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	dir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
	up, err := os.ReadFile(filepath.Join(dir, "353_research_search_corpus_lineage.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	contents := string(up)
	for _, required := range []string{
		"CREATE TABLE research_search_plan",
		"CREATE TABLE research_query_execution",
		"CREATE TABLE research_source_candidate",
		"CREATE TABLE research_screening_decision",
		"'search_plan', 'query_execution', 'source_candidate', 'screening_decision'",
		"FOREIGN KEY (workspace_id, session_id, search_plan_id)",
		"FOREIGN KEY (workspace_id, session_id, query_execution_id, source_candidate_id)",
		"research_query_execution_append_only_guard",
		"research_search_plan_append_only_guard",
		"research_source_candidate_append_only_guard",
		"research_screening_decision_append_only_guard",
		"research_search_plan_artifact_passport_guard",
		"research_query_execution_artifact_passport_guard",
		"research_source_candidate_artifact_passport_guard",
		"research_screening_decision_artifact_passport_guard",
		"research_search_lineage_passport_delete_guard",
		"research_source_snapshot_ingestion_lineage_check",
		"research_source_snapshot_screening_lineage_guard",
		"d.disposition = 'accepted'",
		"candidate.canonical_url <> NEW.canonical_url",
		"candidate.content_hash <> ('sha256:' || NEW.content_hash)",
	} {
		if !strings.Contains(contents, required) {
			t.Errorf("migration 353 missing %q", required)
		}
	}
	down, err := os.ReadFile(filepath.Join(dir, "353_research_search_corpus_lineage.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"DROP COLUMN IF EXISTS screening_decision_id",
		"DROP COLUMN IF EXISTS ingestion_kind",
		"DROP TABLE IF EXISTS research_screening_decision",
		"DROP TABLE IF EXISTS research_source_candidate",
		"DROP TABLE IF EXISTS research_query_execution",
		"DROP TABLE IF EXISTS research_search_plan",
	} {
		if !strings.Contains(string(down), required) {
			t.Errorf("migration 353 down missing %q", required)
		}
	}
}
