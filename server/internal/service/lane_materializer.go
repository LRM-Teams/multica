// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"fmt"
)

// LaneMaterializerDeps is the narrow set of control-plane operations one lane
// needs. Sandbox creation and env/project copying already exist as service
// primitives; the runtime step is handler-owned because it writes chat_session,
// channel_agent_session and the derived agent in the same shape the branch
// provisioning path does.
type LaneMaterializerDeps interface {
	CreateSandboxInstance(ctx context.Context, in CreateSandboxInstanceInput, actorUserID string) (SandboxInstanceRef, error)
	CreateEnv(ctx context.Context, workspaceID string, sandboxIDs []string, parentEnvID string, mode EnvMode, domain EnvDomain) (envID string, err error)
	CopyProjectSubtree(ctx context.Context, sourceProjectID, workspaceID, envID string) (newProjectID string, issueIDMap, chatSessionIDMap map[string]string, err error)
	// ProvisionLaneAgentRuntime attaches an execution identity to an existing
	// lane sandbox: it discovers the runtime the lane's daemon registered,
	// derives the executing agent, and opens that agent's session on the lane's
	// channel. It never creates a channel -- the lane is given one.
	ProvisionLaneAgentRuntime(ctx context.Context, in LaneAgentProvisionInput) (LaneBinding, error)
}

// LaneAgentProvisionInput is the lane identity the runtime step acts on. Every
// id here is already recorded on the lane row, so a retry after a crash asks for
// exactly the same thing.
type LaneAgentProvisionInput struct {
	WorkspaceID string
	ActorUserID string
	LaneKey     string
	// AgentID is the addressed source agent; the executing agent is derived from
	// it by the deps implementation, mirroring branch provisioning.
	AgentID    string
	InstanceID string
	ProjectID  string
	EnvID      string
	ChannelID  string
}

type laneMaterializer struct {
	deps LaneMaterializerDeps
}

// NewLaneMaterializer builds the production LaneMaterializer.
func NewLaneMaterializer(deps LaneMaterializerDeps) LaneMaterializer {
	return &laneMaterializer{deps: deps}
}

// CreateLaneInstance boots the lane from the savepoint's Cube template. This is
// the point of the consolidation on this path: the lane starts from the captured
// filesystem state instead of the source's original template (which loses the
// work) or a live clone of a still-running source.
func (m *laneMaterializer) CreateLaneInstance(ctx context.Context, in LaneInstanceInput) (SandboxInstanceRef, error) {
	if in.WorkspaceID == "" {
		return SandboxInstanceRef{}, fmt.Errorf("lane instance: workspace id required")
	}
	// A savepoint that is not ready has no state to boot from, and one with no
	// Cube template would create against the node default: a healthy-looking
	// sandbox that silently lost the captured state. Both are reported as
	// ErrSavepointGone so the caller retires the savepoint instead of retrying
	// a lane that can never succeed.
	if in.Savepoint.Status != savepointStatusReady {
		return SandboxInstanceRef{}, fmt.Errorf("%w: savepoint %s is %s",
			ErrSavepointGone, in.Savepoint.SnapshotID, in.Savepoint.Status)
	}
	if in.Savepoint.CubeSnapshotID == "" {
		return SandboxInstanceRef{}, fmt.Errorf("%w: savepoint %s has no cube template",
			ErrSavepointGone, in.Savepoint.SnapshotID)
	}
	ref, err := m.deps.CreateSandboxInstance(ctx, CreateSandboxInstanceInput{
		WorkspaceID: in.WorkspaceID,
		Template:    in.Savepoint.CubeSnapshotID,
		Name:        laneSandboxName(in.LaneKey),
		// The continuation is routed to the lane's in-sandbox daemon, so a lane
		// booted without one has no runtime to re-engage.
		DaemonEnabled: true,
	}, in.ActorUserID)
	if err != nil {
		return SandboxInstanceRef{}, fmt.Errorf("lane %q: create from savepoint %s: %w",
			in.LaneKey, in.Savepoint.SnapshotID, err)
	}
	return ref, nil
}

// CopyLaneProjectSubtree gives the lane its own copy of the source project. The
// env is reserved with no sandboxes and the lane's instance is attached by the
// caller once provisioning succeeds, so a half-materialized lane never points an
// env at the source's sandbox.
//
// The lane env is created without a parent and in the self-play domain because a
// checkpoint does not record the env it was taken from. Branch dispatch never
// reaches this step -- its env already exists with the right lineage (design D6)
// -- so the gap only affects standalone fan-out, and is closed by the same
// checkpoint columns design D8 adds for the source conversation.
func (m *laneMaterializer) CopyLaneProjectSubtree(ctx context.Context, in LaneProjectInput) (string, string, error) {
	if in.WorkspaceID == "" {
		return "", "", fmt.Errorf("lane subtree: workspace id required")
	}
	if in.SourceProjectID == "" {
		return "", "", fmt.Errorf("lane %q subtree: source project id required", in.LaneKey)
	}
	envID, err := m.deps.CreateEnv(ctx, in.WorkspaceID, nil, "", EnvModeBranch, EnvDomainSelfPlay)
	if err != nil {
		return "", "", fmt.Errorf("lane %q: create env: %w", in.LaneKey, err)
	}
	projectID, _, _, err := m.deps.CopyProjectSubtree(ctx, in.SourceProjectID, in.WorkspaceID, envID)
	if err != nil {
		return "", "", fmt.Errorf("lane %q: copy project subtree: %w", in.LaneKey, err)
	}
	return projectID, envID, nil
}

// ProvisionLaneAgent attaches the lane's execution identity to its sandbox and
// opens the agent's session on the lane's own channel.
func (m *laneMaterializer) ProvisionLaneAgent(ctx context.Context, in LaneRuntimeInput) (LaneBinding, error) {
	if in.WorkspaceID == "" {
		return LaneBinding{}, fmt.Errorf("lane runtime: workspace id required")
	}
	if in.InstanceID == "" {
		return LaneBinding{}, fmt.Errorf("lane %q runtime: sandbox instance required", in.LaneKey)
	}
	if in.AgentID == "" {
		return LaneBinding{}, fmt.Errorf("lane %q runtime: source agent required", in.LaneKey)
	}
	if in.ProjectID == "" {
		return LaneBinding{}, fmt.Errorf("lane %q runtime: project required", in.LaneKey)
	}
	// Design D8: a lane must continue its own conversation. Reusing the source's
	// channel would make every lane post into one thread, and minting the lane a
	// channel of its own needs the source conversation's identity and roster,
	// which a checkpoint does not record yet. Until it does, a lane without a
	// pre-seeded channel is refused rather than quietly collapsed onto a shared
	// one.
	if in.ChannelID == "" {
		return LaneBinding{}, fmt.Errorf("%w: lane %q has no channel",
			ErrLaneConversationUnavailable, in.LaneKey)
	}
	binding, err := m.deps.ProvisionLaneAgentRuntime(ctx, LaneAgentProvisionInput{
		WorkspaceID: in.WorkspaceID,
		ActorUserID: in.ActorUserID,
		LaneKey:     in.LaneKey,
		AgentID:     in.AgentID,
		InstanceID:  in.InstanceID,
		ProjectID:   in.ProjectID,
		EnvID:       in.EnvID,
		ChannelID:   in.ChannelID,
	})
	if err != nil {
		return LaneBinding{}, fmt.Errorf("lane %q: provision agent runtime: %w", in.LaneKey, err)
	}
	return binding, nil
}

func laneSandboxName(laneKey string) string {
	if laneKey == "" {
		return "env-checkpoint-lane"
	}
	return "lane-" + laneKey
}
