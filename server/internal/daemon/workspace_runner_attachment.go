package daemon

import (
	"errors"
	"fmt"

	"github.com/multica-ai/multica/server/internal/agentworkspace"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// applyAttachmentAttach establishes a durable local responsibility before the
// corresponding receipt leaves this Runner. Attachment is deliberately not a
// provider launch: it only prepares the persistent AgentRoot and Inbox owned
// by this Workspace Runner.
func (runner *WorkspaceRunner) applyAttachmentAttach(payload protocol.WorkspaceRunnerAgentAttachPayload) (protocol.WorkspaceRunnerAgentAttachedPayload, error) {
	if runner == nil || runner.daemon == nil || runner.attachments == nil || runner.inboxes == nil {
		return protocol.WorkspaceRunnerAgentAttachedPayload{}, errors.New("Workspace Runner Attachment dependencies are unavailable")
	}
	if err := payload.Validate(); err != nil {
		return protocol.WorkspaceRunnerAgentAttachedPayload{}, fmt.Errorf("validate Attachment attach: %w", err)
	}
	workspaceID := runner.config.WorkspaceID
	if !runner.daemon.ownsWorkspaceRunnerRuntime(workspaceID, payload.RuntimeID) {
		return protocol.WorkspaceRunnerAgentAttachedPayload{}, errors.New("Attachment Runtime is outside Workspace Runner scope")
	}
	if _, err := runner.attachments.Apply(workspaceID, AgentAttachmentEvent{
		Kind:                 AgentAttachmentEventAttach,
		AgentID:              payload.AgentID,
		RuntimeID:            payload.RuntimeID,
		AttachmentGeneration: AttachmentGeneration(payload.AttachmentGeneration),
		LifecycleSeq:         AttachmentLifecycleSequence(payload.LifecycleSeq),
	}); err != nil {
		return protocol.WorkspaceRunnerAgentAttachedPayload{}, fmt.Errorf("persist Attachment attach: %w", err)
	}
	attachment, attached := runner.attachments.Resolve(workspaceID, payload.AgentID)
	if !attached || attachment.RuntimeID != payload.RuntimeID || int64(attachment.AttachmentGeneration) != payload.AttachmentGeneration {
		return protocol.WorkspaceRunnerAgentAttachedPayload{}, errors.New("Attachment attach was superseded by a newer generation")
	}
	agentRoot := agentworkspace.Root(runner.daemon.cfg.WorkspacesRoot, workspaceID, payload.AgentID)
	if err := ensureMulticaAgentRoot(agentRoot); err != nil {
		return protocol.WorkspaceRunnerAgentAttachedPayload{}, fmt.Errorf("create Attachment AgentRoot: %w", err)
	}
	if _, err := runner.inboxes.Ensure(payload.AgentID); err != nil {
		return protocol.WorkspaceRunnerAgentAttachedPayload{}, fmt.Errorf("ensure Attachment Inbox: %w", err)
	}
	return protocol.WorkspaceRunnerAgentAttachedPayload(payload), nil
}

// applyAttachmentDetach commits the tombstone before tearing down volatile
// state. A replayed or stale detach is allowed to converge harmlessly, but it
// only stops a launch, resident Runtime, or Inbox when the pre-commit durable
// Attachment exactly matches the command being detached.
func (runner *WorkspaceRunner) applyAttachmentDetach(payload protocol.WorkspaceRunnerAgentDetachPayload) (protocol.WorkspaceRunnerAgentDetachedPayload, error) {
	if runner == nil || runner.daemon == nil || runner.attachments == nil || runner.inboxes == nil || runner.processes == nil {
		return protocol.WorkspaceRunnerAgentDetachedPayload{}, errors.New("Workspace Runner Attachment dependencies are unavailable")
	}
	if err := payload.Validate(); err != nil {
		return protocol.WorkspaceRunnerAgentDetachedPayload{}, fmt.Errorf("validate Attachment detach: %w", err)
	}
	workspaceID := runner.config.WorkspaceID
	if !runner.daemon.ownsWorkspaceRunnerRuntime(workspaceID, payload.RuntimeID) {
		return protocol.WorkspaceRunnerAgentDetachedPayload{}, errors.New("Attachment Runtime is outside Workspace Runner scope")
	}
	if _, err := runner.attachments.Apply(workspaceID, AgentAttachmentEvent{
		Kind:                 AgentAttachmentEventDetach,
		AgentID:              payload.AgentID,
		RuntimeID:            payload.RuntimeID,
		AttachmentGeneration: AttachmentGeneration(payload.AttachmentGeneration),
		LifecycleSeq:         AttachmentLifecycleSequence(payload.LifecycleSeq),
	}); err != nil {
		return protocol.WorkspaceRunnerAgentDetachedPayload{}, fmt.Errorf("persist Attachment detach: %w", err)
	}
	// A newer current Attachment wins over a late detach. Conversely, when the
	// tombstone is already durable we must still retry its volatile teardown:
	// the previous attempt may have failed after persistence (for example while
	// force-killing a busy resident Runtime).
	if current, attached := runner.attachments.Resolve(workspaceID, payload.AgentID); attached {
		if current.RuntimeID != payload.RuntimeID || int64(current.AttachmentGeneration) > payload.AttachmentGeneration {
			return protocol.WorkspaceRunnerAgentDetachedPayload(payload), nil
		}
		return protocol.WorkspaceRunnerAgentDetachedPayload{}, errors.New("Attachment detach did not remove the current generation")
	}
	if launch, found := runner.processes.Snapshot(payload.AgentID); found && launch.RuntimeID == payload.RuntimeID {
		if err := runner.processes.Stop(agentProcessCallback{AgentID: payload.AgentID, LaunchID: launch.LaunchID}); err != nil {
			return protocol.WorkspaceRunnerAgentDetachedPayload{}, fmt.Errorf("stop detached Agent launch: %w", err)
		}
		if runner.activity != nil {
			if err := runner.activity.PublishForManagedAgent(payload.AgentID, runner.daemon.runnerInstanceID, protocol.ActivityKindOffline, "detached", nil); err != nil && runner.daemon.logger != nil {
				runner.daemon.logger.Debug("Workspace Runner detached Activity publish deferred", "workspace_id", workspaceID, "agent_id", payload.AgentID, "reason", "activity_not_managed", "error", err)
			}
			runner.activity.RemoveManaged(payload.AgentID, launch.LaunchID)
		}
	}
	if err := runner.runtimes.forceInvalidateSession(payload.AgentID, payload.RuntimeID); err != nil {
		return protocol.WorkspaceRunnerAgentDetachedPayload{}, fmt.Errorf("release detached Agent runtime: %w", err)
	}
	runner.inboxes.Remove(payload.AgentID, payload.RuntimeID)
	return protocol.WorkspaceRunnerAgentDetachedPayload(payload), nil
}
