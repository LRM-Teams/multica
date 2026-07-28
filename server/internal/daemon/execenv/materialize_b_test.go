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

func TestMaterializeBResolvedSlugDrivesBriefAndReceipt(t *testing.T) {
	workDir, ledgerRoot := materializeLayout(t)
	userSkill := filepath.Join(workDir, ".grok", "skills", "review", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(userSkill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userSkill, []byte("USER_OWNED_SKILL"), 0o644); err != nil {
		t.Fatal(err)
	}
	brief, receipt, err := MaterializeCanonicalTurnContextB(workDir, ledgerRoot, "grok", TaskContextForEnv{
		AgentID: "a", ChatSessionID: "c", Directed: true,
		AgentSkills: []SkillContextForEnv{{Name: "review", Description: "d", Content: "MANAGED"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.Skills) != 1 || receipt.Skills[0].ActualSlug == "review" {
		t.Fatalf("receipt: %+v", receipt.Skills)
	}
	wantLoc := ".grok/skills/" + receipt.Skills[0].ActualSlug + "/SKILL.md"
	if !strings.Contains(brief, wantLoc) {
		t.Fatalf("brief must index actual slug %s:\n%s", wantLoc, brief)
	}
	if strings.Contains(brief, ".grok/skills/review/SKILL.md") {
		t.Fatal("brief must not point at user-owned review path")
	}
}

func TestMaterializeBResolveFailureLeavesNoSkillResidue(t *testing.T) {
	// AGENTS as symlink → fail before apply; resolve must not leave skill dirs.
	workDir, ledgerRoot := materializeLayout(t)
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workDir, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	_, _, err := MaterializeCanonicalTurnContextB(workDir, ledgerRoot, "grok", TaskContextForEnv{
		AgentID: "a", ChatSessionID: "c", Directed: true,
		AgentSkills: []SkillContextForEnv{{Name: "review", Content: "body"}},
	})
	if err == nil {
		t.Fatal("expected AGENTS symlink failure")
	}
	// No skill package under .grok/skills
	skillsRoot := filepath.Join(workDir, ".grok", "skills")
	if entries, err := os.ReadDir(skillsRoot); err == nil && len(entries) > 0 {
		t.Fatalf("resolve/preflight failure left skill residue: %v", entries)
	}
}

func TestMaterializeBStagingDoesNotDeleteUserOwnedSibling(t *testing.T) {
	workDir, ledgerRoot := materializeLayout(t)
	// User owns a path that would collide with the old fixed staging name.
	userStage := filepath.Join(workDir, ".grok", "skills", "review.multica-staging", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(userStage), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userStage, []byte("USER_STAGE_BYTES"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := MaterializeCanonicalTurnContextB(workDir, ledgerRoot, "grok", TaskContextForEnv{
		AgentID: "a", ChatSessionID: "c", Directed: true,
		AgentSkills: []SkillContextForEnv{{Name: "review", Content: "MANAGED"}},
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(userStage)
	if err != nil || string(raw) != "USER_STAGE_BYTES" {
		t.Fatalf("user staging sibling deleted/clobbered: %v %q", err, raw)
	}
}

func TestMaterializeBReservesSlugsAcrossAssignedSkills(t *testing.T) {
	workDir, ledgerRoot := materializeLayout(t)
	_, receipt, err := MaterializeCanonicalTurnContextB(workDir, ledgerRoot, "grok", TaskContextForEnv{
		AgentID: "a", ChatSessionID: "c", Directed: true,
		AgentSkills: []SkillContextForEnv{
			{Name: "Review", Content: "BODY_A"},
			{Name: "review!", Content: "BODY_B"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.Skills) != 2 {
		t.Fatalf("want 2 skills, got %d", len(receipt.Skills))
	}
	if receipt.Skills[0].ActualSlug == receipt.Skills[1].ActualSlug {
		t.Fatalf("slugs must differ: %+v", receipt.Skills)
	}
	for _, sk := range receipt.Skills {
		p := filepath.Join(workDir, ".grok", "skills", sk.ActualSlug, "SKILL.md")
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("missing skill package %s: %v", sk.ActualSlug, err)
		}
	}
}

func TestMaterializeBSupportingFileCannotEscapePackage(t *testing.T) {
	workDir, ledgerRoot := materializeLayout(t)
	// Pre-seed a file that escape would clobber.
	target := filepath.Join(workDir, ".grok", "skills", "USER_OWNED.txt")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("USER_BYTES"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := MaterializeCanonicalTurnContextB(workDir, ledgerRoot, "grok", TaskContextForEnv{
		AgentID: "a", ChatSessionID: "c", Directed: true,
		AgentSkills: []SkillContextForEnv{{
			Name: "safe", Content: "body",
			Files: []SkillFileContextForEnv{{Path: "../USER_OWNED.txt", Content: "CLOBBERED"}},
		}},
	})
	if err == nil {
		t.Fatal("expected reject escaping supporting path")
	}
	raw, err := os.ReadFile(target)
	if err != nil || string(raw) != "USER_BYTES" {
		t.Fatalf("user file clobbered: %v %q", err, raw)
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
