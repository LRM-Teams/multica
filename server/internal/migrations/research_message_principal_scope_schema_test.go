package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestMigration372ScopesResearchMessagePrincipalsAndLineage(t *testing.T) {
	up, err := os.ReadFile("../../migrations/372_research_message_principal_scope.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(up)
	for _, required := range []string{
		"research_message_sender_principal",
		"research_message_target_agent_scoped_fkey",
		"FOREIGN KEY (workspace_id,target_agent_id)",
		"REFERENCES agent(workspace_id,id)",
		"research_message_run_event_scoped_fkey",
		"FOREIGN KEY (workspace_id,session_id,run_event_id)",
		"REFERENCES research_run_event(workspace_id,session_id,id)",
		"research_message_sender_principal_guard",
		"WHEN 'system'",
		"WHEN 'user'",
		"WHEN 'agent'",
		"cross_scope_reference",
		"research_message_relationship_diagnostic_refresh",
		"research_artifact_scan_session_migration_diagnostics",
		"research_artifact_scan_research_message_sender_diagnostics($1,$2,$3)",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("migration 372 missing %q", required)
		}
	}
}
