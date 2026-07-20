package migrations

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMigration186UpContainsBindingAndTriggerConstraints(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	path := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations", "186_env_dispatch_message_channels.up.sql")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration 186: %v", err)
	}
	contents := string(body)
	for _, required := range []string{
		"ADD COLUMN collaboration_trigger JSONB",
		"CREATE TABLE environment_agent_sandbox",
		"PRIMARY KEY (env_id, agent_id)",
		"UNIQUE (sandbox_instance_id)",
		"UNIQUE (runtime_id)",
		"status IN ('pending', 'provisioning', 'ready', 'failed', 'deleting')",
		"'clone'",
	} {
		if !strings.Contains(contents, required) {
			t.Errorf("migration 186 missing %q", required)
		}
	}
}

func TestMigration200RetiresThreadProjectionAndDraftOptions(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")

	up, err := os.ReadFile(filepath.Join(migrationsDir, "200_retire_channel_thread_main_projection.up.sql"))
	if err != nil {
		t.Fatalf("read migration 200 up: %v", err)
	}
	for _, required := range []string{
		"DROP COLUMN IF EXISTS main_timeline_visible",
		"DROP COLUMN IF EXISTS options",
	} {
		if !strings.Contains(string(up), required) {
			t.Errorf("migration 200 up missing %q", required)
		}
	}

	down, err := os.ReadFile(filepath.Join(migrationsDir, "200_retire_channel_thread_main_projection.down.sql"))
	if err != nil {
		t.Fatalf("read migration 200 down: %v", err)
	}
	if !strings.Contains(string(down), "ADD COLUMN IF NOT EXISTS options JSONB NOT NULL DEFAULT '{}'::jsonb") {
		t.Error("migration 200 down does not restore draft options")
	}
}

func TestMigration201ScopesFreshnessDraftsBySourceAndFailsClosed(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")

	up, err := os.ReadFile(filepath.Join(migrationsDir, "201_agent_transport_draft_decision_scope.up.sql"))
	if err != nil {
		t.Fatalf("read migration 201 up: %v", err)
	}
	contents := string(up)
	for _, required := range []string{
		"ADD COLUMN IF NOT EXISTS task_id UUID",
		"ADD COLUMN IF NOT EXISTS inbox_event_id UUID",
		"ADD COLUMN IF NOT EXISTS decision_fact_id TEXT",
		"audit.client_message_id = draft.client_message_id",
		"HAVING COUNT(audit.id) = 0",
		"(audit.task_id IS NULL) = (audit.inbox_event_id IS NULL)",
		"COALESCE(btrim(audit.context_pack->>'producer_fact_id'), '') = ''",
		"COUNT(DISTINCT CASE",
		"THEN 'task:' || audit.task_id::text",
		"THEN 'inbox:' || audit.inbox_event_id::text",
		"audit.context_pack->>'seen_up_to_seq' = draft.seen_up_to_seq::text",
		"audit.context_pack->>'latest_seq' = draft.held_to_seq::text",
		"COUNT(DISTINCT audit.context_pack->>'producer_fact_id') FILTER",
		"ROW_NUMBER() OVER",
		"ORDER BY audit.created_at DESC, audit.id DESC",
		"winner.winner_rank = 1",
		"RAISE EXCEPTION",
		"idx_agent_transport_draft_source_target",
		"idx_agent_transport_draft_inbox_target",
		"CHECK ((task_id IS NOT NULL) <> (inbox_event_id IS NOT NULL))",
		"ALTER COLUMN decision_fact_id SET NOT NULL",
		"agent_transport_draft_decision_fact_nonempty_check",
	} {
		if !strings.Contains(contents, required) {
			t.Errorf("migration 201 up missing %q", required)
		}
	}
}

func TestMigration202SeparatesExplicitUnfollowFromDirectedNoWake(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")

	up, err := os.ReadFile(filepath.Join(migrationsDir, "202_thread_participant_explicit_unfollow.up.sql"))
	if err != nil {
		t.Fatalf("read migration 202 up: %v", err)
	}
	contents := string(up)
	for _, required := range []string{
		"'active', 'no_wake', 'unfollowed', 'removed'",
		"participant.followed_at IS NULL",
		"participant.wake_state = 'no_wake'",
		"part.value->>'event' = 'thread_unfollowed'",
		"audit.action = 'thread_unfollow'",
		"audit.channel_message_id = participant.root_message_id",
		"audit.agent_id = participant.member_id",
		"FROM channel_thread_state state",
		"participant.member_type = 'user'",
		"participant.member_id = state.user_id",
		"participant.followed_at IS NULL",
		"state.followed_at IS NULL",
		"participant.wake_state IN ('active', 'no_wake')",
	} {
		if !strings.Contains(contents, required) {
			t.Errorf("migration 202 up missing %q", required)
		}
	}

	down, err := os.ReadFile(filepath.Join(migrationsDir, "202_thread_participant_explicit_unfollow.down.sql"))
	if err != nil {
		t.Fatalf("read migration 202 down: %v", err)
	}
	if !strings.Contains(string(down), "SET wake_state = 'no_wake'") {
		t.Error("migration 202 down does not restore unfollowed rows to no_wake")
	}
}

func TestResolveDirSkipsNonMigrationDirectory(t *testing.T) {
	root := t.TempDir()
	internalMigrations := filepath.Join(root, "server", "internal", "migrations")
	if err := os.MkdirAll(internalMigrations, 0o755); err != nil {
		t.Fatalf("create internal migrations directory: %v", err)
	}

	migrationDir := filepath.Join(root, "server", "migrations")
	if err := os.MkdirAll(migrationDir, 0o755); err != nil {
		t.Fatalf("create migration directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(migrationDir, "001_initial.up.sql"), []byte("SELECT 1;"), 0o644); err != nil {
		t.Fatalf("write migration: %v", err)
	}

	got, err := resolveDir([]string{internalMigrations})
	if err != nil {
		t.Fatalf("resolve migrations directory: %v", err)
	}
	if got != migrationDir {
		t.Fatalf("resolved directory = %q, want %q", got, migrationDir)
	}
}
