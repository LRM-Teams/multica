package service

import (
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/util"
	"gopkg.in/yaml.v3"
)

// Built-in skills are the platform's standard "template" skills. These evals
// pin the template every skill must follow and — crucially — couple each
// skill's documented contract to the real backend behavior it describes, so a
// drift in the source-of-truth (e.g. the mention regex) breaks CI instead of
// silently turning the skill into a lie agents act on.
//
// The evals live in a _test.go file on purpose: anything *inside* a skill
// directory is walked into AgentSkillData.Files and shipped to agent machines
// (see loadBuiltinSkill). Tests must stay out of that payload.

const (
	// maxSkillBodyLines is Anthropic's L2 budget for a SKILL.md body
	// (~5k tokens). Past this, content belongs in one-level-deep supporting
	// files, not the always-loaded body.
	maxSkillBodyLines = 500
	// maxDescriptionChars is the frontmatter description cap — it is the only
	// thing an agent sees when deciding whether to load the skill. Keep this
	// high enough to preserve trigger semantics, but low enough to prevent
	// runtime prompt bloat from command manuals in descriptions.
	maxDescriptionChars = 700
)

// TestBuiltinSkillsConformToTemplate enforces the standard-template invariants
// on every built-in skill, current and future. A new skill that violates the
// shape fails here without anyone having to remember the rules.
func TestBuiltinSkillsConformToTemplate(t *testing.T) {
	skills := loadBuiltinSkills()
	if len(skills) == 0 {
		t.Fatal("no built-in skills loaded; embed or layout is broken")
	}

	for _, skill := range skills {
		t.Run(skill.Name, func(t *testing.T) {
			// The multica- prefix keeps the on-disk slug from colliding with a
			// user-authored workspace skill.
			if !strings.HasPrefix(skill.Name, "multica-") {
				t.Errorf("skill name %q must carry the multica- prefix", skill.Name)
			}

			fm, body, ok := splitFrontmatter(skill.Content)
			if !ok {
				t.Fatalf("SKILL.md must lead with a --- frontmatter block")
			}
			if strings.TrimSpace(fm["name"]) == "" {
				t.Errorf("frontmatter is missing a non-empty name")
			}
			desc := strings.TrimSpace(fm["description"])
			if desc == "" {
				t.Errorf("frontmatter is missing a description (the only thing an agent sees when deciding to load the skill)")
			}
			if len(desc) > maxDescriptionChars {
				t.Errorf("description is %d chars, over the %d cap", len(desc), maxDescriptionChars)
			}
			if n := strings.Count(body, "\n") + 1; n > maxSkillBodyLines {
				t.Errorf("SKILL.md body is %d lines, over the %d-line L2 budget; move detail into one-level-deep supporting files", n, maxSkillBodyLines)
			}

			// Evals must never ride along to agent machines as supporting files.
			for _, f := range skill.Files {
				lower := strings.ToLower(f.Path)
				if strings.Contains(lower, "eval") || strings.HasSuffix(lower, "_test.go") || strings.HasSuffix(lower, "_test.md") {
					t.Errorf("supporting file %q looks like an eval/test; evals belong in _test.go, not the shipped skill payload", f.Path)
				}
			}
		})
	}
}

func TestBuiltinSkillsForAgent_ExcludesHiringFromOrdinaryAgents(t *testing.T) {
	ordinary := builtinSkillsForAgent(false)
	onboarding := builtinSkillsForAgent(true)
	for _, skill := range ordinary {
		if skill.Name == hiringBuiltinSkillName || strings.Contains(skill.Content, "/api/agent/actions/prepare") {
			t.Fatalf("ordinary Agent received hiring contract in %q", skill.Name)
		}
	}
	found := false
	for _, skill := range onboarding {
		if skill.Name == hiringBuiltinSkillName {
			found = true
			for _, want := range []string{
				"Only a Human-authored staffing request",
				"requester is an Agent",
				"do not call `multica action prepare`",
			} {
				if !strings.Contains(skill.Content, want) {
					t.Fatalf("Onboarding Agent hiring skill missing confused-deputy rule %q", want)
				}
			}
		}
	}
	if !found {
		t.Fatalf("Onboarding Agent did not receive %q", hiringBuiltinSkillName)
	}
}

func TestBuiltinStickerSkillSeparatesStandaloneAndChannelDelivery(t *testing.T) {
	t.Parallel()
	skill, ok := findSkill(t, "multica-stickers")
	if !ok {
		return
	}

	for _, want := range []string{
		"No `ChannelID`",
		`{"action":"message_send","parts":[{"type":"sticker","sticker_id":"hi"}]}`,
		"`ChannelID` present",
		"explicit target",
		"Do not search for a DM/channel target",
		"agent task is not a channel task",
	} {
		if !strings.Contains(skill.Content, want) {
			t.Errorf("multica-stickers skill missing delivery contract %q", want)
		}
	}
}

// TestBuiltinSkillsFrontmatterIsStrictYAML is the regression guard for MUL-3100
// / GitHub #3851: a built-in SKILL.md whose frontmatter is not valid YAML 1.2
// (the canonical break is an unquoted `: ` inside the description) is silently
// dropped by strict runtimes like Codex, so the agent runs without that
// platform-contract skill.
//
// This check is deliberately separate from TestBuiltinSkillsConformToTemplate:
// that test reads the frontmatter through splitFrontmatter, a naive line parser
// that splits on the first ':' and never runs a YAML parse — so it passes even
// on the broken files. Only a real yaml.Unmarshal reproduces what Codex does,
// which is exactly what is needed to catch this class of bug before it ships.
func TestBuiltinSkillsFrontmatterIsStrictYAML(t *testing.T) {
	skills := loadBuiltinSkills()
	if len(skills) == 0 {
		t.Fatal("no built-in skills loaded; embed or layout is broken")
	}

	for _, skill := range skills {
		t.Run(skill.Name, func(t *testing.T) {
			content := skill.Content
			if !strings.HasPrefix(content, "---\n") {
				t.Fatalf("SKILL.md must lead with a --- frontmatter block")
			}
			rest := content[len("---\n"):]
			end := strings.Index(rest, "\n---")
			if end < 0 {
				t.Fatalf("frontmatter has no closing --- delimiter")
			}

			var fm map[string]any
			if err := yaml.Unmarshal([]byte(rest[:end]), &fm); err != nil {
				t.Fatalf("frontmatter is not valid YAML — a strict runtime (e.g. Codex) "+
					"will drop this skill on load; quote values containing ': ': %v", err)
			}

			if name, ok := fm["name"].(string); !ok || strings.TrimSpace(name) == "" {
				t.Errorf("frontmatter name must parse as a non-empty string, got %#v", fm["name"])
			}
			if desc, ok := fm["description"].(string); !ok || strings.TrimSpace(desc) == "" {
				t.Errorf("frontmatter description must parse as a non-empty string, got %#v", fm["description"])
			}
		})
	}
}

// TestMentioningSkillFollowsContractFrontmatter locks the reference template:
// the mentioning skill is a context-triggered platform-contract skill, so it
// must declare user-invocable:false and fence itself to the multica CLI. New
// contract skills should copy this shape.
func TestMentioningSkillFollowsContractFrontmatter(t *testing.T) {
	skill, ok := findSkill(t, "multica-mentioning")
	if !ok {
		return
	}
	fm, _, _ := splitFrontmatter(skill.Content)

	if got := strings.TrimSpace(fm["user-invocable"]); got != "false" {
		t.Errorf("user-invocable = %q, want false (a platform-contract skill triggers from context, not a slash command)", got)
	}
	if got := strings.TrimSpace(fm["allowed-tools"]); got != "Bash(multica *)" {
		t.Errorf("allowed-tools = %q, want Bash(multica *) (fence the skill to the CLI it teaches)", got)
	}
}

// TestMentioningSkillTeachesTheParserContract is the eval that gives the skill
// its value: it proves the skill teaches exactly what util.ParseMentions
// enforces. The skill's "Incorrect" examples must parse to nothing (the
// @gpt-boy class of bug: a name where a UUID belongs fails silently), and its
// "Correct" example must parse. If mention.go:16 drifts, this breaks and the
// skill's claims must be re-checked.
func TestMentioningSkillTeachesTheParserContract(t *testing.T) {
	const uuid = "7f3a1b2c-0000-4000-8000-000000000abc"

	cases := []struct {
		name    string
		content string
		want    []util.Mention
	}{
		{
			// Skill: "Writing [@Alice](mention://member/Alice) does NOTHING."
			// 'l'/'i' are not hex, so the id fails to parse — link is dead.
			name:    "name where a uuid belongs is silently dead",
			content: "[@Alice](mention://member/Alice) please review",
			want:    nil,
		},
		{
			// Skill: a bare @name is plain text, nobody is notified.
			name:    "bare @name is plain text",
			content: "@alice please review",
			want:    nil,
		},
		{
			// Skill Step 2: type and id source matched → fires.
			name:    "real uuid with matching type fires",
			content: "[@Alice](mention://member/" + uuid + ") please review",
			want:    []util.Mention{{Type: "member", ID: uuid}},
		},
		{
			// Skill: @all uses the literal `all`, never a UUID.
			name:    "all uses the literal all",
			content: "[@all](mention://all/all) heads up",
			want:    []util.Mention{{Type: "all", ID: "all"}},
		},
		{
			// Skill: "Using the wrong type for an id points at the wrong
			// entity." The link still parses — it just resolves wrong — which
			// is exactly why the skill stresses matching type to id source.
			name:    "wrong type still parses (points at wrong entity)",
			content: "[@Bot](mention://member/" + uuid + ")",
			want:    []util.Mention{{Type: "member", ID: uuid}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := util.ParseMentions(tc.content)
			if len(got) != len(tc.want) {
				t.Fatalf("ParseMentions(%q) = %+v, want %+v", tc.content, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("mention[%d] = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestWorkingOnIssuesSkillCoversIssueLoopContracts(t *testing.T) {
	skill, ok := findSkill(t, "multica-working-on-issues")
	if !ok {
		return
	}
	fm, body, _ := splitFrontmatter(skill.Content)

	if got := strings.TrimSpace(fm["user-invocable"]); got != "false" {
		t.Errorf("user-invocable = %q, want false (issue workflow guidance triggers from context)", got)
	}
	if got := strings.TrimSpace(fm["allowed-tools"]); !strings.Contains(got, "Bash(multica *)") {
		t.Errorf("allowed-tools = %q, want access to the Multica CLI", got)
	}

	// Contract anchors only — exact file:line citations live in the skill's
	// references/source-map.md, not here, so a downstream main merge that
	// shifts a line cannot rot this test into pinning a stale lie.
	mustContain := []string{
		"multica issue mine --with-prs --with-gates --output json",
		"one CLI/API round-trip",
		"multica issue pull-requests <issue-id> --output json",
		"`GATE`",
		"Default for code-changing issue work",
		"open or update a PR before posting the final Multica issue comment",
		"This is a default, not",
		"Use a routable issue key in the PR title, body, or branch",
		"include the PR URL when a PR exists",
		"Closes MUL-2759",
		"--status backlog",
		"pr_url",
		"references/working-on-issues-source-map.md",
	}
	for _, want := range mustContain {
		if !strings.Contains(body, want) {
			t.Errorf("working-on-issues skill missing %q", want)
		}
	}

	mustNotContain := []string{
		"Start from the trigger, not from memory",
		"multica issue get <issue-id> --output json",
		"multica issue metadata list <issue-id> --output json",
		"multica issue comment list <issue-id> --thread <trigger-comment-id>",
		"multica issue comment add <issue-id> --parent <trigger-comment-id>",
	}
	for _, forbidden := range mustNotContain {
		if strings.Contains(body, forbidden) {
			t.Errorf("working-on-issues skill duplicates runtime prompt contract %q", forbidden)
		}
	}

	if !skillHasFile(skill, "references/working-on-issues-source-map.md") {
		t.Errorf("working-on-issues skill missing supporting file references/working-on-issues-source-map.md")
	}
}

func TestSkillImportingSkillCoversWorkspaceImportContracts(t *testing.T) {
	skill, ok := findSkill(t, "multica-skill-importing")
	if !ok {
		return
	}
	fm, body, _ := splitFrontmatter(skill.Content)

	if got := strings.TrimSpace(fm["user-invocable"]); got != "false" {
		t.Errorf("user-invocable = %q, want false (skill import guidance triggers from context)", got)
	}
	if got := strings.TrimSpace(fm["allowed-tools"]); !strings.Contains(got, "Bash(multica *)") {
		t.Errorf("allowed-tools = %q, want access to the Multica CLI", got)
	}

	mustContain := []string{
		"multica skill import --url <url> --output json",
		"/api/skills/import",
		"clawhub.ai",
		"skills.sh",
		"github.com",
		"config.origin",
		"--on-conflict fail",
		"--on-conflict overwrite",
		"--on-conflict rename",
		"--on-conflict skip",
		"status",
		"conflict",
		"skipped",
		"409",
		"existing_skill",
		"id",
		"name",
		"legacy",
		"multica skill list --output json",
		"npx skills add",
		"POST /api/agents/{id}/skills/add",
		"GET  /api/agents/{id}/skills",
		"PUT /api/agents/{id}/skills",
		"replace-all",
		"`set` is the replacement path",
		"references/skill-importing-source-map.md",
	}
	for _, want := range mustContain {
		if !strings.Contains(body, want) {
			t.Errorf("skill-importing skill missing %q", want)
		}
	}

	mustNotContain := []string{
		"multica agent skills add",
		"multica agent skills set",
		"multica agent skills list",
		"merge the new skill id with the existing ids",
	}
	for _, forbidden := range mustNotContain {
		if strings.Contains(body, forbidden) {
			t.Errorf("skill-importing skill should not teach stale or removed agent CLI %q", forbidden)
		}
	}

	if !skillHasFile(skill, "references/skill-importing-source-map.md") {
		t.Errorf("skill-importing skill missing supporting file references/skill-importing-source-map.md")
	}
}

func TestCreatingAgentsSkillCoversAgentCreationContracts(t *testing.T) {
	skill, ok := findSkill(t, "multica-creating-agents")
	if !ok {
		return
	}
	fm, body, _ := splitFrontmatter(skill.Content)

	if got := strings.TrimSpace(fm["user-invocable"]); got != "false" {
		t.Errorf("user-invocable = %q, want false (agent creation guidance triggers from context)", got)
	}
	if got := strings.TrimSpace(fm["allowed-tools"]); !strings.Contains(got, "Bash(multica *)") {
		t.Errorf("allowed-tools = %q, want access to the Multica CLI", got)
	}

	mustContain := []string{
		"not a parameter manual",
		"`description` is a catalog summary",
		"`instructions` is the runtime behavior contract",
		"POST /api/agents",
		"multica workspace info --agents",
		"`model` is a first-class persisted column",
		"custom_env",
		"GET /api/agents/{id}/env",
		"PUT /api/agents/{id}/env",
		"POST /api/agents/{id}/skills/add",
		"PUT /api/agents/{id}/skills",
		"GET  /api/agents/{id}/skills",
		"255",
		"references/creating-agents-source-map.md",
	}
	for _, want := range mustContain {
		if !strings.Contains(body, want) {
			t.Errorf("creating-agents skill missing %q", want)
		}
	}

	mustNotContain := []string{
		"--from-template",
		"/api/agent-templates",
		"template_slug",
		"curated template",
		"copy this parameter list",
		"multica agent get",
		"multica agent create",
		"multica agent skills",
		// De-coaching: this skill states source-backed contracts, it does not
		// teach a generic how-to methodology.
		"Define the job first",
		"Run a low-risk task",
		"Decision flow",
	}
	for _, forbidden := range mustNotContain {
		if strings.Contains(body, forbidden) {
			t.Errorf("creating-agents skill should not teach immature template content or generic how-to coaching %q", forbidden)
		}
	}

	if !skillHasFile(skill, "references/creating-agents-source-map.md") {
		t.Errorf("creating-agents skill missing supporting file references/creating-agents-source-map.md")
	}
}

func TestSquadsSkillRetired(t *testing.T) {
	// Squad product retired (Frank 2026-07-28): multica-squads skill must not ship.
	for _, s := range loadBuiltinSkills() {
		if s.Name == "multica-squads" {
			t.Fatal("multica-squads skill must be removed from builtin_skills")
		}
	}
}

func TestAutopilotsSkillRetired(t *testing.T) {
	// Autopilot product retired (LRM-1049 / Frank): skill must not ship.
	for _, s := range loadBuiltinSkills() {
		if s.Name == "multica-autopilots" {
			t.Fatal("multica-autopilots skill must be removed from builtin_skills")
		}
	}
}

func TestRuntimesSkillCoversClaimChain(t *testing.T) {
	skill, ok := findSkill(t, "multica-runtimes")
	if !ok {
		return
	}
	fm, body, _ := splitFrontmatter(skill.Content)

	if got := strings.TrimSpace(fm["user-invocable"]); got != "false" {
		t.Errorf("user-invocable = %q, want false", got)
	}
	if got := strings.TrimSpace(fm["allowed-tools"]); !strings.Contains(got, "Bash(multica *)") {
		t.Errorf("allowed-tools = %q, want access to the Multica CLI", got)
	}

	mustContain := []string{
		"agent_inbox_event",
		"Computer resident drains and leases the canonical inbox event",
		"multica runtime list --output json",
		"multica computer upgrade --target-version <version>",
		"upgrade_service_unreachable",
		"There is no top-level",
		"Agent's durable workspace",
		"references/runtimes-source-map.md",
	}
	for _, want := range mustContain {
		if !strings.Contains(body, want) {
			t.Errorf("runtimes skill missing %q", want)
		}
	}
	if !skillHasFile(skill, "references/runtimes-source-map.md") {
		t.Errorf("runtimes skill missing supporting source map")
	}
	if strings.Contains(body, "multica computer upgrade --target-version <version> --output json") {
		t.Errorf("runtimes skill still documents the retired Computer upgrade output flag")
	}
}

func TestProjectsSkillUsesWorkspaceInfo(t *testing.T) {
	skill, ok := findSkill(t, "multica-projects")
	if !ok {
		return
	}
	fm, body, _ := splitFrontmatter(skill.Content)

	if got := strings.TrimSpace(fm["user-invocable"]); got != "false" {
		t.Errorf("user-invocable = %q, want false", got)
	}
	if got := strings.TrimSpace(fm["allowed-tools"]); !strings.Contains(got, "Bash(multica *)") {
		t.Errorf("allowed-tools = %q, want access to the Multica CLI", got)
	}

	mustContain := []string{
		"Projects group issues",
		"multica workspace info --projects --output json",
		"CLI no longer exposes",
		"Agent memory",
		"AGENTS.md",
		"references/projects-source-map.md",
	}
	for _, want := range mustContain {
		if !strings.Contains(body, want) {
			t.Errorf("projects skill missing %q", want)
		}
	}
	if !skillHasFile(skill, "references/projects-source-map.md") {
		t.Errorf("projects skill missing supporting source map")
	}
}

func TestPeriodWorkBriefSkillRequiresReportingShape(t *testing.T) {
	skill, ok := findSkill(t, "multica-period-work-brief")
	if !ok {
		return
	}
	fm, body, _ := splitFrontmatter(skill.Content)

	if got := strings.TrimSpace(fm["user-invocable"]); got != "false" {
		t.Errorf("user-invocable = %q, want false", got)
	}
	if got := strings.TrimSpace(fm["allowed-tools"]); !strings.Contains(got, "Bash(multica *)") {
		t.Errorf("allowed-tools = %q, want Multica CLI for retry + note-write", got)
	}

	mustContain := []string{
		"Reporting shape",
		"Audience",
		"No evidence layer",
		"STRICT TIME WINDOW",
		"Start from collector ## Work groups",
		"Group by initiative identity",
		"Interleaved",
		"## Summary",
		"Work Summary",
		"Next Steps",
		"Technique",
		"Achievements",
		"Research",
		"omit",
		"filesystem path",
		"nested sub-points",
		"Mermaid",
		"retry-collectors",
		"--note-write",
		"references/period-work-brief-source-map.md",
	}
	for _, want := range mustContain {
		if !strings.Contains(body, want) {
			t.Errorf("period-work-brief skill missing %q", want)
		}
	}
	if !skillHasFile(skill, "references/period-work-brief-source-map.md") {
		t.Errorf("period-work-brief skill missing source map")
	}
}

func TestPeriodWorkPlanSkillRequiresCollectPlanDelivery(t *testing.T) {
	skill, ok := findSkill(t, "multica-period-work-plan")
	if !ok {
		return
	}
	fm, body, _ := splitFrontmatter(skill.Content)

	if got := strings.TrimSpace(fm["user-invocable"]); got != "false" {
		t.Errorf("user-invocable = %q, want false", got)
	}
	if got := strings.TrimSpace(fm["allowed-tools"]); !strings.Contains(got, "Bash(multica *)") {
		t.Errorf("allowed-tools = %q, want Multica CLI for submit-collect-plan", got)
	}
	for _, want := range []string{
		"submit-collect-plan",
		"roster",
		"paths",
		"topics",
		"aspects",
		"SCAN_ROOTS",
		"Forbidden",
		"references/period-work-plan-source-map.md",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("period-work-plan skill missing %q", want)
		}
	}
	if !skillHasFile(skill, "references/period-work-plan-source-map.md") {
		t.Errorf("period-work-plan skill missing source map")
	}
}

func TestNotesAssistantSkillRequiresSelectiveReads(t *testing.T) {
	skill, ok := findSkill(t, "multica-notes-assistant")
	if !ok {
		return
	}
	fm, body, _ := splitFrontmatter(skill.Content)

	if got := strings.TrimSpace(fm["user-invocable"]); got != "false" {
		t.Errorf("user-invocable = %q, want false", got)
	}
	if got := strings.TrimSpace(fm["allowed-tools"]); !strings.Contains(got, "Bash(multica *)") {
		t.Errorf("allowed-tools = %q, want Multica CLI", got)
	}
	for _, want := range []string{
		"Selective reads",
		"notes tree",
		"notes get",
		"final assistant output",
		"Do **not** run `multica message send`",
		"context_note_page_id",
		"references/notes-assistant-source-map.md",
	} {
		if !strings.Contains(body, want) && !strings.Contains(skill.Content, want) {
			t.Errorf("notes-assistant skill missing %q", want)
		}
	}
	for _, banned := range []string{
		"--note-write",
		"message send --target chat:",
	} {
		if strings.Contains(skill.Content, banned) {
			t.Errorf("notes-assistant skill must not teach %q", banned)
		}
	}
	if !skillHasFile(skill, "references/notes-assistant-source-map.md") {
		t.Errorf("notes-assistant skill missing supporting source map")
	}
}

func TestPeriodWorkCollectSkillCoversOSHarvestAndNoteWrite(t *testing.T) {
	skill, ok := findSkill(t, "multica-period-work-collect")
	if !ok {
		return
	}
	fm, body, _ := splitFrontmatter(skill.Content)

	if got := strings.TrimSpace(fm["user-invocable"]); got != "false" {
		t.Errorf("user-invocable = %q, want false", got)
	}
	if got := strings.TrimSpace(fm["allowed-tools"]); !strings.Contains(got, "Bash(git *)") {
		t.Errorf("allowed-tools = %q, want git access for OS harvest", got)
	}
	if got := strings.TrimSpace(fm["allowed-tools"]); !strings.Contains(got, "Bash(multica *)") {
		t.Errorf("allowed-tools = %q, want Multica CLI for submit-pack", got)
	}

	mustContain := []string{
		"collector pack",
		"$HOME",
		"STRICT WINDOW",
		"Do not",
		"Host Digest",
		"submit-pack",
		"## Repos / roots",
		"## Highlights",
		"## Work groups",
		"## Diagrams",
		"Mermaid",
		"references/collect-recipes.md",
		"references/period-work-collect-source-map.md",
		"final Period Work Brief",
	}
	for _, want := range mustContain {
		if !strings.Contains(body, want) {
			t.Errorf("period-work-collect skill missing %q", want)
		}
	}
	if !skillHasFile(skill, "references/collect-recipes.md") {
		t.Errorf("period-work-collect skill missing collect-recipes.md")
	}
	if !skillHasFile(skill, "references/period-work-collect-source-map.md") {
		t.Errorf("period-work-collect skill missing source map")
	}
	recipes := ""
	for _, f := range skill.Files {
		if f.Path == "references/collect-recipes.md" {
			recipes = f.Content
			break
		}
	}
	for _, want := range []string{"find ", "git -C", "--after=", "status --porcelain", "head -n"} {
		if !strings.Contains(recipes, want) {
			t.Errorf("collect-recipes.md missing %q", want)
		}
	}
}

func findSkill(t *testing.T, name string) (AgentSkillData, bool) {
	t.Helper()
	for _, s := range loadBuiltinSkills() {
		if s.Name == name {
			return s, true
		}
	}
	t.Errorf("built-in skill %q not found", name)
	return AgentSkillData{}, false
}

func skillHasFile(skill AgentSkillData, path string) bool {
	for _, f := range skill.Files {
		if f.Path == path {
			return true
		}
	}
	return false
}

// splitFrontmatter returns the top-level scalar keys of a leading YAML
// frontmatter block, the body after it, and whether a block was found. It only
// understands flat `key: value` lines — enough for the template's frontmatter.
func splitFrontmatter(content string) (map[string]string, string, bool) {
	if !strings.HasPrefix(content, "---\n") {
		return nil, content, false
	}
	rest := content[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, content, false
	}
	block := rest[:end]
	body := rest[end:]
	if nl := strings.Index(body, "\n"); nl >= 0 {
		body = body[nl+1:] // drop the closing --- line
	}

	fm := make(map[string]string)
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue // nested value; the template uses only flat scalars
		}
		key, val, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		fm[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(val), `"'`)
	}
	return fm, body, true
}
