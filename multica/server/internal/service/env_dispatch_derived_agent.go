package service

import (
	"context"
	"encoding/json"
	"fmt"
)

// CloneEnvDispatchAgentInput identifies the source agent to clone for an
// env-dispatch binding and the online runtime the derived agent will bind to.
// See openspec change env-dispatch-agent-runtime-config Task 5.1 (derived-agent
// clone service).
type CloneEnvDispatchAgentInput struct {
	WorkspaceID   string
	SourceAgentID string
	RuntimeID     string
	EnvID         string
	ChannelID     string
	BindingID     string
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
