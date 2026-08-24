package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// messageCoordinator resolves an Inbox only inside this WorkspaceDaemon's immutable
// Workspace scope. Callers never inspect the Runner's Inbox registry directly.
func (runner *WorkspaceDaemon) messageCoordinator(agentID string) (*MessageCoordinator, string, bool) {
	if runner == nil || runner.inboxes == nil {
		return nil, "", false
	}
	return runner.inboxes.Resolve(agentID)
}

func (runner *WorkspaceDaemon) messageRuntimeID(agentID string) string {
	_, runtimeID, _ := runner.messageCoordinator(agentID)
	return runtimeID
}

func (runner *WorkspaceDaemon) agentInboxPendingSnapshot(agentID string) []protocol.AgentMessageProjection {
	coordinator, _, ok := runner.messageCoordinator(agentID)
	if !ok {
		return nil
	}
	return coordinator.PendingSnapshot()
}

func (runner *WorkspaceDaemon) ensureMessageInbox(agentID, expectedRuntimeID string) (bool, error) {
	if runner == nil || runner.inboxes == nil || runner.processes == nil {
		return false, errors.New("WorkspaceDaemon Inbox registry is unavailable")
	}
	launch, ok := runner.processes.Snapshot(agentID)
	if !ok || !launch.Managed || launch.RuntimeID == "" {
		return false, fmt.Errorf("no accepted agent:start for Agent %q", agentID)
	}
	if expectedRuntimeID != "" && launch.RuntimeID != expectedRuntimeID {
		return false, fmt.Errorf("WorkspaceDaemon Inbox Runtime mismatch for Agent %q", agentID)
	}
	created, err := runner.inboxes.AcceptStart(agentID, launch.RuntimeID)
	if err != nil {
		return false, err
	}
	_, runtimeID, ok := runner.messageCoordinator(agentID)
	if !ok || (expectedRuntimeID != "" && runtimeID != expectedRuntimeID) {
		runner.inboxes.Remove(agentID, runtimeID)
		return false, fmt.Errorf("WorkspaceDaemon Inbox Runtime mismatch for Agent %q", agentID)
	}
	return created, nil
}

func (runner *WorkspaceDaemon) hasMessageInbox(agentID string) bool {
	_, _, ok := runner.messageCoordinator(agentID)
	return ok
}

func (runner *WorkspaceDaemon) hasAcceptedStart(agentID, runtimeID string) bool {
	if runner == nil || runner.processes == nil {
		return false
	}
	launch, ok := runner.processes.Snapshot(agentID)
	return ok && launch.Managed && launch.RuntimeID == runtimeID
}

func (runner *WorkspaceDaemon) ensureMessageInboxForDelivery(agentID string) error {
	if runner == nil || runner.inboxes == nil || runner.processes == nil {
		return errors.New("WorkspaceDaemon Message lifecycle is unavailable")
	}
	if runner.hasMessageInbox(agentID) {
		return nil
	}
	launch, ok := runner.processes.Snapshot(agentID)
	if !ok || !launch.Managed || launch.RuntimeID == "" {
		return fmt.Errorf("no accepted agent:start for %q", agentID)
	}
	if _, err := runner.inboxes.AcceptStart(agentID, launch.RuntimeID); err != nil {
		return fmt.Errorf("repair Agent Message coordinator: %w", err)
	}
	return nil
}

func (runner *WorkspaceDaemon) messageContextBoundary(agentID, target string) (int64, bool, error) {
	coordinator, _, ok := runner.messageCoordinator(agentID)
	if !ok {
		return 0, false, errors.New("Message coordinator is unavailable")
	}
	seq, known := coordinator.ContextBoundary(target)
	return seq, known, nil
}

func (runner *WorkspaceDaemon) prepareMessageCoverage(agentID string, request CoverageRequest) (CoverageOffer, error) {
	coordinator, _, ok := runner.messageCoordinator(agentID)
	if !ok {
		return CoverageOffer{}, errors.New("Message coordinator is unavailable")
	}
	return coordinator.PrepareCoverage(request)
}

func (runner *WorkspaceDaemon) messageSendBoundarySnapshot(agentID, target string) (int64, error) {
	coordinator, _, ok := runner.messageCoordinator(agentID)
	if !ok {
		return 0, errors.New("Message coordinator is unavailable")
	}
	return coordinator.SendBoundarySnapshot(target), nil
}

func (runner *WorkspaceDaemon) preflightMessageSend(agentID, target string) (MessageSendFreshness, error) {
	coordinator, _, ok := runner.messageCoordinator(agentID)
	if !ok {
		return MessageSendFreshness{}, errors.New("Message coordinator is unavailable")
	}
	return coordinator.PreflightMessageSend(target)
}

func (runner *WorkspaceDaemon) removeMessageInbox(agentID, runtimeID string) {
	if runner == nil || runner.inboxes == nil {
		return
	}
	runner.inboxes.Remove(agentID, runtimeID)
}

type messageDeliveryAcceptanceOutcome string

const (
	messageDeliveryProviderAccepted messageDeliveryAcceptanceOutcome = "provider_accepted"
	messageDeliveryPendingBuffered  messageDeliveryAcceptanceOutcome = "pending_buffered"
	messageDeliveryDeduplicated     messageDeliveryAcceptanceOutcome = "deduplicated"
)

var (
	errDeliveryRejectedNoProcess       = errors.New("delivery rejected_no_process")
	errDeliveryRejectedNoInbox         = errors.New("delivery rejected_no_inbox")
	errDeliveryInboxRuntimeMismatch    = errors.New("delivery rejected_inbox_runtime_mismatch")
	errDeliveryIdleRestoreFailed       = errors.New("delivery idle_restore_failed")
	errDeliveryResidentIdentityInvalid = errors.New("delivery resident_identity_invalid")
	errDeliveryProviderRejected        = errors.New("delivery provider_rejected")
)

type messageDeliveryAcceptance struct {
	ack     protocol.AgentDeliverAckPayload
	outcome messageDeliveryAcceptanceOutcome
}

// acceptMessageDelivery is the Raft-aligned per-Agent acceptance seam. It
// gives the current launch's in-memory Pending projection responsibility before
// attempting provider delivery. A missing live launch is not a terminal NACK:
// already-consumed, terminal, idle-snapshot, and spawn-cooldown deliveries
// stay locally responsible. Only "no process and no snapshot" rejects.
func (runner *WorkspaceDaemon) acceptMessageDelivery(ctx context.Context, delivery protocol.AgentDeliverPayload) (messageDeliveryAcceptance, error) {
	launch, managed := runner.processes.Snapshot(delivery.AgentID)
	if !managed {
		if acceptance, consumed := runner.acknowledgeConsumedDelivery(delivery); consumed {
			return acceptance, nil
		}
		return runner.acceptDeliveryWithoutLiveProcess(ctx, delivery)
	}
	coordinator, runtimeID, ok := runner.messageCoordinator(delivery.AgentID)
	if !ok || runtimeID != launch.RuntimeID {
		return messageDeliveryAcceptance{}, fmt.Errorf("%w: agent %s runtime %s", errDeliveryInboxRuntimeMismatch, delivery.AgentID, launch.RuntimeID)
	}
	delivery.Message.RunID = delivery.RunID
	delivery.Message.RunAgentID = delivery.RunAgentID
	delivery.Message.DeliveryID = delivery.DeliveryID
	accepted, err := coordinator.Accept(ctx, delivery)
	if err != nil {
		return messageDeliveryAcceptance{}, err
	}
	result := messageDeliveryAcceptance{
		ack:     coordinator.Acknowledgement(delivery),
		outcome: messageDeliveryPendingBuffered,
	}
	if !accepted {
		result.outcome = messageDeliveryDeduplicated
	}
	runner.recordDiagnostic(canonicalMessageDiagnosticEvent(
		runner.config.WorkspaceID, runtimeID, delivery, "coordinator_accepted", string(result.outcome), "",
	))
	if launch.QueueState != protocol.AgentStartQueueRunning {
		return result, nil
	}
	runIdentity, identityErr := residentPiRunIdentity(delivery.RunID, delivery.RunAgentID)
	if identityErr != nil {
		runner.recordDiagnostic(canonicalMessageDiagnosticEvent(
			runner.config.WorkspaceID, runtimeID, delivery, "runtime_delivery_attempted", "failed", canonicalMessageFailureReason(identityErr),
		))
		return messageDeliveryAcceptance{}, fmt.Errorf("%w: %v", errDeliveryResidentIdentityInvalid, identityErr)
	}
	if err := runner.ensureResidentRuntime(ctx, delivery.AgentID, runtimeID, runIdentity); err != nil {
		runner.recordDiagnostic(canonicalMessageDiagnosticEvent(
			runner.config.WorkspaceID, runtimeID, delivery, "runtime_delivery_attempted", "deferred", canonicalMessageFailureReason(err),
		))
		if runner.logger != nil {
			runner.logger.Warn("WorkspaceDaemon resident Message Runtime unavailable before delivery acknowledgement", "error", err, "workspace_id", runner.config.WorkspaceID, "agent_id", delivery.AgentID, "runtime_id", runtimeID, "delivery_id", delivery.DeliveryID, "acceptance", result.outcome)
		}
		return result, nil
	}

	handedOff, err := coordinator.flushWithResult(ctx, true)
	if handedOff {
		result.outcome = messageDeliveryProviderAccepted
	}
	if err != nil {
		deferred := errors.Is(err, ErrCanonicalAgentRuntimeBusy)
		outcome := map[bool]string{true: "deferred", false: "failed"}[deferred]
		runner.recordDiagnostic(canonicalMessageDiagnosticEvent(
			runner.config.WorkspaceID, runtimeID, delivery, "context_boundary_advanced", outcome, canonicalMessageFailureReason(err),
		))
		if runner.logger != nil {
			if deferred {
				runner.logger.Debug("WorkspaceDaemon Agent Message delivery deferred before acknowledgement", "reason", "runtime_busy", "outcome", outcome, "workspace_id", runner.config.WorkspaceID, "agent_id", delivery.AgentID, "runtime_id", runtimeID, "delivery_id", delivery.DeliveryID, "acceptance", result.outcome)
			} else {
				runner.logger.Warn("WorkspaceDaemon Agent Message delivery incomplete before acknowledgement", "error", err, "outcome", outcome, "workspace_id", runner.config.WorkspaceID, "agent_id", delivery.AgentID, "runtime_id", runtimeID, "delivery_id", delivery.DeliveryID, "acceptance", result.outcome)
			}
		}
		if deferred {
			runner.recoverStalledRuntimeForQueuedMessage(coordinator, delivery.AgentID, runtimeID)
			return result, nil
		}
		return messageDeliveryAcceptance{}, fmt.Errorf("%w: %v", errDeliveryProviderRejected, err)
	}
	runner.recordDiagnostic(canonicalMessageDiagnosticEvent(
		runner.config.WorkspaceID, runtimeID, delivery, "context_boundary_advanced", "accepted", "",
	))
	return result, nil
}

// recoverStalledRuntimeForQueuedMessage asks the resident pool to terminate a
// silent-past-window runtime now that a Message has been queued against it
// (ErrCanonicalAgentRuntimeBusy). It only bothers when Messages are actually
// waiting: an empty Pending queue means nothing is stuck behind the wedge, so
// there is nothing to recover for yet. See resident_stall_queued_recovery.go
// for why this check does not require the process to be confirmed dead.
func (runner *WorkspaceDaemon) recoverStalledRuntimeForQueuedMessage(coordinator *MessageCoordinator, agentID, runtimeID string) {
	if runner == nil || runner.runtimes == nil || coordinator == nil {
		return
	}
	pending := coordinator.PendingCount()
	if pending == 0 {
		return
	}
	recovered, err := runner.runtimes.recoverStalledSlotForQueuedMessage(agentID, runtimeID)
	if err != nil {
		if runner.logger != nil {
			runner.logger.Warn("WorkspaceDaemon failed to recover stalled resident runtime for queued Messages", "error", err, "workspace_id", runner.config.WorkspaceID, "agent_id", agentID, "runtime_id", runtimeID)
		}
		return
	}
	if recovered && runner.logger != nil {
		runner.logger.Warn("WorkspaceDaemon terminated stalled resident runtime for queued Messages", "workspace_id", runner.config.WorkspaceID, "agent_id", agentID, "runtime_id", runtimeID, "pending_count", pending)
	}
}

func (runner *WorkspaceDaemon) acknowledgeConsumedDelivery(delivery protocol.AgentDeliverPayload) (messageDeliveryAcceptance, bool) {
	coordinator, _, ok := runner.messageCoordinator(delivery.AgentID)
	if !ok {
		return messageDeliveryAcceptance{}, false
	}
	seq, known, err := runner.messageContextBoundary(delivery.AgentID, delivery.Target)
	if err != nil || !known || delivery.Seq > seq {
		return messageDeliveryAcceptance{}, false
	}
	return messageDeliveryAcceptance{
		ack:     coordinator.Acknowledgement(delivery),
		outcome: messageDeliveryDeduplicated,
	}, true
}

func (runner *WorkspaceDaemon) acceptDeliveryWithoutLiveProcess(ctx context.Context, delivery protocol.AgentDeliverPayload) (messageDeliveryAcceptance, error) {
	res, had := runner.residency.get(delivery.AgentID)
	now := time.Now()
	if runner.residency != nil && runner.residency.now != nil {
		now = runner.residency.now()
	}
	switch {
	case had && res.terminal:
		acceptance, err := runner.bufferAcceptedDelivery(ctx, delivery)
		if err == nil {
			runner.republishTerminalFailure(delivery.AgentID, res)
		}
		return acceptance, err
	case had && res.idle && res.coolingDown(now):
		return runner.bufferAcceptedDelivery(ctx, delivery)
	case had && res.idle:
		if _, managed := runner.processes.Snapshot(delivery.AgentID); managed {
			return runner.bufferAcceptedDelivery(ctx, delivery)
		}
		parent := ctx
		if runner.life != nil {
			parent = runner.life
		}
		restartCtx, started := runner.residency.beginRestart(parent, delivery.AgentID)
		if !started {
			return runner.bufferAcceptedDelivery(ctx, delivery)
		}
		if err := runner.restartFromIdleSnapshot(delivery.AgentID, res); err != nil {
			runner.residency.endRestart(delivery.AgentID)
			return messageDeliveryAcceptance{}, fmt.Errorf("%w: agent %s: %v", errDeliveryIdleRestoreFailed, delivery.AgentID, err)
		}
		acceptance, err := runner.bufferAcceptedDelivery(ctx, delivery)
		if err != nil {
			runner.residency.endRestart(delivery.AgentID)
			return acceptance, err
		}
		go func() {
			defer runner.residency.endRestart(delivery.AgentID)
			runner.completeIdleSnapshotStart(restartCtx, delivery.AgentID, res)
		}()
		return acceptance, nil
	default:
		runner.reportProcessUnavailable(delivery.AgentID)
		return messageDeliveryAcceptance{}, fmt.Errorf("%w: agent %s", errDeliveryRejectedNoProcess, delivery.AgentID)
	}
}

func (runner *WorkspaceDaemon) bufferAcceptedDelivery(ctx context.Context, delivery protocol.AgentDeliverPayload) (messageDeliveryAcceptance, error) {
	coordinator, runtimeID, ok := runner.messageCoordinator(delivery.AgentID)
	if !ok {
		return messageDeliveryAcceptance{}, fmt.Errorf("%w: agent %s", errDeliveryRejectedNoInbox, delivery.AgentID)
	}
	delivery.Message.RunID = delivery.RunID
	delivery.Message.RunAgentID = delivery.RunAgentID
	delivery.Message.DeliveryID = delivery.DeliveryID
	accepted, err := coordinator.Accept(ctx, delivery)
	if err != nil {
		return messageDeliveryAcceptance{}, err
	}
	result := messageDeliveryAcceptance{
		ack:     coordinator.Acknowledgement(delivery),
		outcome: messageDeliveryPendingBuffered,
	}
	if !accepted {
		result.outcome = messageDeliveryDeduplicated
	}
	runner.recordDiagnostic(canonicalMessageDiagnosticEvent(
		runner.config.WorkspaceID, runtimeID, delivery, "coordinator_accepted", string(result.outcome), "",
	))
	return result, nil
}

func (runner *WorkspaceDaemon) restartFromIdleSnapshot(agentID string, res agentResidency) error {
	if runner.processes == nil || res.runtimeID == "" || res.agentInstanceID == "" {
		return fmt.Errorf("idle snapshot for Agent %q is incomplete", agentID)
	}
	return runner.processes.RestoreIdle(agentID, res.runtimeID, res.agentInstanceID, runner.residency)
}

func (runner *WorkspaceDaemon) completeIdleSnapshotStart(ctx context.Context, agentID string, res agentResidency) {
	if ctx == nil {
		ctx = context.Background()
	}
	current, found := runner.processes.Snapshot(agentID)
	if !found {
		return
	}
	callback := agentProcessCallback{AgentID: agentID, AgentInstanceID: current.AgentInstanceID}
	if ctx.Err() != nil {
		runner.processes.completeFailedManagedStart(callback)
		return
	}
	start := protocol.AgentStartPayload{
		AgentID: agentID, RuntimeID: res.runtimeID}
	ack := protocol.AgentStartAckPayload{
		AgentID: agentID, QueueState: protocol.AgentStartQueueStarting,
	}
	runner.startAgentNow(ctx, start, callback, ack, nil, nil, nil, nil)
}

func (runner *WorkspaceDaemon) reportProcessUnavailable(agentID string) {
	if runner == nil || agentID == "" {
		return
	}
	agentInstanceID := ""
	if res, ok := runner.residency.get(agentID); ok {
		agentInstanceID = res.agentInstanceID
	}
	if agentInstanceID == "" {
		return
	}
	runner.sendAgentFrame(protocol.EventAgentStatus, protocol.AgentStatusPayload{
		AgentID: agentID, Status: protocol.AgentStatusInactive,
	})
	now := time.Now().UTC()
	entry, err := activityStatusEntry("runtime_unavailable", "Process unavailable; restart required")
	if err != nil {
		return
	}
	runner.sendAgentFrame(protocol.EventAgentActivity, protocol.AgentActivityPayload{
		Snapshot: protocol.AgentActivitySnapshot{
			AgentID:          agentID,
			DaemonInstanceID: runner.config.DaemonInstanceID,
			ClientSequence:   1,
			ProducerFactID:   fmt.Sprintf("runtime-unavailable-%s-%d", agentID, now.UnixNano()),
			ObservedAt:       now,
			ActivityKind:     protocol.ActivityKindOffline,
			DetailKind:       "runtime_unavailable",
		},
		Detail:  "Process unavailable; restart required",
		Entries: []protocol.AgentActivityEntry{entry},
	})
}

func (runner *WorkspaceDaemon) republishTerminalFailure(agentID string, res agentResidency) {
	if runner.activity == nil {
		return
	}
	kind := AgentObservationError
	if res.terminalStage == managedRuntimeFailureSpawn {
		kind = AgentObservationOffline
	}
	runner.observeActivity(AgentObservation{
		AgentID: agentID, Kind: kind,
		Data: AgentErrorObservationData{RuntimeID: res.runtimeID, ReasonCode: res.terminalReason, Message: res.terminalDetail},
		At:   time.Now().UTC(),
	}, "Runtime failure")
}

func (runner *WorkspaceDaemon) notifyPendingMessagesAfterTurn(agentID string) {
	coordinator, _, ok := runner.messageCoordinator(agentID)
	if ok {
		coordinator.NotifyPendingAfterTurn()
	}
}

func (runner *WorkspaceDaemon) commitMessageCoverage(key InboxKey, receiptID string) error {
	coordinator, _, ok := runner.messageCoordinator(key.AgentID)
	if !ok || !coordinator.hasInboxKey(key) {
		return ErrCoverageReceiptInvalid
	}
	if !coordinator.ownsCoverageReceipt(receiptID) {
		return ErrCoverageReceiptInvalid
	}
	return coordinator.CommitCoverage(receiptID)
}

func (runner *WorkspaceDaemon) ownsMessageCoverageReceipt(receiptID string) bool {
	if runner == nil || runner.inboxes == nil {
		return false
	}
	for _, entry := range runner.inboxes.snapshot() {
		if entry.coordinator != nil && entry.coordinator.ownsCoverageReceipt(receiptID) {
			return true
		}
	}
	return false
}

// handleMessageDelivery owns the wire half of one durable Message transition.
// The deep acceptance module above decides whether the provider or per-Agent
// Pending projection accepted the body; only then may this caller ACK.
func (runner *WorkspaceDaemon) handleMessageDelivery(
	ctx context.Context,
	delivery protocol.AgentDeliverPayload,
	writeFrame func(string, any) error,
) error {
	runtimeID := runner.messageRuntimeID(delivery.AgentID)
	runner.recordDiagnostic(canonicalMessageDiagnosticEvent(
		runner.config.WorkspaceID, runtimeID, delivery, "runner_received", "accepted", "",
	))
	acceptance, err := runner.acceptMessageDelivery(ctx, delivery)
	// Acceptance may create the Inbox and discover its durable Runtime.
	runtimeID = runner.messageRuntimeID(delivery.AgentID)
	if err != nil {
		runner.recordDiagnostic(canonicalMessageDiagnosticEvent(
			runner.config.WorkspaceID, runtimeID, delivery, "coordinator_accepted", "rejected", canonicalMessageFailureReason(err),
		))
		if runner.logger != nil {
			runner.logger.Warn("WorkspaceDaemon Agent delivery not acknowledged", "error", err, "workspace_id", runner.config.WorkspaceID, "agent_id", delivery.AgentID, "delivery_id", delivery.DeliveryID)
		}
		return nil
	}
	// Cache the delivery's effective graph memory profile for this workspace
	// (spec §10): resident-message memory prep applies it per call. An empty
	// profile from an old server never clobbers the cached entry.
	if runner.rememberGraphProfile != nil {
		runner.rememberGraphProfile(delivery.MemoryType, delivery.ExploreAgents, delivery.ExploreMaxRounds)
	}
	runner.recordDiagnostic(canonicalMessageDiagnosticEvent(
		runner.config.WorkspaceID, runtimeID, delivery, "ack_attempted", string(acceptance.outcome), "",
	))
	// engineering-principles.md §1.5: channel Raft may ACK into Pending before
	// the provider is Running. Standalone chat: (FAB / Notes bubble) must not —
	// acked_at stops server redelivery, and a stuck Starting launch then leaves
	// the UI pending forever with no ledger recovery.
	if !runner.shouldPersistDeliveryAck(delivery, acceptance) {
		if runner.logger != nil {
			runner.logger.Info("WorkspaceDaemon deferred standalone chat delivery acknowledgement",
				"workspace_id", runner.config.WorkspaceID,
				"agent_id", delivery.AgentID,
				"runtime_id", runtimeID,
				"delivery_id", delivery.DeliveryID,
				"message_id", delivery.Message.ID,
				"target", delivery.Target,
				"seq", delivery.Seq,
				"acceptance", acceptance.outcome,
			)
		}
		runner.recordDiagnostic(canonicalMessageDiagnosticEvent(
			runner.config.WorkspaceID, runtimeID, delivery, "ack_deferred", string(acceptance.outcome), "",
		))
		return nil
	}
	if err := writeFrame(protocol.EventAgentDeliverAck, acceptance.ack); err != nil {
		runner.recordDiagnostic(canonicalMessageDiagnosticEvent(
			runner.config.WorkspaceID, runtimeID, delivery, "ack_sent", "failed", "runner_connection_write_failed",
		))
		if runner.logger != nil {
			runner.logger.Warn("WorkspaceDaemon Agent delivery acknowledgement failed", "error", err, "workspace_id", runner.config.WorkspaceID, "agent_id", delivery.AgentID, "runtime_id", runtimeID, "delivery_id", delivery.DeliveryID, "seq", delivery.Seq, "acceptance", acceptance.outcome)
		}
		return err
	}
	runner.recordDiagnostic(canonicalMessageDiagnosticEvent(
		runner.config.WorkspaceID, runtimeID, delivery, "ack_sent", string(acceptance.outcome), "",
	))
	if runner.logger != nil {
		runner.logger.Info("WorkspaceDaemon Agent delivery acknowledged", "workspace_id", runner.config.WorkspaceID, "agent_id", delivery.AgentID, "runtime_id", runtimeID, "delivery_id", delivery.DeliveryID, "message_id", delivery.Message.ID, "target", delivery.Target, "seq", delivery.Seq, "acceptance", acceptance.outcome)
	}
	return nil
}

// shouldPersistDeliveryAck decides whether the server ledger may mark this
// delivery acked. Channel targets keep Raft's accept-into-Pending ACK. Standalone
// chat: targets only ACK after provider handoff or a true consumed boundary.
func (runner *WorkspaceDaemon) shouldPersistDeliveryAck(delivery protocol.AgentDeliverPayload, acceptance messageDeliveryAcceptance) bool {
	if !strings.HasPrefix(strings.TrimSpace(delivery.Target), "chat:") {
		return true
	}
	switch acceptance.outcome {
	case messageDeliveryProviderAccepted:
		return true
	case messageDeliveryDeduplicated:
		seq, known, err := runner.messageContextBoundary(delivery.AgentID, delivery.Target)
		return err == nil && known && delivery.Seq <= seq
	default:
		return false
	}
}

func (runner *WorkspaceDaemon) sendAgentFrame(eventType string, payload any) bool {
	return runner != nil && runner.sendOnCurrentConnection(eventType, payload) == nil
}

func (runner *WorkspaceDaemon) hasRuntime(runtimeID string) bool {
	if runner == nil || runner.runtimeIDs == nil {
		return false
	}
	for _, current := range runner.runtimeIDs() {
		if current == strings.TrimSpace(runtimeID) {
			return true
		}
	}
	return false
}
