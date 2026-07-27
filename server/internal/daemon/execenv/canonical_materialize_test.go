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
	}
	if _, err := MaterializeCanonicalTurnContext(dir, "grok", ctxA); err != nil {
		t.Fatalf("materialize A: %v", err)
	}
	// Extra residual file under .agent_context that rewrite alone would not remove.
	poison := filepath.Join(dir, ".agent_context", "poison_from_A.md")
	if err := os.WriteFile(poison, []byte("SECRET_A"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Poison skill tree under provider skills dir.
	skillPoison := filepath.Join(dir, ".grok", "skills", "old-skill", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPoison), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPoison, []byte("old skill body"), 0o644); err != nil {
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
		// issue assignment mode embeds IssueID in the brief.
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

	// Tier C: poison residual and old skills gone.
	if _, err := os.Stat(poison); !os.IsNotExist(err) {
		t.Fatalf("poison residual still present: %v", err)
	}
	if _, err := os.Stat(skillPoison); !os.IsNotExist(err) {
		t.Fatalf("prior-turn skill residual still present: %v", err)
	}
}
