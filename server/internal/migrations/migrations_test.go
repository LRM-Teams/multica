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
		"ADD COLUMN avatar_attachment_id UUID REFERENCES attachment(id) ON DELETE RESTRICT",
		"CHECK (avatar_source IN ('assigned', 'picked', 'uploaded'))",
		"CHECK ((avatar_source = 'uploaded') = (avatar_attachment_id IS NOT NULL))",
		"ADD CONSTRAINT agent_avatar_attachment_unique",
		"UNIQUE (avatar_attachment_id)",
		"WHERE avatar_url IS NULL OR btrim(avatar_url) = ''",
		"CREATE TRIGGER agent_assign_durable_avatar_on_insert",
		"BEFORE INSERT ON agent",
		"NEW.avatar_url := default_agent_avatar_url(NEW.id)",
		"NEW.avatar_source := 'assigned'",
		"NEW.avatar_attachment_id := NULL",
		"ADD CONSTRAINT agent_avatar_url_nonblank_check",
		"CHECK (btrim(avatar_url) <> '')",
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
		"DROP CONSTRAINT IF EXISTS agent_avatar_url_nonblank_check",
		"DROP TRIGGER IF EXISTS agent_assign_durable_avatar_on_insert ON agent",
		"DROP CONSTRAINT IF EXISTS agent_avatar_attachment_unique",
		"DROP COLUMN IF EXISTS avatar_attachment_id",
		"DROP COLUMN IF EXISTS avatar_source",
		"DROP FUNCTION IF EXISTS default_agent_avatar_url(UUID)",
	} {
		if !strings.Contains(downContents, required) {
			t.Errorf("migration 203 down missing %q", required)
		}
	}
}

func TestMigration314MovesSystemAgentAvatarsToOSS(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")

	up, err := os.ReadFile(filepath.Join(migrationsDir, "314_agent_avatar_oss_presets.up.sql"))
	if err != nil {
		t.Fatalf("read migration 314 up: %v", err)
	}
	contents := string(up)
	for _, required := range []string{
		"CREATE OR REPLACE FUNCTION default_agent_avatar_url(agent_id UUID)",
		"https://cdn.leagent.me/agent-avatars/v2/agent-%s.png",
		"% 15",
		"avatar_source IN ('assigned', 'picked')",
		"^/agent-avatars/human-",
		"https://cdn.leagent.me/agent-avatars/v1/",
	} {
		if !strings.Contains(contents, required) {
			t.Errorf("migration 314 up missing %q", required)
		}
	}

	down, err := os.ReadFile(filepath.Join(migrationsDir, "314_agent_avatar_oss_presets.down.sql"))
	if err != nil {
		t.Fatalf("read migration 314 down: %v", err)
	}
	downContents := string(down)
	for _, required := range []string{
		"/agent-avatars/human-%s.jpg",
		"% 24",
		"^https://cdn\\.leagent\\.me/agent-avatars/v1/human-",
		"^https://cdn\\.leagent\\.me/agent-avatars/v2/agent-",
	} {
		if !strings.Contains(downContents, required) {
			t.Errorf("migration 314 down missing %q", required)
		}
	}
}

func TestMigration218CreatesCanonicalAgentRuntimeStateWithoutQueueDependency(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")

	up, err := os.ReadFile(filepath.Join(migrationsDir, "218_agent_runtime_state.up.sql"))
	if err != nil {
		t.Fatalf("read migration 218 up: %v", err)
	}
	contents := string(up)
	for _, required := range []string{
		"CREATE TABLE agent_runtime_state",
		"PRIMARY KEY (agent_id, runtime_id)",
		"provider_session_id TEXT",
		"work_dir TEXT",
		"provider_config_fingerprint TEXT",
		"generation BIGINT NOT NULL DEFAULT 1",
		"last_turn_id UUID",
		"fresh_session_notice_reason TEXT",
		"fresh_session_notice_reason IN ('cutover', 'reset')",
		"legacy_resume_archived_at TIMESTAMPTZ",
		"agent_runtime_state_notice_session_check",
		"agent_runtime_state_archived_empty_notice_check",
		"CHECK (generation >= 1)",
		"INSERT INTO agent_runtime_state",
		"a.runtime_id",
		"'cutover'",
		"'cutover',\n    NULL",
		"ON CONFLICT (agent_id, runtime_id) DO NOTHING",
	} {
		if !strings.Contains(contents, required) {
			t.Errorf("migration 218 up missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"REFERENCES agent_task_queue",
		"UPDATE agent_task_queue",
		"UPDATE chat_session",
		"DELETE FROM agent_task_queue",
		"DELETE FROM chat_session",
	} {
		if strings.Contains(contents, forbidden) {
			t.Errorf("migration 218 up must leave legacy resume evidence untouched; found %q", forbidden)
		}
	}

	down, err := os.ReadFile(filepath.Join(migrationsDir, "218_agent_runtime_state.down.sql"))
	if err != nil {
		t.Fatalf("read migration 218 down: %v", err)
	}
	if !strings.Contains(string(down), "DROP TABLE IF EXISTS agent_runtime_state") {
		t.Error("migration 218 down does not drop agent_runtime_state")
	}
}

func TestMigration385HardCutsLegacyRuntimeStateAndLifecycleNaming(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")

	up, err := os.ReadFile(filepath.Join(migrationsDir, "385_agent_restart_state_hard_cut.up.sql"))
	if err != nil {
		t.Fatalf("read migration 385 up: %v", err)
	}
	contents := string(up)
	for _, required := range []string{
		"ALTER TABLE agent_lifecycle_operation RENAME TO agent_restart_operation",
		"ADD COLUMN start_session_id TEXT NOT NULL DEFAULT ''",
		"DROP TABLE agent_runtime_state",
	} {
		if !strings.Contains(contents, required) {
			t.Errorf("migration 385 up missing %q", required)
		}
	}
}

func TestMigration386RemovesAttachmentAndReminderProjectionProtocols(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
	up, err := os.ReadFile(filepath.Join(migrationsDir, "386_remove_attachment_and_reminder_projection_protocol.up.sql"))
	if err != nil {
		t.Fatalf("read migration 386 up: %v", err)
	}
	contents := string(up)
	for _, required := range []string{
		"DROP FUNCTION IF EXISTS project_agent_attachment_projection()",
		"DROP TABLE IF EXISTS agent_attachment_projection_event",
		"DROP FUNCTION IF EXISTS project_agent_reminder_daemon_owner_event()",
		"CREATE OR REPLACE FUNCTION cancel_agent_reminders_on_archive()",
		"CREATE TRIGGER cancel_agent_reminders_on_archive_trigger",
		"DROP TABLE IF EXISTS agent_reminder_daemon_projection_event",
		"DROP TABLE IF EXISTS agent_reminder_daemon_owner_event",
		"DROP SEQUENCE IF EXISTS agent_reminder_placement_generation_seq",
	} {
		if !strings.Contains(contents, required) {
			t.Errorf("migration 386 up missing %q", required)
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

func TestMigration209SuppressesEnvDispatchChannelOnboarding(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
	read := func(name string) string {
		t.Helper()
		body, err := os.ReadFile(filepath.Join(migrationsDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(body)
	}

	up := read("209_env_dispatch_channel_join_source.up.sql")
	for _, required := range []string{
		"'env_dispatch'",
		"WHEN (NEW.join_source <> 'env_dispatch')",
		"trg_maintain_channel_agent_onboarding_insert",
		"trg_maintain_channel_agent_onboarding_delete",
	} {
		if !strings.Contains(up, required) {
			t.Errorf("migration 209 up missing %q", required)
		}
	}

	down := read("209_env_dispatch_channel_join_source.down.sql")
	for _, required := range []string{
		"SET join_source = 'system'",
		"AFTER INSERT OR DELETE ON channel_member",
		"trg_maintain_channel_agent_onboarding",
	} {
		if !strings.Contains(down, required) {
			t.Errorf("migration 209 down missing %q", required)
		}
	}
}

func TestMigration245ReplacesUserShapedChannelMemberProvenance(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
	read := func(name string) string {
		t.Helper()
		body, err := os.ReadFile(filepath.Join(migrationsDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(body)
	}

	up := read("245_channel_member_actor_provenance.up.sql")
	for _, required := range []string{
		"added_by_type",
		"added_by_id",
		"source_actor_type",
		"source_actor_id",
		"'user'",
		"'agent'",
		"'system'",
		"WHEN (NEW.join_source <> 'env_dispatch')",
		"trg_maintain_channel_agent_onboarding_insert",
		"trg_maintain_channel_agent_onboarding_delete",
		"CREATE OR REPLACE FUNCTION channel_seed_human_owner_on_insert()",
	} {
		if !strings.Contains(up, required) {
			t.Errorf("migration 245 up missing %q", required)
		}
	}
	if strings.Contains(up, "ADD COLUMN added_by UUID") {
		t.Error("migration 245 must replace, not preserve, the user-only added_by column")
	}

	down := read("245_channel_member_actor_provenance.down.sql")
	for _, required := range []string{
		"LOSSY",
		"agent",
		"added_by",
		"DROP COLUMN",
		"WHEN (NEW.join_source <> 'env_dispatch')",
	} {
		if !strings.Contains(down, required) {
			t.Errorf("migration 245 down missing rollback contract %q", required)
		}
	}
}

func TestMigration233RestoresWendyAmbientRadarAuthorization(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")

	up, err := os.ReadFile(filepath.Join(migrationsDir, "233_restore_wendy_ambient_radar_auth.up.sql"))
	if err != nil {
		t.Fatalf("read migration 233 up: %v", err)
	}
	contents := string(up)
	for _, required := range []string{
		"CREATE OR REPLACE FUNCTION workspace_radar_task_is_authorized(candidate_task_id UUID)",
		"FROM agent_inbox_event event",
		"run.trigger_kind = 'manual'",
		"run.trigger_kind = 'scheduled'",
		"run.trigger_kind = 'event'",
		"run.cooldown_key LIKE 'wendy_ambient:%'",
		"CAST(substring(run.cooldown_key FROM 15) AS uuid)",
		"cm.member_type = 'agent'",
	} {
		if !strings.Contains(contents, required) {
			t.Errorf("migration 233 up missing %q", required)
		}
	}

	down, err := os.ReadFile(filepath.Join(migrationsDir, "233_restore_wendy_ambient_radar_auth.down.sql"))
	if err != nil {
		t.Fatalf("read migration 233 down: %v", err)
	}
	downContents := string(down)
	if !strings.Contains(downContents, "CREATE OR REPLACE FUNCTION workspace_radar_task_is_authorized") {
		t.Error("migration 233 down missing function replace")
	}
	if strings.Contains(downContents, "wendy_ambient") {
		t.Error("migration 233 down must not keep wendy_ambient branch")
	}
}

func TestMigration234KillsWendyAmbientRadarAuthorization(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")

	up, err := os.ReadFile(filepath.Join(migrationsDir, "234_kill_wendy_ambient_radar_auth.up.sql"))
	if err != nil {
		t.Fatalf("read migration 234 up: %v", err)
	}
	contents := string(up)
	for _, required := range []string{
		"CREATE OR REPLACE FUNCTION workspace_radar_task_is_authorized(candidate_task_id UUID)",
		"FROM agent_inbox_event event",
		"run.trigger_kind = 'manual'",
		"run.trigger_kind = 'scheduled'",
		"wendy_ambient product kill",
		"failure_reason = 'radar_unauthorized'",
	} {
		if !strings.Contains(contents, required) {
			t.Errorf("migration 234 up missing %q", required)
		}
	}
	// Auth function body must not re-authorize event ambient.
	fnStart := strings.Index(contents, "CREATE OR REPLACE FUNCTION workspace_radar_task_is_authorized")
	fnEnd := strings.Index(contents[fnStart:], "$$;")
	if fnStart < 0 || fnEnd < 0 {
		t.Fatal("could not isolate auth function in migration 234 up")
	}
	fnBody := contents[fnStart : fnStart+fnEnd]
	if strings.Contains(fnBody, "run.trigger_kind = 'event'") {
		t.Error("migration 234 auth function must not authorize trigger_kind=event")
	}
	if strings.Contains(fnBody, "wendy_ambient:%") {
		t.Error("migration 234 auth function must not reference wendy_ambient cooldown")
	}

	down, err := os.ReadFile(filepath.Join(migrationsDir, "234_kill_wendy_ambient_radar_auth.down.sql"))
	if err != nil {
		t.Fatalf("read migration 234 down: %v", err)
	}
	if !strings.Contains(string(down), "wendy_ambient:%") {
		t.Error("migration 234 down should restore ambient branch for emergency rollback only")
	}
}

func TestMigration235ChannelListPerfIndexesRenumberedFrom233(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")

	if _, err := os.Stat(filepath.Join(migrationsDir, "233_channel_list_perf_indexes.up.sql")); err == nil {
		t.Fatal("stale 233_channel_list_perf_indexes.up.sql must not remain after #785 renumber")
	}
	if _, err := os.Stat(filepath.Join(migrationsDir, "233_restore_wendy_ambient_radar_auth.up.sql")); err != nil {
		t.Fatal("233_restore_wendy_ambient_radar_auth.up.sql must remain unchanged")
	}
	if _, err := os.Stat(filepath.Join(migrationsDir, "234_kill_wendy_ambient_radar_auth.up.sql")); err != nil {
		t.Fatal("234_kill_wendy_ambient_radar_auth.up.sql must remain unchanged")
	}
	upPath := filepath.Join(migrationsDir, "235_channel_list_perf_indexes.up.sql")
	up, err := os.ReadFile(upPath)
	if err != nil {
		t.Fatalf("read 235 up: %v", err)
	}
	contents := string(up)
	for _, required := range []string{
		"main_unread_count",
		"idx_channel_message_list_main_seq",
		"idx_channel_member_avatar_stack",
		"ADD COLUMN IF NOT EXISTS",
		"CREATE INDEX IF NOT EXISTS",
		"Renumbered from 233_channel_list_perf_indexes",
	} {
		if !strings.Contains(contents, required) {
			t.Errorf("235 up missing %q", required)
		}
	}
	down, err := os.ReadFile(filepath.Join(migrationsDir, "235_channel_list_perf_indexes.down.sql"))
	if err != nil {
		t.Fatalf("read 235 down: %v", err)
	}
	for _, required := range []string{
		"DROP INDEX IF EXISTS idx_channel_member_avatar_stack",
		"DROP INDEX IF EXISTS idx_channel_message_list_main_seq",
		"DROP COLUMN IF EXISTS main_unread_count",
	} {
		if !strings.Contains(string(down), required) {
			t.Errorf("235 down missing %q", required)
		}
	}
}
func TestMigration246SeparatesDaemonCredentialsAndEnforcesOneUnrevokedSubject(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")

	up, err := os.ReadFile(filepath.Join(migrationsDir, "246_agent_credential_issuance_source.up.sql"))
	if err != nil {
		t.Fatalf("read migration 246 up: %v", err)
	}
	contents := string(up)
	for _, required := range []string{
		"ADD COLUMN issuance_source TEXT NOT NULL DEFAULT 'manual'",
		"CHECK (issuance_source IN ('manual', 'daemon'))",
		"audit.action = 'agent_credential_daemon_ensured'",
		"audit.details->>'source' = 'daemon_runtime_ensure'",
		"audit.details->>'reused' = 'false'",
		"audit.details->>'agent_credential_id' = credential.id::text",
		"PARTITION BY credential.agent_id, credential.workspace_id, credential.user_id",
		"(credential.last_used_at IS NOT NULL) DESC",
		"'daemon_issuance_backfill_deduplicate'",
		"CREATE UNIQUE INDEX idx_agent_credential_daemon_subject_unrevoked",
		"WHERE issuance_source = 'daemon'",
		"AND revoked_at IS NULL",
	} {
		if !strings.Contains(contents, required) {
			t.Errorf("migration 246 up missing %q", required)
		}
	}

	down, err := os.ReadFile(filepath.Join(migrationsDir, "246_agent_credential_issuance_source.down.sql"))
	if err != nil {
		t.Fatalf("read migration 246 down: %v", err)
	}
	downContents := string(down)
	for _, required := range []string{
		"DROP INDEX IF EXISTS idx_agent_credential_daemon_subject_unrevoked",
		"DROP CONSTRAINT IF EXISTS agent_credential_issuance_source_check",
		"DROP COLUMN IF EXISTS issuance_source",
	} {
		if !strings.Contains(downContents, required) {
			t.Errorf("migration 246 down missing %q", required)
		}
	}
}

func TestMigration307ComputerWorkspaceBindingsForwardAndBackward(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")

	up, err := os.ReadFile(filepath.Join(migrationsDir, "307_computer_workspace_bindings.up.sql"))
	if err != nil {
		t.Fatalf("read migration 307 up: %v", err)
	}
	// Forward contract: keyed by immutable workspace_id, never by slug; idempotent;
	// credential stored as a hash only; revocable via active/revoked_at; one
	// machine-wide Computer identity cannot be claimed by multiple users.
	for _, required := range []string{
		"CREATE TABLE IF NOT EXISTS computer_identity_owner",
		"daemon_id          TEXT        PRIMARY KEY",
		"CREATE TABLE IF NOT EXISTS computer_workspace_bindings",
		"daemon_id          TEXT",
		"workspace_id       UUID",
		"execution_token_hash TEXT",
		"revoked_at         TIMESTAMPTZ",
		"PRIMARY KEY (daemon_id, workspace_id)",
		"computer identity has bindings owned by multiple users",
		"o.user_id <> b.user_id",
		"computer identity owner conflicts with workspace connection owner",
	} {
		if !strings.Contains(string(up), required) {
			t.Errorf("migration 307 up missing %q", required)
		}
	}
	down, err := os.ReadFile(filepath.Join(migrationsDir, "307_computer_workspace_bindings.down.sql"))
	if err != nil {
		t.Fatalf("read migration 307 down: %v", err)
	}
	if !strings.Contains(string(down), "DROP TABLE IF EXISTS computer_workspace_bindings") {
		t.Error("migration 307 down must drop the bindings table")
	}
	if !strings.Contains(string(down), "DROP TABLE IF EXISTS computer_identity_owner") {
		t.Error("migration 307 down must drop the Computer owner table")
	}
}

func TestMigration316RepairsMissingComputerWorkspaceBindingTables(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
	up, err := os.ReadFile(filepath.Join(migrationsDir, "316_computer_workspace_bindings_repair.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"CREATE TABLE IF NOT EXISTS computer_identity_owner",
		"CREATE TABLE IF NOT EXISTS computer_workspace_bindings",
		"PRIMARY KEY (daemon_id, workspace_id)",
		"computer identity has bindings owned by multiple users",
		"computer identity owner conflicts with workspace connection owner",
		"Deployment/apply notes",
	} {
		if !strings.Contains(string(up), required) {
			t.Errorf("migration 316 up missing %q", required)
		}
	}
	down, err := os.ReadFile(filepath.Join(migrationsDir, "316_computer_workspace_bindings_repair.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(down), "DROP TABLE") {
		t.Fatal("migration 316 down must not drop schema owned by migration 307")
	}
}

func TestMigration314ComputerGenerationFenceAndWorkspaceAttestation(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
	up, err := os.ReadFile(filepath.Join(migrationsDir, "314_computer_generation_fence.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"CREATE TABLE computer_generation",
		"generation  BIGINT",
		"ADD COLUMN computer_generation BIGINT",
		"accepted_workspace_ids UUID[]",
		"attested_workspace_ids UUID[]",
		"deployment/apply notes",
	} {
		if !strings.Contains(string(up), required) {
			t.Errorf("migration 314 up missing %q", required)
		}
	}
	down, err := os.ReadFile(filepath.Join(migrationsDir, "314_computer_generation_fence.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"DROP TABLE IF EXISTS computer_generation", "DROP COLUMN IF EXISTS computer_generation", "DROP COLUMN IF EXISTS accepted_workspace_ids"} {
		if !strings.Contains(string(down), required) {
			t.Errorf("migration 314 down missing %q", required)
		}
	}
}

func TestMigration313ChannelAttentionRoundSchema(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")

	up, err := os.ReadFile(filepath.Join(migrationsDir, "314_channel_attention_round.up.sql"))
	if err != nil {
		t.Fatalf("read migration 313 up: %v", err)
	}
	for _, required := range []string{
		"CREATE TABLE channel_attention_round",
		"CREATE TABLE channel_attention_participant",
		"CREATE TABLE channel_attention_response_grant",
		"idx_channel_attention_round_collecting_channel",
		"idx_channel_attention_participant_agent_id",
		"SILENT", "ANSWER", "CONTRIBUTE", "COORDINATE",
		"UNIQUE (round_id)",
	} {
		if !strings.Contains(string(up), required) {
			t.Errorf("migration 313 up missing %q", required)
		}
	}

	down, err := os.ReadFile(filepath.Join(migrationsDir, "314_channel_attention_round.down.sql"))
	if err != nil {
		t.Fatalf("read migration 313 down: %v", err)
	}
	for _, table := range []string{
		"channel_attention_response_grant",
		"channel_attention_participant",
		"channel_attention_round",
	} {
		if !strings.Contains(string(down), "DROP TABLE IF EXISTS "+table) {
			t.Errorf("migration 313 down missing DROP for %s", table)
		}
	}
}
