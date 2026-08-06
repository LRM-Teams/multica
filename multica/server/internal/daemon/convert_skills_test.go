package daemon

import "testing"

func TestConvertSkillsForEnvFillsDescriptionFromFrontmatter(t *testing.T) {
	t.Parallel()
	skills := convertSkillsForEnv([]SkillData{{
		Name:    "demo",
		Content: "---\nname: demo\ndescription: Use when verifying progressive skill loading.\n---\n\n# Demo\n",
	}})
	if len(skills) != 1 {
		t.Fatalf("len = %d", len(skills))
	}
	if skills[0].Description != "Use when verifying progressive skill loading." {
		t.Fatalf("description = %q", skills[0].Description)
	}
}

func TestConvertSkillsForEnvKeepsExplicitDescription(t *testing.T) {
	t.Parallel()
	skills := convertSkillsForEnv([]SkillData{{
		Name:        "demo",
		Description: "DB description wins",
		Content:     "---\nname: demo\ndescription: Frontmatter description.\n---\n\n# Demo\n",
	}})
	if skills[0].Description != "DB description wins" {
		t.Fatalf("description = %q", skills[0].Description)
	}
}
