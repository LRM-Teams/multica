package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func TestPrepareExecutionMemoryLoadsOnlyCurrentScopesAndToday(t *testing.T) {
	root := filepath.Join(t.TempDir(), "agent")
	if err := ensureMulticaAgentRoot(root); err != nil {
		t.Fatal(err)
	}
	task := Task{AgentID: "agent-1", InitiatorType: "member", InitiatorID: "member-a", ProjectID: "project-a", ChannelID: "channel-a"}
	paths := scopedMemoryPathsForTask(root, task)
	ensureScopedMemoryFiles(paths)
	writes := map[string]string{
		filepath.Join(paths.UserDir, "USER.md"):                   "Frank prefers an acknowledgement before work.\n",
		filepath.Join(paths.ProjectDir, "MEMORY.md"):              "Project A uses Go.\n",
		filepath.Join(paths.ProjectDir, "STATE.md"):               "Project A release is blocked.\n",
		filepath.Join(paths.ProjectDir, "DECISIONS.md"):           "Project A targets dev.\n",
		filepath.Join(paths.ChannelDir, "CONTEXT.md"):             "This channel discusses Project A.\n",
		filepath.Join(root, "memory", "MEMORY.md"):                "Agent-wide testing convention.\n",
		filepath.Join(root, "users", "member-b", "USER.md"):       "Other user's private preference.\n",
		filepath.Join(root, "projects", "project-b", "MEMORY.md"): "Other project secret.\n",
	}
	for path, content := range writes {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	todayPath := scopedMemoryTodayPath(root, time.Now())
	if err := os.WriteFile(todayPath, []byte("Today's activity only.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	yesterday := time.Now().Add(-24 * time.Hour)
	if err := os.WriteFile(scopedMemoryTodayPath(root, yesterday), []byte("Yesterday must stay lazy.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	memories, gotPaths := prepareExecutionMemory(root, task, []execenv.MemoryContextForEnv{{Name: "Reviewed", Content: "Reviewed exact preference.", Scope: "user"}})
	if gotPaths != paths {
		t.Fatalf("paths = %#v, want %#v", gotPaths, paths)
	}
	var combined strings.Builder
	total := 0
	for _, memory := range memories {
		combined.WriteString(memory.Content)
		total += len(memory.Content)
	}
	content := combined.String()
	for _, want := range []string{"acknowledgement", "Project A uses Go", "release is blocked", "targets dev", "This channel", "Agent-wide", "Today's activity", "Reviewed exact"} {
		if !strings.Contains(content, want) {
			t.Fatalf("memory pack missing %q:\n%s", want, content)
		}
	}
	for _, unwanted := range []string{"Other user's", "Other project", "Yesterday"} {
		if strings.Contains(content, unwanted) {
			t.Fatalf("memory pack leaked %q:\n%s", unwanted, content)
		}
	}
	if total > executionMemoryBudgetBytes {
		t.Fatalf("memory pack bytes = %d, budget = %d", total, executionMemoryBudgetBytes)
	}
}

func TestPrepareExecutionMemoryEnforcesBudgetAndDeduplicates(t *testing.T) {
	duplicate := strings.Repeat("same ", 300)
	large := strings.Repeat("界", executionMemoryBudgetBytes)
	memories, _ := prepareExecutionMemory("", Task{}, []execenv.MemoryContextForEnv{
		{Name: "first", Content: duplicate, Scope: "user"},
		{Name: "duplicate", Content: duplicate, Scope: "project"},
		{Name: "large", Content: large, Scope: "workspace"},
	})
	if len(memories) != 2 {
		t.Fatalf("memories = %d, want deduplicated 2", len(memories))
	}
	total := 0
	for _, memory := range memories {
		total += len(memory.Content)
	}
	if total > executionMemoryBudgetBytes {
		t.Fatalf("memory pack bytes = %d, budget = %d", total, executionMemoryBudgetBytes)
	}
}
