package service

import (
	"embed"
	"io/fs"
	"path"
	"sort"
	"strings"

	skillpkg "github.com/multica-ai/multica/server/internal/skill"
)

//go:embed installable_skills
var installableSkillsFS embed.FS

const installableSkillsRoot = "installable_skills"

// InstallableSkill is a platform-bundled skill that a workspace owner/admin can
// install into the workspace catalog. Unlike BuiltinSkills, these are not
// injected into every run automatically; they appear in the Skills page after
// installation and must be attached to an agent like any other workspace skill.
type InstallableSkill struct {
	Name        string
	Description string
	Content     string
	Files       []AgentSkillFileData
}

// ListInstallableSkills returns platform-bundled skills that users can install
// into a workspace skill catalog on demand.
func ListInstallableSkills() []InstallableSkill {
	entries, err := fs.ReadDir(installableSkillsFS, installableSkillsRoot)
	if err != nil {
		return nil
	}
	skills := make([]InstallableSkill, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if skill, ok := LoadInstallableSkill(entry.Name()); ok {
			skills = append(skills, skill)
		}
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return skills
}

// LoadInstallableSkill loads one platform-bundled installable skill by its
// on-disk slug or frontmatter name.
func LoadInstallableSkill(name string) (InstallableSkill, bool) {
	name = strings.TrimSpace(name)
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
		return InstallableSkill{}, false
	}
	dir := path.Join(installableSkillsRoot, name)
	content, err := fs.ReadFile(installableSkillsFS, path.Join(dir, "SKILL.md"))
	if err != nil {
		return InstallableSkill{}, false
	}

	parsedName, description := skillpkg.ParseSkillFrontmatter(string(content))
	if parsedName == "" {
		parsedName = name
	}
	skill := InstallableSkill{Name: parsedName, Description: description, Content: string(content)}
	_ = fs.WalkDir(installableSkillsFS, dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		rel := strings.TrimPrefix(p, dir+"/")
		if rel == "SKILL.md" {
			return nil
		}
		data, readErr := fs.ReadFile(installableSkillsFS, p)
		if readErr != nil {
			return nil
		}
		skill.Files = append(skill.Files, AgentSkillFileData{Path: rel, Content: string(data)})
		return nil
	})
	return skill, true
}
