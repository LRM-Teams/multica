package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestMigration355DefinesCanonicalTaskInquiryTargets(t *testing.T) {
	up, err := os.ReadFile("../../migrations/355_research_task_inquiry_target.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(up)
	for _, required := range []string{
		"CREATE TABLE IF NOT EXISTS research_task_inquiry_target",
		"PRIMARY KEY (workspace_id, session_id, task_id, target_kind, target_entity_id)",
		"REFERENCES research_session(workspace_id, id) ON DELETE CASCADE",
		"REFERENCES research_task(workspace_id, session_id, id) ON DELETE CASCADE",
		"REFERENCES research_task_attempt(workspace_id, session_id, id) ON DELETE CASCADE",
		"research_inquiry_entity_exists(NEW.workspace_id, NEW.session_id, NEW.target_kind, NEW.target_entity_id)",
		"SELECT 1 FROM research_dispute",
		"t.goal_version=NEW.goal_version AND t.plan_version=NEW.plan_version",
		"research_task_inquiry_target_insert_guard",
		"research_task_inquiry_target_immutable",
		"Task Inquiry targets are append-only",
		"WHERE target_kind='branch'",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("migration 355 missing %q", required)
		}
	}
}

func TestMigration355DownRemovesTaskInquiryTargetsInDependencyOrder(t *testing.T) {
	down, err := os.ReadFile("../../migrations/355_research_task_inquiry_target.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(down)
	trigger := strings.Index(sql, "DROP TRIGGER IF EXISTS research_task_inquiry_target_immutable")
	function := strings.Index(sql, "DROP FUNCTION IF EXISTS research_task_inquiry_target_append_only")
	table := strings.Index(sql, "DROP TABLE IF EXISTS research_task_inquiry_target")
	if trigger < 0 || function <= trigger || table <= function {
		t.Fatal("down migration does not remove trigger, function, and table in dependency order")
	}
}
