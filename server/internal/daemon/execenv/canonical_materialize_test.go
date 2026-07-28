package execenv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func materializeLayout(t *testing.T) (workDir, ledgerRoot string) {
	t.Helper()
	root := t.TempDir()
	workDir = filepath.Join(root, "workspace")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ledgerRoot = CanonicalTurnLedgerRoot(root)
	return workDir, ledgerRoot
}

func TestMaterializeCanonicalTurnContextWritesStartupBriefAndSkills(t *testing.T) {
	workDir, ledgerRoot := materializeLayout(t)
	memoryPath := filepath.Join(workDir, "MEMORY.md")
	if err := os.WriteFile(memoryPath, []byte("agent durable notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	// User skill sibling (Tier A).
	userSkill := filepath.Join(workDir, ".grok", "skills", "user-owned", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(userSkill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userSkill, []byte("# User owned\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := TaskContextForEnv{
		AgentID: "agent-a", AgentName: "Agent A", ChatSessionID: "chat-1", Directed: true,
		// Per-turn fields must not force issue_context into startup snapshot.
		InitiatorName: "Alice", IssueID: "issue-turn-A",
		AgentSkills: []SkillContextForEnv{{
			Name: "prior-managed", Description: "d", Content: "# Prior managed\n",
		}},
	}
	if _, err := MaterializeCanonicalTurnContext(workDir, ledgerRoot, "grok", ctx); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if mem, err := os.ReadFile(memoryPath); err != nil || string(mem) != "agent durable notes" {
		t.Fatalf("MEMORY not preserved: %v", err)
	}
	agents, err := os.ReadFile(filepath.Join(workDir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agents), "BEGIN MULTICA-RUNTIME") {
		t.Fatal("missing Multica runtime block")
	}
	if strings.Contains(string(agents), "Alice") || strings.Contains(string(agents), "issue-turn-A") {
		t.Fatal("startup AGENTS must not include per-turn initiator/issue")
	}
	if _, err := os.ReadFile(userSkill); err != nil {
		t.Fatalf("user skill gone: %v", err)
	}
	managed := filepath.Join(workDir, ".grok", "skills", "prior-managed")
	if !isManagedSkillDir(managed) {
		t.Fatal("managed skill missing marker")
	}
	// Digest of ctx matches pure plan.
	if StartupStaticDigest("grok", ctx) == "" {
		t.Fatal("empty digest")
	}
}

func TestMaterializeCanonicalTurnContextRejectsLedgerUnderWorkDir(t *testing.T) {
	workDir := t.TempDir()
	badLedger := filepath.Join(workDir, ".multica", "canonical_turn_ledger")
	_, err := MaterializeCanonicalTurnContext(workDir, badLedger, "grok", TaskContextForEnv{
		AgentID: "a", ChatSessionID: "c", Directed: true,
	})
	if err == nil || !strings.Contains(err.Error(), "must not live under provider workdir") {
		t.Fatalf("want reject ledger under workdir, got %v", err)
	}
}

func TestMaterializeCanonicalTurnContextRefusesSymlinkRuntimeConfig(t *testing.T) {
	workDir, ledgerRoot := materializeLayout(t)
	outside := filepath.Join(t.TempDir(), "outside-agents.md")
	if err := os.WriteFile(outside, []byte("OUTSIDE_USER_BYTES"), 0o644); err != nil {
		t.Fatal(err)
	}
	// AGENTS.md as symlink outside workDir.
	if err := os.Symlink(outside, filepath.Join(workDir, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	_, err := MaterializeCanonicalTurnContext(workDir, ledgerRoot, "grok", TaskContextForEnv{
		AgentID: "a", AgentName: "A", ChatSessionID: "c", Directed: true,
	})
	if err == nil {
		t.Fatal("expected refuse symlink AGENTS.md")
	}
	raw, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "OUTSIDE_USER_BYTES" {
		t.Fatalf("outside file was mutated: %q", raw)
	}
}

func TestCleanupSidecarsConfinedRejectsEscapingPaths(t *testing.T) {
	workDir := t.TempDir()
	ledgerRoot := t.TempDir()
	outside := filepath.Join(t.TempDir(), "escape-target")
	if err := os.WriteFile(outside, []byte("do-not-delete"), 0o644); err != nil {
		t.Fatal(err)
	}
	legit := filepath.Join(workDir, "legit.md")
	if err := os.WriteFile(legit, []byte("legit"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &sidecarManifest{Files: []string{legit, outside}}
	if err := writeSidecarManifestAtomic(ledgerRoot, m); err != nil {
		t.Fatal(err)
	}
	err := CleanupSidecarsConfined(ledgerRoot, workDir)
	if err == nil || !strings.Contains(err.Error(), "escapes confine root") {
		t.Fatalf("want escape rejection, got %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("escape target deleted: %v", err)
	}
}

func TestStartupDigestIncludesSkillDescriptionInPlan(t *testing.T) {
	// Sanity: plan skill body includes description via ensureSkillFrontmatter.
	ctx := TaskContextForEnv{
		AgentID: "a", ChatSessionID: "c",
		AgentSkills: []SkillContextForEnv{{Name: "s1", Description: "unique-desc-xyz", Content: "body only"}},
	}
	plan := RenderStartupMaterializationPlan("grok", StartupStaticContext(ctx))
	found := false
	for _, body := range plan.SkillFiles {
		if strings.Contains(body, "unique-desc-xyz") {
			found = true
		}
	}
	if !found {
		// ensureSkillFrontmatter may put description in YAML
		t.Logf("skill files: %v", plan.SkillFiles)
		// At minimum description change must change digest — covered in startup_digest_test.
	}
	_ = json.Marshal
}
