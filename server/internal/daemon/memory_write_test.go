package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClassifyMemoryWritePath(t *testing.T) {
	cases := []struct {
		rel      string
		scope    string
		key      string
		ok       bool
		excluded bool
	}{
		{"memory/MEMORY.md", "agent_global", "MEMORY", true, false},
		{"memory/STATE.md", "agent_state", "STATE", true, false},
		{"memory/daily/2026-07-22.md", "agent_daily", "DAILY", true, false},
		{"memory/REVIEW.md", "", "", false, true},
		{"users/u1/USER.md", "user", "USER", true, false},
		{"users/u1/RELATIONSHIP.md", "user", "RELATIONSHIP", true, false},
		{"channels/c1/CONTEXT.md", "channel", "CONTEXT", true, false},
		{"projects/p1/MEMORY.md", "project", "MEMORY", true, false},
		{"projects/p1/STATE.md", "project", "STATE", true, false},
		{"projects/p1/DECISIONS.md", "project", "DECISIONS", true, false},
		{"notes/agents.md", "agent_notes", "AGENTS", true, false},
		{"notes/relationship-map.md", "agent_notes", "RELATIONSHIP_MAP", true, false},
		{"notes/work-log.md", "agent_notes", "WORK_LOG", true, false},
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

func TestDiffAgentMemoryWritesDetectsDailyAndScopedFiles(t *testing.T) {
	root := t.TempDir()
	paths := map[string]string{
		"memory/daily/2026-07-22.md": "daily",
		"users/u1/RELATIONSHIP.md":   "relationship",
		"projects/p1/STATE.md":       "state",
		"notes/agents.md":            "agents",
		"notes/relationship-map.md":  "relationship map",
		"notes/work-log.md":          "work log",
	}
	for rel, body := range paths {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, changes, err := diffAgentMemoryWrites(root, memoryWriteSnapshot{Files: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != len(paths) {
		t.Fatalf("changes = %d, want %d: %+v", len(changes), len(paths), changes)
	}
}

func TestDailyCloseoutGuardSkipsGreetingAndAppendsSubstantiveStub(t *testing.T) {
	root := t.TempDir()
	d := &Daemon{cfg: Config{WorkspacesRoot: root}}
	task := Task{WorkspaceID: "ws-1", AgentID: "agent-1", ChatSessionID: "chat-1", ChatMessage: "hi"}
	d.maybeAppendDailyCloseoutStub(task, TaskResult{Status: "completed"})
	dailyDir := filepath.Join(root, "ws-1", ".multica", "agents", "agent-1", "memory", "daily")
	if entries, _ := os.ReadDir(dailyDir); len(entries) != 0 {
		t.Fatalf("greeting wrote daily entries: %+v", entries)
	}

	task.ChatMessage = "please investigate the failing deploy"
	d.maybeAppendDailyCloseoutStub(task, TaskResult{Status: "completed", BranchName: "fix/deploy"})
	entries, err := os.ReadDir(dailyDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("daily entries = %d, want 1", len(entries))
	}
	data, err := os.ReadFile(filepath.Join(dailyDir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); !strings.Contains(got, "Auto Closeout") || !strings.Contains(got, "branch=fix/deploy") {
		t.Fatalf("daily stub missing expected content:\n%s", got)
	}
}

func TestDailyCloseoutGuardDoesNotDuplicateFreshDaily(t *testing.T) {
	root := t.TempDir()
	d := &Daemon{cfg: Config{WorkspacesRoot: root}}
	agentRoot := multicaAgentRoot(d.cfg, "ws-1", "agent-1")
	dailyPath := filepath.Join(agentRoot, "memory", "daily", time.Now().UTC().Format("2006-01-02")+".md")
	if err := os.MkdirAll(filepath.Dir(dailyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dailyPath, []byte("agent already wrote daily"), 0o644); err != nil {
		t.Fatal(err)
	}
	d.maybeAppendDailyCloseoutStub(Task{WorkspaceID: "ws-1", AgentID: "agent-1", IssueID: "issue-1"}, TaskResult{Status: "completed"})
	data, err := os.ReadFile(dailyPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "Auto Closeout") {
		t.Fatalf("fresh daily was duplicated:\n%s", string(data))
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

func TestMemoryWriteTriggerText(t *testing.T) {
	if got := memoryWriteTriggerText(Task{ChatMessage: "记住这个"}); got != "记住这个" {
		t.Fatalf("got %q", got)
	}
	if got := memoryWriteTriggerText(Task{TriggerCommentContent: "以后都先反馈"}); got != "以后都先反馈" {
		t.Fatalf("got %q", got)
	}
}

func TestLoadAndClearMemorySignals(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sync_queue", "memory-signal.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "{\"action\":\"write\",\"topic\":\"progress_feedback\",\"summary\":\"先反馈\"}\n{\"action\":\"none\"}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	signals := loadMemorySignals(root)
	if len(signals) != 2 {
		t.Fatalf("signals=%+v", signals)
	}
	if signals[0].Topic != "progress_feedback" {
		t.Fatalf("topic=%q", signals[0].Topic)
	}
	if err := clearMemorySignalQueue(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected signal queue cleared, err=%v", err)
	}
}
