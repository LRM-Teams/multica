package researchrun

import (
	"os"
	"strings"
	"testing"
)

func requireSourceFragments(t *testing.T, path string, fragments ...string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, fragment := range fragments {
		if !strings.Contains(text, fragment) {
			t.Fatalf("%s missing %q", path, fragment)
		}
	}
}

func TestV6SubmissionApplicationTransactionBoundary(t *testing.T) {
	requireSourceFragments(t, "postgres_submission_processor_v6.go",
		"beginResearchTx(ctx, txOpV6SubmissionApply", "FOR UPDATE SKIP LOCKED", "applyTx.Rollback", "commitResearchTx")
}

func TestV6DiscussionPersistenceContract(t *testing.T) {
	requireSourceFragments(t, "../../../server/migrations/394_research_v6_discussion.up.sql",
		"CREATE TABLE research_discussion_input", "CREATE TABLE research_discussion_turn", "CREATE TABLE research_discussion_vote")
	requireSourceFragments(t, "postgres_discussion_v6.go", "FOR UPDATE OF f,st", "consensus_accept", "escalated")
}

func TestV6MatchDecisionPersistenceContract(t *testing.T) {
	requireSourceFragments(t, "../../../server/migrations/393_research_v6_absorption.up.sql",
		"input_artifact_version_ids UUID[]", "research_v6_match_current_idx", "UNIQUE(session_id,input_artifact_version_id)")
}

func TestV6ArtifactPassportHasFirstClassWorkAttemptProvenance(t *testing.T) {
	requireSourceFragments(t, "../../../server/migrations/403_research_v6_artifact_provenance.up.sql",
		"produced_by_work_item_attempt_id", "research_artifact_version_v6_attempt_fkey",
		"research_result_artifact_one_attempt_origin_check", "research_v6_result_node_version_fkey")
}

func TestV6DispatchPreparationTransactionBoundary(t *testing.T) {
	requireSourceFragments(t, "postgres_work_dispatch_v6.go", "txOpV6DispatchPrepare", "research_work_item_attempt", "dispatch_work_item", "persistV6CatalogPagesTx")
}

func TestV6DispatchCompletionTransactionBoundary(t *testing.T) {
	requireSourceFragments(t, "postgres_work_dispatch_v6.go", "txOpV6DispatchComplete", "inbox_task_id", "v6_work_item_dispatched")
}
