package handler

import (
	"strings"
	"testing"
)

func TestResearchRoleKickoffBrief(t *testing.T) {
	cases := map[string]string{
		"lead":      "统筹",
		"scout":     "检索",
		"reader":    "深读",
		"validator": "冲突",
		"reporter":  "报告",
		"custom":    "custom",
	}
	for role, needle := range cases {
		got := researchRoleKickoffBrief(role)
		if got == "" {
			t.Fatalf("role %s: empty brief", role)
		}
		if !strings.Contains(got, needle) {
			t.Fatalf("role %s: brief %q missing %q", role, got, needle)
		}
	}
}
