package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeAgentIDsDedupesAndPreservesOrder(t *testing.T) {
	t.Parallel()

	got := mergeAgentIDs([]string{"agent-b", "agent-a"}, []string{"agent-a", "agent-c"})
	want := []string{"agent-b", "agent-a", "agent-c"}
	if len(got) != len(want) {
		t.Fatalf("mergeAgentIDs = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mergeAgentIDs = %#v, want %#v", got, want)
		}
	}
}

func TestEvolutionDeliveryPathMissing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	missingDir := filepath.Join(root, "missing")
	if evolutionDeliveryPathMissing(missingDir) != true {
		t.Fatal("expected missing directory to need repair")
	}

	emptyDir := filepath.Join(root, "empty")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatalf("mkdir empty dir: %v", err)
	}
	if evolutionDeliveryPathMissing(emptyDir) != true {
		t.Fatal("expected directory without SKILL.md to need repair")
	}

	skillDir := filepath.Join(root, "skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# skill\n"), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	if evolutionDeliveryPathMissing(skillDir) {
		t.Fatal("expected existing skill bundle not to need repair")
	}

	if evolutionDeliveryPathMissing("") {
		t.Fatal("empty path should not trigger repair")
	}
}

func TestEnableGeneratedSkillDeliveryCopiesBundleAndMarksEnabled(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	agentRoot := piAgentRoot(Config{WorkspacesRoot: root}, "workspace-1", "agent-1")
	if err := ensurePiAgentRoot(agentRoot); err != nil {
		t.Fatalf("ensurePiAgentRoot: %v", err)
	}

	d := &Daemon{cfg: Config{WorkspacesRoot: root}}
	delivery := EvolutionDelivery{
		ID:           "delivery-1",
		UnitID:       "unit-1",
		VersionID:    "version-1",
		DeliveryType: "generated",
		UnitType:     "skill",
		Files: []SkillFileData{
			{Path: "SKILL.md", Content: "---\nname: Enabled Skill\n---\n# Enabled Skill\n"},
			{Path: "references/notes.md", Content: "helper notes"},
		},
	}

	generatedDir, err := writeGeneratedSkillDelivery(agentRoot, delivery)
	if err != nil {
		t.Fatalf("writeGeneratedSkillDelivery: %v", err)
	}
	enabledDir, err := d.enableGeneratedSkillDelivery("workspace-1", "agent-1", delivery, generatedDir)
	if err != nil {
		t.Fatalf("enableGeneratedSkillDelivery: %v", err)
	}
	if enabledDir != filepath.Join(agentRoot, "skills", "enabled", safePathName(delivery.UnitID)) {
		t.Fatalf("enabled dir = %q", enabledDir)
	}

	got, err := os.ReadFile(filepath.Join(enabledDir, "SKILL.md"))
	if err != nil {
		t.Fatalf("read enabled SKILL.md: %v", err)
	}
	if !strings.Contains(string(got), "Enabled Skill") {
		t.Fatalf("enabled SKILL.md missing content: %s", string(got))
	}

	got, err = os.ReadFile(filepath.Join(enabledDir, "references", "notes.md"))
	if err != nil {
		t.Fatalf("read enabled supporting file: %v", err)
	}
	if string(got) != "helper notes" {
		t.Fatalf("supporting file = %q", string(got))
	}

	var generatedMeta map[string]any
	generatedRaw, err := os.ReadFile(filepath.Join(generatedDir, ".multica-delivery.json"))
	if err != nil {
		t.Fatalf("read generated metadata: %v", err)
	}
	if err := json.Unmarshal(generatedRaw, &generatedMeta); err != nil {
		t.Fatalf("decode generated metadata: %v", err)
	}
	if enabled, _ := generatedMeta["enabled"].(bool); enabled {
		t.Fatal("generated delivery metadata should remain disabled")
	}

	var enabledMeta map[string]any
	enabledRaw, err := os.ReadFile(filepath.Join(enabledDir, ".multica-delivery.json"))
	if err != nil {
		t.Fatalf("read enabled metadata: %v", err)
	}
	if err := json.Unmarshal(enabledRaw, &enabledMeta); err != nil {
		t.Fatalf("decode enabled metadata: %v", err)
	}
	if enabled, _ := enabledMeta["enabled"].(bool); !enabled {
		t.Fatal("enabled delivery metadata should be enabled")
	}
}

func TestEvolutionDeliveryRejectsWindowsUnsafeRelativePaths(t *testing.T) {
	t.Parallel()

	unsafe := []string{
		`..\\evil.md`,
		`C:\\temp\\skill.md`,
		`nested\\skill.md`,
		`C:/temp/skill.md`,
		`nested/../evil.md`,
	}
	for _, path := range unsafe {
		if isSafeRelativePath(path) {
			t.Fatalf("path %q should be rejected", path)
		}
	}
	for _, path := range []string{"SKILL.md", "references/notes.md"} {
		if !isSafeRelativePath(path) {
			t.Fatalf("path %q should be accepted", path)
		}
	}
}

func TestNormalizeLocalSkillKeyRejectsWindowsUnsafePaths(t *testing.T) {
	t.Parallel()

	unsafe := []string{`..\\evil`, `C:\\skills\\demo`, `demo\\nested`, `C:/skills/demo`, `demo/../evil`}
	for _, key := range unsafe {
		if got, err := normalizeLocalSkillKey(key); err == nil {
			t.Fatalf("key %q normalized to %q; expected rejection", key, got)
		}
	}
	if got, err := normalizeLocalSkillKey("release/reporter"); err != nil || got != "release/reporter" {
		t.Fatalf("valid nested key normalized to %q err=%v", got, err)
	}
}

func TestLoadEnabledPiSkillsReadsEnabledBundles(t *testing.T) {
	t.Parallel()

	agentRoot := filepath.Join(t.TempDir(), "workspace", ".pi", "agents", "agent-1")
	enabledDir := filepath.Join(agentRoot, "skills", "enabled", "review-helper")
	if err := os.MkdirAll(enabledDir, 0o755); err != nil {
		t.Fatalf("mkdir enabled skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(enabledDir, "SKILL.md"), []byte("---\nname: Review Helper\ndescription: Check pull requests\n---\n# Review Helper\n"), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(enabledDir, "templates"), 0o755); err != nil {
		t.Fatalf("mkdir templates: %v", err)
	}
	if err := os.WriteFile(filepath.Join(enabledDir, "templates", "checklist.md"), []byte("- item\n"), 0o644); err != nil {
		t.Fatalf("write supporting file: %v", err)
	}

	skills, err := loadEnabledPiSkills(agentRoot)
	if err != nil {
		t.Fatalf("loadEnabledPiSkills: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	got := skills[0]
	if got.Name != "Review Helper" {
		t.Fatalf("name = %q", got.Name)
	}
	if got.Description != "Check pull requests" {
		t.Fatalf("description = %q", got.Description)
	}
	if !strings.Contains(got.Content, "# Review Helper") {
		t.Fatalf("content = %q", got.Content)
	}
	if len(got.Files) != 1 || got.Files[0].Path != "templates/checklist.md" {
		t.Fatalf("files = %#v", got.Files)
	}
	if got.Files[0].Content != "- item\n" {
		t.Fatalf("supporting file content = %q", got.Files[0].Content)
	}
}

func TestMergeSkillsForEnvKeepsPrimarySkillsFirstAndDedupes(t *testing.T) {
	t.Parallel()

	merged := mergeSkillsForEnv(
		[]SkillData{{Name: "Review", Content: "primary"}, {Name: "Docs", Content: "doc"}},
		[]SkillData{{Name: "review", Content: "secondary"}, {Name: "Test", Content: "new"}},
	)
	if len(merged) != 3 {
		t.Fatalf("expected 3 skills, got %d", len(merged))
	}
	if merged[0].Content != "primary" || merged[1].Content != "doc" || merged[2].Content != "new" {
		t.Fatalf("unexpected order/content: %#v", merged)
	}
}
