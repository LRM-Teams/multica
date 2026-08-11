package memorypolicy

import (
	"strings"
	"testing"
)

func TestSoftFileLimitMatchesRecallBudgets(t *testing.T) {
	tests := map[string]int64{
		"memory/MEMORY.md":                2 * 1024,
		"memory/daily/2026-08-11.md":      2 * 1024,
		"users/member-1/USER.md":          2 * 1024,
		"users/member-1/RELATIONSHIP.md":  1024,
		"projects/project-1/MEMORY.md":    4 * 1024,
		"projects/project-1/STATE.md":     2 * 1024,
		"projects/project-1/DECISIONS.md": 3 * 1024,
		"channels/channel-1/CONTEXT.md":   1536,
	}
	for rel, want := range tests {
		if got := SoftFileLimit(rel); got != want {
			t.Errorf("SoftFileLimit(%q) = %d, want %d", rel, got, want)
		}
	}
	if got := SoftFileLimit("notes/work-log.md"); got != 0 {
		t.Fatalf("notes soft limit = %d, want unmanaged", got)
	}
}

func TestValidateFileRejectsLongEntriesAndOversizedFiles(t *testing.T) {
	if err := ValidateFile("memory/MEMORY.md", []byte("# Memory\n\n- Keep one durable fact per bullet.\n")); err != nil {
		t.Fatal(err)
	}
	longEntry := "# Memory\n\n- " + strings.Repeat("记", DurableEntryMaxRunes+1) + "\n"
	if err := ValidateFile("memory/MEMORY.md", []byte(longEntry)); err == nil || !strings.Contains(err.Error(), "entry limit") {
		t.Fatalf("long-entry error = %v", err)
	}
	oversized := []byte(strings.Repeat("x", int(SoftFileLimit("memory/MEMORY.md"))+1))
	if err := ValidateFile("memory/MEMORY.md", oversized); err == nil || !strings.Contains(err.Error(), "compact target") {
		t.Fatalf("oversized error = %v", err)
	}
}
