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

func (runner *WorkspaceRunner) acceptMessageDelivery(ctx context.Context, delivery protocol.AgentDeliverPayload) (protocol.AgentDeliverAckPayload, error) {
	coordinator, runtimeID, ok := runner.messageCoordinator(delivery.AgentID)
	if !ok {
		if _, err := runner.ensureMessageInbox(delivery.AgentID, ""); err != nil {
			return protocol.AgentDeliverAckPayload{}, err
		}
		coordinator, runtimeID, ok = runner.messageCoordinator(delivery.AgentID)
		if !ok {
			return protocol.AgentDeliverAckPayload{}, fmt.Errorf("no Message coordinator for Agent %q", delivery.AgentID)
		}
	}
	accepted, err := coordinator.Accept(ctx, delivery)
	if err != nil {
		return protocol.AgentDeliverAckPayload{}, err
	}
	outcome := "accepted"
	if !accepted {
		outcome = "deduplicated"
	}
	runner.recordDiagnostic(canonicalMessageDiagnosticEvent(
		runner.config.WorkspaceID, runtimeID, delivery, "coordinator_accepted", outcome, "",
	))
	return coordinator.Acknowledgement(delivery), nil
}

func (runner *WorkspaceRunner) flushMessageDelivery(ctx context.Context, agentID string) error {
	coordinator, _, ok := runner.messageCoordinator(agentID)
	if !ok {
		return fmt.Errorf("no Message coordinator for Agent %q", agentID)
	}
	return coordinator.Flush(ctx)
}

func (runner *WorkspaceRunner) beginMessageRecovery(agentID string) {
	coordinator, _, ok := runner.messageCoordinator(agentID)
	if !ok {
		return
	}
	request := coordinator.BeginRecovery(agentID, 100)
	if err := runner.sendOnCurrentConnection(protocol.EventAgentRecoveryRequest, request); err != nil && runner.logger != nil {
		runner.logger.Warn("Agent Message recovery request failed", "error", err, "workspace_id", runner.config.WorkspaceID, "agent_id", agentID, "reason", "runner_connection_write_failed")
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
	if page.HasMore {
		return send(coordinator.RecoveryRequest(page.AgentID, 100))
	}

	// Freshness is durable after MergeRecoveryPage. Runtime availability is a
	// separate best-effort concern and must never block the Runner read loop.
	flushCtx, cancel := context.WithTimeout(context.Background(), recoveryFlushTimeout)
	go func() {
		defer cancel()
		if err := runner.ensureResidentRuntime(flushCtx, page.AgentID, runtimeID); err != nil {
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

// handleMessageDelivery owns the complete durable Message transition for one
// Runner: accept/deduplicate, ACK, then best-effort resident Runtime handoff.
func (runner *WorkspaceRunner) handleMessageDelivery(
	ctx context.Context,
	delivery protocol.AgentDeliverPayload,
	writeFrame func(string, any) error,
) error {
	runtimeID := runner.messageRuntimeID(delivery.AgentID)
	runner.recordDiagnostic(canonicalMessageDiagnosticEvent(
		runner.config.WorkspaceID, runtimeID, delivery, "runner_received", "accepted", "",
	))
	ack, err := runner.acceptMessageDelivery(ctx, delivery)
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
		runner.config.WorkspaceID, runtimeID, delivery, "ack_attempted", "attempted", "",
	))
	if err := writeFrame(protocol.EventAgentDeliverAck, ack); err != nil {
		runner.recordDiagnostic(canonicalMessageDiagnosticEvent(
			runner.config.WorkspaceID, runtimeID, delivery, "ack_sent", "failed", "runner_connection_write_failed",
		))
		return err
	}
	runner.recordDiagnostic(canonicalMessageDiagnosticEvent(
		runner.config.WorkspaceID, runtimeID, delivery, "ack_sent", "accepted", "",
	))
	if err := runner.ensureResidentRuntime(ctx, delivery.AgentID, runtimeID); err != nil {
		runner.recordDiagnostic(canonicalMessageDiagnosticEvent(
			runner.config.WorkspaceID, runtimeID, delivery, "runtime_handoff_attempted", "failed", canonicalMessageFailureReason(err),
		))
		if runner.logger != nil {
			runner.logger.Warn("Workspace Runner resident Message Runtime unavailable after delivery acknowledgement", "error", err, "workspace_id", runner.config.WorkspaceID, "agent_id", delivery.AgentID, "delivery_id", delivery.DeliveryID)
		}
		return nil
	}
	if err := runner.flushMessageDelivery(ctx, delivery.AgentID); err != nil {
		outcome := "failed"
		if errors.Is(err, ErrCanonicalAgentRuntimeBusy) || strings.Contains(err.Error(), "freshness is unknown") {
			outcome = "deferred"
		}
		runner.recordDiagnostic(canonicalMessageDiagnosticEvent(
			runner.config.WorkspaceID, runtimeID, delivery, "context_boundary_persisted", outcome, canonicalMessageFailureReason(err),
		))
		if runner.logger != nil {
			runner.logger.Warn("Workspace Runner idle Agent Message handoff failed after delivery acknowledgement", "error", err, "workspace_id", runner.config.WorkspaceID, "agent_id", delivery.AgentID, "delivery_id", delivery.DeliveryID)
		}
		return nil
	}
	runner.recordDiagnostic(canonicalMessageDiagnosticEvent(
		runner.config.WorkspaceID, runtimeID, delivery, "context_boundary_persisted", "accepted", "",
	))
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
