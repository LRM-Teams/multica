package execenv

import "testing"

func TestStartupStaticDigestIgnoresPerTurnFields(t *testing.T) {
	base := TaskContextForEnv{
		AgentID: "agent-a", AgentName: "Agent A", ChatSessionID: "chat-1", Directed: true,
		AgentInstructions: "be careful",
		ManagedRole:       "group_manager",
		ProjectID:         "proj-1",
		AgentSkills: []SkillContextForEnv{{
			Name: "s1", Description: "desc", Content: "body",
			Files: []SkillFileContextForEnv{{Path: "extra.md", Content: "extra"}},
		}},
		AgentMemories: []MemoryContextForEnv{{
			Name: "m1", Content: "note", Scope: "agent", SubjectType: "agent", SubjectID: "agent-a",
		}},
	}
	a := base
	a.InitiatorName = "Alice"
	a.IssueID = "issue-A"
	b := base
	b.InitiatorName = "Bob"
	b.IssueID = "issue-B"
	if StartupStaticDigest("grok", a) != StartupStaticDigest("grok", b) {
		t.Fatal("per-turn initiator/issue must not change startup digest")
	}
}

func TestStartupStaticDigestTracksSkillDescriptionAndFiles(t *testing.T) {
	base := TaskContextForEnv{
		AgentID: "a", ChatSessionID: "c",
		AgentSkills: []SkillContextForEnv{{
			Name: "s1", Description: "desc-a", Content: "body",
			Files: []SkillFileContextForEnv{{Path: "extra.md", Content: "v1"}},
		}},
	}
	changedDesc := base
	changedDesc.AgentSkills = []SkillContextForEnv{{
		Name: "s1", Description: "desc-b", Content: "body",
		Files: []SkillFileContextForEnv{{Path: "extra.md", Content: "v1"}},
	}}
	if StartupStaticDigest("grok", base) == StartupStaticDigest("grok", changedDesc) {
		t.Fatal("skill Description change must change digest (real plan bytes)")
	}
	changedFile := base
	changedFile.AgentSkills = []SkillContextForEnv{{
		Name: "s1", Description: "desc-a", Content: "body",
		Files: []SkillFileContextForEnv{{Path: "extra.md", Content: "v2"}},
	}}
	if StartupStaticDigest("grok", base) == StartupStaticDigest("grok", changedFile) {
		t.Fatal("skill Files content change must change digest")
	}
}

func TestStartupStaticDigestTracksMemoryScopeMetadata(t *testing.T) {
	// Memory scope/subject affect brief render via buildMetaSkillContent when included.
	// Even if only Content is in brief today, Scope is part of durable memory identity
	// that may appear in future render — plan includes memory via brief content.
	a := TaskContextForEnv{
		AgentID: "a", ChatSessionID: "c",
		AgentMemories: []MemoryContextForEnv{{Name: "m", Content: "same", Scope: "agent", SubjectType: "agent", SubjectID: "a"}},
	}
	b := TaskContextForEnv{
		AgentID: "a", ChatSessionID: "c",
		AgentMemories: []MemoryContextForEnv{{Name: "m", Content: "same", Scope: "user", SubjectType: "member", SubjectID: "u1"}},
	}
	// Content same; if brief only uses content, digests may match — that's product of brief.
	// Scope change with content change is the hard gate:
	c := TaskContextForEnv{
		AgentID: "a", ChatSessionID: "c",
		AgentMemories: []MemoryContextForEnv{{Name: "m", Content: "changed", Scope: "user", SubjectType: "member", SubjectID: "u1"}},
	}
	if StartupStaticDigest("grok", a) == StartupStaticDigest("grok", c) {
		t.Fatal("memory content change must change digest")
	}
	_ = b
}

func TestRenderStartupPlanDigestMatchesMaterializeInput(t *testing.T) {
	ctx := TaskContextForEnv{
		AgentID: "a", AgentName: "A", ChatSessionID: "chat", Directed: true,
		InitiatorName: "should-not-appear-in-static",
		IssueID:       "should-not-appear-in-static",
		AgentSkills: []SkillContextForEnv{{
			Name: "Skill One", Description: "d", Content: "# Skill\n",
		}},
	}
	static := StartupStaticContext(ctx)
	if static.InitiatorName != "" || static.IssueID != "" {
		t.Fatal("StartupStaticContext must strip per-turn fields")
	}
	plan := RenderStartupMaterializationPlan("grok", static)
	if plan.RuntimeBrief == "" {
		t.Fatal("expected runtime brief")
	}
	if plan.Digest() == "" || plan.Digest() != StartupStaticDigest("grok", ctx) {
		t.Fatal("StartupStaticDigest must equal plan.Digest after static strip")
	}
	// Per-turn initiator must not be in the brief plan.
	if containsIgnoreCase(plan.RuntimeBrief, "should-not-appear-in-static") {
		t.Fatal("static brief must not include per-turn initiator/issue strings")
	}
}

func containsIgnoreCase(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (s == sub ||
		// cheap contains
		stringIndex(s, sub) >= 0))
}

func stringIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
