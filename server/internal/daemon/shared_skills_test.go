package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/memorycuration"
)

func TestLocalMemoryCurationRuntimesPreferOnlinePi(t *testing.T) {
	d := &Daemon{
		workspaces: map[string]*workspaceState{
			"ws-1": {runtimeIDs: []string{"rt-codex", "rt-pi"}},
			"ws-2": {runtimeIDs: []string{"rt-offline"}},
		},
		runtimeIndex: map[string]Runtime{
			"rt-codex":   {ID: "rt-codex", WorkspaceID: "ws-1", Provider: "codex", Status: "online"},
			"rt-pi":      {ID: "rt-pi", WorkspaceID: "ws-1", Provider: "pi", Status: "online"},
			"rt-offline": {ID: "rt-offline", WorkspaceID: "ws-2", Provider: "pi", Status: "offline"},
		},
	}

	runtimes := d.localMemoryCurationRuntimes()
	if len(runtimes) != 1 || runtimes[0].ID != "rt-pi" {
		t.Fatalf("runtimes = %#v, want only online Pi runtime", runtimes)
	}
}

func TestClaimLocalMemoryCurationRunOncePerBeijingDate(t *testing.T) {
	d := &Daemon{memoryCurationRuns: map[string]string{}}
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	if !d.claimLocalMemoryCurationRun("ws-1", memorycuration.StageL3, now) {
		t.Fatal("first run was not claimed")
	}
	if d.claimLocalMemoryCurationRun("ws-1", memorycuration.StageL3, now.Add(30*time.Minute)) {
		t.Fatal("same date was claimed twice")
	}
	d.releaseLocalMemoryCurationRun("ws-1", memorycuration.StageL3)
	if !d.claimLocalMemoryCurationRun("ws-1", memorycuration.StageL3, now.Add(30*time.Minute)) {
		t.Fatal("released failed run was not retryable")
	}
}

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
		filepath.Join(root, "notes", "agent-plan.md"),
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
	plan, err := os.ReadFile(filepath.Join(root, "notes", "agent-plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# Agent Plan",
		"## Mission",
		"## Ownership",
		"## Current Project State",
		"## Active Work",
		"## Watchlist",
		"## Completed Work",
		"## Future Bets",
		"## Collaboration Map",
		"## Initiative Rules",
		"## Last Checks",
	} {
		if !strings.Contains(string(plan), want) {
			t.Fatalf("agent-plan.md missing %q:\n%s", want, plan)
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
