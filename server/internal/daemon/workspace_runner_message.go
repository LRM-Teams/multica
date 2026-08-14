package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// messageCoordinator resolves an Inbox only inside this Runner's immutable
// Workspace scope. Callers never inspect the Runner's Inbox registry directly.
func (runner *WorkspaceRunner) messageCoordinator(agentID string) (*MessageCoordinator, string, bool) {
	if runner == nil || runner.inboxes == nil {
		return nil, "", false
	}
	return runner.inboxes.Resolve(agentID)
}

func (runner *WorkspaceRunner) messageRuntimeID(agentID string) string {
	_, runtimeID, _ := runner.messageCoordinator(agentID)
	return runtimeID
}

func (runner *WorkspaceRunner) ensureMessageInbox(agentID, expectedRuntimeID string) (bool, error) {
	if runner == nil || runner.inboxes == nil {
		return false, errors.New("Workspace Runner Inbox registry is unavailable")
	}
	created, err := runner.inboxes.Ensure(agentID)
	if err != nil {
		return false, err
	}
	_, runtimeID, ok := runner.messageCoordinator(agentID)
	if !ok || (expectedRuntimeID != "" && runtimeID != expectedRuntimeID) {
		runner.inboxes.Remove(agentID, runtimeID)
		return false, fmt.Errorf("Workspace Runner Inbox Runtime mismatch for Agent %q", agentID)
	}
	return created, nil
}

func (runner *WorkspaceRunner) hasMessageInbox(agentID string) bool {
	_, _, ok := runner.messageCoordinator(agentID)
	return ok
}

func (runner *WorkspaceRunner) messageContextBoundary(agentID, target string) (int64, bool, error) {
	coordinator, _, ok := runner.messageCoordinator(agentID)
	if !ok {
		return 0, false, errors.New("Message coordinator is unavailable")
	}
	seq, known := coordinator.ContextBoundary(target)
	return seq, known, nil
}

func (runner *WorkspaceRunner) prepareMessageCoverage(agentID string, request CoverageRequest) (CoverageOffer, error) {
	coordinator, _, ok := runner.messageCoordinator(agentID)
	if !ok {
		return CoverageOffer{}, errors.New("Message coordinator is unavailable")
	}
	return coordinator.PrepareCoverage(request)
}

func (runner *WorkspaceRunner) messageSendBoundarySnapshot(agentID, target string) (int64, error) {
	coordinator, _, ok := runner.messageCoordinator(agentID)
	if !ok {
		return 0, errors.New("Message coordinator is unavailable")
	}
	return coordinator.SendBoundarySnapshot(target), nil
}

func (runner *WorkspaceRunner) preflightMessageSend(agentID, target string) (MessageSendFreshness, error) {
	coordinator, _, ok := runner.messageCoordinator(agentID)
	if !ok {
		return MessageSendFreshness{}, errors.New("Message coordinator is unavailable")
	}
	return coordinator.PreflightMessageSend(target)
}

func (runner *WorkspaceRunner) removeMessageInbox(agentID, runtimeID string) {
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
// gives the current launch's durable Pending projection responsibility before
// attempting provider handoff. A missing live launch is not a terminal NACK:
// already-consumed, terminal, idle-snapshot, and spawn-cooldown deliveries
// stay locally responsible. Only "no process and no snapshot" rejects.
func (runner *WorkspaceRunner) acceptMessageDelivery(ctx context.Context, delivery protocol.AgentDeliverPayload) (messageDeliveryAcceptance, error) {
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
			runner.config.WorkspaceID, runtimeID, delivery, "runtime_handoff_attempted", "failed", canonicalMessageFailureReason(identityErr),
		))
		return messageDeliveryAcceptance{}, fmt.Errorf("%w: %v", errDeliveryResidentIdentityInvalid, identityErr)
	}
	if err := runner.ensureResidentRuntime(ctx, delivery.AgentID, runtimeID, runIdentity); err != nil {
		runner.recordDiagnostic(canonicalMessageDiagnosticEvent(
			runner.config.WorkspaceID, runtimeID, delivery, "runtime_handoff_attempted", "deferred", canonicalMessageFailureReason(err),
		))
		if runner.logger != nil {
			runner.logger.Warn("Workspace Runner resident Message Runtime unavailable before delivery acknowledgement", "error", err, "workspace_id", runner.config.WorkspaceID, "agent_id", delivery.AgentID, "runtime_id", runtimeID, "delivery_id", delivery.DeliveryID, "acceptance", result.outcome)
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
			runner.config.WorkspaceID, runtimeID, delivery, "context_boundary_persisted", outcome, canonicalMessageFailureReason(err),
		))
		if runner.logger != nil {
			if deferred {
				runner.logger.Debug("Workspace Runner Agent Message handoff deferred before delivery acknowledgement", "reason", "runtime_busy", "outcome", outcome, "workspace_id", runner.config.WorkspaceID, "agent_id", delivery.AgentID, "runtime_id", runtimeID, "delivery_id", delivery.DeliveryID, "acceptance", result.outcome)
			} else {
				runner.logger.Warn("Workspace Runner Agent Message handoff incomplete before delivery acknowledgement", "error", err, "outcome", outcome, "workspace_id", runner.config.WorkspaceID, "agent_id", delivery.AgentID, "runtime_id", runtimeID, "delivery_id", delivery.DeliveryID, "acceptance", result.outcome)
			}
		}
		if deferred {
			return result, nil
		}
		return messageDeliveryAcceptance{}, fmt.Errorf("%w: %v", errDeliveryProviderRejected, err)
	}
	runner.recordDiagnostic(canonicalMessageDiagnosticEvent(
		runner.config.WorkspaceID, runtimeID, delivery, "context_boundary_persisted", "accepted", "",
	))
	return result, nil
}

func (runner *WorkspaceRunner) acknowledgeConsumedDelivery(delivery protocol.AgentDeliverPayload) (messageDeliveryAcceptance, bool) {
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

func (runner *WorkspaceRunner) acceptDeliveryWithoutLiveProcess(ctx context.Context, delivery protocol.AgentDeliverPayload) (messageDeliveryAcceptance, error) {
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

func (runner *WorkspaceRunner) bufferAcceptedDelivery(ctx context.Context, delivery protocol.AgentDeliverPayload) (messageDeliveryAcceptance, error) {
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

func (runner *WorkspaceRunner) restartFromIdleSnapshot(agentID string, res agentResidency) error {
	if runner.processes == nil || res.runtimeID == "" || res.launchID == "" {
		return fmt.Errorf("idle snapshot for Agent %q is incomplete", agentID)
	}
	return runner.processes.RestoreIdle(agentID, res.runtimeID, res.launchID)
}

func (runner *WorkspaceRunner) completeIdleSnapshotStart(ctx context.Context, agentID string, res agentResidency) {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return
	}
	start := protocol.WorkspaceRunnerAgentStartPayload{
		AgentID: agentID, RuntimeID: res.runtimeID, LaunchID: res.launchID, StartDispatchID: res.startDispatchID,
	}
	ack := protocol.AgentStartAckPayload{AgentID: agentID, LaunchID: res.launchID, StartDispatchID: res.startDispatchID}
	if _, _, err := runner.completeManagedAgentStart(ctx, start, ack); err != nil {
		if runner.logger != nil && ctx.Err() == nil {
			runner.logger.Warn("Workspace Runner idle snapshot start failed", runner.managedStartLogAttrs(start, protocol.AgentStartQueueStarting, "provider_start_failed", "failed", err)...)
		}
		return
	}
	runner.publishManagedAgentStartActivity(agentID, res.runtimeID)
}

func (runner *WorkspaceRunner) reportProcessUnavailable(agentID string) {
	if runner == nil || agentID == "" {
		return
	}
	launchID := ""
	if res, ok := runner.residency.get(agentID); ok {
		launchID = res.launchID
	}
	if launchID == "" {
		return
	}
	runner.sendAgentFrame(protocol.EventAgentStatus, protocol.AgentStatusPayload{
		AgentID: agentID, LaunchID: launchID, Status: protocol.AgentStatusInactive,
	})
	now := time.Now().UTC()
	entry, err := activityNarrativeEntry("runtime_unavailable", "Process unavailable; restart required")
	if err != nil {
		return
	}
	runner.sendAgentFrame(protocol.EventAgentActivity, protocol.AgentActivityPayload{
		Snapshot: protocol.AgentActivitySnapshot{
			AgentID:          agentID,
			LaunchID:         launchID,
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

func (runner *WorkspaceRunner) republishTerminalFailure(agentID string, res agentResidency) {
	if runner.activity == nil {
		return
	}
	kind := AgentObservationError
	if res.terminalStage == managedRuntimeFailureSpawn {
		kind = AgentObservationOffline
	}
	runner.observeActivity(AgentObservation{
		AgentID: agentID, LaunchID: res.launchID, Kind: kind,
		Data: AgentErrorObservationData{RuntimeID: res.runtimeID, ReasonCode: res.terminalReason},
		At:   time.Now().UTC(),
	}, "Runtime failure")
}

func (runner *WorkspaceRunner) notifyPendingMessagesAfterTurn(agentID string) {
	coordinator, _, ok := runner.messageCoordinator(agentID)
	if ok {
		coordinator.NotifyPendingAfterTurn()
	}
}

func (runner *WorkspaceRunner) commitMessageCoverage(key InboxKey, receiptID string) error {
	coordinator, _, ok := runner.messageCoordinator(key.AgentID)
	if !ok || !coordinator.hasInboxKey(key) {
		return ErrCoverageReceiptInvalid
	}
	if !coordinator.ownsCoverageReceipt(receiptID) {
		return ErrCoverageReceiptInvalid
	}
	return coordinator.CommitCoverage(receiptID)
}

func (runner *WorkspaceRunner) ownsMessageCoverageReceipt(receiptID string) bool {
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
func (runner *WorkspaceRunner) handleMessageDelivery(
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
			runner.logger.Warn("Workspace Runner Agent delivery not acknowledged", "error", err, "workspace_id", runner.config.WorkspaceID, "agent_id", delivery.AgentID, "delivery_id", delivery.DeliveryID)
		}
		return nil
	}
	runner.recordDiagnostic(canonicalMessageDiagnosticEvent(
		runner.config.WorkspaceID, runtimeID, delivery, "ack_attempted", string(acceptance.outcome), "",
	))
	if err := writeFrame(protocol.EventAgentDeliverAck, acceptance.ack); err != nil {
		runner.recordDiagnostic(canonicalMessageDiagnosticEvent(
			runner.config.WorkspaceID, runtimeID, delivery, "ack_sent", "failed", "runner_connection_write_failed",
		))
		if runner.logger != nil {
			runner.logger.Warn("Workspace Runner Agent delivery acknowledgement failed", "error", err, "workspace_id", runner.config.WorkspaceID, "agent_id", delivery.AgentID, "runtime_id", runtimeID, "delivery_id", delivery.DeliveryID, "seq", delivery.Seq, "acceptance", acceptance.outcome)
		}
		return err
	}
	runner.recordDiagnostic(canonicalMessageDiagnosticEvent(
		runner.config.WorkspaceID, runtimeID, delivery, "ack_sent", string(acceptance.outcome), "",
	))
	if runner.logger != nil {
		runner.logger.Info("Workspace Runner Agent delivery acknowledged", "workspace_id", runner.config.WorkspaceID, "agent_id", delivery.AgentID, "runtime_id", runtimeID, "delivery_id", delivery.DeliveryID, "message_id", delivery.Message.ID, "target", delivery.Target, "seq", delivery.Seq, "acceptance", acceptance.outcome)
	}
	return nil
}

func (runner *WorkspaceRunner) sendAgentFrame(eventType string, payload any) bool {
	return runner != nil && runner.sendOnCurrentConnection(eventType, payload) == nil
}

func (runner *WorkspaceRunner) hasRuntime(runtimeID string) bool {
	if runner == nil || runner.runtimeSet == nil {
		return false
	}
	for _, current := range runner.runtimeSet().RuntimeIDs {
		if current == strings.TrimSpace(runtimeID) {
			return true
		}
	}
	return false
}
