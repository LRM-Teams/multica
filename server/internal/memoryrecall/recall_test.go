package memoryrecall

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchRanksScopedProjectMemory(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "memory", "MEMORY.md"), "- Always confirm before force-push.\n")
	write(t, filepath.Join(root, "projects", "proj-1", "MEMORY.md"), "- Fixture conflicts cause make check retry loops; reset testdata/shared.json.\n")
	write(t, filepath.Join(root, "users", "mem-1", "USER.md"), "- Prefer TypeScript.\n")
	write(t, filepath.Join(root, "users", "other", "USER.md"), "- Secret preference must not leak.\n")

	got, err := Search(Scope{AgentRoot: root, MemberID: "mem-1", ProjectID: "proj-1"}, "fixture conflict make check", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Hits) == 0 {
		t.Fatal("expected at least one hit")
	}
	if got.Hits[0].Path != "projects/proj-1/MEMORY.md" {
		t.Fatalf("top hit = %s, want project memory", got.Hits[0].Path)
	}
	for _, hit := range got.Hits {
		if strings.Contains(hit.Path, "users/other") {
			t.Fatalf("leaked other member file: %+v", hit)
		}
	}
}

func TestSearchOmitsUnscopedUserAndProjectFiles(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "users", "mem-1", "USER.md"), "- Prefer TypeScript.\n")
	write(t, filepath.Join(root, "projects", "proj-1", "MEMORY.md"), "- Always run make check.\n")

	got, err := Search(Scope{AgentRoot: root}, "TypeScript check", 8)
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range got.Hits {
		if strings.HasPrefix(hit.Path, "users/") || strings.HasPrefix(hit.Path, "projects/") {
			t.Fatalf("unscoped search leaked %s", hit.Path)
		}
	}
}

func TestGetRejectsTraversalAndOtherMembers(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "users", "mem-1", "USER.md"), "- Prefer TypeScript.\n")
	write(t, filepath.Join(root, "users", "other", "USER.md"), "- Secret.\n")

	scope := Scope{AgentRoot: root, MemberID: "mem-1"}
	if _, err := Get(scope, "../etc/passwd", 1, 10); err == nil {
		t.Fatal("expected traversal reject")
	}
	if _, err := Get(scope, "/etc/passwd", 1, 10); err == nil {
		t.Fatal("expected absolute reject")
	}
	if _, err := Get(scope, "users/other/USER.md", 1, 10); err == nil {
		t.Fatal("expected other-member reject")
	}
	got, err := Get(scope, "users/mem-1/USER.md", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Content, "TypeScript") {
		t.Fatalf("content = %q", got.Content)
	}
}

func TestGetLineWindow(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "memory", "MEMORY.md"), "one\ntwo\nthree\nfour\n")
	got, err := Get(Scope{AgentRoot: root}, "memory/MEMORY.md", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got.LineStart != 2 || got.LineEnd != 3 {
		t.Fatalf("window = %d-%d", got.LineStart, got.LineEnd)
	}
	if got.Content != "two\nthree" {
		t.Fatalf("content = %q", got.Content)
	}
}

func TestSearchRecordsHits(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "memory", "MEMORY.md"), "- Fixture conflicts cause make check retry loops.\n")
	if _, err := Search(Scope{AgentRoot: root}, "fixture conflict", 5); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".multica", "memory-search-hits.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "memory/MEMORY.md") {
		t.Fatalf("hits = %s", data)
	}
}

func TestSearchRequiresQueryAndRoot(t *testing.T) {
	if _, err := Search(Scope{AgentRoot: t.TempDir()}, "   ", 4); err == nil {
		t.Fatal("empty query")
	}
	if _, err := Search(Scope{}, "fixture", 4); err == nil {
		t.Fatal("missing root")
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
