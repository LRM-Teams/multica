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
