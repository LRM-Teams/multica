package agentworkspace

import "testing"

func TestIsNeverVisibleHiddenEntry(t *testing.T) {
	t.Parallel()
	hidden := []string{".aws", ".gnupg", ".ssh", ".multica", ".multica-runtime", ".multica-bound-mirror", ".multica-managed-skill"}
	for _, name := range hidden {
		if !IsNeverVisibleHiddenEntry(name) {
			t.Fatalf("%q should be never-visible", name)
		}
	}
	visible := []string{".gitignore", ".env", ".git", "node_modules", "memory", "multica-review"}
	for _, name := range visible {
		if IsNeverVisibleHiddenEntry(name) {
			t.Fatalf("%q should not be never-visible", name)
		}
	}
}

func TestIsHiddenPath(t *testing.T) {
	t.Parallel()
	if !IsHiddenPath(".gitignore") || !IsHiddenPath("notes/.private.md") || !IsHiddenPath(".ssh/id_rsa") {
		t.Fatal("expected hidden paths")
	}
	if IsHiddenPath("AGENTS.md") || IsHiddenPath("memory/MEMORY.md") || IsHiddenPath("") || IsHiddenPath(".") {
		t.Fatal("expected visible paths")
	}
}

func TestIsSecretFilePath(t *testing.T) {
	t.Parallel()
	secrets := []string{
		".env",
		".env.local",
		"api-token.json",
		"my-secret.md",
		"db-credentials.yaml",
		"tokens",
		"memory/openai-token.txt",
	}
	for _, path := range secrets {
		if !IsSecretFilePath(path) {
			t.Fatalf("%q should match a secret pattern", path)
		}
	}
	plain := []string{".gitignore", "AGENTS.md", "memory/MEMORY.md", "notatoken.txt", ".environment"}
	for _, path := range plain {
		if IsSecretFilePath(path) {
			t.Fatalf("%q should not match a secret pattern", path)
		}
	}
}

func TestPreviewDeniedReason(t *testing.T) {
	t.Parallel()
	denied := []string{".env", ".ssh/id_rsa", ".multica-runtime/x", "api-token.json", "my-secret.md"}
	for _, path := range denied {
		if PreviewDeniedReason(path) == "" {
			t.Fatalf("preview of %q should be denied", path)
		}
	}
	if PreviewDeniedReason("AGENTS.md") != "" || PreviewDeniedReason(".gitignore") != "" {
		t.Fatal("ordinary and hidden-but-allowed files should remain readable")
	}
}

func TestListDirDenied(t *testing.T) {
	t.Parallel()
	if !ListDirDenied(".ssh", true) {
		t.Fatal("listing .ssh must be denied with hidden on")
	}
	if !ListDirDenied(".multica-runtime", false) || !ListDirDenied(".multica-runtime", true) {
		t.Fatal("listing .multica-runtime must be denied")
	}
	if !ListDirDenied(".gitignore", false) {
		t.Fatal(".gitignore is hidden when includeHidden is off")
	}
	if ListDirDenied(".gitignore", true) {
		t.Fatal(".gitignore may be listed when includeHidden is on")
	}
	if ListDirDenied("", false) || ListDirDenied("memory", false) {
		t.Fatal("root and ordinary dirs are listable")
	}
}
