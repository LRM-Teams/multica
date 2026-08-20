package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestPiGlobalCatalogIncludesRegisteredPackageWithoutBundleLimits(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", "")
	agentDir := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("mkdir agent dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"packages":["https://github.com/cathrynlavery/diagram-design"]}`), 0o644); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	skillDir := filepath.Join(agentDir, "git", "github.com", "cathrynlavery", "diagram-design", "skills", "diagram-design")
	if err := os.MkdirAll(filepath.Join(skillDir, "references", "deep", "beyond", "old", "depth", "limit"), 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	main := "---\nname: diagram-design\ndescription: Create architecture diagrams\n---\n# Diagram Design\n"
	if err := os.WriteFile(filepath.Join(skillDir, ContentFilename), []byte(main), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	// The real package has 160 supporting files. Use more than both that and
	// the removed 128-file ceiling to prove discovery mirrors Pi rather than
	// imposing an importer-specific bundle limit.
	for i := 0; i < 300; i++ {
		path := filepath.Join(skillDir, "references", fmt.Sprintf("file-%03d.md", i))
		if err := os.WriteFile(path, []byte("reference"), 0o644); err != nil {
			t.Fatalf("write supporting file %d: %v", i, err)
		}
	}
	deepPath := filepath.Join(skillDir, "references", "deep", "beyond", "old", "depth", "limit", "guide.md")
	if err := os.WriteFile(deepPath, []byte("deep reference"), 0o644); err != nil {
		t.Fatalf("write deep file: %v", err)
	}

	roots, err := piGlobalRoots(home)
	if err != nil {
		t.Fatalf("piGlobalRoots: %v", err)
	}
	catalog := NewLocalCatalog(roots...)
	items, err := catalog.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 || items[0].Key != "diagram-design" || items[0].FileCount != 302 {
		t.Fatalf("items = %#v", items)
	}
	bundle, err := catalog.Load("diagram-design")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(bundle.Files) != 301 {
		t.Fatalf("file count = %d, want 301", len(bundle.Files))
	}
}

func TestPiGlobalRootsIgnoreUnregisteredManagedCheckout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", "")
	stale := filepath.Join(home, ".pi", "agent", "git", "github.com", "owner", "stale", "skills", "stale")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatalf("mkdir stale package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stale, ContentFilename), []byte("# Stale\n"), 0o644); err != nil {
		t.Fatalf("write stale skill: %v", err)
	}

	roots, err := piGlobalRoots(home)
	if err != nil {
		t.Fatalf("piGlobalRoots: %v", err)
	}
	items, err := NewLocalCatalog(roots...).List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("items = %#v", items)
	}
}

func TestPiGlobalRootsResolveFileURLsAndLocalPackages(t *testing.T) {
	home := t.TempDir()
	agentDir := filepath.Join(home, "custom pi")
	t.Setenv("PI_CODING_AGENT_DIR", "file://"+agentDir)
	packageDir := filepath.Join(home, "packages", "local-skills")
	skillDir := filepath.Join(packageDir, "skills", "local-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, ContentFilename), []byte("# Local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := `{"packages":["file://` + packageDir + `"]}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}

	catalog, supported, err := GlobalCatalog("pi", home)
	if err != nil || !supported {
		t.Fatalf("GlobalCatalog: supported=%v err=%v", supported, err)
	}
	items, err := catalog.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Key != "local-skill" {
		t.Fatalf("items = %#v", items)
	}
}
