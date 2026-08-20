package execenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializeSlimWritesOnlyAgentsManagedBlock(t *testing.T) {
	workDir := t.TempDir()
	ledgerRoot := t.TempDir()
	userPrefix := "# user AGENTS prefix\n\nKeep me.\n"
	if err := os.WriteFile(filepath.Join(workDir, "AGENTS.md"), []byte(userPrefix), 0o644); err != nil {
		t.Fatal(err)
	}
	// User skill tree must not be touched / no Multica package written.
	userSkill := filepath.Join(workDir, ".grok", "skills", "review", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(userSkill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userSkill, []byte("user skill body\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	brief, receipt, err := MaterializeCanonicalTurnContextB(workDir, ledgerRoot, "grok", TaskContextForEnv{
		AgentID:           "agent-a",
		AgentName:         "Agent A",
		AgentInstructions: "be helpful",
		MessageDelivery:   true,
		AgentSkills: []SkillContextForEnv{{
			Name: "review", Description: "d", Content: "managed skill should not land on disk\n",
		}},
	})
	if err != nil {
		t.Fatalf("MaterializeCanonicalTurnContextB: %v", err)
	}
	if strings.TrimSpace(brief) == "" {
		t.Fatal("expected non-empty runtime brief")
	}
	if receipt.AgentsFinalSHA256 == "" || receipt.ManagedInputDigest == "" {
		t.Fatalf("receipt incomplete: %+v", receipt)
	}

	raw, err := os.ReadFile(filepath.Join(workDir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, "Keep me.") {
		t.Fatalf("user prefix not preserved:\n%s", s)
	}
	if !strings.Contains(s, runtimeMarkerBegin) || !strings.Contains(s, runtimeMarkerEnd) {
		t.Fatalf("missing managed markers:\n%s", s)
	}
	// Slim: no skill packages under workdir
	if _, err := os.Stat(filepath.Join(workDir, ".grok", "skills", "review-multica")); !os.IsNotExist(err) {
		t.Fatalf("expected no managed skill package on disk, err=%v", err)
	}
	userBody, err := os.ReadFile(userSkill)
	if err != nil || string(userBody) != "user skill body\n" {
		t.Fatalf("user skill mutated: %v %q", err, userBody)
	}
	// No issue_context / resources written
	if _, err := os.Stat(filepath.Join(workDir, ".agent_context")); !os.IsNotExist(err) {
		t.Fatal("unexpected .agent_context written")
	}
}

func TestMaterializeAppendsAgentScopeMemoryWithoutDigestChurn(t *testing.T) {
	workDir := t.TempDir()
	base := TaskContextForEnv{
		AgentID:   "agent-a",
		AgentName: "Agent A",
		AgentScopeMemories: []MemoryContextForEnv{{
			Name: "Agent global memory", Content: "Prefer terse replies.", Scope: "agent",
		}},
	}
	brief, receipt, err := MaterializeCanonicalTurnContextB(workDir, t.TempDir(), "codex", base)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(brief, "Prefer terse replies") {
		t.Fatalf("brief missing agent-scope memory:\n%s", brief)
	}
	raw, err := os.ReadFile(filepath.Join(workDir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Prefer terse replies") {
		t.Fatalf("AGENTS.md missing agent-scope memory:\n%s", raw)
	}
	wantDigest := StartupStaticDigest("codex", base)
	if receipt.ManagedInputDigest != wantDigest {
		t.Fatalf("digest = %q, want static digest %q (agent memory must not churn)", receipt.ManagedInputDigest, wantDigest)
	}
}

func TestMaterializeSlimRefusesSymlinkAgents(t *testing.T) {
	workDir := t.TempDir()
	ledgerRoot := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	agents := filepath.Join(workDir, "AGENTS.md")
	if err := os.Symlink(outside, agents); err != nil {
		t.Fatal(err)
	}
	_, _, err := MaterializeCanonicalTurnContextB(workDir, ledgerRoot, "grok", TaskContextForEnv{
		AgentID: "a", AgentName: "A",
	})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("want symlink refusal, got %v", err)
	}
	// outside file must not be clobbered
	raw, err := os.ReadFile(outside)
	if err != nil || string(raw) != "outside\n" {
		t.Fatalf("outside file changed: %v %q", err, raw)
	}
}

func TestStartupStaticContextStripsPerTurnSurface(t *testing.T) {
	in := TaskContextForEnv{
		AgentID: "a", AgentName: "A", AgentInstructions: "stay",
		MessageDelivery: true, ChannelID: "ch-1", ProjectID: "p1", ProjectTitle: "P",
		InitiatorName: "Bob", IssueID: "iss",
	}
	out := StartupStaticContext(in)
	if out.MessageDelivery || out.ChannelID != "" || out.ProjectID != "" || out.IssueID != "" || out.InitiatorName != "" {
		t.Fatalf("per-turn fields leaked into static: %+v", out)
	}
	if out.AgentID != "a" || out.AgentInstructions != "stay" {
		t.Fatalf("static agent fields lost: %+v", out)
	}
	// Digest ignores chat surface differences
	d1 := ManagedStartupInputDigest("grok", in)
	in2 := in
	in2.MessageDelivery = false
	d2 := ManagedStartupInputDigest("grok", in2)
	if d1 != d2 {
		t.Fatalf("digest changed across chat surface: %s vs %s", d1, d2)
	}
}

// Reviewer control: slim does not write provider-native skill packages into
// user/workspace CWD. The startup index must point at the durable agent-local
// mirror that actually exists.
func TestBarryMaterializeSlimSkillIndexPointsAtDurableReadableMirror(t *testing.T) {
	agentRoot := t.TempDir()
	workDir := filepath.Join(agentRoot, "workspace")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skills := []SkillContextForEnv{{
		Name: "review", Description: "review helper", Content: "# Review\n",
	}}
	if err := mirrorBoundSkillsToAgentEnabled(agentRoot, skills); err != nil {
		t.Fatalf("mirror bound skills: %v", err)
	}
	realSkill := filepath.Join(agentRoot, "skills", "enabled", "review", "SKILL.md")
	if _, err := os.Stat(realSkill); err != nil {
		t.Fatalf("durable mirror missing: %v", err)
	}

	brief, _, err := MaterializeCanonicalTurnContextB(workDir, t.TempDir(), "grok", TaskContextForEnv{
		AgentID:     "agent-a",
		AgentName:   "Agent A",
		AgentRoot:   agentRoot,
		AgentSkills: skills,
	})
	if err != nil {
		t.Fatalf("materialize slim: %v", err)
	}
	if !strings.Contains(brief, realSkill) {
		t.Fatalf("skill index does not point at durable readable mirror %q:\n%s", realSkill, brief)
	}
	if strings.Contains(brief, ".grok/skills/review/SKILL.md") {
		t.Fatalf("skill index advertises provider-CWD path that slim never creates:\n%s", brief)
	}
}
