package execenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// MaterializeCanonicalTurnContext is the D6-1b contract for stable turn.WorkDir:
// Tier B refresh + Tier C residual clear (task A facts must not leak into turn B).
func TestMaterializeCanonicalTurnContextRefreshAndNoResidual(t *testing.T) {
	dir := t.TempDir()

	// Durable agent file (Tier A) must survive across materialize.
	memoryPath := filepath.Join(dir, "MEMORY.md")
	if err := os.WriteFile(memoryPath, []byte("agent durable notes"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctxA := TaskContextForEnv{
		AgentID:   "agent-a",
		AgentName: "Agent A",
		IssueID:   "issue-marker-A-UNIQUE",
		Directed:  true,
		AgentSkills: []SkillContextForEnv{{
			Name:    "prior-managed",
			Content: "# Prior managed skill A\n",
		}},
	}
	if _, err := MaterializeCanonicalTurnContext(dir, "grok", ctxA); err != nil {
		t.Fatalf("materialize A: %v", err)
	}
	// Extra residual file under .agent_context that rewrite alone would not remove.
	poison := filepath.Join(dir, ".agent_context", "poison_from_A.md")
	if err := os.WriteFile(poison, []byte("SECRET_A"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctxB := TaskContextForEnv{
		AgentID:   "agent-a",
		AgentName: "Agent A",
		IssueID:   "issue-marker-B-UNIQUE",
		Directed:  true,
	}
	if _, err := MaterializeCanonicalTurnContext(dir, "grok", ctxB); err != nil {
		t.Fatalf("materialize B: %v", err)
	}

	// Tier A preserved.
	mem, err := os.ReadFile(memoryPath)
	if err != nil || string(mem) != "agent durable notes" {
		t.Fatalf("Tier A MEMORY.md not preserved: %v %q", err, mem)
	}

	// Tier B: AGENTS managed block + B context present.
	agents, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agents), "BEGIN MULTICA-RUNTIME") {
		t.Fatal("AGENTS.md missing managed Multica block")
	}
	if !strings.Contains(string(agents), "issue-marker-B-UNIQUE") {
		t.Fatalf("AGENTS.md missing turn B issue marker:\n%s", agents)
	}
	if strings.Contains(string(agents), "issue-marker-A-UNIQUE") {
		t.Fatalf("AGENTS.md still has turn A issue marker:\n%s", agents)
	}

	ctxRaw, err := os.ReadFile(filepath.Join(dir, ".agent_context", "issue_context.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ctxRaw), "issue-marker-B-UNIQUE") {
		t.Fatalf("issue_context missing B: %s", ctxRaw)
	}
	if strings.Contains(string(ctxRaw), "issue-marker-A-UNIQUE") {
		t.Fatalf("issue_context still has A: %s", ctxRaw)
	}

	// Tier C: poison residual gone; prior Multica-managed skill gone.
	if _, err := os.Stat(poison); !os.IsNotExist(err) {
		t.Fatalf("poison residual still present: %v", err)
	}
	managedPrior := filepath.Join(dir, ".grok", "skills", "prior-managed")
	if _, err := os.Stat(managedPrior); !os.IsNotExist(err) {
		t.Fatalf("prior Multica-managed skill residual still present: %v", err)
	}
}

func TestMaterializeCanonicalTurnContextPreservesUserOwnedSkills(t *testing.T) {
	// Barry BLOCK: must not RemoveAll the whole .grok/skills tree.
	dir := t.TempDir()
	userSkill := filepath.Join(dir, ".grok", "skills", "user-owned", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(userSkill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userSkill, []byte("# User owned skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Multica-managed sibling from a prior turn (marker present).
	managedDir := filepath.Join(dir, ".grok", "skills", "prior-managed")
	if err := os.MkdirAll(managedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(managedDir, managedSkillMarker), []byte("prior-managed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(managedDir, "SKILL.md"), []byte("# Prior managed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := MaterializeCanonicalTurnContext(dir, "grok", TaskContextForEnv{
		AgentID: "agent-a", AgentName: "Agent A", IssueID: "issue-B", Directed: true,
		AgentSkills: []SkillContextForEnv{{
			Name:    "this-turn-managed",
			Content: "# This turn managed\n",
		}},
	}); err != nil {
		t.Fatalf("materialize: %v", err)
	}

	// User-owned sibling preserved (Tier A).
	raw, err := os.ReadFile(userSkill)
	if err != nil {
		t.Fatalf("user-owned skill missing after materialize: %v", err)
	}
	if !strings.Contains(string(raw), "User owned skill") {
		t.Fatalf("user-owned skill content lost: %s", raw)
	}
	// Prior Multica-managed skill removed (Tier C).
	if _, err := os.Stat(managedDir); !os.IsNotExist(err) {
		t.Fatalf("prior managed skill still present: %v", err)
	}
	// This-turn managed skill written with marker.
	thisTurn := filepath.Join(dir, ".grok", "skills", "this-turn-managed")
	if !isManagedSkillDir(thisTurn) {
		t.Fatal("this-turn managed skill missing Multica marker")
	}
	if _, err := os.Stat(filepath.Join(thisTurn, "SKILL.md")); err != nil {
		t.Fatalf("this-turn managed SKILL.md: %v", err)
	}
}
