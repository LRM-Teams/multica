package service

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestInstallableSkillsLoad(t *testing.T) {
	skills := ListInstallableSkills()
	if len(skills) == 0 {
		t.Fatal("no installable skills loaded; embed or layout is broken")
	}

	var found bool
	for _, skill := range skills {
		if skill.Name != "multica-memory-migration" {
			continue
		}
		found = true
		if strings.TrimSpace(skill.Description) == "" {
			t.Fatal("migration skill description is empty")
		}
		if !strings.Contains(skill.Content, "# Multica memory migration") {
			t.Fatal("migration skill content did not load")
		}
		var fm map[string]any
		rest := strings.TrimPrefix(skill.Content, "---\n")
		end := strings.Index(rest, "\n---")
		if end < 0 {
			t.Fatal("frontmatter has no closing delimiter")
		}
		if err := yaml.Unmarshal([]byte(rest[:end]), &fm); err != nil {
			t.Fatalf("frontmatter is not valid YAML: %v", err)
		}
	}
	if !found {
		t.Fatal("multica-memory-migration installable skill not found")
	}
}
