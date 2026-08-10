package daemon

import (
	"errors"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/diagnosticlog"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// runnerDiagnosticRegistry is the only daemon-side owner of Workspace Runner
// diagnostic sinks. Producers supply a Workspace identity and a typed event;
// they never construct paths or propagate sink failures into product control
// flow.
type runnerDiagnosticRegistry struct {
	store              *diagnosticlog.Store
	environment        diagnosticlog.Environment
	runnerGeneration   string
	computerID         string
	computerGeneration string

	mu      sync.Mutex
	loggers map[string]*diagnosticlog.Logger
	failed  map[string]struct{}
}

func (d *Daemon) initializeRunnerDiagnostics() {
	if d == nil || d.runnerDiagnostics != nil {
		return
	}
	environment := diagnosticlog.Environment(strings.TrimSpace(d.cfg.Environment))
	if environment != diagnosticlog.EnvironmentProduction && environment != diagnosticlog.EnvironmentTest {
		if d.logger != nil {
			d.logger.Warn("Workspace Runner diagnostics disabled", "reason", "invalid_environment")
		}
		return
	}
	store, err := diagnosticlog.Open(diagnosticlog.Config{})
	if err != nil {
		if d.logger != nil {
			d.logger.Warn("Workspace Runner diagnostics disabled", "reason", "open_failed")
		}
		return
	}
	computerID := strings.TrimSpace(d.cfg.DaemonID)
	if _, err := uuid.Parse(computerID); err != nil {
		computerID = ""
	}
	computerGeneration := ""
	if d.cfg.ComputerGeneration > 0 {
		computerGeneration = strconv.FormatInt(d.cfg.ComputerGeneration, 10)
	}
	d.runnerDiagnostics = &runnerDiagnosticRegistry{
		store:              store,
		environment:        environment,
		runnerGeneration:   d.runnerInstanceID,
		computerID:         computerID,
		computerGeneration: computerGeneration,
		loggers:            make(map[string]*diagnosticlog.Logger),
		failed:             make(map[string]struct{}),
	}
}

func (d *Daemon) recordRunnerDiagnostic(workspaceID string, event diagnosticlog.Event) {
	if d == nil || d.runnerDiagnostics == nil {
		return
	}
	if err := d.runnerDiagnostics.record(workspaceID, event); err != nil && d.logger != nil {
		// Do not include the storage error or event payload in the fallback log.
		// Sink health and the later Service summary own those details.
		d.logger.Warn("Workspace Runner diagnostic record dropped", "reason", "sink_unavailable")
	}
}

func (r *runnerDiagnosticRegistry) record(workspaceID string, event diagnosticlog.Event) error {
	workspaceID = strings.TrimSpace(workspaceID)
	if r == nil || r.store == nil {
		return errors.New("diagnostic store is unavailable")
	}
	r.mu.Lock()
	logger := r.loggers[workspaceID]
	_, failed := r.failed[workspaceID]
	if logger == nil && !failed {
		var err error
		logger, err = r.store.Runner(diagnosticlog.RunnerOptions{
			Environment:        r.environment,
			WorkspaceID:        workspaceID,
			RunnerGeneration:   r.runnerGeneration,
			ComputerID:         r.computerID,
			ComputerGeneration: r.computerGeneration,
		})
		if err != nil {
			r.failed[workspaceID] = struct{}{}
			r.mu.Unlock()
			return err
		}
		r.loggers[workspaceID] = logger
	}
	r.mu.Unlock()
	if logger == nil {
		return errors.New("diagnostic logger is unavailable")
	}
	return logger.Record(event)
}

func (d *Daemon) canonicalMessageDiagnosticEvent(
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

func (d *Daemon) recordResidentMessageBatch(
	workspaceID, runtimeID, agentID string,
	messages []protocol.AgentMessageProjection,
	phase, outcome, reasonCode string,
) {
	for _, message := range messages {
		event := d.canonicalMessageDiagnosticEvent(workspaceID, runtimeID, protocol.AgentDeliverPayload{
			AgentID: agentID,
			Target:  message.Target,
			Seq:     message.Seq,
			Message: message,
		}, phase, outcome, reasonCode)
		event.Component = "resident_runtime"
		d.recordRunnerDiagnostic(workspaceID, event)
	}
}

func (d *Daemon) recordAgentMessageResponse(
	workspaceID, agentID, requestID, contextTarget string,
	response map[string]any,
	phase, outcome, reasonCode string,
) {
	if d == nil {
		return
	}
	d.messageCoordinatorMu.RLock()
	runtimeID := d.messageRuntimeIDs[agentID]
	d.messageCoordinatorMu.RUnlock()
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
func (d *Daemon) recordStandaloneChatCheckpoint(
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
		return "message_handoff_failed"
	}
}
