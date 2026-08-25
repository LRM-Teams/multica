package skills

import (
	"os"
	"path/filepath"
	"strings"

	agentpkg "github.com/multica-ai/multica/server/pkg/agent"
)

// GlobalCatalog returns the user-level Agent Skills visible to a provider.
// Workspace skills are owned by execenv and intentionally excluded here.
func GlobalCatalog(provider, home string) (LocalCatalog, bool, error) {
	var roots []string
	switch provider {
	case agentpkg.ProviderClaude:
		roots = []string{filepath.Join(home, ".claude", "skills")}
	case agentpkg.ProviderCodex:
		codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
		if codexHome == "" {
			codexHome = filepath.Join(home, ".codex")
		}
		roots = []string{
			filepath.Join(codexHome, "skills"),
			filepath.Join(codexHome, "skills", ".system"),
			filepath.Join(home, ".agents", "skills"),
		}
	case agentpkg.ProviderOpenCode:
		roots = []string{filepath.Join(home, ".config", agentpkg.ProviderOpenCode, "skills")}
	case agentpkg.ProviderPi:
		var err error
		roots, err = piGlobalRoots(home)
		if err != nil {
			return LocalCatalog{}, true, err
		}
	case agentpkg.ProviderCursor:
		roots = []string{filepath.Join(home, ".cursor", "skills")}
	case agentpkg.ProviderKiro:
		roots = []string{filepath.Join(home, ".kiro", "skills")}
	case agentpkg.ProviderGrok:
		roots = []string{filepath.Join(home, ".grok", "skills")}
	default:
		return LocalCatalog{}, false, nil
	}
	return NewLocalCatalog(roots...), true, nil
}

// WorkspaceCatalog returns every project-level Agent Skills root discovered
// by a provider. The first root is also where Multica materializes assigned
// skills; later roots are read-only compatibility/discovery locations.
func WorkspaceCatalog(provider, workspaceRoot string) (LocalCatalog, error) {
	roots := workspaceRoots(provider, workspaceRoot)
	if provider == agentpkg.ProviderPi {
		home, err := os.UserHomeDir()
		if err != nil {
			return LocalCatalog{}, err
		}
		roots, err = piWorkspaceRoots(workspaceRoot, home)
		if err != nil {
			return LocalCatalog{}, err
		}
	}
	return NewLocalCatalog(roots...), nil
}

// PrimaryWorkspaceRoot returns the provider-native directory where Multica
// should materialize assigned skills.
func PrimaryWorkspaceRoot(provider, workspaceRoot string) string {
	return workspaceRoots(provider, workspaceRoot)[0]
}

func workspaceRoots(provider, root string) []string {
	switch provider {
	case agentpkg.ProviderClaude:
		return []string{filepath.Join(root, ".claude", "skills")}
	case agentpkg.ProviderCodex:
		return []string{filepath.Join(root, ".agents", "skills")}
	case agentpkg.ProviderOpenCode:
		return []string{filepath.Join(root, ".opencode", "skills")}
	case agentpkg.ProviderPi:
		return []string{filepath.Join(root, ".pi", "skills")}
	case agentpkg.ProviderCursor:
		return []string{filepath.Join(root, ".cursor", "skills")}
	case agentpkg.ProviderKiro:
		return []string{filepath.Join(root, ".kiro", "skills")}
	case agentpkg.ProviderGrok:
		return []string{filepath.Join(root, ".grok", "skills")}
	default:
		return []string{filepath.Join(root, ".agent_context", "skills")}
	}
}
