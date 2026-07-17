package migrations

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMigration186UpContainsBindingAndTriggerConstraints(t *testing.T) {
	contents := readMigration(t, "186_env_dispatch_message_channels.up.sql")
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

func TestMigration186PreservesExistingSandboxJobTypes(t *testing.T) {
	up := readMigration(t, "186_env_dispatch_message_channels.up.sql")
	down := readMigration(t, "186_env_dispatch_message_channels.down.sql")

	for _, jobType := range []string{
		"'create'", "'stop'", "'resume'", "'delete'", "'reconfigure'",
		"'create_template'", "'delete_template'", "'exec'", "'message'",
	} {
		if !strings.Contains(up, jobType) {
			t.Errorf("migration 186 up drops existing sandbox job type %s", jobType)
		}
		if !strings.Contains(down, jobType) {
			t.Errorf("migration 186 down drops existing sandbox job type %s", jobType)
		}
	}
	if !strings.Contains(up, "'clone'") {
		t.Error("migration 186 up must add clone sandbox job type")
	}
}

func readMigration(t *testing.T, name string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	path := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations", name)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	return string(body)
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
