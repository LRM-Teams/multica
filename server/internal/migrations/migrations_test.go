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
