package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAgentRuntimeSessionStoreIsInMemoryAndClearsLegacyDisk(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, ".multica", "runtime-sessions", "agent-1", "runtime-1")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("disk-session\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	store := newAgentRuntimeSessionStore(root)
	got, err := store.Get("agent-1", "runtime-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("new DaemonCore cache = %q, want empty (Raft starts empty until agent:start)", got)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy disk pointer still present: %v", err)
	}

	if err := store.Put("agent-1", "runtime-1", "live-session"); err != nil {
		t.Fatal(err)
	}
	got, err = store.Get("agent-1", "runtime-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "live-session" {
		t.Fatalf("in-process cache = %q, want live-session", got)
	}
	if _, err := os.Stat(filepath.Join(root, ".multica", "runtime-sessions")); !os.IsNotExist(err) {
		t.Fatal("in-process Put must not recreate the disk pointer")
	}

	if err := store.Invalidate("cmd", "agent-1", "runtime-1"); err != nil {
		t.Fatal(err)
	}
	got, err = store.Get("agent-1", "runtime-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("after invalidate = %q, want empty", got)
	}
}
