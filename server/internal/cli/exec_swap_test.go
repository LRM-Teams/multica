package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSwapExecutableReplacesInstallPathAndKeepsPrev(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "multica")
	staged := filepath.Join(dir, "staged")
	if err := os.WriteFile(current, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SwapExecutable(current, staged); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(current)
	if err != nil || string(got) != "new" {
		t.Fatalf("current = %q, %v", got, err)
	}
	prev, err := os.ReadFile(current + ".prev")
	if err != nil || string(prev) != "old" {
		t.Fatalf("prev = %q, %v", prev, err)
	}
}

func TestRollbackExecutableRestoresPrev(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "multica")
	if err := os.WriteFile(current, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current+".prev", []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := RollbackExecutable(current); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(current)
	if err != nil || string(got) != "old" {
		t.Fatalf("current = %q, %v", got, err)
	}
}
