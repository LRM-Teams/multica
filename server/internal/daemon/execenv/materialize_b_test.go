package execenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializeBClearsLegacyOrphanIssueContext(t *testing.T) {
	workDir, ledgerRoot := materializeLayout(t)
	ctxDir := filepath.Join(workDir, ".agent_context")
	if err := os.MkdirAll(ctxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(ctxDir, "issue_context.md")
	if err := os.WriteFile(orphan, []byte("ISSUE_A_STALE_ORPHAN"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := MaterializeCanonicalTurnContextB(workDir, ledgerRoot, "grok", TaskContextForEnv{
		AgentID: "a", AgentName: "A", ChatSessionID: "c", Directed: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("legacy orphan should be reclaimed: %v", err)
	}
}

func TestMaterializeBSkillCollisionKeepsUserAndManagedSibling(t *testing.T) {
	workDir, ledgerRoot := materializeLayout(t)
	userSkill := filepath.Join(workDir, ".grok", "skills", "review", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(userSkill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userSkill, []byte("USER_OWNED_SKILL"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, receipt, err := MaterializeCanonicalTurnContextB(workDir, ledgerRoot, "grok", TaskContextForEnv{
		AgentID: "a", ChatSessionID: "c", Directed: true,
		AgentSkills: []SkillContextForEnv{{
			Name: "review", Description: "managed", Content: "MANAGED_SKILL_BYTES",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	userRaw, err := os.ReadFile(userSkill)
	if err != nil || string(userRaw) != "USER_OWNED_SKILL" {
		t.Fatalf("user skill clobbered: %v %q", err, userRaw)
	}
	if len(receipt.Skills) != 1 {
		t.Fatalf("receipt skills=%d", len(receipt.Skills))
	}
	if receipt.Skills[0].Decision != "sibling" || receipt.Skills[0].ActualSlug == "review" {
		t.Fatalf("want sibling slug, got %+v", receipt.Skills[0])
	}
	managed := filepath.Join(workDir, ".grok", "skills", receipt.Skills[0].ActualSlug, "SKILL.md")
	body, err := os.ReadFile(managed)
	if err != nil || !strings.Contains(string(body), "MANAGED_SKILL_BYTES") {
		t.Fatalf("managed sibling missing: %v %s", err, body)
	}
}

func TestMaterializeBAgentsFinalHashDiffersByUserPrefix(t *testing.T) {
	// Same managed input, different user AGENTS prefix → input digest same, receipt hash differs.
	mk := func(prefix string) (digest, finalHash string) {
		workDir, ledgerRoot := materializeLayout(t)
		if err := os.WriteFile(filepath.Join(workDir, "AGENTS.md"), []byte(prefix), 0o644); err != nil {
			t.Fatal(err)
		}
		ctx := TaskContextForEnv{AgentID: "a", AgentName: "A", ChatSessionID: "c", Directed: true}
		_, receipt, err := MaterializeCanonicalTurnContextB(workDir, ledgerRoot, "grok", ctx)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(workDir, "AGENTS.md"))
		if err != nil {
			t.Fatal(err)
		}
		if receipt.AgentsFinalSHA256 != sha256Hex(raw) {
			t.Fatalf("receipt hash mismatch disk")
		}
		return StartupStaticDigest("grok", ctx), receipt.AgentsFinalSHA256
	}
	d1, h1 := mk("USER_PREFIX_ONE\n")
	d2, h2 := mk("USER_PREFIX_TWO\n")
	if d1 != d2 {
		t.Fatal("managed input digest should ignore user AGENTS prefix")
	}
	if h1 == h2 {
		t.Fatal("final AGENTS hash must differ for different user prefixes")
	}
}

func TestMaterializeBRefusesAgentContextSymlink(t *testing.T) {
	workDir, ledgerRoot := materializeLayout(t)
	outside := t.TempDir()
	// Symlink .agent_context to outside — legacy reclaim or mkdir should fail closed.
	if err := os.Symlink(outside, filepath.Join(workDir, ".agent_context")); err != nil {
		t.Fatal(err)
	}
	// Seed orphan that would trigger reclaim path
	if err := os.WriteFile(filepath.Join(outside, "issue_context.md"), []byte("ORPHAN"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := MaterializeCanonicalTurnContextB(workDir, ledgerRoot, "grok", TaskContextForEnv{
		AgentID: "a", ChatSessionID: "c", Directed: true,
	})
	if err == nil {
		t.Fatal("expected fail-closed on .agent_context symlink")
	}
}
