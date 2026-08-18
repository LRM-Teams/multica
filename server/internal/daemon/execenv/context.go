package execenv

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	skillpkg "github.com/multica-ai/multica/server/internal/skill"
	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"gopkg.in/yaml.v3"
)

// writeContextFiles renders and writes .agent_context/issue_context.md and
// skills into the appropriate provider-native location.
//
// Claude:      skills → {agentRoot}/.claude/skills/{name}/SKILL.md  (native discovery)
// Codex:       skills → {agentRoot}/.agents/skills/{name}/SKILL.md (workspace discovery)
// OpenCode:    skills → {agentRoot}/.opencode/skills/{name}/SKILL.md  (native discovery)
// Pi:          skills → {agentRoot}/.pi/skills/{name}/SKILL.md  (native discovery)
// Cursor:      skills → {agentRoot}/.cursor/skills/{name}/SKILL.md  (native discovery)
// Kiro:        skills → {agentRoot}/.kiro/skills/{name}/SKILL.md  (native discovery)
// Grok Build:  skills → {agentRoot}/.grok/skills/{name}/SKILL.md  (native discovery)
// Default:     skills → {agentRoot}/.agent_context/skills/{name}/SKILL.md
//
// manifest, when non-nil, is populated with every file we created and every
// intermediate directory we had to MkdirAll (skipping any that pre-existed).
// CleanupSidecars uses it to replace only Multica-managed context on the next
// refresh. Callers may pass nil when they do not need bookkeeping.
func writeContextFiles(agentRoot, provider string, ctx TaskContextForEnv, manifest *sidecarManifest) error {
	contextDir := filepath.Join(agentRoot, ".agent_context")
	if err := recordMkdirAll(contextDir, 0o755, manifest); err != nil {
		return fmt.Errorf("create .agent_context dir: %w", err)
	}

	content := renderIssueContext(provider, ctx)
	path := filepath.Join(contextDir, "issue_context.md")
	if err := recordWriteFile(path, []byte(content), 0o644, manifest); err != nil {
		// A pre-existing path means the user already owns
		// .agent_context/issue_context.md — either they created it
		// themselves or it survived from a crashed prior run we can't
		// safely distinguish from intentional content. Refusing the
		// write is the correct call: the runtime brief (CLAUDE.md /
		// AGENTS.md / GEMINI.md) already carries every fact this file
		// would, so the agent runs fine without the sidecar copy.
		// Anything else is a real failure.
		if !errors.Is(err, errPathPreExists) {
			return fmt.Errorf("write issue_context.md: %w", err)
		}
	}

	if len(ctx.AgentSkills) > 0 {
		skillsDir, err := resolveSkillsDir(agentRoot, provider, manifest)
		if err != nil {
			return fmt.Errorf("resolve skills dir: %w", err)
		}
		if err := writeSkillFiles(skillsDir, ctx.AgentSkills, manifest); err != nil {
			return fmt.Errorf("write skill files: %w", err)
		}
	}

	return nil
}

// resolveSkillsDir returns the directory where skills should be written
// based on the agent provider, creating it. manifest, when non-nil, is
// populated with every intermediate directory we had to MkdirAll so
// CleanupSidecars can remove them before the next refresh.
func resolveSkillsDir(agentRoot, provider string, manifest *sidecarManifest) (string, error) {
	skillsDir := skillsDirPath(agentRoot, provider)
	if err := recordMkdirAll(skillsDir, 0o755, manifest); err != nil {
		return "", err
	}
	return skillsDir, nil
}

// skillsDirPath returns the provider-native skills parent directory under
// agentRoot WITHOUT creating it or recording anything. resolveSkillsDir wraps
// this with the MkdirAll/manifest bookkeeping; the reuse-path skill rollback
// (removeReusedManagedSkillDirs) needs the bare path with no side effects so
// it can match the managed skill roots the prior manifest recorded.
func skillsDirPath(agentRoot, provider string) string {
	switch provider {
	case agent.ProviderClaude:
		// Claude Code natively discovers skills from .claude/skills/ in the workdir.
		return filepath.Join(agentRoot, ".claude", "skills")
	case agent.ProviderOpenCode:
		// OpenCode natively discovers project skills from .opencode/skills/ in
		// the workdir. ConfigPaths.directories() walks up from the discovery
		// root looking for a bare `.opencode` directory (no opencode.json
		// signal required), then skill/index.ts scans `{skill,skills}/**/SKILL.md`
		// under each match. Discovery is anchored at the Agent workspace via
		// `opencode run --dir <agentRoot>` + PWD override in opencodeBackend —
		// without those, OpenCode walks from the daemon's inherited PWD and
		// misses .opencode/skills + AGENTS.md entirely (MUL-2416).
		return filepath.Join(agentRoot, ".opencode", "skills")
	case agent.ProviderCodex:
		// Codex follows Raft's split: CODEX_HOME and global skills remain
		// outside the agent workspace, while assigned skills are workspace-local.
		return filepath.Join(agentRoot, ".agents", "skills")
	case agent.ProviderPi:
		// Pi natively discovers skills from .pi/skills/ in the workdir.
		return filepath.Join(agentRoot, ".pi", "skills")
	case agent.ProviderCursor:
		// Cursor natively discovers skills from .cursor/skills/ in the workdir.
		return filepath.Join(agentRoot, ".cursor", "skills")
	case agent.ProviderKiro:
		// Kiro CLI auto-discovers project-level skills from .kiro/skills/
		// in the workdir.
		return filepath.Join(agentRoot, ".kiro", "skills")
	case agent.ProviderGrok:
		// Grok Build discovers project skills from .grok/skills/ (and
		// .grok/commands/) under the workdir.
		return filepath.Join(agentRoot, ".grok", "skills")
	default:
		// Fallback: write to .agent_context/skills/ (referenced by meta config).
		return filepath.Join(agentRoot, ".agent_context", "skills")
	}
}

var nonAlphaNum = regexp.MustCompile(`[^a-z0-9]+`)

// ensureSkillFrontmatter returns SKILL.md content guaranteed to lead with a
// YAML frontmatter block carrying a parseable, non-empty `name` key.
//
// Runtimes like OpenCode silently drop SKILL.md whose frontmatter is missing
// or whose `name` doesn't parse, so we handle three cases:
//
//   - No frontmatter at all → synthesize one with `name: <slug>` (and the DB
//     description when available).
//   - Frontmatter present, has a non-empty `name`, AND parses as valid YAML →
//     leave it untouched. The upstream import may have shaped that block
//     deliberately to match a specific runtime, and we don't want to clobber it.
//   - Frontmatter present and has a non-empty `name` but YAML is invalid (e.g.
//     unquoted colon in description) → strip and re-synthesize so runtimes like
//     Codex don't discard the skill on parse errors.
//   - Frontmatter present but missing `name` (e.g. an upstream skill whose
//     YAML only set `description`, with the directory slug filling in for
//     `name` at import time) → prepend `name: <slug>` as the first key of
//     the existing block so OpenCode can still route the skill.
func ensureSkillFrontmatter(content, slug, description string) string {
	fmStart, ok := frontmatterBodyStart(content)
	if !ok {
		return synthesizeFrontmatter(content, slug, description)
	}
	// Frontmatter exists and has a parseable name. If it's valid YAML, leave
	// it untouched so upstream-imported frontmatter survives round-trips.
	if hasFrontmatterName(content[fmStart:]) {
		if isFrontmatterValidYAML(content) {
			return content
		}
		// Frontmatter has a name but the YAML is invalid (e.g. unquoted
		// colon in the description). Strip and re-synthesize so runtimes
		// like Codex don't hard-reject the whole skill at load time.
		// frontmatterParts returns the full content as the body when it
		// can't find a closing delimiter, so the malformed block is kept
		// rather than silently dropped.
		_, body, _ := frontmatterParts(content)
		return synthesizeFrontmatter(body, slug, description)
	}
	// Frontmatter exists but lacks a parseable `name`. Inject one as the
	// first key of the existing block and keep the rest verbatim (including
	// `description`, body, and any runtime-specific keys the import path
	// preserved).
	return content[:fmStart] + "name: " + slug + "\n" + content[fmStart:]
}

// synthesizeFrontmatter produces a SKILL.md body with a YAML frontmatter block
// carrying at least `name` and (when non-empty) `description`. The description
// is always escaped as a double-quoted YAML string so values containing colons,
// brackets, or other YAML-significant characters parse safely.
func synthesizeFrontmatter(body, slug, description string) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", slug)
	if d := strings.TrimSpace(description); d != "" {
		fmt.Fprintf(&b, "description: %s\n", yamlEscapeInline(d))
	}
	b.WriteString("---\n\n")
	b.WriteString(body)
	return b.String()
}

// isFrontmatterValidYAML reports whether the opening YAML frontmatter block of
// content parses as a YAML mapping. Returns false when there is no frontmatter,
// the block has no closing delimiter, is empty, or unmarshalling fails.
func isFrontmatterValidYAML(content string) bool {
	fmBody, _, ok := frontmatterParts(content)
	if !ok || strings.TrimSpace(fmBody) == "" {
		return false
	}
	var m map[string]any
	return yaml.Unmarshal([]byte(fmBody), &m) == nil
}

// frontmatterParts splits content into the raw YAML frontmatter body (the text
// between the opening `---` line and the closing `---` line) and the document
// body that follows the closing delimiter. ok is false when content has no
// opening delimiter or no closing delimiter line; in that case body is the full
// content so callers can keep a malformed block instead of dropping it.
//
// A closing delimiter is a line whose only content is `---`, terminated by
// `\n`, `\r\n`, or end-of-file. Centralizing the rule here keeps the validity
// check and the re-synthesis path from disagreeing on where a block ends (e.g.
// for EOF- or CRLF-terminated frontmatter), which previously left a stale block
// behind when the two definitions diverged.
func frontmatterParts(content string) (fmBody, body string, ok bool) {
	start, ok := frontmatterBodyStart(content)
	if !ok {
		return "", content, false
	}
	rest := content[start:]
	for searchFrom := 0; ; {
		nl := strings.Index(rest[searchFrom:], "\n---")
		if nl < 0 {
			return "", content, false
		}
		closeAt := searchFrom + nl
		after := rest[closeAt+len("\n---"):]
		switch {
		case after == "" || after == "\r":
			return rest[:closeAt], "", true
		case strings.HasPrefix(after, "\n"):
			return rest[:closeAt], after[len("\n"):], true
		case strings.HasPrefix(after, "\r\n"):
			return rest[:closeAt], after[len("\r\n"):], true
		default:
			// Not a standalone delimiter line (e.g. "----" or "--- text");
			// keep scanning for the real close.
			searchFrom = closeAt + len("\n---")
		}
	}
}

// frontmatterBodyStart returns the byte offset where the YAML body begins
// (just after the opening `---` line) and whether a valid opening delimiter
// was found.
func frontmatterBodyStart(content string) (int, bool) {
	if strings.HasPrefix(content, "---\n") {
		return 4, true
	}
	if strings.HasPrefix(content, "---\r\n") {
		return 5, true
	}
	return 0, false
}

// hasFrontmatterName reports whether the frontmatter body (the slice starting
// just after the opening `---` line) contains a parseable, non-empty `name:`
// scalar before the closing `---`.
func hasFrontmatterName(fmBody string) bool {
	closeIdx := strings.Index(fmBody, "\n---")
	if closeIdx < 0 {
		// Missing close — scan everything we have and fall through. The
		// frontmatter is malformed and OpenCode will reject it anyway, but
		// detecting an existing name keeps us from layering a second one
		// on top.
		closeIdx = len(fmBody)
	}
	for _, line := range strings.Split(fmBody[:closeIdx], "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "name:") {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(line, "name:"))
		v = strings.Trim(v, `"'`)
		if v != "" {
			return true
		}
	}
	return false
}

// yamlEscapeInline returns a double-quoted YAML scalar that always parses as
// a string. Plain scalars are deliberately avoided: values like `[foo]`,
// `{x: y}`, `false`, `null`, or `2024-01-01` would parse as flow sequences,
// flow mappings, booleans, nulls, or timestamps under YAML 1.2, and
// OpenCode's frontmatter check rejects non-string descriptions outright. We
// flatten newlines (frontmatter values are single-line per key) and escape
// `\` and `"` so any input is a safe inline string.
func yamlEscapeInline(s string) string {
	flat := strings.ReplaceAll(s, "\r\n", " ")
	flat = strings.ReplaceAll(flat, "\n", " ")
	flat = strings.ReplaceAll(flat, "\r", " ")
	escaped := strings.ReplaceAll(flat, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}

// sanitizeSkillName converts a skill name to a safe directory name.
func sanitizeSkillName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = nonAlphaNum.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "skill"
	}
	return s
}

// writeSkillFiles writes skill directories into the given parent directory.
// Each skill gets its own subdirectory containing SKILL.md and supporting
// files. manifest, when non-nil, is populated with every newly-created
// directory and file so CleanupSidecars can refresh them without touching
// user-owned skill directories
// that happen to live alongside ours under the same skills/ parent.
//
// When a Multica skill's natural slug collides with a user-installed
// skill at the same path, we allocate a collision-free sibling slug
// (e.g. `issue-review-multica`) and write there instead. Provider-native
// discovery still picks it up because every subdir under skillsDir is a
// distinct skill; the user's original directory stays bit-for-bit
// intact. Without this fallback writeSkillFiles would have to either
// overwrite user bytes (the bug PR #3444 review caught) or skip the
// skill entirely (which would silently drop a Multica skill the agent
// expects to see).
func writeSkillFiles(skillsDir string, skills []SkillContextForEnv, manifest *sidecarManifest) error {
	if err := recordMkdirAll(skillsDir, 0o755, manifest); err != nil {
		return fmt.Errorf("create skills dir: %w", err)
	}

	for _, skill := range skills {
		baseSlug := sanitizeSkillName(skill.Name)
		slug, dir, err := allocateCollisionFreeSkillDir(skillsDir, baseSlug)
		if err != nil {
			return fmt.Errorf("allocate skill dir for %q: %w", skill.Name, err)
		}
		if err := recordMkdirAll(dir, 0o755, manifest); err != nil {
			return err
		}
		// Marker lets a later refresh reclaim this directory instead of
		// bumping to -multica-N.
		if err := recordWriteFile(filepath.Join(dir, managedSkillMarker), []byte(skill.Name+"\n"), 0o644, manifest); err != nil {
			return err
		}

		// ensureSkillFrontmatter synthesises a `name:` value when the
		// upstream skill is missing one. Use the chosen slug (which
		// may differ from baseSlug on collision) so the YAML name
		// matches the directory name; runtimes that key on either
		// stay consistent.
		body := ensureSkillFrontmatter(skill.Content, slug, skill.Description)
		if err := recordWriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644, manifest); err != nil {
			return err
		}

		// Write supporting files. The skill directory is collision-
		// free by construction, so a recordWriteFile collision under
		// it would mean the skill's bundled files list two entries
		// at the same path — that's an upstream data bug, not a
		// user-content collision, and we surface it.
		//
		// One common data bug is storing SKILL.md as both the primary
		// content (skill.Content) and as a supporting file. Skip the
		// duplicate so the agent still gets every unique file. The check
		// is canonical (see skillpkg.IsReservedContentPath) so a
		// non-canonical spelling like "./SKILL.md" — which filepath.Join
		// resolves onto the same dir/SKILL.md we just wrote — is caught
		// too, instead of colliding and failing prep with errPathPreExists.
		for _, f := range skill.Files {
			if skillpkg.IsReservedContentPath(f.Path) {
				continue
			}
			fpath, err := safeSkillFilePath(dir, f.Path)
			if err != nil {
				return fmt.Errorf("invalid supporting file path %q: %w", f.Path, err)
			}
			if err := recordMkdirAll(filepath.Dir(fpath), 0o755, manifest); err != nil {
				return err
			}
			if err := recordWriteFile(fpath, []byte(f.Content), 0o644, manifest); err != nil {
				return err
			}
		}
		reclaimManagedSkillCollisionSiblings(skillsDir, baseSlug, slug)
	}

	return nil
}

func safeSkillFilePath(root, rel string) (string, error) {
	rel = strings.TrimSpace(filepath.FromSlash(rel))
	if rel == "" || filepath.IsAbs(rel) {
		return "", errors.New("path must be relative")
	}
	clean := filepath.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes skill directory")
	}
	target := filepath.Join(root, clean)
	relToRoot, err := filepath.Rel(root, target)
	if err != nil || relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes skill directory")
	}
	return target, nil
}

// isChatLikeContext reports a durable Message runtime. Channel identity is
// deliberately not a selector: one Agent process can serve many channels and
// must not inherit a current channel, task, or session at startup.
func isChatLikeContext(ctx TaskContextForEnv) bool {
	return ctx.MessageDelivery
}

// renderIssueContext builds the markdown content for issue_context.md.
func renderIssueContext(provider string, ctx TaskContextForEnv) string {
	if ctx.AutopilotRunID != "" {
		return renderAutopilotContext(ctx)
	}
	if ctx.QuickCreatePrompt != "" {
		return renderQuickCreateContext(ctx)
	}
	if isChatLikeContext(ctx) {
		return renderChatWakeContext(ctx)
	}

	var b strings.Builder

	b.WriteString("# Task Assignment\n\n")
	fmt.Fprintf(&b, "**Issue ID:** %s\n\n", ctx.IssueID)

	if ctx.TriggerCommentID != "" {
		b.WriteString("**Trigger:** Comment Reply\n")
		b.WriteString("**Triggering comment ID:** `" + ctx.TriggerCommentID + "`\n\n")
	} else {
		b.WriteString("**Trigger:** New Assignment\n\n")
	}

	b.WriteString("## Quick Start\n\n")
	if ctx.TriggerCommentID == "" && ctx.AssignmentSnapshot != nil {
		b.WriteString("The current task prompt already includes the assignment snapshot and claim-time status. Start from that context; do not fetch the issue merely to repeat it.\n\n")
	} else {
		fmt.Fprintf(&b, "Run `multica issue get %s --output json` to fetch the full issue details.\n\n", ctx.IssueID)
	}

	writeAgentSkillsIndex(&b, ctx.AgentSkills)

	return b.String()
}

// renderChatWakeContext is the startup sidecar for a durable Message runtime.
// It contains no current channel, task, or session identity.
func renderChatWakeContext(ctx TaskContextForEnv) string {
	var b strings.Builder
	b.WriteString("# Message Runtime\n\n")
	b.WriteString("This durable Agent runtime receives canonical Message Deliveries. It has no current channel, task, lease, execution, or session identity.\n\n")
	b.WriteString("Use `multica message check` to inspect pending input, then use the returned canonical target for reads or sends when needed. Do not run `multica issue get` unless the user asks you to create or inspect an issue.\n\n")
	writeAgentSkillsIndex(&b, ctx.AgentSkills)
	return b.String()
}

// writeAgentSkillsIndex appends a progressive skill index (name + description).
// Full SKILL.md bodies live on disk; the agent must open matching files.
func writeAgentSkillsIndex(b *strings.Builder, skills []SkillContextForEnv) {
	if len(skills) == 0 {
		return
	}
	b.WriteString("## Agent Skills\n\n")
	b.WriteString("The following skills are available. When a name/description matches the task, open the corresponding `SKILL.md` and follow it:\n\n")
	for _, skill := range skills {
		if desc := strings.TrimSpace(skill.Description); desc != "" {
			fmt.Fprintf(b, "- **%s** — %s\n", skill.Name, desc)
		} else {
			fmt.Fprintf(b, "- **%s**\n", skill.Name)
		}
	}
	b.WriteString("\n")
}

// renderQuickCreateContext renders issue_context.md for quick-create tasks.
// This file carries only task data (user input, skills). Behavioral rules
// and guardrails live in AGENTS.md (runtime config) and the per-turn prompt
// to avoid redundancy and conflicting instructions.
func renderQuickCreateContext(ctx TaskContextForEnv) string {
	var b strings.Builder
	b.WriteString("# Quick Create\n\n")
	b.WriteString("**Trigger:** Quick-create modal\n\n")
	b.WriteString("## User input\n\n")
	b.WriteString("> ")
	b.WriteString(ctx.QuickCreatePrompt)
	b.WriteString("\n\n")
	if ctx.QuickCreateSource != nil {
		b.WriteString("## Source chat context\n\n")
		b.WriteString(renderQuickCreateSourceContext(ctx.QuickCreateSource))
		b.WriteString("\n\n")
	}
	writeAgentSkillsIndex(&b, ctx.AgentSkills)
	return b.String()
}

func renderQuickCreateSourceContext(src *protocol.QuickCreateSourceContext) string {
	if src == nil {
		return ""
	}
	var b strings.Builder
	if src.ChannelKind == "dm" {
		b.WriteString("- Source surface: DM thread\n")
	} else if strings.TrimSpace(src.ChannelName) != "" {
		fmt.Fprintf(&b, "- Source surface: channel #%s thread\n", src.ChannelName)
	} else {
		b.WriteString("- Source surface: channel thread\n")
	}
	fmt.Fprintf(&b, "- Channel ID: %s\n", src.ChannelID)
	fmt.Fprintf(&b, "- Thread root message ID: %s\n", src.ThreadRootMessageID)
	fmt.Fprintf(&b, "- Source message ID: %s\n", src.SourceMessageID)
	if src.SourceAuthorName != "" || src.SourceAuthorType != "" {
		fmt.Fprintf(&b, "- Source author: %s", src.SourceAuthorName)
		if src.SourceAuthorType != "" {
			fmt.Fprintf(&b, " (%s)", src.SourceAuthorType)
		}
		if src.SourceAuthorID != "" {
			fmt.Fprintf(&b, " [%s]", src.SourceAuthorID)
		}
		b.WriteString("\n")
	}
	if src.SourceExcerpt != "" {
		fmt.Fprintf(&b, "- Source excerpt: %s\n", src.SourceExcerpt)
	}
	if len(src.AttachmentIDs) > 0 {
		fmt.Fprintf(&b, "- Source attachment IDs: %s\n", strings.Join(src.AttachmentIDs, ", "))
	}
	if strings.TrimSpace(src.Summary) != "" {
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(src.Summary))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func renderAutopilotContext(ctx TaskContextForEnv) string {
	var b strings.Builder

	b.WriteString("# Autopilot Run\n\n")
	fmt.Fprintf(&b, "**Autopilot run ID:** %s\n\n", ctx.AutopilotRunID)
	if ctx.AutopilotID != "" {
		fmt.Fprintf(&b, "**Autopilot ID:** %s\n\n", ctx.AutopilotID)
	}
	if ctx.AutopilotTitle != "" {
		fmt.Fprintf(&b, "**Title:** %s\n\n", ctx.AutopilotTitle)
	}
	if ctx.AutopilotSource != "" {
		fmt.Fprintf(&b, "**Trigger source:** %s\n\n", ctx.AutopilotSource)
	}
	if ctx.AutopilotTriggerPayload != "" {
		fmt.Fprintf(&b, "## Trigger Payload\n\n```json\n%s\n```\n\n", ctx.AutopilotTriggerPayload)
	}

	b.WriteString("## Quick Start\n\n")
	b.WriteString("This is a run-only autopilot task with no assigned issue. Do not run `multica issue get` unless the autopilot instructions explicitly ask you to create or update an issue.\n\n")
	// Autopilot CLI retired (task #40): configuration is only what is already in this brief.
	b.WriteString("The Autopilot product is retired — there is no `multica autopilot` CLI. Use the instructions and payload in this brief only.\n\n")
	if strings.TrimSpace(ctx.AutopilotDescription) != "" {
		b.WriteString("## Autopilot Instructions\n\n")
		b.WriteString(ctx.AutopilotDescription)
		b.WriteString("\n\n")
	}

	writeAgentSkillsIndex(&b, ctx.AgentSkills)

	return b.String()
}
