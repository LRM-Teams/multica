package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"

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

type messageDeliveryAcceptance struct {
	ack     protocol.AgentDeliverAckPayload
	outcome messageDeliveryAcceptanceOutcome
}

// acceptMessageDelivery is the Raft-aligned per-Agent acceptance seam. It
// gives the current launch's durable Pending projection responsibility before
// attempting provider handoff. That is Raft's APM-accepted boundary: provider
// startup may still be deferred, while an unknown/stale launch is not ACKed.
func (runner *WorkspaceRunner) acceptMessageDelivery(ctx context.Context, delivery protocol.AgentDeliverPayload) (messageDeliveryAcceptance, error) {
	launch, managed := runner.processes.Snapshot(delivery.AgentID)
	if !managed || launch.QueueState == protocol.AgentStartQueueQueued {
		return messageDeliveryAcceptance{}, fmt.Errorf("Agent %q has not been accepted by APM", delivery.AgentID)
	}
	coordinator, runtimeID, ok := runner.messageCoordinator(delivery.AgentID)
	if !ok || runtimeID != launch.RuntimeID {
		return messageDeliveryAcceptance{}, fmt.Errorf("APM Agent %q has no Inbox for Runtime %q", delivery.AgentID, launch.RuntimeID)
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
	runIdentity, identityErr := residentPiRunIdentity(delivery.RunID, delivery.RunAgentID)
	if identityErr != nil {
		runner.recordDiagnostic(canonicalMessageDiagnosticEvent(
			runner.config.WorkspaceID, runtimeID, delivery, "runtime_handoff_attempted", "failed", canonicalMessageFailureReason(identityErr),
		))
		return messageDeliveryAcceptance{}, identityErr
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
		deferred := errors.Is(err, ErrCanonicalAgentRuntimeBusy) || strings.Contains(err.Error(), "freshness is unknown")
		outcome := map[bool]string{true: "deferred", false: "failed"}[deferred]
		runner.recordDiagnostic(canonicalMessageDiagnosticEvent(
			runner.config.WorkspaceID, runtimeID, delivery, "context_boundary_persisted", outcome, canonicalMessageFailureReason(err),
		))
		if runner.logger != nil {
			runner.logger.Warn("Workspace Runner Agent Message handoff incomplete before delivery acknowledgement", "error", err, "workspace_id", runner.config.WorkspaceID, "agent_id", delivery.AgentID, "runtime_id", runtimeID, "delivery_id", delivery.DeliveryID, "acceptance", result.outcome)
		}
		return result, nil
	}
	runner.recordDiagnostic(canonicalMessageDiagnosticEvent(
		runner.config.WorkspaceID, runtimeID, delivery, "context_boundary_persisted", "accepted", "",
	))
	return result, nil
}

func (runner *WorkspaceRunner) beginMessageRecovery(agentID string) {
	runner.beginMessageRecoveryForAgent(agentID, func(request protocol.AgentRecoveryRequest) error {
		return runner.sendOnCurrentConnection(protocol.EventAgentRecoveryRequest, request)
	})
}

func (runner *WorkspaceRunner) beginMessageRecoveryForAgent(agentID string, send func(protocol.AgentRecoveryRequest) error) {
	if send == nil {
		return
	}
	coordinator, _, ok := runner.messageCoordinator(agentID)
	if !ok {
		return
	}
	request := coordinator.BeginRecovery(agentID, 100)
	if runner.logger != nil {
		runner.logger.Info("Agent Message recovery started", "workspace_id", runner.config.WorkspaceID, "agent_id", agentID, "recovery_id", request.RecoveryID, "reason", "agent_scoped_recovery")
	}
	if err := send(request); err != nil && runner.logger != nil {
		runner.logger.Warn("Agent Message recovery request failed", "error", err, "workspace_id", runner.config.WorkspaceID, "agent_id", agentID, "recovery_id", request.RecoveryID, "reason", "runner_connection_write_failed")
	}
}

func (runner *WorkspaceRunner) beginMessageRecoveryForAll(send func(protocol.AgentRecoveryRequest) error) {
	if runner == nil || runner.inboxes == nil {
		return
	}
	runner.inboxes.BeginRecovery(send)
}

func (runner *WorkspaceRunner) mergeMessageRecoveryPage(page protocol.AgentRecoveryPage, send func(protocol.AgentRecoveryRequest) error) error {
	if send == nil {
		return errors.New("Message recovery sender is unavailable")
	}
	coordinator, runtimeID, ok := runner.messageCoordinator(page.AgentID)
	if !ok {
		return fmt.Errorf("no Message coordinator for recovery Agent %q", page.AgentID)
	}
	if err := coordinator.MergeRecoveryPage(page); err != nil {
		return err
	}
	if runner.logger != nil {
		runner.logger.Debug("Agent Message recovery page merged", "workspace_id", runner.config.WorkspaceID, "agent_id", page.AgentID, "runtime_id", runtimeID, "recovery_id", page.RecoveryID, "snapshot_id", page.SnapshotID, "message_count", len(page.Messages), "has_more", page.HasMore)
	}
	if page.HasMore {
		return send(coordinator.RecoveryRequest(page.AgentID, 100))
	}
	if runner.logger != nil {
		runner.logger.Info("Agent Message recovery completed", "workspace_id", runner.config.WorkspaceID, "agent_id", page.AgentID, "runtime_id", runtimeID, "recovery_id", page.RecoveryID, "snapshot_id", page.SnapshotID, "high_watermark", page.HighWatermark)
	}

	// Freshness is durable after MergeRecoveryPage. Runtime availability is a
	// separate best-effort concern and must never block the Runner read loop.
	runIdentity, identityErr := residentPiRunIdentity(page.RunID, page.RunAgentID)
	if identityErr != nil {
		return identityErr
	}
	flushCtx, cancel := context.WithTimeout(context.Background(), recoveryFlushTimeout)
	go func() {
		defer cancel()
		if err := runner.ensureResidentRuntime(flushCtx, page.AgentID, runtimeID, runIdentity); err != nil {
			if runner.logger != nil {
				runner.logger.Warn("Workspace Runner Message recovery Runtime unavailable", "error", err, "workspace_id", runner.config.WorkspaceID, "agent_id", page.AgentID, "recovery_id", page.RecoveryID)
			}
			return
		}
		if err := coordinator.Flush(flushCtx); err != nil && runner.logger != nil {
			runner.logger.Warn("Workspace Runner Message recovery flush deferred", "error", err, "workspace_id", runner.config.WorkspaceID, "agent_id", page.AgentID, "recovery_id", page.RecoveryID)
		}
	}()
	return nil
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
