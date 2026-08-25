package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLifecycleDiagnosticsOwnerOnlyAndRedacted(t *testing.T) {
	now := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	writer := newLifecycleDiagnosticWriter(dir, func() time.Time { return now })
	if err := writer.Record(agentLifecycleTransition{StateInstanceID: "state-1", AgentInstanceID: "instance-1", Sequence: 1, Phase: "runtime_readiness", State: "waiting", Event: "enter", At: now}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("diagnostic files = %v, %v", entries, err)
	}
	path := filepath.Join(dir, entries[0].Name())
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("diagnostic permissions = %o, want 0600", info.Mode().Perm())
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"prompt", "credential", "environment", "stderr", "path", "tool"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("diagnostic leaked %q: %s", forbidden, body)
		}
	}
}

func TestLifecycleDiagnosticsRotateByDateAndSize(t *testing.T) {
	now := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	writer := newLifecycleDiagnosticWriter(dir, func() time.Time { return now })
	writer.currentPath = filepath.Join(dir, "lifecycle-2026-08-06-000.jsonl")
	writer.currentDay = "2026-08-06"
	writer.currentSize = lifecycleDiagnosticFileBytes
	if err := os.WriteFile(writer.currentPath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(writer.currentPath, lifecycleDiagnosticFileBytes); err != nil {
		t.Fatal(err)
	}
	if err := writer.Record(agentLifecycleTransition{StateInstanceID: "state-1", AgentInstanceID: "instance-1", Sequence: 1, Phase: "process_residency", State: "starting", Event: "enter", At: now}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(writer.currentPath, "-001.jsonl") {
		t.Fatalf("size rotation path = %s", writer.currentPath)
	}
	now = now.Add(24 * time.Hour)
	if err := writer.Record(agentLifecycleTransition{StateInstanceID: "state-2", AgentInstanceID: "instance-1", Sequence: 2, Phase: "runtime_readiness", State: "waiting", Event: "enter", At: now}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(writer.currentPath, "2026-08-07") {
		t.Fatalf("date rotation path = %s", writer.currentPath)
	}
}

func TestLifecycleDiagnosticsCleanupRetainsCurrentAndDeletesOldestOverCap(t *testing.T) {
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	writer := newLifecycleDiagnosticWriter(dir, func() time.Time { return now })
	old := filepath.Join(dir, "lifecycle-2026-08-01-000.jsonl")
	newer := filepath.Join(dir, "lifecycle-2026-08-09-000.jsonl")
	current := filepath.Join(dir, "lifecycle-2026-08-10-000.jsonl")
	for _, path := range []string{old, newer, current} {
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(path, lifecycleDiagnosticCapBytes/2+1); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := now.Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(old, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	writer.currentPath = current
	if err := writer.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("expired diagnostic remains: %v", err)
	}
	if _, err := os.Stat(current); err != nil {
		t.Fatalf("current writable diagnostic was deleted: %v", err)
	}
	if _, err := os.Stat(newer); !os.IsNotExist(err) {
		t.Fatalf("oldest over-cap diagnostic remains: %v", err)
	}
}
