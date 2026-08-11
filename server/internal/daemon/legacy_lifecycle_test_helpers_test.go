package daemon

import (
	"errors"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// These helpers keep Reminder cache fixtures readable while the production
// wake-socket lifecycle authority is absent. They apply directly to the native
// Attachment registry and must never be linked into a daemon binary.
func (d *Daemon) handleDaemonAgentStart(payload protocol.DaemonAgentStartPayload) error {
	if d == nil || d.attachmentRegistry() == nil {
		return nil
	}
	result, err := d.attachmentRegistry().applyEvent(payload.WorkspaceID, AgentAttachmentEvent{
		Kind: AgentAttachmentEventAttach, AgentID: payload.AgentID, RuntimeID: payload.RuntimeID,
		AttachmentGeneration: AttachmentGeneration(payload.PlacementGeneration), LifecycleSeq: AttachmentLifecycleSequence(payload.LifecycleSeq),
	}, payload.LifecycleSeq > 0, true)
	if err != nil || !result.accepted {
		return err
	}
	if _, err := d.ensureIdleMessageCoordinator(payload.WorkspaceID, payload.AgentID, payload.RuntimeID); err != nil {
		return err
	}
	if _, err := d.ensureWorkspaceRunner(payload.WorkspaceID); err != nil {
		return err
	}
	if result.change.Kind == AgentAttachmentAttached || result.change.Kind == AgentAttachmentMoved {
		d.requestReminderSnapshot(payload.WorkspaceID, payload.AgentID)
	}
	return nil
}

func (d *Daemon) handleDaemonAgentStop(payload protocol.DaemonAgentStopPayload) error {
	if d == nil || d.attachmentRegistry() == nil {
		return nil
	}
	result, err := d.attachmentRegistry().applyEvent("", AgentAttachmentEvent{
		Kind: AgentAttachmentEventDetach, AgentID: payload.AgentID, RuntimeID: payload.RuntimeID,
		AttachmentGeneration: AttachmentGeneration(payload.PlacementGeneration), LifecycleSeq: AttachmentLifecycleSequence(payload.LifecycleSeq),
	}, payload.LifecycleSeq > 0, false)
	if err != nil || result.change.Kind != AgentAttachmentDetached {
		return err
	}
	if runner := d.currentWorkspaceRunner(result.change.Previous.WorkspaceID); runner != nil && runner.inboxes != nil {
		runner.inboxes.Remove(payload.AgentID, payload.RuntimeID)
	}
	return d.removeDetachedReminderAgent(payload.AgentID)
}

func (d *Daemon) handleDaemonAgentStartFrame(payload protocol.DaemonAgentStartPayload) error {
	return d.handleDaemonAgentStart(payload)
}

func (d *Daemon) handleDaemonAgentLifecycleReplayEnd(payload protocol.DaemonAgentLifecycleReplayEndPayload) error {
	if d == nil || d.attachmentRegistry() == nil {
		return nil
	}
	if err := d.attachmentRegistry().advanceRecovery(nil, payload.RuntimeCursors); err != nil {
		return err
	}
	if !d.queueReminderFrame(protocol.EventDaemonAgentLifecycleAck, protocol.DaemonAgentLifecycleAckPayload{RuntimeCursors: payload.RuntimeCursors}) {
		return errors.New("queue test lifecycle ack")
	}
	if !d.startReminderProjectionReplay() {
		return errors.New("queue test reminder projection replay")
	}
	return nil
}

func (d *Daemon) requestAgentLifecycleReplay() bool {
	stored := d.attachmentRecoveryCursors()
	cursors := map[string]int64{}
	d.mu.Lock()
	for runtimeID := range d.runtimeIndex {
		cursors[runtimeID] = stored[runtimeID]
	}
	d.mu.Unlock()
	if !d.queueReminderFrame(protocol.EventDaemonAgentLifecycleReq, protocol.DaemonAgentLifecycleRequestPayload{RuntimeCursors: cursors}) {
		return false
	}
	return true
}
