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
	if err == nil {
		runner.publishManagedAgentStartActivity(payload.AgentID, payload.RuntimeID)
	}
	return ack, status, session, err
}

// registerManagedAgentStart runs on the socket reader before a later stop or
// replacement command can overtake this launch. Provider startup stays async.
func (runner *WorkspaceRunner) registerManagedAgentStart(payload protocol.WorkspaceRunnerAgentStartPayload) (protocol.AgentStartAckPayload, error) {
	ack, _, err := runner.registerManagedAgentStartOnce(payload)
	return ack, err
}

func (runner *WorkspaceRunner) registerManagedAgentStartOnce(payload protocol.WorkspaceRunnerAgentStartPayload) (protocol.AgentStartAckPayload, bool, error) {
	if runner == nil || runner.attachments == nil || runner.processes == nil || runner.activity == nil {
		return protocol.AgentStartAckPayload{}, false, errors.New("Workspace Runner launch dependencies are unavailable")
	}
	if err := payload.Validate(); err != nil {
		return protocol.AgentStartAckPayload{}, false, fmt.Errorf("validate managed start: %w", err)
	}
	if !runner.hasRuntime(payload.RuntimeID) {
		return protocol.AgentStartAckPayload{}, false, errors.New("managed start Runtime is outside Workspace Runner scope")
	}
	result, err := runner.processes.startWithDisposition(agentProcessStartRequest{AgentID: payload.AgentID, RuntimeID: payload.RuntimeID, LaunchID: payload.LaunchID, StartDispatchID: payload.StartDispatchID, ReadinessPolicy: agentRuntimeReadinessFirstEvent})
	if err != nil {
		return protocol.AgentStartAckPayload{}, false, fmt.Errorf("start managed Agent: %w", err)
	}
	ack := result.Acknowledgement
	if result.Replayed {
		return ack, true, nil
	}
	// Raft lifecycle authority is the server-provided start command. Register
	// it before changing the Inbox so a cross-Runtime start that omitted its
	// required stop fails without corrupting the current launch's routing.
	if _, err := runner.inboxes.AcceptStart(payload.AgentID, payload.RuntimeID); err != nil {
		_ = runner.processes.Stop(agentProcessCallback{AgentID: payload.AgentID, LaunchID: payload.LaunchID})
		return protocol.AgentStartAckPayload{}, false, fmt.Errorf("prepare managed Agent Inbox: %w", err)
	}
	if runner.residency != nil {
		runner.residency.rememberLaunch(payload.AgentID, payload.RuntimeID, payload.LaunchID, payload.StartDispatchID)
	}
	return ack, false, nil
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
	if err := runner.admitManagedProviderProcess(payload); err != nil {
		status := runner.failManagedRuntime(payload.AgentID, payload.RuntimeID, payload.LaunchID, managedRuntimeFailureSpawn, "provider_spawn_failed", runner.activity.now().UTC())
		return status, protocol.AgentSessionPayload{}, fmt.Errorf("admit managed Agent process: %w", err)
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
	if runner.residency != nil {
		runner.residency.rememberIdle(payload.AgentID, payload.RuntimeID, payload.LaunchID, payload.StartDispatchID)
	}
	if coordinator, runtimeID, ok := runner.messageCoordinator(payload.AgentID); ok && runtimeID == payload.RuntimeID {
		if _, err := coordinator.flushWithResult(ctx, true); err != nil {
			if runner.logger != nil {
				if errors.Is(err, ErrCanonicalAgentRuntimeBusy) {
					runner.logger.Debug("Workspace Runner buffered Message flush deferred", runner.managedStartLogAttrs(payload, ack.QueueState, "runtime_busy", "deferred", err)...)
				} else {
					runner.logger.Warn("Workspace Runner buffered Message flush failed", runner.managedStartLogAttrs(payload, ack.QueueState, "message_flush_failed", "failed", err)...)
				}
			}
		}
	}
	return status, session, nil
}

func (runner *WorkspaceRunner) managedStartLogAttrs(payload protocol.WorkspaceRunnerAgentStartPayload, queueState, reason, outcome string, err error) []any {
	args := []any{
		"computer_id", runner.config.DaemonID,
		"workspace_id", runner.config.WorkspaceID,
		"agent_id", payload.AgentID,
		"runtime_id", payload.RuntimeID,
		"launch_id", payload.LaunchID,
		"start_dispatch_id", payload.StartDispatchID,
		"queue_state", queueState,
		"reason", reason,
		"outcome", outcome,
	}
	if err != nil {
		args = append(args, "error", err)
	}
	return args
}

// admitManagedProviderProcess is Raft's this.agents.set after spawn: the
// launch is Running only once the provider process exists. Starting Activity
// is a separate frame and does not mean the process is missing.
func (runner *WorkspaceRunner) admitManagedProviderProcess(payload protocol.WorkspaceRunnerAgentStartPayload) error {
	if runner == nil || runner.processes == nil {
		return errors.New("Workspace Runner process manager is unavailable")
	}
	current, ok := runner.processes.Snapshot(payload.AgentID)
	if !ok || current.LaunchID != payload.LaunchID {
		return errors.New("managed start was superseded before process admission")
	}
	if current.QueueState == protocol.AgentStartQueueRunning {
		return nil
	}
	if current.QueueState != protocol.AgentStartQueueStarting {
		return fmt.Errorf("managed start is not admitted for process spawn: %s", current.QueueState)
	}
	callback := agentProcessCallback{
		AgentID:           payload.AgentID,
		LaunchID:          payload.LaunchID,
		ProcessInstanceID: "resident-" + payload.LaunchID,
	}
	if err := runner.processes.ProcessSpawned(callback); err != nil {
		return err
	}
	return runner.processes.RuntimeReady(callback)
}

// applyAttachmentAttach establishes a durable local responsibility before the
// corresponding receipt leaves this Runner. Attachment is deliberately not a
// provider or Message lifecycle: it only prepares durable ownership and the
// persistent AgentRoot. APM acceptance of agent:start owns Inbox creation.
func (runner *WorkspaceRunner) applyAttachmentAttach(payload protocol.WorkspaceRunnerAgentAttachPayload) (protocol.WorkspaceRunnerAgentAttachedPayload, error) {
	if runner == nil || runner.attachments == nil {
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

// applyAttachmentDetach commits only the durable responsibility tombstone.
// A replayed or stale detach converges harmlessly; process, Runtime, Inbox,
// Activity, and AgentRoot lifecycles remain owned by their explicit managers.
func (runner *WorkspaceRunner) applyAttachmentDetach(payload protocol.WorkspaceRunnerAgentDetachPayload) (protocol.WorkspaceRunnerAgentDetachedPayload, error) {
	if runner == nil || runner.attachments == nil {
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
	// A newer current Attachment wins over a late detach. The tombstone itself
	// is the whole operation, so an already-durable replay has no volatile work
	// to retry.
	if current, attached := runner.attachments.Resolve(workspaceID, payload.AgentID); attached {
		if current.RuntimeID != payload.RuntimeID || int64(current.AttachmentGeneration) > payload.AttachmentGeneration {
			return protocol.WorkspaceRunnerAgentDetachedPayload(payload), nil
		}
		return protocol.WorkspaceRunnerAgentDetachedPayload{}, errors.New("Attachment detach did not remove the current generation")
	}
	return protocol.WorkspaceRunnerAgentDetachedPayload(payload), nil
}
