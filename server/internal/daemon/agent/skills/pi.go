package skills

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// piGlobalRoots mirrors Pi's global resource ownership. Workspace skills are
// a separate concern owned by execenv at <workingDirectory>/.pi/skills/.
func piGlobalRoots(home string) ([]string, error) {
	agentDir := piAgentDir(home)
	return piConfiguredRoots(agentDir, home, []string{
		filepath.Join(agentDir, "skills"),
		filepath.Join(home, ".agents", "skills"),
	})
}

func piWorkspaceRoots(workspaceRoot, home string) ([]string, error) {
	piDir := filepath.Join(workspaceRoot, ".pi")
	autoRoots := []string{filepath.Join(piDir, "skills")}
	autoRoots = append(autoRoots, piAncestorAgentSkillRoots(workspaceRoot)...)
	return piConfiguredRoots(piDir, home, autoRoots)
}

func piConfiguredRoots(scopeDir, home string, autoRoots []string) ([]string, error) {
	settingsRoots := make([]string, 0)
	packageRoots := make([]string, 0)

	data, err := os.ReadFile(filepath.Join(scopeDir, "settings.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return uniquePaths(autoRoots), nil
		}
		return nil, fmt.Errorf("read Pi settings: %w", err)
	}
	var settings struct {
		Skills   []string          `json:"skills"`
		Packages []json.RawMessage `json:"packages"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("parse Pi settings: %w", err)
	}

	for _, configured := range settings.Skills {
		if root, ok := piSkillRoot(scopeDir, home, configured); ok {
			settingsRoots = append(settingsRoots, root)
		}
	}
	for _, raw := range settings.Packages {
		setting, ok := parsePiPackageSetting(raw)
		if !ok || setting.skillsDisabled() {
			continue
		}
		packageRoot, ok := piPackageRoot(scopeDir, home, setting.Source)
		if !ok {
			continue
		}
		if setting.Skills != nil {
			for _, configured := range *setting.Skills {
				if root, ok := piSkillRoot(packageRoot, home, configured); ok {
					packageRoots = append(packageRoots, root)
				}
			}
			continue
		}
		packageRoots = append(packageRoots, piPackageSkillRoots(packageRoot)...)
	}
	roots := append(settingsRoots, autoRoots...)
	roots = append(roots, packageRoots...)
	return uniquePaths(roots), nil
}

func piAncestorAgentSkillRoots(workspaceRoot string) []string {
	current, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil
	}
	gitRoot := nearestGitRoot(current)
	var roots []string
	for {
		roots = append(roots, filepath.Join(current, ".agents", "skills"))
		if current == gitRoot {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return roots
}

func nearestGitRoot(start string) string {
	current := start
	for {
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return current
		}
		current = parent
	}
}

func piAgentDir(home string) string {
	configured := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR"))
	if configured == "" {
		return filepath.Join(home, ".pi", "agent")
	}
	return resolvePiPath(configured, home, "")
}

type piPackageSetting struct {
	Source   string    `json:"source"`
	Autoload *bool     `json:"autoload"`
	Skills   *[]string `json:"skills"`
}

func parsePiPackageSetting(raw json.RawMessage) (piPackageSetting, bool) {
	var source string
	if json.Unmarshal(raw, &source) == nil {
		return piPackageSetting{Source: source}, source != ""
	}
	var setting piPackageSetting
	if json.Unmarshal(raw, &setting) != nil {
		return piPackageSetting{}, false
	}
	return setting, setting.Source != ""
}

func (s piPackageSetting) skillsDisabled() bool {
	return (s.Autoload != nil && !*s.Autoload && s.Skills == nil) || (s.Skills != nil && len(*s.Skills) == 0)
}

func piSkillRoot(baseDir, home, configured string) (string, bool) {
	configured = strings.TrimSpace(configured)
	if configured == "" || strings.HasPrefix(configured, "!") || strings.HasPrefix(configured, "-") || strings.ContainsAny(configured, "*?[") {
		return "", false
	}
	configured = resolvePiPath(strings.TrimPrefix(configured, "+"), home, baseDir)
	info, err := os.Stat(configured)
	if err != nil {
		return "", false
	}
	if !info.IsDir() {
		if strings.EqualFold(filepath.Base(configured), "SKILL.md") {
			return filepath.Dir(filepath.Dir(configured)), true
		}
		return "", false
	}
	if _, err := os.Stat(filepath.Join(configured, "SKILL.md")); err == nil {
		return filepath.Dir(configured), true
	}
	return configured, true
}

func piPackageSkillRoots(packageRoot string) []string {
	data, err := os.ReadFile(filepath.Join(packageRoot, "package.json"))
	if err != nil {
		return []string{filepath.Join(packageRoot, "skills")}
	}
	var manifest struct {
		Pi *struct {
			Skills []string `json:"skills"`
		} `json:"pi"`
	}
	if json.Unmarshal(data, &manifest) != nil || manifest.Pi == nil {
		return []string{filepath.Join(packageRoot, "skills")}
	}
	var roots []string
	for _, configured := range manifest.Pi.Skills {
		if root, ok := piSkillRoot(packageRoot, "", configured); ok {
			roots = append(roots, root)
		}
	}
	return roots
}

func piPackageRoot(agentDir, home, source string) (string, bool) {
	source = strings.TrimSpace(source)
	if strings.HasPrefix(source, "npm:") {
		name := strings.TrimPrefix(source, "npm:")
		if at := strings.LastIndex(name, "@"); at > strings.LastIndex(name, "/") {
			name = name[:at]
		}
		return filepath.Join(agentDir, "npm", "node_modules", filepath.FromSlash(name)), name != ""
	}
	if !isPiGitSource(source) {
		return resolvePiPath(source, home, agentDir), source != ""
	}

	repository := strings.TrimPrefix(source, "git:")
	if lastAt := strings.LastIndex(repository, "@"); lastAt > strings.LastIndex(repository, "/") {
		repository = repository[:lastAt]
	}
	host, path, ok := piGitRepositoryParts(repository)
	if !ok {
		return "", false
	}
	return filepath.Join(agentDir, "git", host, filepath.FromSlash(path)), true
}

func isPiGitSource(source string) bool {
	if strings.HasPrefix(source, "git:") {
		return true
	}
	if at := strings.Index(source, "@"); at >= 0 && strings.Contains(source[at+1:], ":") {
		return true
	}
	parsed, err := url.Parse(source)
	if err != nil || parsed.Hostname() == "" {
		return false
	}
	switch parsed.Scheme {
	case "http", "https", "ssh", "git":
		return true
	default:
		return false
	}
}

func piGitRepositoryParts(repository string) (string, string, bool) {
	if parsed, err := url.Parse(repository); err == nil && parsed.Hostname() != "" {
		path := strings.TrimSuffix(strings.Trim(parsed.Path, "/"), ".git")
		return parsed.Hostname(), path, path != ""
	}
	if at := strings.Index(repository, "@"); at >= 0 {
		if relativeColon := strings.Index(repository[at+1:], ":"); relativeColon >= 0 {
			colon := at + 1 + relativeColon
			host := repository[at+1 : colon]
			path := strings.TrimSuffix(strings.Trim(repository[colon+1:], "/"), ".git")
			return host, path, host != "" && path != ""
		}
	}
	parts := strings.SplitN(strings.Trim(repository, "/"), "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], strings.TrimSuffix(parts[1], ".git"), true
}

func resolvePiPath(path, home, relativeTo string) string {
	if parsed, err := url.Parse(path); err == nil && parsed.Scheme == "file" {
		path = parsed.Path
		if unescaped, err := url.PathUnescape(path); err == nil {
			path = unescaped
		}
	}
	if path == "~" {
		path = home
	}
	if strings.HasPrefix(path, "~"+string(filepath.Separator)) {
		path = filepath.Join(home, strings.TrimPrefix(path, "~"+string(filepath.Separator)))
	}
	if relativeTo != "" && !filepath.IsAbs(path) {
		path = filepath.Join(relativeTo, path)
	}
	return filepath.Clean(path)
}

func uniquePaths(paths []string) []string {
	result := make([]string, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		path = filepath.Clean(path)
		if path == "." || seen[path] {
			continue
		}
		seen[path] = true
		result = append(result, path)
	}
	return result
}
