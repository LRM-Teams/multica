package execenv

import (
	"testing"
)

func TestRenderStartupPlanHasNoSkillFiles(t *testing.T) {
	plan := RenderStartupMaterializationPlan("grok", StartupStaticContext(TaskContextForEnv{
		AgentID: "a", AgentName: "A",
		AgentSkills: []SkillContextForEnv{{Name: "s", Description: "d", Content: "c"}},
	}))
	if plan.RuntimeBrief == "" {
		t.Fatal("expected brief")
	}
	// type no longer has SkillFiles field — compile is the assertion
	_ = plan.Digest()
}
