package execenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializeCanonicalTurnContextRefreshAndNoResidual(t *testing.T) {
	dir := t.TempDir()

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

	// User-owned sibling under .agent_context (must survive CleanupSidecars).
	userSibling := filepath.Join(dir, ".agent_context", "user_notes.md")
	if err := os.WriteFile(userSibling, []byte("USER_OWNED_NOTES"), 0o644); err != nil {
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

	mem, err := os.ReadFile(memoryPath)
	if err != nil || string(mem) != "agent durable notes" {
		t.Fatalf("Tier A MEMORY.md not preserved: %v %q", err, mem)
	}
	if raw, err := os.ReadFile(userSibling); err != nil || string(raw) != "USER_OWNED_NOTES" {
		t.Fatalf("user-owned .agent_context sibling not preserved: %v %q", err, raw)
	}

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

	managedPrior := filepath.Join(dir, ".grok", "skills", "prior-managed")
	if _, err := os.Stat(managedPrior); !os.IsNotExist(err) {
		t.Fatalf("prior Multica-managed skill residual still present: %v", err)
	}
}

func TestMaterializeCanonicalTurnContextPreservesUserOwnedSkills(t *testing.T) {
	dir := t.TempDir()
	userSkill := filepath.Join(dir, ".grok", "skills", "user-owned", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(userSkill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userSkill, []byte("# User owned skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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

	raw, err := os.ReadFile(userSkill)
	if err != nil {
		t.Fatalf("user-owned skill missing after materialize: %v", err)
	}
	if !strings.Contains(string(raw), "User owned skill") {
		t.Fatalf("user-owned skill content lost: %s", raw)
	}
	if _, err := os.Stat(managedDir); !os.IsNotExist(err) {
		t.Fatalf("prior managed skill still present: %v", err)
	}
	thisTurn := filepath.Join(dir, ".grok", "skills", "this-turn-managed")
	if !isManagedSkillDir(thisTurn) {
		t.Fatal("this-turn managed skill missing Multica marker")
	}
}

func TestMaterializeCanonicalTurnContextRefusesToClobberPreExistingSidecars(t *testing.T) {
	// Barry: writeContextFiles must not use nil-manifest os.WriteFile on stable cwd.
	dir := t.TempDir()
	ctxDir := filepath.Join(dir, ".agent_context")
	if err := os.MkdirAll(ctxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	userIssue := filepath.Join(ctxDir, "issue_context.md")
	if err := os.WriteFile(userIssue, []byte("USER_OWNED_ISSUE_CONTEXT"), 0o644); err != nil {
		t.Fatal(err)
	}
	projDir := filepath.Join(dir, ".multica", "project")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	userResources := filepath.Join(projDir, "resources.json")
	if err := os.WriteFile(userResources, []byte(`{"user":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := MaterializeCanonicalTurnContext(dir, "grok", TaskContextForEnv{
		AgentID: "agent-a", AgentName: "Agent A", IssueID: "issue-multica", Directed: true,
		ProjectID: "proj-1", ProjectTitle: "P",
	}); err != nil {
		t.Fatalf("materialize: %v", err)
	}

	gotIssue, err := os.ReadFile(userIssue)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotIssue) != "USER_OWNED_ISSUE_CONTEXT" {
		t.Fatalf("pre-existing issue_context.md was clobbered: %q", gotIssue)
	}
	gotRes, err := os.ReadFile(userResources)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotRes) != `{"user":true}` {
		t.Fatalf("pre-existing resources.json was clobbered: %q", gotRes)
	}
	// Multica still refreshed the managed AGENTS block (facts live there when sidecars refuse).
	agents, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agents), "BEGIN MULTICA-RUNTIME") {
		t.Fatal("AGENTS.md missing Multica block after refuse-to-clobber path")
	}
}
