package execenv

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// StartupStaticContext returns the positive allowlist of durable agent×runtime
// facts used for create-time AGENTS materialization.
//
// Constructed empty then filled — never "copy whole TaskContextForEnv and zero
// some fields" (Barry #1274 CODE blocker: leak of FreshSession / user memory /
// autopilot / quick-create / radar / squad-leader into digest + brief).
//
// Everything not listed here is per-turn prompt/env and must not restart the
// resident process or land in the managed AGENTS block.
func StartupStaticContext(ctx TaskContextForEnv) TaskContextForEnv {
	// Skills: keep name/description (+ content unused by index) for the brief
	// skill index. Files are NOT written to user workdir on the slim path.
	skills := make([]SkillContextForEnv, 0, len(ctx.AgentSkills))
	for _, s := range ctx.AgentSkills {
		skills = append(skills, SkillContextForEnv{
			Name:        s.Name,
			Description: s.Description,
			// Content intentionally omitted from startup digest input: slim
			// does not materialize SKILL.md under CWD; progressive load uses
			// durable mirror. Description changes still rotate digest via
			// brief text.
		})
	}
	return TaskContextForEnv{
		AgentID:           strings.TrimSpace(ctx.AgentID),
		AgentName:         strings.TrimSpace(ctx.AgentName),
		AgentInstructions: ctx.AgentInstructions,
		AgentRoot:         strings.TrimSpace(ctx.AgentRoot),
		AgentSkills:       skills,
		// Workspace-level standing context (not issue/chat turn)
		WorkspaceContext: ctx.WorkspaceContext,
		// ManagerChannels is intentionally excluded: it no longer renders into
		// the create-time brief (daemon.currentStateOverlay is now the sole
		// source, refreshed every wake), so it must not rotate/recreate the
		// resident process either — that would just be an expensive no-op.
		// Runtime-owner profile is process-stable for this agent binding.
		RequestingUserName:               ctx.RequestingUserName,
		RequestingUserProfileDescription: ctx.RequestingUserProfileDescription,
	}
}

// StartupMaterializationPlan is the pure, zero-I/O render of create-time AGENTS.
// Slim: only RuntimeBrief — no skill files, issue_context, or resources.json.
type StartupMaterializationPlan struct {
	Provider     string
	RuntimeBrief string // buildStartupKernelContent body (before marker wrap)
}

// RenderStartupMaterializationPlan pure-renders the create-time AGENTS brief.
// Prefer StartupStaticContext(ctx) as input.
func RenderStartupMaterializationPlan(provider string, ctx TaskContextForEnv) StartupMaterializationPlan {
	return StartupMaterializationPlan{
		Provider:     provider,
		RuntimeBrief: buildStartupKernelContent(provider, ctx),
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

// StartupStaticDigest is the alias for ManagedStartupInputDigest.
func StartupStaticDigest(provider string, ctx TaskContextForEnv) string {
	return ManagedStartupInputDigest(provider, ctx)
}
