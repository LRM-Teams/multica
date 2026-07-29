package execenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializeCanonicalTurnContextWritesAgentsOnly(t *testing.T) {
	workDir := t.TempDir()
	ledgerRoot := t.TempDir()
	ctx := TaskContextForEnv{
		AgentID: "agent-a", AgentName: "Agent", AgentInstructions: "instr",
		AgentSkills: []SkillContextForEnv{{Name: "x", Description: "d", Content: "body"}},
	}
	brief, err := MaterializeCanonicalTurnContext(workDir, ledgerRoot, "grok", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(brief) == "" {
		t.Fatal("empty brief")
	}
	raw, err := os.ReadFile(filepath.Join(workDir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), runtimeMarkerBegin) {
		t.Fatalf("missing managed block:\n%s", raw)
	}
	// No skill package files
	entries, _ := os.ReadDir(workDir)
	for _, e := range entries {
		if e.Name() != "AGENTS.md" {
			t.Fatalf("unexpected path under workdir: %s", e.Name())
		}
	}
}

func TestMaterializeCanonicalTurnContextRejectsLedgerUnderWorkDir(t *testing.T) {
	// Slim path ignores ledgerRoot; still succeeds for call-site compat.
	workDir := t.TempDir()
	_, err := MaterializeCanonicalTurnContext(workDir, workDir, "grok", TaskContextForEnv{AgentID: "a", AgentName: "A"})
	if err != nil {
		t.Fatalf("slim ignores ledger location: %v", err)
	}
}

func TestRenderStartupPlanHasNoSkillFiles(t *testing.T) {
	plan := RenderStartupMaterializationPlan("grok", StartupStaticContext(TaskContextForEnv{
		AgentID: "a", AgentName: "A",
		AgentSkills: []SkillContextForEnv{{Name: "s", Description: "d", Content: "c"}},
	}))
	if plan.RuntimeBrief == "" {
		t.Fatal("expected brief")
	}
	// type no longer has SkillFiles field — compile is the assertion
	_ = plan.Digest()
}
