package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClassifyMemoryWritePath(t *testing.T) {
	cases := []struct {
		rel       string
		scope     string
		key       string
		ok        bool
		excluded  bool
	}{
		{"memory/MEMORY.md", "agent_global", "MEMORY", true, false},
		{"memory/STATE.md", "agent_state", "STATE", true, false},
		{"memory/REVIEW.md", "", "", false, true},
		{"users/u1/USER.md", "user", "USER", true, false},
		{"channels/c1/CONTEXT.md", "channel", "CONTEXT", true, false},
		{"projects/p1/MEMORY.md", "project", "MEMORY", true, false},
		{"projects/p1/DECISIONS.md", "project", "DECISIONS", true, false},
		{"sync_queue/memory-candidates.jsonl", "", "", false, false},
	}
	for _, tc := range cases {
		if tc.excluded {
			if !isExcludedMemoryWritePath(tc.rel) {
				t.Fatalf("%s should be excluded", tc.rel)
			}
			continue
		}
		scope, key, ok := classifyMemoryWritePath(tc.rel)
		if ok != tc.ok || scope != tc.scope || key != tc.key {
			t.Fatalf("classify(%q) = (%q,%q,%v), want (%q,%q,%v)", tc.rel, scope, key, ok, tc.scope, tc.key, tc.ok)
		}
	}
}

func TestDiffAgentMemoryWritesDetectsChangeAndSkipsEmpty(t *testing.T) {
	root := t.TempDir()
	memoryDir := filepath.Join(root, "memory")
	if err := os.MkdirAll(memoryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memoryDir, "MEMORY.md"), []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memoryDir, "REVIEW.md"), []byte("draft"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memoryDir, "STATE.md"), []byte("   \n"), 0o644); err != nil {
		t.Fatal(err)
	}

	prior := memoryWriteSnapshot{Files: map[string]string{}}
	next, changes, err := diffAgentMemoryWrites(root, prior)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("changes = %d, want 1", len(changes))
	}
	if changes[0].RelPath != "memory/MEMORY.md" || changes[0].ScopeType != "agent_global" {
		t.Fatalf("unexpected change: %+v", changes[0])
	}

	next2, changes2, err := diffAgentMemoryWrites(root, next)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes2) != 0 {
		t.Fatalf("expected no changes on second diff, got %+v", changes2)
	}
	if next2.Files["memory/MEMORY.md"] == "" {
		t.Fatal("expected snapshot hash persisted")
	}
}

func TestDiffAgentMemoryWritesDetectsUpdate(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "memory", "MEMORY.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	prior := memoryWriteSnapshot{Files: map[string]string{}}
	next, changes, err := diffAgentMemoryWrites(root, prior)
	if err != nil || len(changes) != 1 {
		t.Fatalf("initial diff: changes=%+v err=%v", changes, err)
	}
	if err := os.WriteFile(path, []byte("v2 longer"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, changes, err = diffAgentMemoryWrites(root, next)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("update diff: changes=%+v", changes)
	}
}
