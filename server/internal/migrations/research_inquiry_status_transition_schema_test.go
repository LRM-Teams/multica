package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestMigration356DefinesEvidenceDrivenInquiryTransitions(t *testing.T) {
	up, err := os.ReadFile("../../migrations/356_research_inquiry_status_transition.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(up)
	for _, required := range []string{
		"CREATE TABLE research_inquiry_status_transition",
		"CREATE TABLE research_inquiry_status_evidence",
		"UNIQUE (workspace_id, session_id, event_sequence)",
		"REFERENCES research_task_attempt(workspace_id,session_id,id)",
		"REFERENCES research_run_event(session_id,sequence)",
		"research_inquiry_status_transition_target_guard",
		"research_inquiry_status_evidence_target_guard",
		"research_inquiry_status_transition_immutable",
		"research_inquiry_status_evidence_immutable",
		"WHEN 'question' THEN CASE old_status",
		"CREATE TRIGGER research_question_status_guard",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("migration 356 missing %q", required)
		}
	}
}

func TestMigration356DownRestoresPriorInquiryTransitionVocabulary(t *testing.T) {
	down, err := os.ReadFile("../../migrations/356_research_inquiry_status_transition.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(down)
	questionTrigger := strings.Index(sql, "DROP TRIGGER IF EXISTS research_question_status_guard")
	evidenceTable := strings.Index(sql, "DROP TABLE IF EXISTS research_inquiry_status_evidence")
	transitionTable := strings.Index(sql, "DROP TABLE IF EXISTS research_inquiry_status_transition")
	if questionTrigger < 0 || evidenceTable < 0 || transitionTable <= evidenceTable {
		t.Fatal("down migration dependency order is unsafe")
	}
	if strings.Contains(sql, "WHEN 'question' THEN CASE old_status") {
		t.Fatal("down migration must restore the pre-356 transition function")
	}
}
