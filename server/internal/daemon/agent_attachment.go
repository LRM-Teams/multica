package daemon

import (
	"errors"
	"fmt"
	"strings"
)

// AttachmentGeneration fences durable Agent-to-Computer responsibility. It is
// not a process generation, launch identity, Activity sequence, or Message
// sequence.
type AttachmentGeneration int64

// AttachmentLifecycleSequence is the replay cursor for one Runtime's ordered
// Attachment events. It is distinct from AttachmentGeneration.
type AttachmentLifecycleSequence int64

// AgentAttachment is the durable fact that this Computer handles an Agent for
// one Workspace and Runtime. Attachment does not imply that an Agent process
// has been started or is currently running.
type AgentAttachment struct {
	WorkspaceID          string
	AgentID              string
	RuntimeID            string
	AttachmentGeneration AttachmentGeneration
}

func (attachment AgentAttachment) Validate() error {
	if strings.TrimSpace(attachment.WorkspaceID) == "" || strings.TrimSpace(attachment.AgentID) == "" || strings.TrimSpace(attachment.RuntimeID) == "" {
		return errors.New("Agent Attachment Workspace, Agent, and Runtime identities are required")
	}
	if attachment.AttachmentGeneration < 1 {
		return errors.New("Agent Attachment generation is required")
	}
	return nil
}

type AgentAttachmentEventKind string

const (
	AgentAttachmentEventAttach AgentAttachmentEventKind = "attach"
	AgentAttachmentEventDetach AgentAttachmentEventKind = "detach"
)

func (kind AgentAttachmentEventKind) Validate() error {
	switch kind {
	case AgentAttachmentEventAttach, AgentAttachmentEventDetach:
		return nil
	default:
		return fmt.Errorf("unknown Agent Attachment event kind %q", kind)
	}
}

// AgentAttachmentEvent deliberately omits WorkspaceID. The authenticated
// Workspace scope is a separate AgentAttachmentRegistry method argument and
// cannot be supplied or changed by the event payload.
type AgentAttachmentEvent struct {
	Kind                 AgentAttachmentEventKind
	AgentID              string
	RuntimeID            string
	AttachmentGeneration AttachmentGeneration
	LifecycleSeq         AttachmentLifecycleSequence
}

func (event AgentAttachmentEvent) Validate() error {
	if err := event.Kind.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(event.AgentID) == "" || strings.TrimSpace(event.RuntimeID) == "" {
		return errors.New("Agent Attachment event Agent and Runtime identities are required")
	}
	if event.AttachmentGeneration < 1 {
		return errors.New("Agent Attachment event generation is required")
	}
	if event.LifecycleSeq < 1 {
		return errors.New("Agent Attachment lifecycle sequence is required")
	}
	return nil
}

type AgentAttachmentChangeKind string

const (
	AgentAttachmentUnchanged AgentAttachmentChangeKind = "unchanged"
	AgentAttachmentAttached  AgentAttachmentChangeKind = "attached"
	AgentAttachmentMoved     AgentAttachmentChangeKind = "moved"
	AgentAttachmentDetached  AgentAttachmentChangeKind = "detached"
)

func (kind AgentAttachmentChangeKind) Validate() error {
	switch kind {
	case AgentAttachmentUnchanged, AgentAttachmentAttached, AgentAttachmentMoved, AgentAttachmentDetached:
		return nil
	default:
		return fmt.Errorf("unknown Agent Attachment change kind %q", kind)
	}
}

// AgentAttachmentChange reports the semantic result of Apply. Callers use the
// Kind and values instead of inspecting registry maps or reproducing generation
// comparisons.
type AgentAttachmentChange struct {
	Kind     AgentAttachmentChangeKind
	Previous AgentAttachment
	Current  AgentAttachment
}

// AgentAttachmentRecoveryCursor is one Runtime's last durably applied event.
type AgentAttachmentRecoveryCursor struct {
	RuntimeID    string
	LifecycleSeq AttachmentLifecycleSequence
}

func (cursor AgentAttachmentRecoveryCursor) Validate() error {
	if strings.TrimSpace(cursor.RuntimeID) == "" || cursor.LifecycleSeq < 1 {
		return errors.New("Agent Attachment recovery Runtime and lifecycle sequence are required")
	}
	return nil
}

// AgentAttachmentRecoveryState is a value snapshot. Implementations must not
// expose their mutable cursor maps through it.
type AgentAttachmentRecoveryState struct {
	WorkspaceID string
	Cursors     []AgentAttachmentRecoveryCursor
}

// AgentAttachmentRegistry is the Workspace-scoped seam for durable Attachment
// state, generation fencing, replay cursors, and tombstones.
type AgentAttachmentRegistry interface {
	Apply(workspaceID string, event AgentAttachmentEvent) (AgentAttachmentChange, error)
	Resolve(workspaceID, agentID string) (AgentAttachment, bool)
	List(workspaceID string) []AgentAttachment
	RecoveryState(workspaceID string) AgentAttachmentRecoveryState
}
