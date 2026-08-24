package migrations

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRonaldoV6StorageMigrationSlicesAreContiguousAndGuarded(t *testing.T) {
	wants := map[int][]string{
		390: {"research_director_assignment", "research_team_membership", "research_v6_team_cap_guard"},
		391: {"research_work_item_attempt", "research_work_catalog_page", "research_v6_work_item_idempotency_idx"},
		392: {"research_result_node", "research_insight_version", "research_branch_frontier"},
		393: {"research_node_absorption", "research_match_decision", "research_v6_absorption_guard"},
		394: {"research_discussion_turn", "research_discussion_vote", "research_v6_discussion_one_active_idx"},
		395: {"research_report_resource", "research_report_review", "research_v6_report_publish"},
		396: {"research_projection_snapshot", "research_projection_slice"},
		397: {"research_v6_artifact_class", "v6_insight_version"},
		398: {"research_v6_activation_evidence", "research_v6_append_only_guard"},
		425: {"research_v6_bootstrap_request_session_fk", "ON DELETE CASCADE"},
		443: {"research_work_item_activity_entry", "work_item_attempt_id", "message_sequence"},
	}
	root := filepath.Join("..", "..", "migrations")
	for number, fragments := range wants {
		matches, err := filepath.Glob(filepath.Join(root, strconv.Itoa(number)+"_research_v6_*.up.sql"))
		if err != nil || len(matches) != 1 {
			t.Fatalf("migration %d matches=%v error=%v", number, matches, err)
		}
		raw, err := os.ReadFile(matches[0])
		if err != nil {
			t.Fatal(err)
		}
		for _, fragment := range fragments {
			if !strings.Contains(string(raw), fragment) {
				t.Errorf("migration %d missing %q", number, fragment)
			}
		}
	}
}

func TestRonaldoV6WorkItemRecoveryMigrationPersistsCASAndFrozenInputs(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "401_research_v6_work_item_recovery.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"ADD COLUMN state_version", "ADD COLUMN manifest JSONB", "research_v6_work_submission",
		"request_content_hash", "research_v6_submission_reconcile_idx",
	} {
		if !strings.Contains(string(raw), fragment) {
			t.Errorf("recovery migration missing %q", fragment)
		}
	}
}
