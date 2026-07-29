package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializeHydrateEntriesActiveAndConflicts(t *testing.T) {
	root := t.TempDir()
	resp := AgentMemoryHydrateResponse{
		Active: []AgentMemoryHydrateEntry{
			{RelPath: "users/m1/USER.md", Content: "长任务先报进度", Topic: "progress_feedback", Status: "active"},
			{RelPath: "users/m1/USER.md", Content: "发现问题直接指出", Topic: "direct_critique", Status: "active"},
		},
		Conflicts: []AgentMemoryHydrateEntry{
			{RelPath: "users/m1/USER.md", Content: "紧急时别报进度直接干", Topic: "progress_feedback", IdentityKey: "user:m1+preference+progress_feedback", Status: "conflict"},
		},
	}
	if err := materializeHydrateEntries(root, resp); err != nil {
		t.Fatal(err)
	}
	userPath := filepath.Join(root, "users", "m1", "USER.md")
	data, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.Contains(body, "长任务先报进度") || !strings.Contains(body, "发现问题直接指出") {
		t.Fatalf("active missing: %s", body)
	}
	if strings.Contains(body, "紧急时别报进度直接干") {
		t.Fatalf("conflict must not land in USER.md: %s", body)
	}
	review, err := os.ReadFile(filepath.Join(root, "memory", "REVIEW.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(review), "Center Sync Conflicts") || !strings.Contains(string(review), "紧急时别报进度直接干") {
		t.Fatalf("review=%s", review)
	}
}

func TestMergeBulletFileIdempotent(t *testing.T) {
	existing := "# User Preferences\n\n- 长任务先报进度\n"
	out := mergeBulletFile(existing, []string{"长任务先报进度", "发现问题直接指出"}, "# User Preferences\n")
	if strings.Count(out, "长任务先报进度") != 1 {
		t.Fatalf("duplicated: %s", out)
	}
	if !strings.Contains(out, "发现问题直接指出") {
		t.Fatalf("missing new bullet: %s", out)
	}
}
