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

func TestMaterializeCanonicalTurnContextRefreshAndNoResidual(t *testing.T) {
	workDir, ledgerRoot := materializeLayout(t)

	memoryPath := filepath.Join(workDir, "MEMORY.md")
	if err := os.WriteFile(memoryPath, []byte("agent durable notes"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctxA := TaskContextForEnv{
		AgentID: "agent-a", AgentName: "Agent A", IssueID: "issue-marker-A-UNIQUE", Directed: true,
		AgentSkills: []SkillContextForEnv{{Name: "prior-managed", Content: "# Prior managed skill A\n"}},
	}
	if _, err := MaterializeCanonicalTurnContext(workDir, ledgerRoot, "grok", ctxA); err != nil {
		t.Fatalf("materialize A: %v", err)
	}

	userSibling := filepath.Join(workDir, ".agent_context", "user_notes.md")
	if err := os.WriteFile(userSibling, []byte("USER_OWNED_NOTES"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctxB := TaskContextForEnv{
		AgentID: "agent-a", AgentName: "Agent A", IssueID: "issue-marker-B-UNIQUE", Directed: true,
	}
	if _, err := MaterializeCanonicalTurnContext(workDir, ledgerRoot, "grok", ctxB); err != nil {
		t.Fatalf("materialize B: %v", err)
	}

	if mem, err := os.ReadFile(memoryPath); err != nil || string(mem) != "agent durable notes" {
		t.Fatalf("MEMORY not preserved: %v %q", err, mem)
	}
	if raw, err := os.ReadFile(userSibling); err != nil || string(raw) != "USER_OWNED_NOTES" {
		t.Fatalf("user sibling not preserved: %v %q", err, raw)
	}
	ctxRaw, err := os.ReadFile(filepath.Join(workDir, ".agent_context", "issue_context.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ctxRaw), "issue-marker-A-UNIQUE") || !strings.Contains(string(ctxRaw), "issue-marker-B-UNIQUE") {
		t.Fatalf("issue_context residual wrong: %s", ctxRaw)
	}
	if _, err := os.Stat(filepath.Join(workDir, ".grok", "skills", "prior-managed")); !os.IsNotExist(err) {
		t.Fatalf("prior managed skill residual: %v", err)
	}
}

func TestMaterializeCanonicalTurnContextPreservesUserOwnedSkills(t *testing.T) {
	workDir, ledgerRoot := materializeLayout(t)
	userSkill := filepath.Join(workDir, ".grok", "skills", "user-owned", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(userSkill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userSkill, []byte("# User owned skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	managedDir := filepath.Join(workDir, ".grok", "skills", "prior-managed")
	if err := os.MkdirAll(managedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(managedDir, managedSkillMarker), []byte("prior-managed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(managedDir, "SKILL.md"), []byte("# Prior\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := MaterializeCanonicalTurnContext(workDir, ledgerRoot, "grok", TaskContextForEnv{
		AgentID: "a", AgentName: "A", IssueID: "i", Directed: true,
		AgentSkills: []SkillContextForEnv{{Name: "this-turn-managed", Content: "# T\n"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(userSkill); err != nil {
		t.Fatalf("user skill gone: %v", err)
	}
	if _, err := os.Stat(managedDir); !os.IsNotExist(err) {
		t.Fatal("prior managed still present")
	}
}

func TestMaterializeCanonicalTurnContextRefusesToClobberPreExistingSidecars(t *testing.T) {
	workDir, ledgerRoot := materializeLayout(t)
	ctxDir := filepath.Join(workDir, ".agent_context")
	if err := os.MkdirAll(ctxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	userIssue := filepath.Join(ctxDir, "issue_context.md")
	if err := os.WriteFile(userIssue, []byte("USER_OWNED_ISSUE_CONTEXT"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MaterializeCanonicalTurnContext(workDir, ledgerRoot, "grok", TaskContextForEnv{
		AgentID: "a", AgentName: "A", IssueID: "issue-multica", Directed: true,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(userIssue)
	if err != nil || string(got) != "USER_OWNED_ISSUE_CONTEXT" {
		t.Fatalf("clobbered: %v %q", err, got)
	}
}

func TestMaterializeCanonicalTurnContextRejectsLedgerUnderWorkDir(t *testing.T) {
	workDir := t.TempDir()
	// Malicious / mistaken placement inside provider CWD.
	badLedger := filepath.Join(workDir, ".multica", "canonical_turn_ledger")
	_, err := MaterializeCanonicalTurnContext(workDir, badLedger, "grok", TaskContextForEnv{
		AgentID: "a", AgentName: "A", IssueID: "i", Directed: true,
	})
	if err == nil || !strings.Contains(err.Error(), "must not live under provider workdir") {
		t.Fatalf("want reject ledger under workdir, got %v", err)
	}
}

func TestCleanupSidecarsConfinedRejectsEscapingPaths(t *testing.T) {
	workDir := t.TempDir()
	ledgerRoot := t.TempDir()
	outside := filepath.Join(t.TempDir(), "escape-target")
	if err := os.WriteFile(outside, []byte("do-not-delete"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Legitimate file under workDir.
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
	// Escape target must still exist.
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("escape target was deleted: %v", err)
	}
	// Legit under confine may be deleted.
	if _, err := os.Stat(legit); !os.IsNotExist(err) {
		t.Fatalf("legit under confine should be removed, err=%v", err)
	}
}

func TestMaterializeCanonicalTurnContextRecoversInterruptedWriteWithoutLedger(t *testing.T) {
	// Simulate: wrote Multica sidecars + ownership markers, crashed before ledger.
	workDir, ledgerRoot := materializeLayout(t)
	ctxDir := filepath.Join(workDir, ".agent_context")
	if err := os.MkdirAll(ctxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ctxDir, "issue_context.md"), []byte("**Issue ID:** issue-marker-A-ORPHAN\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ctxDir, managedIssueContextMarker), []byte("issue_context.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// No ledger file yet (crash window).
	if _, err := MaterializeCanonicalTurnContext(workDir, ledgerRoot, "grok", TaskContextForEnv{
		AgentID: "a", AgentName: "A", IssueID: "issue-marker-B-RECOVERED", Directed: true,
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(ctxDir, "issue_context.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "issue-marker-A-ORPHAN") {
		t.Fatalf("orphan A context survived: %s", raw)
	}
	if !strings.Contains(string(raw), "issue-marker-B-RECOVERED") {
		t.Fatalf("B context missing after recovery: %s", raw)
	}
	// Ledger should now exist under daemon-owned root.
	if _, err := os.Stat(filepath.Join(ledgerRoot, sidecarManifestFile)); err != nil {
		t.Fatalf("ledger missing: %v", err)
	}
	// Sanity: ledger JSON has only confined paths.
	data, _ := os.ReadFile(filepath.Join(ledgerRoot, sidecarManifestFile))
	var m sidecarManifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	for _, f := range m.Files {
		if !pathWithin(workDir, f) && filepath.Clean(f) != filepath.Clean(workDir) {
			// pathWithin requires not equal root; files should be under workDir
			absW, _ := filepath.Abs(workDir)
			absF, _ := filepath.Abs(f)
			if absF != absW && !pathWithin(absW, absF) {
				t.Fatalf("ledger file not under workDir: %s", f)
			}
		}
	}
}
