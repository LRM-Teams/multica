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

func TestCanonicalAgentRuntimeStateQueriesStayQueueIndependent(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	queryPath := filepath.Join(
		filepath.Dir(thisFile),
		"..", "..", "pkg", "db", "queries", "agent_runtime_state.sql",
	)
	body, err := os.ReadFile(queryPath)
	if err != nil {
		t.Fatalf("read canonical runtime-state queries: %v", err)
	}
	contents := string(body)
	for _, required := range []string{
		"-- name: EnsureAgentRuntimeState :one",
		"-- name: GetCurrentAgentRuntimeState :one",
		"-- name: AdvanceAgentRuntimeStateCAS :one",
		"-- name: ClearAgentRuntimeSessionCAS :one",
		"state.generation = sqlc.arg('expected_generation')",
		"state.generation + 1",
		"current.runtime_id = state.runtime_id",
		"sqlc.arg('notice_reason')::text = 'reset'",
		"state.fresh_session_notice_reason = 'reset'",
		"NULLIF(btrim(sqlc.narg('provider_session_id')::text), '') IS NOT NULL",
	} {
		if !strings.Contains(contents, required) {
			t.Errorf("canonical runtime-state query contract missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"agent_task_queue",
		"chat_session",
		"task_id",
		"issue_id",
		"channel_id",
	} {
		if strings.Contains(contents, forbidden) {
			t.Errorf("canonical runtime state must not inherit wake/surface semantics; found %q", forbidden)
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

func TestMigration244AddsSaveModeAndCheckpointOwnedSavepoints(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")

	up, err := os.ReadFile(filepath.Join(migrationsDir, "244_env_checkpoint_save_mode.up.sql"))
	if err != nil {
		t.Fatalf("read 244 up: %v", err)
	}
	contents := string(up)
	for _, required := range []string{
		"ADD COLUMN IF NOT EXISTS save_mode text NOT NULL DEFAULT 'pause_in_place'",
		"env_checkpoint_save_mode_check",
		"CHECK (save_mode IN ('pause_in_place', 'snapshot'))",
		"ALTER TABLE sandbox_snapshot",
		"ADD COLUMN IF NOT EXISTS checkpoint_id uuid",
		"REFERENCES env_checkpoint(id) ON DELETE CASCADE",
		"CREATE INDEX IF NOT EXISTS sandbox_snapshot_checkpoint_idx",
		"WHERE checkpoint_id IS NOT NULL",
	} {
		if !strings.Contains(contents, required) {
			t.Errorf("244 up missing %q", required)
		}
	}
	// The default carries every pre-existing row, so no data migration is
	// needed and none may be smuggled in here.
	for _, forbidden := range []string{
		"UPDATE env_checkpoint",
		"UPDATE sandbox_snapshot",
		"DELETE FROM",
		"DROP TABLE",
	} {
		if strings.Contains(contents, forbidden) {
			t.Errorf("244 up must not backfill or destroy data; found %q", forbidden)
		}
	}
	// A savepoint nobody owns must stay legal, so the ownership column cannot
	// be NOT NULL.
	if strings.Contains(contents, "checkpoint_id uuid NOT NULL") {
		t.Error("244 up must leave checkpoint_id nullable for unowned snapshots")
	}

	down, err := os.ReadFile(filepath.Join(migrationsDir, "244_env_checkpoint_save_mode.down.sql"))
	if err != nil {
		t.Fatalf("read 244 down: %v", err)
	}
	for _, required := range []string{
		"DROP INDEX IF EXISTS sandbox_snapshot_checkpoint_idx",
		"DROP COLUMN IF EXISTS checkpoint_id",
		"DROP CONSTRAINT IF EXISTS env_checkpoint_save_mode_check",
		"DROP COLUMN IF EXISTS save_mode",
	} {
		if !strings.Contains(string(down), required) {
			t.Errorf("244 down missing %q", required)
		}
	}
}

func TestMigration245CreatesLaneTableWithIdempotencyAndRecoveryColumns(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")

	up, err := os.ReadFile(filepath.Join(migrationsDir, "245_env_checkpoint_lane.up.sql"))
	if err != nil {
		t.Fatalf("read 245 up: %v", err)
	}
	contents := string(up)
	for _, required := range []string{
		"CREATE TABLE IF NOT EXISTS env_checkpoint_lane",
		"checkpoint_id UUID NOT NULL REFERENCES env_checkpoint(id) ON DELETE CASCADE",
		"workspace_id UUID NOT NULL",
		"lane_key TEXT NOT NULL",
		"CHECK (status IN ('provisioning', 'ready', 'failed'))",
		// The unique index is the idempotency mechanism, not an optimization.
		"UNIQUE (checkpoint_id, lane_key)",
		"CREATE INDEX IF NOT EXISTS env_checkpoint_lane_provisioning_idx",
		"WHERE status = 'provisioning'",
	} {
		if !strings.Contains(contents, required) {
			t.Errorf("245 up missing %q", required)
		}
	}
	// The lane's conversation must be its own and must be recorded, or a lane
	// interrupted between copying its channel and starting its run would copy a
	// second channel on recovery.
	for _, conversation := range []string{"channel_id UUID", "chat_session_id UUID", "source_message_id UUID"} {
		if !strings.Contains(contents, conversation) {
			t.Errorf("245 up missing per-lane %q", conversation)
		}
	}
	// The per-step ids must stay nullable: a lane interrupted partway is
	// continued from its first unfilled step, which is impossible if they are
	// required up front.
	for _, step := range []string{
		"instance_id", "project_id", "runtime_id", "task_id", "env_id",
		"channel_id", "chat_session_id", "source_message_id",
	} {
		if strings.Contains(contents, step+" UUID NOT NULL") {
			t.Errorf("245 up makes %s NOT NULL, which breaks continuing an interrupted lane", step)
		}
	}

	down, err := os.ReadFile(filepath.Join(migrationsDir, "245_env_checkpoint_lane.down.sql"))
	if err != nil {
		t.Fatalf("read 245 down: %v", err)
	}
	for _, required := range []string{
		"DROP INDEX IF EXISTS env_checkpoint_lane_provisioning_idx",
		"DROP TABLE IF EXISTS env_checkpoint_lane",
	} {
		if !strings.Contains(string(down), required) {
			t.Errorf("245 down missing %q", required)
		}
	}
}

func TestLaneQueriesClaimIdempotentlyAndStayWorkspaceScoped(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	body, err := os.ReadFile(filepath.Join(
		filepath.Dir(thisFile), "..", "..", "pkg", "db", "queries", "env_checkpoint_lane.sql"))
	if err != nil {
		t.Fatalf("read lane queries: %v", err)
	}
	contents := string(body)
	for _, required := range []string{
		"ON CONFLICT (checkpoint_id, lane_key) DO NOTHING",
		// The lane's workspace comes from its checkpoint, so a lane cannot land
		// in a different workspace than the checkpoint it belongs to.
		"SELECT c.id, c.workspace_id, @lane_key, 'provisioning'",
		"COALESCE(sqlc.narg(instance_id), instance_id)",
		"COALESCE(sqlc.narg(channel_id), channel_id)",
		"COALESCE(sqlc.narg(source_message_id), source_message_id)",
	} {
		if !strings.Contains(contents, required) {
			t.Errorf("lane queries missing %q", required)
		}
	}
	if strings.Contains(contents, "VALUES (@checkpoint_id, @workspace_id, @lane_key") {
		t.Error("claim must derive workspace_id from the checkpoint, not accept it from the caller")
	}
	// Every lane query except the cross-workspace sweeper must be workspace
	// scoped.
	for _, name := range []string{
		"GetEnvCheckpointLane",
		"ListEnvCheckpointLanes",
		"UpdateEnvCheckpointLaneStep",
		"MarkEnvCheckpointLaneReady",
		"MarkEnvCheckpointLaneFailed",
		"CountProvisioningEnvCheckpointLanes",
	} {
		idx := strings.Index(contents, "-- name: "+name+" :")
		if idx < 0 {
			t.Errorf("lane queries missing %q", name)
			continue
		}
		stmt := contents[idx:]
		if end := strings.Index(stmt[1:], "\n-- name: "); end >= 0 {
			stmt = stmt[:end+1]
		}
		if !strings.Contains(stmt, "workspace_id = @workspace_id") {
			t.Errorf("%s is not workspace scoped", name)
		}
	}
}

func TestSavepointOwnershipQueriesStayWorkspaceScopedAndNonStealing(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	queriesDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "pkg", "db", "queries")

	checkpoint, err := os.ReadFile(filepath.Join(queriesDir, "env_checkpoint.sql"))
	if err != nil {
		t.Fatalf("read env_checkpoint queries: %v", err)
	}
	for _, required := range []string{
		"    save_mode\n",
		"    @save_mode\n",
		"-- name: UpdateEnvCheckpointSaveMode :one",
		"SET save_mode = @save_mode, updated_at = now()",
	} {
		if !strings.Contains(string(checkpoint), required) {
			t.Errorf("env_checkpoint queries missing %q", required)
		}
	}

	sandbox, err := os.ReadFile(filepath.Join(queriesDir, "sandbox.sql"))
	if err != nil {
		t.Fatalf("read sandbox queries: %v", err)
	}
	for _, required := range []string{
		"-- name: AttachSandboxSnapshotToCheckpoint :one",
		// A savepoint has exactly one owner, so attaching must never reassign
		// one that another checkpoint already owns.
		"AND (checkpoint_id IS NULL OR checkpoint_id = @checkpoint_id)",
		"-- name: ListSandboxSnapshotsForCheckpoint :many",
		"WHERE checkpoint_id = @checkpoint_id AND workspace_id = @workspace_id",
	} {
		if !strings.Contains(string(sandbox), required) {
			t.Errorf("sandbox queries missing %q", required)
		}
	}
}

// TestGeneratedSnapshotScanMatchesSelectedColumns guards the hand-maintained
// generated code in this repository: sandbox.sql.go routes every sandbox_snapshot
// row through one shared scan helper, so a column added to the SELECT lists
// without a matching scan target would misalign every snapshot read at runtime,
// which no compile check would catch.
func TestGeneratedSnapshotScanMatchesSelectedColumns(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	generatedDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "pkg", "db", "generated")

	sandbox, err := os.ReadFile(filepath.Join(generatedDir, "sandbox.sql.go"))
	if err != nil {
		t.Fatalf("read generated sandbox: %v", err)
	}
	contents := string(sandbox)
	const snapshotColumns = "id, workspace_id, node_id, instance_id, creator_user_id, cube_snapshot_id, name, description, status, error, metadata, created_at, updated_at, checkpoint_id"
	if got := strings.Count(contents, snapshotColumns); got != 9 {
		t.Errorf("snapshot column list appears %d times, want 9 (7 pre-existing queries plus attach and list-for-checkpoint)", got)
	}
	// The scan helper must end with checkpoint_id, in the same order.
	if !strings.Contains(contents, "&i.Metadata, &i.CreatedAt, &i.UpdatedAt, &i.CheckpointID,") {
		t.Error("scanSandboxSnapshot does not scan checkpoint_id last, so snapshot reads would misalign")
	}
	if strings.Count(contents, "func scanSandboxSnapshot(") != 1 {
		t.Error("expected exactly one shared sandbox_snapshot scan helper")
	}

	checkpoint, err := os.ReadFile(filepath.Join(generatedDir, "env_checkpoint.sql.go"))
	if err != nil {
		t.Fatalf("read generated env_checkpoint: %v", err)
	}
	checkpointContents := string(checkpoint)
	const checkpointColumns = "id, workspace_id, project_id, event_ref, checkpoint_kind, env_id_map, sandbox_refs, db_snapshot, entropy_score, save_timeout_ms, save_status, save_error, created_at, updated_at, resume_trigger, save_mode"
	if got := strings.Count(checkpointContents, checkpointColumns); got != 5 {
		t.Errorf("checkpoint column list appears %d times, want 5 (4 pre-existing queries plus update-save-mode)", got)
	}
	// Every checkpoint scan must take save_mode after resume_trigger.
	scans := strings.Count(checkpointContents, "&i.ResumeTrigger,")
	saveModeScans := strings.Count(checkpointContents, "&i.ResumeTrigger,\n\t\t&i.SaveMode,") +
		strings.Count(checkpointContents, "&i.ResumeTrigger,\n\t\t\t&i.SaveMode,")
	if scans != saveModeScans {
		t.Errorf("%d checkpoint scans read resume_trigger but only %d follow it with save_mode", scans, saveModeScans)
	}
}
