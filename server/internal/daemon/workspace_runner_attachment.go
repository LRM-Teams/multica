package daemon

import (
	"context"
	"errors"
	"fmt"

	"github.com/multica-ai/multica/server/internal/agentworkspace"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (runner *WorkspaceRunner) attachmentReplayRequest(runtimeSet AgentAttachmentRuntimeSet) (protocol.WorkspaceRunnerAttachmentReplayRequest, error) {
	if runner == nil || runner.attachments == nil {
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
	if runner == nil || runner.runtimeSet == nil {
		return AgentAttachmentRuntimeSet{}
	}
	return runner.runtimeSet()
}

// startManagedAgent treats the server-provided launch as lifecycle authority.
// It registers the Agent with APM before provider startup so the connection's
// per-Agent start buffer can safely hold deliveries until startup completes.
func (runner *WorkspaceRunner) startManagedAgent(ctx context.Context, payload protocol.WorkspaceRunnerAgentStartPayload) (protocol.AgentStartAckPayload, protocol.AgentStatusPayload, protocol.AgentSessionPayload, error) {
	ack, err := runner.registerManagedAgentStart(payload)
	if err != nil {
		return protocol.AgentStartAckPayload{}, protocol.AgentStatusPayload{}, protocol.AgentSessionPayload{}, err
	}
	status, session, err := runner.completeManagedAgentStart(ctx, payload, ack)
	return ack, status, session, err
}

// registerManagedAgentStart runs on the socket reader before a later stop or
// replacement command can overtake this launch. Provider startup stays async.
func (runner *WorkspaceRunner) registerManagedAgentStart(payload protocol.WorkspaceRunnerAgentStartPayload) (protocol.AgentStartAckPayload, error) {
	if runner == nil || runner.attachments == nil || runner.processes == nil || runner.activity == nil {
		return protocol.AgentStartAckPayload{}, errors.New("Workspace Runner launch dependencies are unavailable")
	}
	if err := payload.Validate(); err != nil {
		return protocol.AgentStartAckPayload{}, fmt.Errorf("validate managed start: %w", err)
	}
	if !runner.hasRuntime(payload.RuntimeID) {
		return protocol.AgentStartAckPayload{}, errors.New("managed start Runtime is outside Workspace Runner scope")
	}
	ack, err := runner.processes.Start(agentProcessStartRequest{AgentID: payload.AgentID, RuntimeID: payload.RuntimeID, LaunchID: payload.LaunchID, StartDispatchID: payload.StartDispatchID, ReadinessPolicy: agentRuntimeReadinessFirstEvent})
	if err != nil {
		return protocol.AgentStartAckPayload{}, fmt.Errorf("start managed Agent: %w", err)
	}
	// Raft lifecycle authority is the server-provided start command. Register
	// it before changing the Inbox so a cross-Runtime start that omitted its
	// required stop fails without corrupting the current launch's routing.
	if _, err := runner.inboxes.AcceptStart(payload.AgentID, payload.RuntimeID); err != nil {
		_ = runner.processes.Stop(agentProcessCallback{AgentID: payload.AgentID, LaunchID: payload.LaunchID})
		return protocol.AgentStartAckPayload{}, fmt.Errorf("prepare managed Agent Inbox: %w", err)
	}
	return ack, nil
}

func (runner *WorkspaceRunner) completeManagedAgentStart(ctx context.Context, payload protocol.WorkspaceRunnerAgentStartPayload, ack protocol.AgentStartAckPayload) (protocol.AgentStatusPayload, protocol.AgentSessionPayload, error) {
	if err := runner.processes.WaitForAdmission(ctx, agentProcessCallback{AgentID: payload.AgentID, LaunchID: payload.LaunchID}); err != nil {
		_ = runner.processes.Stop(agentProcessCallback{AgentID: payload.AgentID, LaunchID: payload.LaunchID})
		return protocol.AgentStatusPayload{}, protocol.AgentSessionPayload{}, fmt.Errorf("wait for managed Agent capacity: %w", err)
	}
	if current, ok := runner.processes.Snapshot(payload.AgentID); !ok || current.LaunchID != payload.LaunchID {
		return protocol.AgentStatusPayload{}, protocol.AgentSessionPayload{}, errors.New("managed start was superseded before provider startup")
	} else {
		ack.QueueState = current.QueueState
	}
	if err := runner.ensureResidentRuntime(ctx, payload.AgentID, payload.RuntimeID, nil); err != nil {
		status := runner.failManagedRuntime(payload.AgentID, payload.RuntimeID, payload.LaunchID, managedRuntimeFailureSpawn, "provider_spawn_failed", runner.activity.now().UTC())
		return status, protocol.AgentSessionPayload{}, fmt.Errorf("start managed Agent provider: %w", err)
	}
	if current, ok := runner.processes.Snapshot(payload.AgentID); !ok || current.LaunchID != payload.LaunchID {
		if !ok || current.RuntimeID != payload.RuntimeID {
			_ = runner.runtimes.forceInvalidateSession(payload.AgentID, payload.RuntimeID)
		}
		return protocol.AgentStatusPayload{}, protocol.AgentSessionPayload{}, errors.New("managed start was superseded during provider startup")
	}
	status := protocol.AgentStatusPayload{AgentID: ack.AgentID, LaunchID: ack.LaunchID, Status: protocol.AgentStatusActive}
	session := protocol.AgentSessionPayload{AgentID: ack.AgentID, LaunchID: ack.LaunchID}
	if err := runner.activity.SetManaged(status, session); err != nil {
		return protocol.AgentStatusPayload{}, protocol.AgentSessionPayload{}, fmt.Errorf("record managed start: %w", err)
	}
	if coordinator, runtimeID, ok := runner.messageCoordinator(payload.AgentID); ok && runtimeID == payload.RuntimeID {
		if _, err := coordinator.flushWithResult(ctx, true); err != nil {
			if runner.logger != nil {
				runner.logger.Warn("Workspace Runner buffered Message flush deferred", "workspace_id", runner.config.WorkspaceID, "agent_id", payload.AgentID, "runtime_id", payload.RuntimeID, "launch_id", payload.LaunchID, "start_dispatch_id", payload.StartDispatchID, "error", err)
			}
		}
	}
	return status, session, nil
}

func (runner *WorkspaceRunner) stopManagedAgent(payload protocol.WorkspaceRunnerAgentStopPayload) (protocol.AgentStatusPayload, error) {
	if runner == nil || runner.processes == nil || runner.runtimes == nil || runner.inboxes == nil {
		return protocol.AgentStatusPayload{}, errors.New("Workspace Runner stop dependencies are unavailable")
	}
	if err := payload.Validate(); err != nil {
		return protocol.AgentStatusPayload{}, err
	}
	launch, found := runner.processes.Snapshot(payload.AgentID)
	if !found || launch.LaunchID != payload.LaunchID {
		return protocol.AgentStatusPayload{}, errors.New("managed stop does not match current launch")
	}
	if err := runner.runtimes.forceInvalidateSession(payload.AgentID, launch.RuntimeID); err != nil {
		return protocol.AgentStatusPayload{}, fmt.Errorf("stop managed Agent provider: %w", err)
	}
	if err := runner.processes.Stop(agentProcessCallback{AgentID: payload.AgentID, LaunchID: payload.LaunchID}); err != nil {
		return protocol.AgentStatusPayload{}, err
	}
	runner.inboxes.Remove(payload.AgentID, launch.RuntimeID)
	if runner.activity != nil {
		runner.activity.RemoveManaged(payload.AgentID, payload.LaunchID)
	}
	return protocol.AgentStatusPayload{AgentID: payload.AgentID, LaunchID: payload.LaunchID, Status: protocol.AgentStatusInactive}, nil
}

// applyAttachmentAttach establishes a durable local responsibility before the
// corresponding receipt leaves this Runner. Attachment is deliberately not a
// provider or Message lifecycle: it only prepares durable ownership and the
// persistent AgentRoot. APM acceptance of agent:start owns Inbox creation.
func (runner *WorkspaceRunner) applyAttachmentAttach(payload protocol.WorkspaceRunnerAgentAttachPayload) (protocol.WorkspaceRunnerAgentAttachedPayload, error) {
	if runner == nil || runner.attachments == nil || runner.inboxes == nil {
		return protocol.WorkspaceRunnerAgentAttachedPayload{}, errors.New("Workspace Runner Attachment dependencies are unavailable")
	}
	if err := payload.Validate(); err != nil {
		return protocol.WorkspaceRunnerAgentAttachedPayload{}, fmt.Errorf("validate Attachment attach: %w", err)
	}
	workspaceID := runner.config.WorkspaceID
	if !runner.hasRuntime(payload.RuntimeID) {
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
	agentRoot := agentworkspace.Root(runner.workspacesRoot, workspaceID, payload.AgentID)
	if err := ensureMulticaAgentRoot(agentRoot); err != nil {
		return protocol.WorkspaceRunnerAgentAttachedPayload{}, fmt.Errorf("create Attachment AgentRoot: %w", err)
	}
	return protocol.WorkspaceRunnerAgentAttachedPayload(payload), nil
}

// applyAttachmentDetach commits the tombstone before tearing down volatile
// state. A replayed or stale detach is allowed to converge harmlessly, but it
// only stops a launch, resident Runtime, or Inbox when the pre-commit durable
// Attachment exactly matches the command being detached.
func (runner *WorkspaceRunner) applyAttachmentDetach(payload protocol.WorkspaceRunnerAgentDetachPayload) (protocol.WorkspaceRunnerAgentDetachedPayload, error) {
	if runner == nil || runner.attachments == nil || runner.inboxes == nil || runner.processes == nil {
		return protocol.WorkspaceRunnerAgentDetachedPayload{}, errors.New("Workspace Runner Attachment dependencies are unavailable")
	}
	if err := payload.Validate(); err != nil {
		return protocol.WorkspaceRunnerAgentDetachedPayload{}, fmt.Errorf("validate Attachment detach: %w", err)
	}
	workspaceID := runner.config.WorkspaceID
	if !runner.hasRuntime(payload.RuntimeID) {
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
