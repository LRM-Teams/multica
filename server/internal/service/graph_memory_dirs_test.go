package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

// P0 evidence (spec §13 P0-2, §3): a root-level memory_graph shared across
// workspaces must never be resolved as a workspace graph directory.
func TestRootLevelGraphDirIsNeverResolvedForWorkspace(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "memory_graph"), 0o755); err != nil {
		t.Fatal(err)
	}
	if dir, ok := graphMemoryDirForWorkspace(root, uuid.NewString()); ok {
		t.Fatalf("root-level fallback resolved %q: workspace isolation violated", dir)
	}
}
