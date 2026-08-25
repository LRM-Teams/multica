package skills

import (
	"os"
	"path/filepath"
	"testing"

	agentpkg "github.com/multica-ai/multica/server/pkg/agent"
)

func TestPiWorkspaceCatalogIncludesPiAndAgentsRoots(t *testing.T) {
	repository := t.TempDir()
	root := filepath.Join(repository, "nested", "workspace")
	if err := os.MkdirAll(filepath.Join(repository, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeWorkspaceSkill := func(parent, name string) {
		t.Helper()
		dir := filepath.Join(root, parent, "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ContentFilename), []byte("# "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeWorkspaceSkill(".pi", "pi-native")
	writeWorkspaceSkill(".agents", "agent-standard")
	ancestorSkill := filepath.Join(repository, ".agents", "skills", "repository-standard")
	if err := os.MkdirAll(ancestorSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ancestorSkill, ContentFilename), []byte("# Repository\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	catalog, err := WorkspaceCatalog(agentpkg.ProviderPi, root)
	if err != nil {
		t.Fatal(err)
	}
	items, err := catalog.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || items[0].Key != "agent-standard" || items[1].Key != "pi-native" || items[2].Key != "repository-standard" {
		t.Fatalf("items = %#v", items)
	}
	if got := PrimaryWorkspaceRoot(agentpkg.ProviderPi, root); got != filepath.Join(root, ".pi", "skills") {
		t.Fatalf("primary root = %q", got)
	}
}

func TestPiWorkspaceCatalogIncludesProjectSettingsPackage(t *testing.T) {
	root := t.TempDir()
	piDir := filepath.Join(root, ".pi")
	if err := os.MkdirAll(piDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(piDir, "settings.json"), []byte(`{"packages":["git:https://github.com/acme/project-skills"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(piDir, "git", "github.com", "acme", "project-skills", "skills", "project-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, ContentFilename), []byte("# Project Skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	catalog, err := WorkspaceCatalog(agentpkg.ProviderPi, root)
	if err != nil {
		t.Fatal(err)
	}
	items, err := catalog.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Key != "project-skill" {
		t.Fatalf("items = %#v", items)
	}
}
