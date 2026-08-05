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
	if err := os.MkdirAll(filepath.Dir(todayPath), 0o755); err != nil {
		t.Fatal(err)
	}
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

func TestPrepareExecutionMemoryDoesNotMaterializeEmptyScopes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "agent")
	task := Task{InitiatorType: "member", InitiatorID: "member-a", ProjectID: "project-a", ChannelID: "channel-a"}

	_, paths := prepareExecutionMemory(root, task, nil)
	for _, path := range []string{paths.UserDir, paths.ProjectDir, paths.ChannelDir, filepath.Join(root, "memory")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("unused scope %s was materialized: %v", path, err)
		}
	}
}

func TestPrepareExecutionMemoryExcludesUserMemoryOnGroupChat(t *testing.T) {
	root := filepath.Join(t.TempDir(), "agent")
	if err := ensureMulticaAgentRoot(root); err != nil {
		t.Fatal(err)
	}
	task := Task{
		AgentID:       "agent-1",
		InitiatorType: "member",
		InitiatorID:   "member-a",
		ChannelID:     "channel-a",
		ChannelKind:   "group",
		ChatSessionID: "chat-1",
		ChatMessage:   "设定个总目标",
	}
	userDir := filepath.Join(root, "users", "member-a")
	channelDir := filepath.Join(root, "channels", "channel-a")
	writes := map[string]string{
		filepath.Join(userDir, "USER.md"):          "Private preference must stay out of group.\n",
		filepath.Join(channelDir, "CONTEXT.md"):    "Channel context is shared.\n",
		filepath.Join(root, "memory", "MEMORY.md"): "Agent-wide ok.\n",
	}
	for path, content := range writes {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	memories, paths := prepareExecutionMemory(root, task, []execenv.MemoryContextForEnv{
		{Name: "User DB", Content: "DB personal preference", Scope: "user"},
		{Name: "Orphan project", Content: "Project without bind", Scope: "project"},
	})
	if paths.UserDir != "" {
		t.Fatalf("group chat UserDir = %q, want empty", paths.UserDir)
	}
	var combined strings.Builder
	for _, memory := range memories {
		combined.WriteString(memory.Content)
	}
	content := combined.String()
	if !strings.Contains(content, "Channel context") || !strings.Contains(content, "Agent-wide") {
		t.Fatalf("group pack missing shared memory:\n%s", content)
	}
	for _, unwanted := range []string{"Private preference", "DB personal", "Project without bind"} {
		if strings.Contains(content, unwanted) {
			t.Fatalf("group pack leaked %q:\n%s", unwanted, content)
		}
	}

	task.ChatMessage = "请带上我的个人偏好再继续"
	memories, paths = prepareExecutionMemory(root, task, nil)
	if paths.UserDir == "" {
		t.Fatal("explicit bring-in should restore UserDir")
	}
	combined.Reset()
	for _, memory := range memories {
		combined.WriteString(memory.Content)
	}
	if !strings.Contains(combined.String(), "Private preference") {
		t.Fatalf("explicit bring-in missing user memory:\n%s", combined.String())
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
