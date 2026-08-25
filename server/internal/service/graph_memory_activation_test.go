package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/memorygraph"
)

// Spec §11: first graph activation starts empty. Creating the canonical
// graph dirs touches nothing outside <root>/<ws>/memory_graph — legacy
// MEMORY.md files stay byte-identical and no root-level graph appears.
func TestGraphActivationLeavesLegacyFilesUntouched(t *testing.T) {
	root := t.TempDir()
	ws, pid := uuid.NewString(), uuid.NewString()
	legacyMemory := filepath.Join(root, ws, "agents", "agent-1", "memory", "MEMORY.md")
	if err := os.MkdirAll(filepath.Dir(legacyMemory), 0o755); err != nil {
		t.Fatal(err)
	}
	before := []byte("# agent memory\nlegacy project notes stay here\n")
	if err := os.WriteFile(legacyMemory, before, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := memorygraph.EnsureScopedDir(root, ws, memorygraph.GraphDirKindProject, pid); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(legacyMemory)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("graph activation modified a legacy memory file")
	}
	if _, err := os.Stat(filepath.Join(root, "memory_graph")); !os.IsNotExist(err) {
		t.Fatal("root-level memory_graph must never be created (spec §3)")
	}
}
