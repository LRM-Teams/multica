package execenv

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// StartupStaticContext returns the TaskContextForEnv subset used for
// create-time AGENTS materialization. Per-turn fields that belong in the
// Execute prompt are zeroed so they cannot force process recreation or
// pollute the startup AGENTS snapshot.
//
// Slim D6-1b: AGENTS is agent-level static only. Chat surface, directed flag,
// initiator, issue, project/resources, and channel/project memory dirs are
// per-turn (prompt / CLI), not startup disk.
func StartupStaticContext(ctx TaskContextForEnv) TaskContextForEnv {
	out := ctx
	// Per-turn surface / speaker / issue
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
	out.ChatSessionID = ""
	out.ChannelID = ""
	out.Directed = false
	// Task-scoped project surface (Barry allowlist: per-turn)
	out.ProjectID = ""
	out.ProjectTitle = ""
	out.ProjectResources = nil
	out.ProjectMemoryDir = ""
	out.ChannelMemoryDir = ""
	out.Repos = nil
	out.SkillDirSlugByName = nil
	// Skills: names/descriptions may stay in the brief index for lazy load;
	// we do NOT write skill package files to workdir. Keep AgentSkills for
	// index rendering in the managed brief.
	return out
}

// StartupMaterializationPlan is the pure, zero-I/O render of create-time AGENTS.
// Slim: only RuntimeBrief — no skill files, issue_context, or resources.json.
type StartupMaterializationPlan struct {
	Provider     string
	RuntimeBrief string // buildMetaSkillContent body (before marker wrap)
}

// RenderStartupMaterializationPlan pure-renders the create-time AGENTS brief.
// Prefer StartupStaticContext(ctx) as input.
func RenderStartupMaterializationPlan(provider string, ctx TaskContextForEnv) StartupMaterializationPlan {
	return StartupMaterializationPlan{
		Provider:     provider,
		RuntimeBrief: buildMetaSkillContent(provider, ctx),
	}
}

// Digest returns sha256 of the canonical encoding of the plan.
func (p StartupMaterializationPlan) Digest() string {
	type wire struct {
		Provider     string `json:"provider"`
		RuntimeBrief string `json:"runtime_brief"`
	}
	raw, err := json.Marshal(wire{
		Provider:     p.Provider,
		RuntimeBrief: p.RuntimeBrief,
	})
	if err != nil {
		return fmt.Sprintf("sha256:error:%s", strings.TrimSpace(err.Error()))
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// StartupStaticDigest is the fingerprint alias for ManagedStartupInputDigest.
func StartupStaticDigest(provider string, ctx TaskContextForEnv) string {
	return ManagedStartupInputDigest(provider, ctx)
}
