package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/agentworkspace"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/skill"
	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	maxLocalSkillFileSize   int64 = 1 << 20
	maxLocalSkillBundleSize int64 = 8 << 20
	maxLocalSkillFileCount        = 128
	// Cap how deep skill discovery descends below a runtime root. opencode
	// stores skills two levels deep (e.g. `release/reporter/SKILL.md`); a
	// few extra levels covers any realistic future layout while bounding
	// work in case an installer accidentally points us at $HOME.
	maxLocalSkillDirDepth = 4
)

type runtimeLocalSkillSummary struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	SourcePath  string `json:"source_path"`
	Provider    string `json:"provider"`
	FileCount   int    `json:"file_count"`
}

func (d *Daemon) handleAgentSkillsList(req protocol.AgentSkillsListPayload, writes chan<- []byte) {
	resp := protocol.AgentSkillsListResultPayload{AgentID: req.AgentID, RequestID: req.RequestID, Global: []protocol.AgentSkillSummary{}, Workspace: []protocol.AgentSkillSummary{}}
	d.mu.Lock()
	runtime, runtimeOK := d.runtimeIndex[req.Runtime]
	d.mu.Unlock()
	if !runtimeOK {
		d.sendDaemonFrame(protocol.EventAgentSkillsListResult, resp, req.RequestID, writes)
		return
	}
	global, _, err := listRuntimeLocalSkills(runtime.Provider)
	if err != nil {
		d.sendDaemonFrame(protocol.EventAgentSkillsListResult, resp, req.RequestID, writes)
		return
	}
	for _, item := range global {
		resp.Global = append(resp.Global, protocol.AgentSkillSummary{
			Name: item.Name, Description: item.Description, Path: item.SourcePath, Source: "global",
		})
	}
	if req.AgentID != "" {
		root := agentworkspace.Root(d.cfg.WorkspacesRoot, runtime.WorkspaceID, req.AgentID)
		workspaceSkills, _, listErr := listLocalSkillsFromRoot(runtime.Provider, execenv.SkillsDirPath(root, runtime.Provider))
		if listErr != nil {
			d.logger.Debug("workspace skill discovery failed", "agent_id", req.AgentID, "error", listErr)
		} else {
			for _, item := range workspaceSkills {
				resp.Workspace = append(resp.Workspace, protocol.AgentSkillSummary{
					Name: item.Name, Description: item.Description, Path: item.SourcePath, Source: "workspace",
				})
			}
		}
	}
	d.sendDaemonFrame(protocol.EventAgentSkillsListResult, resp, req.RequestID, writes)
}

type runtimeLocalSkillBundle struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Content     string          `json:"content"`
	SourcePath  string          `json:"source_path"`
	Provider    string          `json:"provider"`
	Files       []SkillFileData `json:"files,omitempty"`
}

// localSkillRootForProvider tracks the user-level skill locations exposed by
// each runtime/provider. Keep these in sync with upstream docs / conventions:
//   - OpenCode: https://opencode.ai/docs/skills
//   - Pi: https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/skills.md
//   - Cursor: official forum guidance referencing the built-in /create-skill flow
//     (https://forum.cursor.com/t/cursor-doesnt-know-new-skills-arens-saved/158507)
//   - Kiro: project and user-level .kiro/skills directories discovered by Kiro CLI
//   - Grok: ~/.grok/skills user-level skill root
//
// Longer-term this mapping would be better colocated with the provider
// definitions under server/pkg/agent so adding a new runtime can't silently
// miss the local-skills surface.
func localSkillRootForProvider(provider string) (string, bool, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", false, fmt.Errorf("resolve user home: %w", err)
	}

	switch provider {
	case agent.ProviderClaude:
		return filepath.Join(home, ".claude", "skills"), true, nil
	case agent.ProviderCodex:
		codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
		if codexHome == "" {
			codexHome = filepath.Join(home, ".codex")
		}
		return filepath.Join(codexHome, "skills"), true, nil
	case agent.ProviderOpenCode:
		return filepath.Join(home, ".config", agent.ProviderOpenCode, "skills"), true, nil
	case agent.ProviderPi:
		return filepath.Join(home, ".pi", "skills"), true, nil
	case agent.ProviderCursor:
		return filepath.Join(home, ".cursor", "skills"), true, nil
	case agent.ProviderKiro:
		return filepath.Join(home, ".kiro", "skills"), true, nil
	case agent.ProviderGrok:
		return filepath.Join(home, ".grok", "skills"), true, nil
	default:
		return "", false, nil
	}
}

func isIgnoredLocalSkillEntry(name string) bool {
	if name == "" {
		return true
	}
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch strings.ToLower(name) {
	case "license", "license.md", "license.txt":
		return true
	default:
		return false
	}
}

func normalizeLocalSkillKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("skill key is required")
	}
	if filepath.IsAbs(key) || strings.HasPrefix(key, "/") || strings.ContainsAny(key, "\\:") {
		return "", fmt.Errorf("invalid skill key")
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == ".." {
			return "", fmt.Errorf("invalid skill key")
		}
	}
	cleaned := filepath.Clean(filepath.FromSlash(key))
	if cleaned == "." {
		return "", fmt.Errorf("invalid skill key")
	}
	return filepath.ToSlash(cleaned), nil
}

func relativizeHomePath(path string) string {
	home, err := userHomeDir()
	if err != nil {
		return filepath.ToSlash(path)
	}
	if path == home {
		return "~"
	}
	prefix := home + string(filepath.Separator)
	if strings.HasPrefix(path, prefix) {
		return filepath.ToSlash("~" + string(filepath.Separator) + strings.TrimPrefix(path, prefix))
	}
	if rel, err := filepath.Rel(home, path); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !strings.HasPrefix(rel, "../") {
		return "~/" + filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}

func readLocalSkillMainFile(skillDir string) (string, error) {
	mainPath := filepath.Join(skillDir, "SKILL.md")
	info, err := os.Stat(mainPath)
	if err != nil {
		return "", err
	}
	if info.Size() > maxLocalSkillFileSize {
		return "", fmt.Errorf("SKILL.md exceeds %d bytes", maxLocalSkillFileSize)
	}
	content, err := os.ReadFile(mainPath)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

func collectLocalSkillFiles(skillDir string, includeContent bool) ([]SkillFileData, error) {
	files := make([]SkillFileData, 0)
	var totalSize int64

	// filepath.WalkDir does not follow a symlinked root, so when the runtime
	// root contains symlinks into a shared skill installer (e.g. lark-cli's
	// ~/.agents/skills/<name>) walking from the symlink path enumerates zero
	// children and every such skill ends up reporting 0 files. Resolve the
	// real path first so the walk descends into the actual directory.
	walkRoot := skillDir
	if resolved, err := filepath.EvalSymlinks(skillDir); err == nil {
		walkRoot = resolved
	}

	err := filepath.WalkDir(walkRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if path == walkRoot {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if isIgnoredLocalSkillEntry(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if isIgnoredLocalSkillEntry(entry.Name()) || strings.EqualFold(entry.Name(), "SKILL.md") {
			return nil
		}

		rel, err := filepath.Rel(walkRoot, path)
		if err != nil {
			return nil
		}
		rel = filepath.Clean(rel)
		if rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil
		}

		info, err := entry.Info()
		if err != nil || info.Size() > maxLocalSkillFileSize {
			return nil
		}
		if len(files) >= maxLocalSkillFileCount {
			return fmt.Errorf("local skill exceeds %d files", maxLocalSkillFileCount)
		}
		totalSize += info.Size()
		if totalSize > maxLocalSkillBundleSize {
			return fmt.Errorf("local skill exceeds %d bytes in total", maxLocalSkillBundleSize)
		}

		file := SkillFileData{Path: filepath.ToSlash(rel)}
		if includeContent {
			content, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			file.Content = string(content)
		}
		files = append(files, file)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})
	return files, nil
}

// localSkillScanFingerprint hashes file paths with size+mtime so sync can skip
// re-reading unchanged skill bundles between polls.
func localSkillScanFingerprint(skillDir string) (string, error) {
	walkRoot := skillDir
	if resolved, err := filepath.EvalSymlinks(skillDir); err == nil {
		walkRoot = resolved
	}
	h := sha256.New()
	err := filepath.WalkDir(walkRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if path != walkRoot && isIgnoredLocalSkillEntry(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if isIgnoredLocalSkillEntry(entry.Name()) {
			return nil
		}
		rel, err := filepath.Rel(walkRoot, path)
		if err != nil {
			return nil
		}
		rel = filepath.Clean(rel)
		if rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(info.ModTime().UTC().Format(time.RFC3339Nano)))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(fmt.Sprintf("%d", info.Size())))
		_, _ = h.Write([]byte{0})
		return nil
	})
	if err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func listRuntimeLocalSkills(provider string) ([]runtimeLocalSkillSummary, bool, error) {
	root, supported, err := localSkillRootForProvider(provider)
	if err != nil || !supported {
		return nil, supported, err
	}
	roots := []string{root}
	if provider == agent.ProviderCodex {
		home, homeErr := userHomeDir()
		if homeErr != nil {
			return nil, false, fmt.Errorf("resolve user home: %w", homeErr)
		}
		codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
		if codexHome == "" {
			codexHome = filepath.Join(home, ".codex")
		}
		// Raft's Codex discovery keeps CODEX_HOME and the shared user-level
		// .agents directory separate, while still exposing both inventories.
		roots = []string{
			filepath.Join(codexHome, "skills"),
			filepath.Join(codexHome, "skills", ".system"),
			filepath.Join(codexHome, ".agents", "skills"),
		}
		if strings.TrimSpace(os.Getenv("CODEX_HOME")) == "" {
			roots = append(roots, filepath.Join(home, ".agents", "skills"))
		}
	}

	var all []runtimeLocalSkillSummary
	seen := make(map[string]bool)
	for _, candidate := range roots {
		skills, _, listErr := listLocalSkillsFromRoot(provider, candidate)
		if listErr != nil {
			return nil, supported, listErr
		}
		for _, skill := range skills {
			if seen[skill.Key] {
				continue
			}
			seen[skill.Key] = true
			all = append(all, skill)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Key < all[j].Key })
	return all, supported, nil
}

func listLocalSkillsFromRoot(provider, root string) ([]runtimeLocalSkillSummary, bool, error) {
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return []runtimeLocalSkillSummary{}, true, nil
		}
		return nil, true, err
	}

	// Walk the runtime root with two extensions over filepath.WalkDir:
	//   - Follow symlinks at every level. Installers like lark-cli ship
	//     each skill as a symlink into a shared ~/.agents/skills/<name>;
	//     the previous WalkDir path silently dropped them via the
	//     os.ModeSymlink early return.
	//   - Allow nested layouts. opencode stores skills as
	//     `release/reporter/SKILL.md`, and `loadRuntimeLocalSkillBundle`
	//     already accepts slash-delimited keys, so the list endpoint
	//     must surface those nested skills too.
	skills := make([]runtimeLocalSkillSummary, 0)
	visited := make(map[string]bool)
	enumerateLocalSkills(provider, root, root, 0, visited, &skills)

	sort.Slice(skills, func(i, j int) bool {
		return skills[i].Key < skills[j].Key
	})
	return skills, true, nil
}

// enumerateLocalSkills walks `currentDir` looking for skill directories
// (directories that contain a SKILL.md). When one is found it is registered
// at a key relative to `walkRoot` and the recursion stops at that branch —
// we never descend into a directory that already qualifies as a skill, even
// if it happens to contain nested SKILL.md files of its own.
//
// `visited` keys on the resolved (symlink-followed) absolute path so a
// cyclic symlink can't loop forever; this is the only reason we eagerly
// EvalSymlinks up front. Errors from EvalSymlinks just stop the descent on
// that branch — most often it's a dangling link, which we want to ignore.
func enumerateLocalSkills(
	provider, walkRoot, currentDir string,
	depth int,
	visited map[string]bool,
	skills *[]runtimeLocalSkillSummary,
) {
	if depth > maxLocalSkillDirDepth {
		return
	}
	resolved, err := filepath.EvalSymlinks(currentDir)
	if err != nil {
		return
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return
	}
	if visited[resolved] {
		return
	}
	visited[resolved] = true

	entries, err := os.ReadDir(currentDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		name := entry.Name()
		if isIgnoredLocalSkillEntry(name) {
			continue
		}
		path := filepath.Join(currentDir, name)
		path, err := filepath.Abs(path)
		if err != nil {
			continue
		}
		info, statErr := os.Stat(path) // follows symlinks
		if statErr != nil || !info.IsDir() {
			continue
		}

		mainPath := filepath.Join(path, "SKILL.md")
		if _, err := os.Stat(mainPath); err == nil {
			rel, err := filepath.Rel(walkRoot, path)
			if err != nil {
				continue
			}
			key, err := normalizeLocalSkillKey(filepath.ToSlash(rel))
			if err != nil {
				continue
			}

			content, err := readLocalSkillMainFile(path)
			if err != nil {
				continue
			}
			skillName, description := skill.ParseSkillFrontmatter(content)
			if skillName == "" {
				skillName = filepath.Base(path)
			}

			files, err := collectLocalSkillFiles(path, false)
			if err != nil {
				continue
			}

			*skills = append(*skills, runtimeLocalSkillSummary{
				Key:         key,
				Name:        skillName,
				Description: description,
				SourcePath:  relativizeHomePath(path),
				Provider:    provider,
				// `files` is the supporting bundle (collectLocalSkillFiles
				// intentionally excludes SKILL.md so the bundle's `Content`
				// field can carry it without duplication on import). For the
				// list summary the user expects the total file count, so add
				// one back for SKILL.md itself.
				FileCount: len(files) + 1,
			})
			continue
		}

		// No SKILL.md here — descend looking for nested skills.
		enumerateLocalSkills(provider, walkRoot, path, depth+1, visited, skills)
	}
}

func loadRuntimeLocalSkillBundle(provider, skillKey string) (*runtimeLocalSkillBundle, bool, error) {
	root, supported, err := localSkillRootForProvider(provider)
	if err != nil || !supported {
		return nil, supported, err
	}
	return loadLocalSkillBundleFromRoot(provider, root, skillKey)
}

func loadLocalSkillBundleFromRoot(provider, root, skillKey string) (*runtimeLocalSkillBundle, bool, error) {
	key, err := normalizeLocalSkillKey(skillKey)
	if err != nil {
		return nil, true, err
	}

	skillDir := filepath.Join(root, filepath.FromSlash(key))
	info, err := os.Stat(skillDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, true, fmt.Errorf("local skill not found")
		}
		return nil, true, err
	}
	if !info.IsDir() {
		return nil, true, fmt.Errorf("local skill is not a directory")
	}

	content, err := readLocalSkillMainFile(skillDir)
	if err != nil {
		return nil, true, err
	}
	name, description := skill.ParseSkillFrontmatter(content)
	if name == "" {
		name = filepath.Base(skillDir)
	}

	files, err := collectLocalSkillFiles(skillDir, true)
	if err != nil {
		return nil, true, err
	}

	return &runtimeLocalSkillBundle{
		Name:        name,
		Description: description,
		Content:     content,
		SourcePath:  relativizeHomePath(skillDir),
		Provider:    provider,
		Files:       files,
	}, true, nil
}
