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

func TestMigration203PersistsAgentAvatarTruthAtWriteBoundary(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")

	up, err := os.ReadFile(filepath.Join(migrationsDir, "203_agent_durable_avatar.up.sql"))
	if err != nil {
		t.Fatalf("read migration 203 up: %v", err)
	}
	contents := string(up)
	for _, required := range []string{
		"CREATE OR REPLACE FUNCTION default_agent_avatar_url(agent_id UUID)",
		"ADD COLUMN avatar_source TEXT NOT NULL DEFAULT 'assigned'",
		"CHECK (avatar_source IN ('assigned', 'picked', 'uploaded'))",
		"WHERE avatar_url IS NULL OR btrim(avatar_url) = ''",
		"CREATE TRIGGER agent_assign_durable_avatar_on_insert",
		"BEFORE INSERT ON agent",
		"NEW.avatar_url := default_agent_avatar_url(NEW.id)",
		"NEW.avatar_source := 'assigned'",
		"ALTER COLUMN avatar_url SET NOT NULL",
	} {
		if !strings.Contains(contents, required) {
			t.Errorf("migration 203 up missing %q", required)
		}
	}

	down, err := os.ReadFile(filepath.Join(migrationsDir, "203_agent_durable_avatar.down.sql"))
	if err != nil {
		t.Fatalf("read migration 203 down: %v", err)
	}
	downContents := string(down)
	for _, required := range []string{
		"ALTER COLUMN avatar_url DROP NOT NULL",
		"DROP TRIGGER IF EXISTS agent_assign_durable_avatar_on_insert ON agent",
		"DROP COLUMN IF EXISTS avatar_source",
		"DROP FUNCTION IF EXISTS default_agent_avatar_url(UUID)",
	} {
		if !strings.Contains(downContents, required) {
			t.Errorf("migration 203 down missing %q", required)
		}
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

// env-dispatch derived-agent lineage + binding state (migration 198).
func TestMigration198UpAddsDerivedAgentLineageAndBindingState(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	path := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations", "198_env_dispatch_derived_agents.up.sql")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration 198 up: %v", err)
	}
	contents := string(body)
	for _, required := range []string{
		"ALTER TABLE agent ADD COLUMN IF NOT EXISTS source_agent_id",
		"agent_source_workspace_fk",
		"REFERENCES agent(workspace_id, id) ON DELETE RESTRICT",
		"ADD COLUMN IF NOT EXISTS id uuid NOT NULL DEFAULT gen_random_uuid()",
		"ADD COLUMN IF NOT EXISTS source_agent_id uuid",
		"ADD COLUMN IF NOT EXISTS derived_agent_id uuid",
		"ADD COLUMN IF NOT EXISTS training_session_id text",
		"ADD COLUMN IF NOT EXISTS training_session_ref text",
		"ADD COLUMN IF NOT EXISTS credential_kind text",
		"ADD COLUMN IF NOT EXISTS model_config_owner_agent_id uuid",
		"environment_agent_sandbox_source_uidx",
		"WHERE source_agent_id IS NOT NULL",
		"'credential_ready', 'sandbox_creating'",
		"'runtime_waiting', 'agent_creating'",
		"'failed_retryable'",
		"'deleted'",
	} {
		if !strings.Contains(contents, required) {
			t.Errorf("migration 198 up missing %q", required)
		}
	}
}

func TestMigration198DownReversesDerivedAgentSchema(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	path := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations", "198_env_dispatch_derived_agents.down.sql")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration 198 down: %v", err)
	}
	contents := string(body)
	for _, required := range []string{
		"DROP INDEX IF EXISTS environment_agent_sandbox_source_uidx",
		"DROP INDEX IF EXISTS environment_agent_sandbox_id_uidx",
		"DROP COLUMN IF EXISTS model_config_owner_agent_id",
		"DROP COLUMN IF EXISTS derived_agent_id",
		"DROP COLUMN IF EXISTS source_agent_id",
		"DROP COLUMN IF EXISTS id",
		"DROP CONSTRAINT IF EXISTS agent_source_workspace_fk",
		"ALTER TABLE agent DROP COLUMN IF EXISTS source_agent_id",
		"status IN ('pending', 'provisioning', 'ready', 'failed', 'deleting')",
	} {
		if !strings.Contains(contents, required) {
			t.Errorf("migration 198 down missing %q", required)
		}
	}
}
