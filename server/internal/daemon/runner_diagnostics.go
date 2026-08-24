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
	store             *diagnosticlog.Store
	environment       diagnosticlog.Environment
	daemonInstanceID  string
	computerID        string
	serviceGeneration string

	mu      sync.Mutex
	loggers map[string]*diagnosticlog.Logger
	failed  map[string]struct{}
}

type runnerDiagnosticSink interface {
	record(string, diagnosticlog.Event) error
}

func (d *Daemon) initializeRunnerDiagnostics() {
	if d == nil || d.runnerDiagnostics != nil {
		return
	}
	environment := diagnosticlog.Environment(strings.TrimSpace(d.cfg.Environment))
	if environment != diagnosticlog.EnvironmentProduction && environment != diagnosticlog.EnvironmentTest {
		if d.logger != nil {
			d.logger.Debug("Workspace Runner diagnostics disabled", "reason", "invalid_environment")
		}
		return
	}
	store, err := diagnosticlog.Open(diagnosticlog.Config{})
	if err != nil {
		if d.logger != nil {
			d.logger.Debug("Workspace Runner diagnostics disabled", "reason", "open_failed")
		}
		return
	}
	computerID := strings.TrimSpace(d.cfg.DaemonID)
	if _, err := uuid.Parse(computerID); err != nil {
		computerID = ""
	}
	d.runnerDiagnostics = &runnerDiagnosticRegistry{
		store: store, environment: environment,
		daemonInstanceID: d.runnerInstanceID, computerID: computerID,
		loggers: make(map[string]*diagnosticlog.Logger), failed: make(map[string]struct{}),
	}
	d.runnerDiagnosticStore = store
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
			Environment:       r.environment,
			WorkspaceID:       workspaceID,
			DaemonInstanceID:  r.daemonInstanceID,
			ComputerID:        r.computerID,
			ServiceGeneration: r.serviceGeneration,
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
			EventID:    delivery.Message.ID,
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

func canonicalMessageDiagnosticEventForTurn(
	workspaceID, runtimeID string,
	delivery protocol.AgentDeliverPayload,
	phase, outcome, reasonCode, executionID string,
	runtimeEpoch int64,
) diagnosticlog.Event {
	event := canonicalMessageDiagnosticEvent(workspaceID, runtimeID, delivery, phase, outcome, reasonCode)
	event.Identity.ExecutionID = executionID
	event.Fields.RuntimeEpoch = runtimeEpoch
	return event
}

func (d *Daemon) recordResidentMessageBatch(
	workspaceID, runtimeID, agentID string,
	messages []protocol.AgentMessageProjection,
	phase, outcome, reasonCode, executionID string, runtimeEpoch int64, launchID, startDispatchID string,
) {
	for _, message := range messages {
		event := canonicalMessageDiagnosticEventForTurn(workspaceID, runtimeID, protocol.AgentDeliverPayload{
			AgentID: agentID,
			Target:  message.Target,
			Seq:     message.Seq,
			Message: message,
		}, phase, outcome, reasonCode, executionID, runtimeEpoch)
		event.Identity.LaunchID = launchID
		event.Identity.StartDispatchID = startDispatchID
		event.Component = "resident_runtime"
		d.recordRunnerDiagnostic(workspaceID, event)
	}
}

func (d *Daemon) runtimeEpochForRuntime(runtimeID string) (string, int64) {
	if d == nil || strings.TrimSpace(runtimeID) == "" {
		return "", 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	runtime, ok := d.runtimeIndex[runtimeID]
	if !ok {
		return "", 0
	}
	if state := d.workspaces[runtime.WorkspaceID]; state != nil {
		return runtime.WorkspaceID, state.runtimeEpoch
	}
	return runtime.WorkspaceID, 0
}

// bindInboxLeaseEpoch snapshots the registration generation at lease
// acquisition. Subsequent renew/ack/terminal events must retain this value
// even if the workspace registers a replacement runtime in the meantime.
func (d *Daemon) bindInboxLeaseEpoch(lease *AgentInboxLease) {
	if d == nil || lease == nil || lease.RuntimeEpoch > 0 || strings.TrimSpace(lease.RuntimeID) == "" {
		return
	}
	_, epoch := d.runtimeEpochForRuntime(lease.RuntimeID)
	lease.RuntimeEpoch = epoch
}

func (d *Daemon) recordInboxLeaseDiagnostic(lease AgentInboxLease, phase, outcome, reason string) {
	if d == nil || strings.TrimSpace(lease.RuntimeID) == "" {
		return
	}
	workspaceID, currentEpoch := d.runtimeEpochForRuntime(lease.RuntimeID)
	if workspaceID == "" {
		return
	}
	runtimeEpoch := lease.RuntimeEpoch
	if runtimeEpoch <= 0 {
		runtimeEpoch = currentEpoch
	}
	d.recordRunnerDiagnostic(workspaceID, diagnosticlog.Event{
		Name: diagnosticlog.EventDeliveryStateChanged, Level: diagnosticLevel(outcome), Component: "agent_inbox",
		Identity: diagnosticlog.Identity{EventID: lease.ID, InboxEventID: lease.ID, RuntimeID: lease.RuntimeID, DeliveryID: lease.DeliveryID, ExecutionID: lease.ExecutionID, ConversationID: lease.ConversationID, SourceMessageID: lease.SourceMessageID},
		Fields:   diagnosticlog.Fields{Phase: phase, Outcome: outcome, ReasonCode: reason, SeqFrom: lease.SeqFrom, SeqTo: lease.SeqTo, ResponseMode: lease.ResponseMode, RuntimeEpoch: runtimeEpoch},
	})
}

func (d *Daemon) recordAgentMessageResponse(
	workspaceID, agentID, requestID, contextTarget string,
	response map[string]any,
	phase, outcome, reasonCode string,
) {
	if d == nil {
		return
	}
	runtimeID := ""
	if runner := d.currentWorkspaceRunner(workspaceID); runner != nil {
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
			EventID:         lease.ID,
			DeliveryID:      lease.DeliveryID,
			InboxEventID:    lease.ID,
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
			RuntimeEpoch: lease.RuntimeEpoch,
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

func terminationReasonFromLifecycle(result string) string {
	switch result {
	case "advanced", "":
		return ""
	case "timeout":
		return "startup_timeout"
	case "superseded":
		return "superseded"
	case "terminal":
		return "natural"
	default:
		return "unknown"
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
