package execenv

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStartupStaticDigestIgnoresPerTurnFields(t *testing.T) {
	base := TaskContextForEnv{
		AgentID: "agent-a", AgentName: "Agent A", MessageDelivery: true, AgentInstructions: "be careful",
		ManagedRole: "group_manager",
		ProjectID:   "proj-1",
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

// TestStartupStaticDigestNoLongerReflectsManagerRole documents the D6 digest
// half of retiring the startup group-manager brief: TaskContextForEnv has no
// ManagerChannels field to feed the digest any longer, so a role change
// cannot rotate/recreate the resident process. That's intentional — role
// truth now reaches every wake via daemon.currentStateOverlay regardless of
// session age, so paying for a process recreate on every promotion/demotion
// would just be an expensive no-op. See TestStartupBriefNeverRendersGroupManagerDutySegment.
func TestStartupStaticDigestNoLongerReflectsManagerRole(t *testing.T) {
	ctx := TaskContextForEnv{AgentID: "agent-a", AgentName: "Agent A"}
	brief := RenderStartupMaterializationPlan("grok", StartupStaticContext(ctx)).RuntimeBrief
	if containsIgnoreCase(brief, "Group manager") {
		t.Fatalf("resident startup brief must not contain manager duty text:\n%s", brief)
	}
}

// Reviewer control: create-time AGENTS is a positive allowlist of durable
// agent/runtime facts. Every value below is turn-scoped and therefore must
// neither rotate the resident backend nor leak into its startup brief.
func TestBarryStartupStaticContextExcludesAllTurnScopedKinds(t *testing.T) {
	a := TaskContextForEnv{
		AgentID:             "agent-a",
		AgentName:           "Agent A",
		AgentInstructions:   "durable-agent-instructions",
		PriorSessionResumed: true,
		TriggerCommentID:    "private-alpha-review-sentinel",
		AgentMemories: []MemoryContextForEnv{{
			Name: "memory-alpha-review-sentinel", Content: "memory-alpha-review-sentinel",
			Scope: "user", SubjectType: "member", SubjectID: "user-alpha",
		}},
		AutopilotRunID:          "autopilot-run-alpha-review-sentinel",
		AutopilotID:             "autopilot-alpha-review-sentinel",
		AutopilotTitle:          "autopilot-title-alpha-review-sentinel",
		AutopilotDescription:    "autopilot-description-alpha-review-sentinel",
		AutopilotSource:         "autopilot-source-alpha-review-sentinel",
		AutopilotTriggerPayload: "autopilot-payload-alpha-review-sentinel",
		QuickCreatePrompt:       "quick-create-alpha-review-sentinel",
		AgentRadarPrompt:        "radar-alpha-review-sentinel",
	}
	b := a
	b.PriorSessionResumed = false
	b.TriggerCommentID = "private-beta-review-sentinel"
	b.AgentMemories = []MemoryContextForEnv{{
		Name: "memory-beta-review-sentinel", Content: "memory-beta-review-sentinel",
		Scope: "user", SubjectType: "member", SubjectID: "user-beta",
	}}
	b.AutopilotRunID = "autopilot-run-beta-review-sentinel"
	b.AutopilotID = "autopilot-beta-review-sentinel"
	b.AutopilotTitle = "autopilot-title-beta-review-sentinel"
	b.AutopilotDescription = "autopilot-description-beta-review-sentinel"
	b.AutopilotSource = "autopilot-source-beta-review-sentinel"
	b.AutopilotTriggerPayload = "autopilot-payload-beta-review-sentinel"
	b.QuickCreatePrompt = "quick-create-beta-review-sentinel"
	b.AgentRadarPrompt = "radar-beta-review-sentinel"

	if gotA, gotB := StartupStaticDigest("grok", a), StartupStaticDigest("grok", b); gotA != gotB {
		t.Fatalf("turn-scoped kind changed startup digest: a=%s b=%s", gotA, gotB)
	}

	brief := RenderStartupMaterializationPlan("grok", StartupStaticContext(a)).RuntimeBrief
	for _, sentinel := range []string{
		"fresh-alpha-review-sentinel",
		"user-alpha-review-sentinel",
		"memory-alpha-review-sentinel",
		"autopilot-alpha-review-sentinel",
		"quick-create-alpha-review-sentinel",
	} {
		if containsIgnoreCase(brief, sentinel) {
			t.Fatalf("turn-scoped sentinel leaked into startup brief: %q", sentinel)
		}
	}
}

func TestStartupStaticDigestTracksSkillDescriptionNotFiles(t *testing.T) {
	// Slim D6-1b: AGENTS brief indexes skill name+description only.
	// Skill package Files are NOT written to workdir, so Files content must
	// not force AGENTS rewrite / process recreation via digest.
	base := TaskContextForEnv{
		AgentID: "a", MessageDelivery: true,
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
	if StartupStaticDigest("grok", base) != StartupStaticDigest("grok", changedFile) {
		t.Fatal("slim: skill Files content must not change startup digest")
	}
}

func TestStartupStaticDigestIgnoresAgentMemories(t *testing.T) {
	// Slim: AgentMemories are per-turn / prompt facts, not create-time AGENTS.
	a := TaskContextForEnv{
		AgentID: "a", AgentInstructions: "stay",
		AgentMemories: []MemoryContextForEnv{{Name: "m", Content: "alpha", Scope: "user", SubjectType: "member", SubjectID: "u1"}},
	}
	b := a
	b.AgentMemories = []MemoryContextForEnv{{Name: "m", Content: "beta", Scope: "agent", SubjectType: "agent", SubjectID: "a"}}
	if StartupStaticDigest("grok", a) != StartupStaticDigest("grok", b) {
		t.Fatal("AgentMemories must not change startup digest")
	}
}

func TestRenderStartupPlanDigestMatchesMaterializeInput(t *testing.T) {
	ctx := TaskContextForEnv{
		AgentID: "a", AgentName: "A", MessageDelivery: true, InitiatorName: "should-not-appear-in-static",
		IssueID: "should-not-appear-in-static",
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

func TestStartupKernelKeepsHotPathsAndExcludesTurnWorkflow(t *testing.T) {
	ctx := TaskContextForEnv{
		AgentID:           "agent-a",
		AgentName:         "Agent A",
		AgentInstructions: "Prefer concise results.",
		AgentRoot:         "/tmp/agent-a",
		WorkspaceContext:  "Workspace conventions.",
		IssueID:           "issue-must-not-leak",
		TriggerCommentID:  "comment-must-not-leak",
		AutopilotRunID:    "run-must-not-leak",
		AgentSkills: []SkillContextForEnv{{
			Name: "multica-working-on-issues", Description: "Issue workflow and decomposition.",
		}},
	}
	brief := RenderStartupMaterializationPlan("codex", StartupStaticContext(ctx)).RuntimeBrief

	for _, want := range []string{
		"multica message check",
		"multica message send --target <target>",
		"multica message react --message-id <id>",
		"multica issue get <id>",
		"multica-working-on-issues",
		"ordinary Issue DAGs",
		"multica <command> --help",
		"## Output utility contract",
		"### Memory Operating Guide (v0.12)",
		"users/<member-id>/USER.md",
		"re-reading or stat-checking that exact path",
		"memory write alone permanently changed Agent identity",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("startup kernel missing hot-path or lazy-loading pointer %q\n--- brief ---\n%s", want, brief)
		}
	}
	for _, forbidden := range []string{
		"issue-must-not-leak",
		"comment-must-not-leak",
		"run-must-not-leak",
		"Work Decomposition Gate",
		"## Comment Formatting",
		"Execution Discipline",
	} {
		if strings.Contains(brief, forbidden) {
			t.Errorf("startup kernel leaked turn workflow %q\n--- brief ---\n%s", forbidden, brief)
		}
	}
	if len(brief) > 8*1024 {
		t.Fatalf("representative startup kernel = %d bytes, want <= 8192", len(brief))
	}
}

func TestStartupKernelBoundsConfigurableSections(t *testing.T) {
	large := strings.Repeat("界", 10*1024)
	ctx := TaskContextForEnv{
		AgentID:                          "agent-a",
		AgentInstructions:                large,
		RequestingUserProfileDescription: large,
		WorkspaceContext:                 large,
		AgentSkills: []SkillContextForEnv{{
			Name: "large-skill", Description: large,
		}},
	}
	brief := RenderStartupMaterializationPlan("codex", StartupStaticContext(ctx)).RuntimeBrief

	if !utf8.ValidString(brief) {
		t.Fatal("startup section truncation split a UTF-8 code point")
	}
	for _, label := range []string{"agent instructions truncated", "requesting-user profile truncated", "workspace context truncated", "skill index truncated"} {
		if !strings.Contains(brief, label) {
			t.Errorf("startup kernel missing %q marker", label)
		}
	}
	if len(brief) > 16*1024 {
		t.Fatalf("maximal startup kernel = %d bytes, want <= 16384", len(brief))
	}
}

func TestNonAgentScopeMemoriesAndRenderAgentScopeMemory(t *testing.T) {
	memories := []MemoryContextForEnv{
		{Name: "agent global", Content: "global convention", Scope: "agent", SubjectType: "agent", SubjectID: "a1"},
		{Name: "member private", Content: "call me JHP", Scope: "user", SubjectType: "member", SubjectID: "m1"},
		{Name: "project", Content: "proj note", Scope: "project", SubjectType: "project", SubjectID: "p1"},
	}
	turn := RenderTurnContext(TaskContextForEnv{AgentMemories: memories})
	if strings.Contains(turn, "global convention") {
		t.Fatal("agent-scope memory must not appear in per-message context")
	}
	for _, want := range []string{"call me JHP", "proj note"} {
		if !strings.Contains(turn, want) {
			t.Errorf("per-message context missing %q", want)
		}
	}
	agent := RenderAgentScopeMemory(memories)
	if !strings.Contains(agent, "global convention") {
		t.Fatal("agent-scope memory missing from system-prompt renderer")
	}
	if strings.Contains(agent, "call me JHP") || strings.Contains(agent, "proj note") {
		t.Fatal("system-prompt agent memory leaked non-agent scope")
	}
	if RenderAgentScopeMemory([]MemoryContextForEnv{{Name: "m", Content: "x", Scope: "user"}}) != "" {
		t.Fatal("no agent-scope memory must render empty")
	}
}

func TestRenderTurnContextKeepsDynamicFactsOutOfStartupAndBoundsMemory(t *testing.T) {
	ctx := TaskContextForEnv{
		AgentInstructions: "stable instructions must not repeat",
		InitiatorType:     "member",
		InitiatorID:       "member-1",
		InitiatorName:     "Alice",
		InitiatorEmail:    "alice@example.com",
		AgentMemories: []MemoryContextForEnv{{
			Name: "Large selected memory", Content: strings.Repeat("记", 8*1024), Scope: "user",
		}},
	}
	turn := RenderTurnContext(ctx)

	for _, want := range []string{"## Current Task Initiator", "Alice", "member-1", "## Effective Promoted Memory Snapshot"} {
		if !strings.Contains(turn, want) {
			t.Errorf("turn context missing %q", want)
		}
	}
	if strings.Contains(turn, ctx.AgentInstructions) {
		t.Fatal("stable agent instructions repeated in per-turn context")
	}
	if !utf8.ValidString(turn) {
		t.Fatal("turn memory truncation split a UTF-8 code point")
	}
	if len(turn) > 10*1024 {
		t.Fatalf("turn context = %d bytes, want <= 10240", len(turn))
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
