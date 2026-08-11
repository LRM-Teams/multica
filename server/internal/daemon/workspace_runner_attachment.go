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
