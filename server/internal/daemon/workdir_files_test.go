package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWalkWorkdirFiles(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string, dir bool) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if dir {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", rel, err)
			}
			return
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir parent %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	mk("src/main.ts", false)
	mk("src/util/helper.ts", false)
	mk("README.md", false)
	mk("node_modules/dep/index.js", false) // ignored subtree
	mk(".git/config", false)               // ignored subtree

	nodes, truncated, err := walkWorkdirFiles(root, 0, 0)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if truncated {
		t.Fatalf("unexpected truncation")
	}

	got := map[string]bool{} // path -> isDir
	for _, n := range nodes {
		got[n.Path] = n.IsDir
	}

	wantFiles := []string{"README.md", "src/main.ts", "src/util/helper.ts"}
	for _, f := range wantFiles {
		if isDir, ok := got[f]; !ok || isDir {
			t.Errorf("expected file %q in listing, got %v (present=%v)", f, isDir, ok)
		}
	}
	wantDirs := []string{"src", "src/util"}
	for _, d := range wantDirs {
		if isDir, ok := got[d]; !ok || !isDir {
			t.Errorf("expected dir %q in listing, got isDir=%v (present=%v)", d, isDir, ok)
		}
	}
	for ignored := range map[string]struct{}{"node_modules": {}, ".git": {}} {
		if _, ok := got[ignored]; ok {
			t.Errorf("ignored dir %q should not appear", ignored)
		}
	}
	for _, n := range nodes {
		if n.Path == "node_modules/dep/index.js" || n.Path == ".git/config" {
			t.Errorf("ignored subtree leaked: %q", n.Path)
		}
	}
}

func TestWalkWorkdirFiles_EntryCapTruncates(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 10; i++ {
		if err := os.WriteFile(filepath.Join(root, string(rune('a'+i))+".txt"), []byte("x"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	nodes, truncated, err := walkWorkdirFiles(root, 5, 0)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if !truncated {
		t.Fatalf("expected truncated when entries exceed cap")
	}
	if len(nodes) > 5 {
		t.Fatalf("expected at most 5 nodes, got %d", len(nodes))
	}
}
