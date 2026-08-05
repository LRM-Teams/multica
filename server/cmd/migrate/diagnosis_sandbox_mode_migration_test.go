package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDiagnosisSharedSandboxMigrationsHaveOneOwner(t *testing.T) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}

	migrationsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		t.Fatalf("read migrations directory: %v", err)
	}

	var sandboxModeOwners []string
	var activeIndexOwners []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(migrationsDir, entry.Name()))
		if err != nil {
			t.Fatalf("read migration %s: %v", entry.Name(), err)
		}
		if strings.Contains(strings.ToLower(string(contents)), "add column sandbox_mode") {
			sandboxModeOwners = append(sandboxModeOwners, entry.Name())
		}
		if strings.Contains(string(contents), "CREATE UNIQUE INDEX interaction_dag_diagnosis_run_active_unique") {
			activeIndexOwners = append(activeIndexOwners, entry.Name())
		}
	}

	if got, want := strings.Join(sandboxModeOwners, ", "), "284_diagnosis_run_sandbox_mode.up.sql"; got != want {
		t.Fatalf("sandbox_mode must have one owning migration: got %q, want %q", got, want)
	}
	if got, want := strings.Join(activeIndexOwners, ", "), "285_diagnosis_run_active_unique.up.sql"; got != want {
		t.Fatalf("active diagnosis index must have one owning migration: got %q, want %q", got, want)
	}
}
