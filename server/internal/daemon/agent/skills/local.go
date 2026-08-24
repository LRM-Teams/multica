package skills

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

	skillpkg "github.com/multica-ai/multica/server/internal/skill"
)

const ContentFilename = skillpkg.ContentFilename

// LocalFile is one supporting file in a filesystem-backed skill.
type LocalFile struct {
	Path    string
	Content string
}

// LocalSummary is the metadata needed to present a filesystem-backed skill.
type LocalSummary struct {
	Key         string
	Name        string
	Description string
	Path        string
	FileCount   int
}

// LocalBundle is a complete filesystem-backed skill ready for import.
type LocalBundle struct {
	Name        string
	Description string
	Content     string
	Path        string
	Files       []LocalFile
}

// LocalCatalog discovers and loads directory-backed Agent Skills from an
// ordered set of roots. The first root wins when multiple roots expose the
// same relative key.
type LocalCatalog struct {
	roots []string
}

func NewLocalCatalog(roots ...string) LocalCatalog {
	return LocalCatalog{roots: uniqueLocalPaths(roots)}
}

func (c LocalCatalog) List() ([]LocalSummary, error) {
	var all []LocalSummary
	seen := make(map[string]bool)
	for _, root := range c.roots {
		skills, err := listLocalRoot(root)
		if err != nil {
			return nil, err
		}
		for _, item := range skills {
			if seen[item.Key] {
				continue
			}
			seen[item.Key] = true
			all = append(all, item)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Key < all[j].Key })
	return all, nil
}

func (c LocalCatalog) Load(key string) (*LocalBundle, error) {
	key, err := normalizeLocalKey(key)
	if err != nil {
		return nil, err
	}
	for _, root := range c.roots {
		dir := filepath.Join(root, filepath.FromSlash(key))
		if _, err := os.Stat(filepath.Join(dir, ContentFilename)); err != nil {
			continue
		}
		return loadLocalDir(dir)
	}
	return nil, fmt.Errorf("local skill not found")
}

func listLocalRoot(root string) ([]LocalSummary, error) {
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return []LocalSummary{}, nil
		}
		return nil, err
	}
	var skills []LocalSummary
	enumerateLocalSkills(root, root, make(map[string]bool), &skills)
	sort.Slice(skills, func(i, j int) bool { return skills[i].Key < skills[j].Key })
	return skills, nil
}

func enumerateLocalSkills(walkRoot, currentDir string, visited map[string]bool, skills *[]LocalSummary) {
	resolved, err := filepath.EvalSymlinks(currentDir)
	if err != nil {
		return
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil || visited[resolved] {
		return
	}
	visited[resolved] = true

	entries, err := os.ReadDir(currentDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if ignoredLocalEntry(entry.Name()) {
			continue
		}
		path, err := filepath.Abs(filepath.Join(currentDir, entry.Name()))
		if err != nil {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(path, ContentFilename)); err == nil {
			content, err := os.ReadFile(filepath.Join(path, ContentFilename))
			if err != nil {
				continue
			}
			rel, err := filepath.Rel(walkRoot, path)
			if err != nil {
				continue
			}
			key, err := normalizeLocalKey(filepath.ToSlash(rel))
			if err != nil {
				continue
			}
			name, description := skillpkg.ParseSkillFrontmatter(string(content))
			if name == "" {
				name = filepath.Base(path)
			}
			files, err := collectLocalFiles(path, false)
			if err != nil {
				continue
			}
			*skills = append(*skills, LocalSummary{
				Key: key, Name: name, Description: description, Path: path, FileCount: len(files) + 1,
			})
			continue
		}
		enumerateLocalSkills(walkRoot, path, visited, skills)
	}
}

func loadLocalDir(dir string) (*LocalBundle, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("local skill is not a directory")
	}
	content, err := os.ReadFile(filepath.Join(dir, ContentFilename))
	if err != nil {
		return nil, err
	}
	name, description := skillpkg.ParseSkillFrontmatter(string(content))
	if name == "" {
		name = filepath.Base(dir)
	}
	files, err := collectLocalFiles(dir, true)
	if err != nil {
		return nil, err
	}
	return &LocalBundle{Name: name, Description: description, Content: string(content), Path: dir, Files: files}, nil
}

func collectLocalFiles(dir string, includeContent bool) ([]LocalFile, error) {
	walkRoot := dir
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		walkRoot = resolved
	}
	var files []LocalFile
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
			if ignoredLocalEntry(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if ignoredLocalEntry(entry.Name()) || strings.EqualFold(entry.Name(), ContentFilename) {
			return nil
		}
		rel, err := safeLocalRel(walkRoot, path)
		if err != nil {
			return nil
		}
		file := LocalFile{Path: filepath.ToSlash(rel)}
		if includeContent {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			file.Content = string(content)
		}
		files = append(files, file)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// LocalFingerprint hashes paths, sizes, and mtimes so polling callers can
// avoid loading unchanged bundles.
func LocalFingerprint(dir string) (string, error) {
	walkRoot := dir
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
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
			if path != walkRoot && ignoredLocalEntry(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if ignoredLocalEntry(entry.Name()) {
			return nil
		}
		rel, err := safeLocalRel(walkRoot, path)
		if err != nil {
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

func ignoredLocalEntry(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") {
		return true
	}
	switch strings.ToLower(name) {
	case "license", "license.md", "license.txt", "node_modules":
		return true
	default:
		return false
	}
}

func normalizeLocalKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" || filepath.IsAbs(key) || strings.HasPrefix(key, "/") || strings.ContainsAny(key, "\\:") {
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

func safeLocalRel(root, path string) (string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	rel = filepath.Clean(rel)
	if rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes skill root")
	}
	return rel, nil
}

func uniqueLocalPaths(paths []string) []string {
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
