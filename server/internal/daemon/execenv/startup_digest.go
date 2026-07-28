package execenv

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// StartupStaticContext returns the TaskContextForEnv subset used for
// provider-startup materialization (option A). Per-turn fields that belong in
// the Execute prompt are zeroed so they cannot force process recreation or
// pollute the startup AGENTS/context snapshot.
func StartupStaticContext(ctx TaskContextForEnv) TaskContextForEnv {
	out := ctx
	out.InitiatorType = ""
	out.InitiatorID = ""
	out.InitiatorName = ""
	out.InitiatorEmail = ""
	out.IssueID = ""
	out.TriggerCommentID = ""
	out.TriggerThreadID = ""
	out.NewCommentCount = 0
	out.NewCommentsSince = ""
	out.AssignmentSnapshot = nil
	// ChatSessionID / Directed / ChannelID / ManagedRole / memories / skills stay.
	return out
}

// StartupMaterializationPlan is the pure, zero-I/O render of everything the
// daemon will write on process create. Digest is over these exact bytes
// (产物即证据) — not a hand-picked field subset.
type StartupMaterializationPlan struct {
	Provider         string
	RuntimeBrief     string            // buildMetaSkillContent body (before marker wrap)
	IssueContext     string            // usually empty after StartupStaticContext
	SkillFiles       map[string]string // path under skills parent -> final file bytes
	ProjectResources string            // resources.json body or empty
}

// RenderStartupMaterializationPlan pure-renders the create-time snapshot.
// Prefer StartupStaticContext(ctx) as input.
func RenderStartupMaterializationPlan(provider string, ctx TaskContextForEnv) StartupMaterializationPlan {
	plan := StartupMaterializationPlan{
		Provider:     provider,
		RuntimeBrief: buildMetaSkillContent(provider, ctx),
		SkillFiles:   map[string]string{},
	}
	if strings.TrimSpace(ctx.IssueID) != "" || ctx.AssignmentSnapshot != nil {
		plan.IssueContext = renderIssueContext(provider, ctx)
	}
	if len(ctx.AgentSkills) > 0 && provider != "codex" {
		for _, sk := range ctx.AgentSkills {
			baseSlug := sanitizeSkillName(sk.Name)
			// Pure plan uses natural slug (collision allocation needs FS).
			// Content bytes match writeSkillFiles after frontmatter ensure.
			body := ensureSkillFrontmatter(sk.Content, baseSlug, sk.Description)
			plan.SkillFiles[baseSlug+"/SKILL.md"] = body
			plan.SkillFiles[baseSlug+"/"+managedSkillMarker] = sk.Name + "\n"
			for _, f := range sk.Files {
				rel := strings.TrimPrefix(filepath.ToSlash(f.Path), "/")
				if rel == "" || rel == "SKILL.md" {
					continue
				}
				plan.SkillFiles[baseSlug+"/"+rel] = f.Content
			}
		}
	}
	if ctx.ProjectID != "" || len(ctx.ProjectResources) > 0 {
		resources := ctx.ProjectResources
		if resources == nil {
			resources = []ProjectResourceForEnv{}
		}
		data, err := json.MarshalIndent(projectResourceFile{
			ProjectID: ctx.ProjectID, ProjectTitle: ctx.ProjectTitle, Resources: resources,
		}, "", "  ")
		if err == nil {
			plan.ProjectResources = string(data) + "\n"
		}
	}
	return plan
}

// Digest returns sha256 of the canonical encoding of the plan.
func (p StartupMaterializationPlan) Digest() string {
	type fileEntry struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	type wire struct {
		Provider         string      `json:"provider"`
		RuntimeBrief     string      `json:"runtime_brief"`
		IssueContext     string      `json:"issue_context,omitempty"`
		SkillFiles       []fileEntry `json:"skill_files,omitempty"`
		ProjectResources string      `json:"project_resources,omitempty"`
	}
	w := wire{
		Provider:         p.Provider,
		RuntimeBrief:     p.RuntimeBrief,
		IssueContext:     p.IssueContext,
		ProjectResources: p.ProjectResources,
	}
	for path, content := range p.SkillFiles {
		w.SkillFiles = append(w.SkillFiles, fileEntry{Path: path, Content: content})
	}
	sort.Slice(w.SkillFiles, func(i, j int) bool { return w.SkillFiles[i].Path < w.SkillFiles[j].Path })
	raw, err := json.Marshal(w)
	if err != nil {
		return fmt.Sprintf("sha256:error:%s", strings.TrimSpace(err.Error()))
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// StartupStaticDigest pure-renders the real create-time plan and digests it.
func StartupStaticDigest(provider string, ctx TaskContextForEnv) string {
	return RenderStartupMaterializationPlan(provider, StartupStaticContext(ctx)).Digest()
}
