package migrations

import (
	"os"
	"path/filepath"
	"testing"
)

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
