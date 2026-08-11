package daemon

import (
	"strings"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// legacyAgentAttachmentAdapter translates the wake-socket lifecycle protocol
// into Agent Attachment events without maintaining placement state of its own.
// Lifecycle sequence zero remains valid only at this compatibility boundary;
// positive sequences are committed atomically by the registry Apply core.
type legacyAgentAttachmentAdapter struct {
	registry *localAgentAttachmentRegistry
}

func (adapter legacyAgentAttachmentAdapter) ApplyStart(payload protocol.DaemonAgentStartPayload) (agentAttachmentApplyResult, error) {
	event := AgentAttachmentEvent{
		Kind:                 AgentAttachmentEventAttach,
		AgentID:              payload.AgentID,
		RuntimeID:            payload.RuntimeID,
		AttachmentGeneration: AttachmentGeneration(payload.PlacementGeneration),
		LifecycleSeq:         AttachmentLifecycleSequence(payload.LifecycleSeq),
	}
	if adapter.registry == nil || !validLegacyAgentAttachmentEvent(event) || strings.TrimSpace(payload.WorkspaceID) == "" {
		return unchangedAgentAttachmentApplyResult("invalid_legacy_start"), nil
	}
	return adapter.registry.applyEvent(payload.WorkspaceID, event, event.LifecycleSeq > 0, true)
}

func (adapter legacyAgentAttachmentAdapter) ApplyStop(payload protocol.DaemonAgentStopPayload) (agentAttachmentApplyResult, error) {
	event := AgentAttachmentEvent{
		Kind:                 AgentAttachmentEventDetach,
		AgentID:              payload.AgentID,
		RuntimeID:            payload.RuntimeID,
		AttachmentGeneration: AttachmentGeneration(payload.PlacementGeneration),
		LifecycleSeq:         AttachmentLifecycleSequence(payload.LifecycleSeq),
	}
	if adapter.registry == nil || !validLegacyAgentAttachmentEvent(event) {
		return unchangedAgentAttachmentApplyResult("invalid_legacy_stop"), nil
	}
	return adapter.registry.applyEvent("", event, event.LifecycleSeq > 0, false)
}

func (adapter legacyAgentAttachmentAdapter) AdvanceRecovery(cursors map[string]int64) error {
	if adapter.registry == nil {
		return nil
	}
	return adapter.registry.advanceRecovery(nil, cursors)
}

func validLegacyAgentAttachmentEvent(event AgentAttachmentEvent) bool {
	return strings.TrimSpace(event.AgentID) != "" &&
		strings.TrimSpace(event.RuntimeID) != "" &&
		event.AttachmentGeneration > 0 &&
		event.LifecycleSeq >= 0
}

func unchangedAgentAttachmentApplyResult(reason string) agentAttachmentApplyResult {
	return agentAttachmentApplyResult{
		change: AgentAttachmentChange{Kind: AgentAttachmentUnchanged},
		reason: reason,
	}
}

func (d *Daemon) legacyAgentAttachmentAdapter() legacyAgentAttachmentAdapter {
	if d == nil || d.reminderAgents == nil {
		return legacyAgentAttachmentAdapter{}
	}
	return legacyAgentAttachmentAdapter{registry: d.reminderAgents.localAgentAttachmentRegistry}
}
