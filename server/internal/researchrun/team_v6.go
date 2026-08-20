package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrV6TeamLimit             = errors.New("research V6 team limit reached")
	ErrV6CapacityReasonMissing = errors.New("research V6 capacity reason required")
)

type V6TeamMemberState string

const (
	V6TeamIdle     V6TeamMemberState = "idle"
	V6TeamWorking  V6TeamMemberState = "working"
	V6TeamOffline  V6TeamMemberState = "offline"
	V6TeamRetiring V6TeamMemberState = "retiring"
	V6TeamArchived V6TeamMemberState = "archived"
	V6TeamFailed   V6TeamMemberState = "failed"
)

type V6TeamMember struct {
	ID               string            `json:"id"`
	WorkspaceID      string            `json:"workspace_id"`
	RunID            string            `json:"run_id"`
	AgentID          string            `json:"agent_id"`
	DirectorCycleID  string            `json:"director_cycle_id,omitempty"`
	Generation       int               `json:"generation"`
	MissionRevision  int               `json:"mission_revision"`
	MissionPrompt    string            `json:"mission_prompt"`
	MissionHash      string            `json:"mission_hash"`
	ModelConfig      json.RawMessage   `json:"model_config"`
	ToolConfig       json.RawMessage   `json:"tool_config"`
	PermissionConfig json.RawMessage   `json:"permission_config"`
	State            V6TeamMemberState `json:"state"`
}

type AddV6TeamMemberInput struct {
	WorkspaceID, RunID, AgentID, DirectorCycleID, FormationDecisionID string
	MissionPrompt, CapacityReason                                     string
	ModelConfig, ToolConfig, PermissionConfig                         json.RawMessage
}

type ArchiveV6TeamMemberInput struct {
	WorkspaceID, RunID, MembershipID, Reason string
}

type teamV6Store interface {
	AddV6TeamMember(context.Context, AddV6TeamMemberInput) (V6TeamMember, error)
	ArchiveV6TeamMember(context.Context, ArchiveV6TeamMemberInput) (V6TeamMember, error)
	// FindActiveV6TeamMemberByAgent reports the agent's current non-terminal
	// membership, if any. Outbox redelivery uses it to converge instead of
	// minting a duplicate membership for an already-onboarded agent.
	FindActiveV6TeamMemberByAgent(ctx context.Context, workspaceID, runID, agentID string) (V6TeamMember, bool, error)
}

type teamV6Module struct{ store teamV6Store }

func (m teamV6Module) Add(ctx context.Context, in AddV6TeamMemberInput) (V6TeamMember, error) {
	if m.store == nil || strings.TrimSpace(in.WorkspaceID) == "" || strings.TrimSpace(in.RunID) == "" ||
		strings.TrimSpace(in.AgentID) == "" || strings.TrimSpace(in.MissionPrompt) == "" {
		return V6TeamMember{}, fmt.Errorf("%w: incomplete team member", ErrInvalidContract)
	}
	return m.store.AddV6TeamMember(ctx, in)
}

func (m teamV6Module) Archive(ctx context.Context, in ArchiveV6TeamMemberInput) (V6TeamMember, error) {
	if m.store == nil || strings.TrimSpace(in.MembershipID) == "" || strings.TrimSpace(in.Reason) == "" {
		return V6TeamMember{}, fmt.Errorf("%w: archive reason is required", ErrInvalidContract)
	}
	return m.store.ArchiveV6TeamMember(ctx, in)
}

type V6AgentSpec struct {
	Name, Capability, MissionPrompt string
	ModelConfig, ToolConfig         json.RawMessage
}

type AgentLifecycleAdapter interface {
	CreateAgent(context.Context, string, string, V6AgentSpec) (string, error)
	ArchiveAgent(context.Context, string, string, string) error
}

type InboxDispatchAdapter interface {
	DispatchV6Work(context.Context, V6AttemptAccess, V6WorkManifest, string) (string, error)
}
