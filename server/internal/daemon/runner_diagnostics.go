package daemon

import (
	"errors"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/diagnosticlog"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type runnerDiagnosticSink interface {
	record(string, diagnosticlog.Event) error
}

func (d *WorkspaceDaemonCore) recordRunnerDiagnostic(workspaceID string, event diagnosticlog.Event) {
	if d == nil || d.runnerDiagnostics == nil {
		return
	}
	if err := d.runnerDiagnostics.record(workspaceID, event); err != nil && d.logger != nil {
		// Do not include the storage error or event payload in the fallback log.
		// Sink health and the later Service summary own those details.
		d.logger.Warn("WorkspaceDaemon diagnostic record dropped", "reason", "sink_unavailable")
	}
}

func canonicalMessageDiagnosticEvent(
	workspaceID, runtimeID string,
	delivery protocol.AgentDeliverPayload,
	phase, outcome, reasonCode string,
) diagnosticlog.Event {
	return diagnosticlog.Event{
		Name:      diagnosticlog.EventDeliveryStateChanged,
		Level:     diagnosticLevel(outcome),
		Component: "message_coordinator",
		Identity: diagnosticlog.Identity{
			AgentID:    delivery.AgentID,
			RuntimeID:  runtimeID,
			MessageID:  delivery.Message.ID,
			DeliveryID: delivery.DeliveryID,
			TraceID:    traceIDFromTraceparent(delivery.Traceparent),
			ChannelID:  delivery.Message.ChannelID,
		},
		Fields: diagnosticlog.Fields{
			Phase:      phase,
			Outcome:    outcome,
			ReasonCode: reasonCode,
			SeqFrom:    delivery.Seq,
			SeqTo:      delivery.Seq,
		},
	}
}

func (d *WorkspaceDaemonCore) recordResidentMessageBatch(
	workspaceID, runtimeID, agentID string,
	messages []protocol.AgentMessageProjection,
	phase, outcome, reasonCode string,
) {
	for _, message := range messages {
		event := canonicalMessageDiagnosticEvent(workspaceID, runtimeID, protocol.AgentDeliverPayload{
			AgentID: agentID,
			Target:  message.Target,
			Seq:     message.Seq,
			Message: message,
		}, phase, outcome, reasonCode)
		event.Component = "resident_runtime"
		d.recordRunnerDiagnostic(workspaceID, event)
	}
}

func (d *WorkspaceDaemonCore) recordAgentMessageResponse(
	workspaceID, agentID, requestID, contextTarget string,
	response map[string]any,
	phase, outcome, reasonCode string,
) {
	if d == nil {
		return
	}
	runtimeID := ""
	if runner := d.currentWorkspaceSession(workspaceID); runner != nil {
		runtimeID = runner.messageRuntimeID(agentID)
	}
	messageID, channelID := responseMessageIdentity(response)
	if channelID == "" {
		channelID = channelIDFromCanonicalTarget(contextTarget)
	}
	d.recordRunnerDiagnostic(workspaceID, diagnosticlog.Event{
		Name:      diagnosticlog.EventDeliveryStateChanged,
		Level:     diagnosticLevel(outcome),
		Component: "credential_proxy",
		Identity: diagnosticlog.Identity{
			AgentID:   agentID,
			RuntimeID: runtimeID,
			MessageID: messageID,
			RequestID: requestID,
			ChannelID: channelID,
		},
		Fields: diagnosticlog.Fields{Phase: phase, Outcome: outcome, ReasonCode: reasonCode},
	})
}

// recordStandaloneChatCheckpoint records only standalone FAB/bubble Chat.
// Ordinary DM/group messages never satisfy this predicate: they use the
// canonical MessageCoordinator agent:deliver path and have their own delivery
// checkpoints above.
func (d *WorkspaceDaemonCore) recordStandaloneChatCheckpoint(
	task Task,
	phase, outcome, status, reasonCode, executionID, provider string,
	ackedSeq int64,
) {
	lease := task.InboxEvent
	if lease == nil ||
		lease.Reason != protocol.AgentInboxReasonChatSession ||
		lease.ResponseMode != "public_response" ||
		strings.TrimSpace(task.ChatSessionID) == "" {
		return
	}
	if executionID == "" {
		executionID = lease.ExecutionID
	}
	component := "agent_inbox"
	if phase == "provider_finished" {
		component = "provider_runner"
	}
	d.recordRunnerDiagnostic(task.WorkspaceID, diagnosticlog.Event{
		Name:      diagnosticlog.EventChatTurnCheckpoint,
		Level:     diagnosticLevel(outcome),
		Component: component,
		Identity: diagnosticlog.Identity{
			AgentID:         task.AgentID,
			RuntimeID:       task.RuntimeID,
			TaskID:          task.ID,
			DeliveryID:      lease.DeliveryID,
			ChatSessionID:   task.ChatSessionID,
			ConversationID:  lease.ConversationID,
			SourceMessageID: lease.SourceMessageID,
			ExecutionID:     executionID,
		},
		Fields: diagnosticlog.Fields{
			Phase:        phase,
			Outcome:      outcome,
			Status:       status,
			ReasonCode:   reasonCode,
			ResponseMode: lease.ResponseMode,
			Provider:     provider,
			SeqFrom:      lease.SeqFrom,
			SeqTo:        lease.SeqTo,
			AckedSeq:     ackedSeq,
			FoldedCount:  int64(len(task.FoldedInboxEvents)),
		},
	})
}

func responseMessageIdentity(response map[string]any) (messageID, channelID string) {
	message, _ := response["message"].(map[string]any)
	messageID, _ = message["id"].(string)
	channelID, _ = message["channel_id"].(string)
	return strings.TrimSpace(messageID), strings.TrimSpace(channelID)
}

func channelIDFromCanonicalTarget(target string) string {
	value, ok := strings.CutPrefix(strings.TrimSpace(target), "channel:")
	if !ok {
		return ""
	}
	if _, err := uuid.Parse(value); err != nil {
		return ""
	}
	return value
}

func diagnosticLevel(outcome string) diagnosticlog.Level {
	switch outcome {
	case "failed", "rejected":
		return diagnosticlog.LevelError
	case "deferred", "held", "degraded", "discarded", "cancelled":
		return diagnosticlog.LevelWarn
	default:
		return diagnosticlog.LevelInfo
	}
}

func traceIDFromTraceparent(traceparent string) string {
	parts := strings.Split(strings.TrimSpace(traceparent), "-")
	if len(parts) != 4 || len(parts[1]) != 32 {
		return ""
	}
	if _, err := strconv.ParseUint(parts[1][:16], 16, 64); err != nil {
		return ""
	}
	if _, err := strconv.ParseUint(parts[1][16:], 16, 64); err != nil {
		return ""
	}
	return parts[1]
}

func canonicalMessageFailureReason(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, ErrCanonicalAgentRuntimeBusy):
		return "runtime_busy"
	case strings.Contains(err.Error(), "freshness is unknown"):
		return "freshness_unknown"
	case strings.Contains(err.Error(), "persist Context Boundary"):
		return "context_boundary_persist_failed"
	case strings.Contains(err.Error(), "resident Agent configuration"):
		return "runtime_config_invalid"
	case strings.Contains(err.Error(), "resident Message runtime") || strings.Contains(err.Error(), "canonical resident runtime"):
		return "runtime_unavailable"
	default:
		return "message_delivery_failed"
	}
}
