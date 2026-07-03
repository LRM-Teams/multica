package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSharedSkillScanRootUsesProviderDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	root, ok := sharedSkillScanRoot(Config{}, "pi")
	if !ok {
		t.Fatal("expected pi shared root")
	}
	want := filepath.Join(home, ".pi", "share", "skills")
	if root != want {
		t.Fatalf("got %q want %q", root, want)
	}

	workspaceRoot := filepath.Join(home, "multica_workspaces")
	agentRoot := piAgentRoot(Config{WorkspacesRoot: workspaceRoot}, "workspace-1", "agent-1")
	agentWant := filepath.Join(workspaceRoot, "workspace-1", ".multica", "agents", "agent-1")
	if agentRoot != agentWant {
		t.Fatalf("got %q want %q", agentRoot, agentWant)
	}

	legacyRoot := legacyPiAgentRoot(Config{WorkspacesRoot: workspaceRoot}, "workspace-1", "agent-1")
	legacyWant := filepath.Join(workspaceRoot, "workspace-1", ".pi", "agents", "agent-1")
	if legacyRoot != legacyWant {
		t.Fatalf("got legacy %q want %q", legacyRoot, legacyWant)
	}

	skillQueue := piAgentSkillCandidatesPath(agentRoot)
	skillQueueWant := filepath.Join(agentWant, "sync_queue", "skill-candidates.jsonl")
	if skillQueue != skillQueueWant {
		t.Fatalf("got %q want %q", skillQueue, skillQueueWant)
	}

	if _, ok := sharedSkillScanRoot(Config{}, "codex"); ok {
		t.Fatal("expected codex to have no default shared root")
	}
}

func TestSharedSkillScanRootGlobalOverride(t *testing.T) {
	root, ok := sharedSkillScanRoot(Config{SharedSkillsDir: "/custom/shared"}, "pi")
	if !ok || root != "/custom/shared" {
		t.Fatalf("expected global override, got %q ok=%v", root, ok)
	}
}

func TestEnsureMulticaAgentRootSeedsManagedFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace-1", ".multica", "agents", "agent-1")
	if err := ensureMulticaAgentRoot(root); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		filepath.Join(root, "memory", "MEMORY.md"),
		filepath.Join(root, "memory", "USER.md"),
		filepath.Join(root, "memory", "REVIEW.md"),
		filepath.Join(root, "notes", "channels.md"),
		filepath.Join(root, "notes", "relationship-map.md"),
		filepath.Join(root, "notes", "role-playbook.md"),
		filepath.Join(root, "runtime", "pi"),
		filepath.Join(root, "runtime", "openclaw"),
		filepath.Join(root, "sync_queue"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
	}

	memoryPath := filepath.Join(root, "memory", "MEMORY.md")
	custom := []byte("custom memory\n")
	if err := os.WriteFile(memoryPath, custom, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureMulticaAgentRoot(root); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(memoryPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(custom) {
		t.Fatalf("ensureMulticaAgentRoot overwrote existing memory: %q", got)
	}
}

func TestLocalSkillScanFingerprintChangesWhenFileChanges(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo-skill")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := localSkillScanFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("version-2"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := localSkillScanFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("expected fingerprint to change after file edit")
	}
}
