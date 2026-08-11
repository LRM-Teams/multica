package daemon

import (
	"errors"
	"fmt"

	"github.com/multica-ai/multica/server/internal/agentworkspace"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (runner *WorkspaceRunner) attachmentReplayRequest(runtimeSet AgentAttachmentRuntimeSet) (protocol.WorkspaceRunnerAttachmentReplayRequest, error) {
	if runner == nil || runner.daemon == nil || runner.attachments == nil {
		return protocol.WorkspaceRunnerAttachmentReplayRequest{}, errors.New("Workspace Runner Attachment replay dependencies are unavailable")
	}
	state, err := runner.attachments.RecoveryState(runtimeSet)
	if err != nil {
		return protocol.WorkspaceRunnerAttachmentReplayRequest{}, fmt.Errorf("read Attachment replay cursors: %w", err)
	}
	cursors := make(map[string]int64, len(state.Cursors))
	for _, cursor := range state.Cursors {
		cursors[cursor.RuntimeID] = int64(cursor.LifecycleSeq)
	}
	return protocol.WorkspaceRunnerAttachmentReplayRequest{RuntimeCursors: cursors}, nil
}

func (runner *WorkspaceRunner) completeAttachmentReplay(runtimeSet AgentAttachmentRuntimeSet, end protocol.WorkspaceRunnerAttachmentReplayEnd) (protocol.WorkspaceRunnerAttachmentReplayAck, error) {
	if runner == nil || runner.attachments == nil {
		return protocol.WorkspaceRunnerAttachmentReplayAck{}, errors.New("Workspace Runner Attachment replay dependencies are unavailable")
	}
	if err := end.Validate(); err != nil {
		return protocol.WorkspaceRunnerAttachmentReplayAck{}, fmt.Errorf("validate Attachment replay end: %w", err)
	}
	allowed := runtimeSet.runtimeIDs()
	if len(end.RuntimeCursors) != len(allowed) {
		return protocol.WorkspaceRunnerAttachmentReplayAck{}, errors.New("Attachment replay end omitted a Runtime cursor")
	}
	cursors := make([]AgentAttachmentRecoveryCursor, 0, len(end.RuntimeCursors))
	ack := make(map[string]int64, len(end.RuntimeCursors))
	for runtimeID, lifecycleSeq := range end.RuntimeCursors {
		if _, ok := allowed[runtimeID]; !ok {
			return protocol.WorkspaceRunnerAttachmentReplayAck{}, fmt.Errorf("Attachment replay Runtime %s is outside Workspace Runner scope", runtimeID)
		}
		cursors = append(cursors, AgentAttachmentRecoveryCursor{RuntimeID: runtimeID, LifecycleSeq: AttachmentLifecycleSequence(lifecycleSeq)})
		ack[runtimeID] = lifecycleSeq
	}
	if err := runner.attachments.AdvanceRecovery(runtimeSet, cursors); err != nil {
		return protocol.WorkspaceRunnerAttachmentReplayAck{}, fmt.Errorf("advance Attachment replay cursors: %w", err)
	}
	return protocol.WorkspaceRunnerAttachmentReplayAck{RuntimeCursors: ack}, nil
}

func (runner *WorkspaceRunner) attachmentRuntimeSet() AgentAttachmentRuntimeSet {
	if runner == nil || runner.daemon == nil {
		return AgentAttachmentRuntimeSet{}
	}
	for _, runtimeSet := range runner.daemon.attachmentRuntimeSets() {
		if runtimeSet.WorkspaceID == runner.config.WorkspaceID {
			return runtimeSet
		}
	}
	return AgentAttachmentRuntimeSet{WorkspaceID: runner.config.WorkspaceID}
}

// startManagedAgent is deliberately stricter than process manager admission:
// an Agent can run only when this Workspace Runner has already durably accepted
// its exact Attachment. A stale or cross-Runtime start must not revive an Agent
// after it was detached or moved.
func (runner *WorkspaceRunner) startManagedAgent(payload protocol.WorkspaceRunnerAgentStartPayload) (protocol.AgentStartAckPayload, protocol.AgentStatusPayload, protocol.AgentSessionPayload, error) {
	if runner == nil || runner.daemon == nil || runner.attachments == nil || runner.processes == nil || runner.activity == nil {
		return protocol.AgentStartAckPayload{}, protocol.AgentStatusPayload{}, protocol.AgentSessionPayload{}, errors.New("Workspace Runner launch dependencies are unavailable")
	}
	if err := payload.Validate(); err != nil {
		return protocol.AgentStartAckPayload{}, protocol.AgentStatusPayload{}, protocol.AgentSessionPayload{}, fmt.Errorf("validate managed start: %w", err)
	}
	workspaceID := runner.config.WorkspaceID
	if !runner.daemon.ownsWorkspaceRunnerRuntime(workspaceID, payload.RuntimeID) {
		return protocol.AgentStartAckPayload{}, protocol.AgentStatusPayload{}, protocol.AgentSessionPayload{}, errors.New("managed start Runtime is outside Workspace Runner scope")
	}
	attachment, attached := runner.attachments.Resolve(workspaceID, payload.AgentID)
	if !attached || attachment.RuntimeID != payload.RuntimeID {
		return protocol.AgentStartAckPayload{}, protocol.AgentStatusPayload{}, protocol.AgentSessionPayload{}, errors.New("managed start requires a matching Attachment")
	}
	ack, err := runner.processes.Start(agentProcessStartRequest{AgentID: payload.AgentID, RuntimeID: payload.RuntimeID, StartDispatchID: payload.StartDispatchID, ReadinessPolicy: agentRuntimeReadinessFirstEvent})
	if err != nil {
		return protocol.AgentStartAckPayload{}, protocol.AgentStatusPayload{}, protocol.AgentSessionPayload{}, fmt.Errorf("start managed Agent: %w", err)
	}
	status := protocol.AgentStatusPayload{AgentID: ack.AgentID, LaunchID: ack.LaunchID, Status: protocol.AgentStatusActive}
	session := protocol.AgentSessionPayload{AgentID: ack.AgentID, LaunchID: ack.LaunchID}
	if err := runner.activity.SetManaged(status, session); err != nil {
		return protocol.AgentStartAckPayload{}, protocol.AgentStatusPayload{}, protocol.AgentSessionPayload{}, fmt.Errorf("record managed start: %w", err)
	}
	return ack, status, session, nil
}

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
			runner.activity.RemoveManaged(payload.AgentID, launch.LaunchID)
		}
	}
	if err := runner.runtimes.forceInvalidateSession(payload.AgentID, payload.RuntimeID); err != nil {
		return protocol.WorkspaceRunnerAgentDetachedPayload{}, fmt.Errorf("release detached Agent runtime: %w", err)
	}
	runner.inboxes.Remove(payload.AgentID, payload.RuntimeID)
	return protocol.WorkspaceRunnerAgentDetachedPayload(payload), nil
}
