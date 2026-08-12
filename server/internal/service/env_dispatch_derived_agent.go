package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// CloneEnvDispatchAgentInput identifies the source agent to clone for an
// env-dispatch binding and the online runtime the derived agent will bind to.
// See openspec change env-dispatch-agent-runtime-config Task 5.1 (derived-agent
// clone service).
type CloneEnvDispatchAgentInput struct {
	WorkspaceID    string
	SourceAgentID  string
	RuntimeID      string
	EnvID          string
	ChannelID      string
	BindingID      string
	ExecutionModel string
}

// SourceAgent is the approved executable configuration copied from the source
// agent during derivation. It carries only non-secret executable fields (name,
// instructions, provider-visible settings as opaque canonical JSON) - never
// credentials, task state, or the source's runtime ownership. The production
// adapter maps the agent row to this shape inside the clone transaction.
type SourceAgent struct {
	WorkspaceID    string
	ID             string
	Name           string
	Instructions   string
	ApprovedConfig json.RawMessage
}

// CreateDerivedAgentInput is the derived agent the clone transaction inserts:
// the source's approved executable configuration plus source_agent_id lineage
// and the discovered runtime_id binding. The source agent is never mutated.
type CreateDerivedAgentInput struct {
	WorkspaceID    string
	SourceAgentID  string
	RuntimeID      string
	Name           string
	Instructions   string
	ApprovedConfig json.RawMessage
	ExecutionModel string
}

// CloneDeps performs the transactional steps of derived-agent creation. The
// production adapter MUST run all methods in a single database transaction so a
// failure in any step rolls back the derived agent, skills copy, channel member
// swap, and binding update atomically. Wired to raw SQL (the
// CreateDerivedEnvDispatchAgent sqlc method is absent pending the deferred
// pkg/db/generated regen); until then this interface lets the clone logic be
// built and tested in isolation, mirroring the RuntimeLookup pattern.
type CloneDeps interface {
	// LoadSourceAgent reads the source agent's approved executable configuration
	// and verifies it belongs to the workspace. Read-only: the source is never
	// mutated by the clone.
	LoadSourceAgent(ctx context.Context, workspaceID, sourceAgentID string) (SourceAgent, error)
	// CreateDerivedAgent inserts a new global agent that copies the source's
	// approved configuration, records source_agent_id lineage, and binds to the
	// discovered runtime_id. Returns the derived agent ID.
	CreateDerivedAgent(ctx context.Context, in CreateDerivedAgentInput) (string, error)
	// CopyApprovedSkills copies the source agent's approved skills to the derived
	// agent. Source skills are read, never mutated.
	CopyApprovedSkills(ctx context.Context, workspaceID, sourceAgentID, derivedAgentID string) error
	// ReplaceDispatchChannelMember replaces an existing source-owned dispatch
	// channel session with the derived agent. The canonical channel_member stays
	// source-keyed so future user mentions resolve through the binding.
	ReplaceDispatchChannelMember(ctx context.Context, channelID, sourceAgentID, derivedAgentID string) error
	// SetBindingDerivedAgent persists the derived agent ID on the claimed
	// env-dispatch binding so subsequent dispatches route to the derived agent.
	SetBindingDerivedAgent(ctx context.Context, bindingID, derivedAgentID string) error
}

// CloneEnvDispatchAgent creates a new global agent derived from the addressed
// source agent and permanently bound to the discovered online runtime for the
// dispatch lifetime. It copies approved executable configuration and skills,
// records source_agent_id lineage, replaces the source member within the
// env-dispatch channel, and records the derived ID on the binding. The source
// agent, its global runtime, and its non-dispatch memberships are never
// modified - the CloneDeps interface exposes no source-mutating method, so
// source immutability is structural.
//
// Fails closed on workspace mismatch and missing identity. Errors are surfaced
// for the orchestration layer to compensate; atomicity across steps is the
// adapter's transaction responsibility. No secret enters this path. See openspec
// change env-dispatch-agent-runtime-config Task 5.1.
func CloneEnvDispatchAgent(ctx context.Context, q CloneDeps, in CloneEnvDispatchAgentInput) (string, error) {
	if in.WorkspaceID == "" || in.SourceAgentID == "" || in.RuntimeID == "" || in.BindingID == "" {
		return "", fmt.Errorf("validation_failed: workspace_id, source_agent_id, runtime_id, and binding_id are required for derived-agent clone")
	}
	src, err := q.LoadSourceAgent(ctx, in.WorkspaceID, in.SourceAgentID)
	if err != nil {
		return "", fmt.Errorf("load source agent: %w", err)
	}
	if src.WorkspaceID != in.WorkspaceID {
		return "", fmt.Errorf("source agent workspace mismatch")
	}
	derivedID, err := q.CreateDerivedAgent(ctx, CreateDerivedAgentInput{
		WorkspaceID:    in.WorkspaceID,
		SourceAgentID:  in.SourceAgentID,
		RuntimeID:      in.RuntimeID,
		Name:           envDispatchDerivedAgentName(in.BindingID),
		Instructions:   src.Instructions,
		ApprovedConfig: src.ApprovedConfig,
		ExecutionModel: strings.TrimSpace(in.ExecutionModel),
	})
	if err != nil {
		return "", fmt.Errorf("create derived agent: %w", err)
	}
	if err := q.CopyApprovedSkills(ctx, in.WorkspaceID, in.SourceAgentID, derivedID); err != nil {
		return "", fmt.Errorf("copy approved skills: %w", err)
	}
	if err := q.ReplaceDispatchChannelMember(ctx, in.ChannelID, in.SourceAgentID, derivedID); err != nil {
		return "", fmt.Errorf("replace dispatch channel member: %w", err)
	}
	if err := q.SetBindingDerivedAgent(ctx, in.BindingID, derivedID); err != nil {
		return "", fmt.Errorf("set binding derived agent: %w", err)
	}
	return derivedID, nil
}

// envDispatchDerivedAgentName is stable for one canonical binding and unique
// within a workspace. It deliberately does not copy the source name because
// agent_workspace_name_unique rejects two agents with the same name.
func envDispatchDerivedAgentName(bindingID string) string {
	return "env-" + bindingID
}

// ValidateMixedDispatchProvisionedBindings verifies every side-effecting
// source/derived/runtime/provider binding before native Pi preparation. Native
// Pi session identity and capture boundary are deliberately allocated by
// PrepareMixedDispatchRunAgent after this validation succeeds.
func ValidateMixedDispatchProvisionedBindings(plan MixedDispatchPlan, agents []MixedDispatchRunAgent) error {
	return validateMixedDispatchProvisionedAgents(plan, agents, false)
}

// ValidateMixedDispatchProvisionedAgents verifies the complete side-effecting
// mixed preflight set after native Pi preparation, including one fresh Pi
// session per run agent.
func ValidateMixedDispatchProvisionedAgents(plan MixedDispatchPlan, agents []MixedDispatchRunAgent) error {
	return validateMixedDispatchProvisionedAgents(plan, agents, true)
}

func validateMixedDispatchProvisionedAgents(plan MixedDispatchPlan, agents []MixedDispatchRunAgent, requirePiSession bool) error {
	if len(agents) != len(plan.RunAgents) {
		return fmt.Errorf("failed_preflight: provisioned run-agent count does not match roster")
	}
	expected := make(map[string]string, len(plan.RunAgents))
	trainable := false
	for _, agent := range plan.RunAgents {
		if agent.SourceAgentID == "" {
			return fmt.Errorf("failed_preflight: planned source agent identity is required")
		}
		switch agent.TrainingMode {
		case "online_rl", "offline_rl", "none":
		default:
			return fmt.Errorf("failed_preflight: unsupported training mode %q for agent %s", agent.TrainingMode, agent.SourceAgentID)
		}
		if _, duplicate := expected[agent.SourceAgentID]; duplicate {
			return fmt.Errorf("failed_preflight: duplicate planned source agent %s", agent.SourceAgentID)
		}
		expected[agent.SourceAgentID] = agent.TrainingMode
		trainable = trainable || agent.TrainingMode != "none"
	}
	if trainable && (strings.TrimSpace(plan.TargetPolicy) == "" || strings.TrimSpace(plan.Tokenizer) == "") {
		return fmt.Errorf("failed_preflight: common target policy and tokenizer are required")
	}

	seenSources := make(map[string]struct{}, len(agents))
	seenExecutions := make(map[string]struct{}, len(agents))
	seenSessions := make(map[string]struct{}, len(agents))
	for _, agent := range agents {
		mode, ok := expected[agent.SourceAgentID]
		if !ok {
			return fmt.Errorf("failed_preflight: provisioned source agent %s is not in roster", agent.SourceAgentID)
		}
		if _, duplicate := seenSources[agent.SourceAgentID]; duplicate {
			return fmt.Errorf("failed_preflight: duplicate provisioned source agent %s", agent.SourceAgentID)
		}
		seenSources[agent.SourceAgentID] = struct{}{}
		if agent.TrainingMode != mode {
			return fmt.Errorf("failed_preflight: training classification changed for agent %s", agent.SourceAgentID)
		}
		if agent.ExecutionAgentID == "" || agent.ExecutionAgentID == agent.SourceAgentID {
			return fmt.Errorf("failed_preflight: source agent %s has no derived execution binding", agent.SourceAgentID)
		}
		if _, duplicate := seenExecutions[agent.ExecutionAgentID]; duplicate {
			return fmt.Errorf("failed_preflight: derived execution agent %s is reused", agent.ExecutionAgentID)
		}
		seenExecutions[agent.ExecutionAgentID] = struct{}{}
		if agent.RuntimeID == "" {
			return fmt.Errorf("failed_preflight: runtime is not ready for agent %s", agent.SourceAgentID)
		}
		if requirePiSession {
			if agent.PiSessionID == "" {
				return fmt.Errorf("failed_preflight: Pi session is required for agent %s", agent.SourceAgentID)
			}
			if _, duplicate := seenSessions[agent.PiSessionID]; duplicate {
				return fmt.Errorf("failed_preflight: Pi session is reused for agent %s", agent.SourceAgentID)
			}
			seenSessions[agent.PiSessionID] = struct{}{}
		}
		if mode == "online_rl" && agent.AReALSessionID == "" {
			return fmt.Errorf("failed_preflight: online provider session is required for agent %s", agent.SourceAgentID)
		}
		if mode != "online_rl" && agent.AReALSessionID != "" {
			return fmt.Errorf("failed_preflight: non-online agent %s has an online provider session", agent.SourceAgentID)
		}
	}
	return nil
}
