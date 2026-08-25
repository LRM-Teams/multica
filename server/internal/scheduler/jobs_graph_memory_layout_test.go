package scheduler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

// P0 evidence (spec §13 P0-2): discovery must return canonical per-workspace
// scoped graphs only — never the legacy root-level memory_graph.
func TestFindMemoryGraphDirsCanonicalOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "memory_graph"), 0o755); err != nil {
		t.Fatal(err)
	}
	ws, pid := uuid.NewString(), uuid.NewString()
	dir := filepath.Join(root, ws, "memory_graph", "projects", pid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	identity := `{"workspace_id":"` + ws + `","kind":"project","owner_id":"` + pid + `"}`
	if err := os.WriteFile(filepath.Join(dir, ".graph_identity.json"), []byte(identity), 0o644); err != nil {
		t.Fatal(err)
	}

	dirs, err := findMemoryGraphDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 1 || dirs[0] != dir {
		t.Fatalf("findMemoryGraphDirs = %v, want exactly [%s]", dirs, dir)
	}
}
