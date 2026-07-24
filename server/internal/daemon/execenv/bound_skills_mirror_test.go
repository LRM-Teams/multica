package execenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMirrorBoundSkillsToAgentEnabled_WritesAndReconciles(t *testing.T) {
	t.Parallel()
	agentRoot := t.TempDir()
	enabled := filepath.Join(agentRoot, "skills", "enabled")

	skills := []SkillContextForEnv{
		{
			Name:        "Demo Helper",
			Description: "helps with demos",
			Content:     "---\nname: demo-helper\ndescription: helps with demos\n---\n\n# Demo\n",
			Files: []SkillFileContextForEnv{
				{Path: "refs/note.md", Content: "note"},
			},
		},
		{
			Name:    "other-skill",
			Content: "# Other\n",
		},
	}
	if err := mirrorBoundSkillsToAgentEnabled(agentRoot, skills); err != nil {
		t.Fatalf("mirror: %v", err)
	}

	demoDir := filepath.Join(enabled, "demo-helper")
	if _, err := os.Stat(filepath.Join(demoDir, boundSkillMirrorMarker)); err != nil {
		t.Fatalf("missing mirror marker: %v", err)
	}
	if _, err := os.Stat(filepath.Join(demoDir, "SKILL.md")); err != nil {
		t.Fatalf("missing SKILL.md: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(demoDir, "refs", "note.md")); err != nil || string(got) != "note" {
		t.Fatalf("supporting file = %q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(enabled, "other-skill", "SKILL.md")); err != nil {
		t.Fatalf("missing other-skill: %v", err)
	}

	// Unbind other-skill → reconcile removes its mirror only.
	if err := mirrorBoundSkillsToAgentEnabled(agentRoot, skills[:1]); err != nil {
		t.Fatalf("remirror: %v", err)
	}
	if _, err := os.Stat(filepath.Join(enabled, "other-skill")); !os.IsNotExist(err) {
		t.Fatalf("expected other-skill mirror removed, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(demoDir, "SKILL.md")); err != nil {
		t.Fatalf("demo-helper should remain: %v", err)
	}
}

func TestMirrorBoundSkillsToAgentEnabled_PreservesUnmarkedUserDir(t *testing.T) {
	t.Parallel()
	agentRoot := t.TempDir()
	enabled := filepath.Join(agentRoot, "skills", "enabled")
	userDir := filepath.Join(enabled, "demo-helper")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	userSkill := []byte("# user owned\n")
	if err := os.WriteFile(filepath.Join(userDir, "SKILL.md"), userSkill, 0o644); err != nil {
		t.Fatal(err)
	}

	err := mirrorBoundSkillsToAgentEnabled(agentRoot, []SkillContextForEnv{{
		Name:    "demo-helper",
		Content: "# platform\n",
	}})
	if err != nil {
		t.Fatalf("mirror: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(userDir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(userSkill) {
		t.Fatalf("user SKILL.md overwritten: %q", got)
	}
	if _, err := os.Stat(filepath.Join(userDir, boundSkillMirrorMarker)); !os.IsNotExist(err) {
		t.Fatalf("should not stamp marker onto user dir, err=%v", err)
	}
}

func TestMirrorBoundSkillsToAgentEnabled_EmptyAgentRootNoop(t *testing.T) {
	t.Parallel()
	if err := mirrorBoundSkillsToAgentEnabled("", []SkillContextForEnv{{Name: "x", Content: "y"}}); err != nil {
		t.Fatalf("expected noop, got %v", err)
	}
}

func TestMirrorBoundSkillsToAgentEnabled_EmptySkillsClearsMirrors(t *testing.T) {
	t.Parallel()
	agentRoot := t.TempDir()
	if err := mirrorBoundSkillsToAgentEnabled(agentRoot, []SkillContextForEnv{{
		Name:    "temp",
		Content: "# temp\n",
	}}); err != nil {
		t.Fatal(err)
	}
	tempDir := filepath.Join(agentRoot, "skills", "enabled", "temp")
	if _, err := os.Stat(tempDir); err != nil {
		t.Fatal(err)
	}
	if err := mirrorBoundSkillsToAgentEnabled(agentRoot, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tempDir); !os.IsNotExist(err) {
		t.Fatalf("expected clear, err=%v", err)
	}
}
