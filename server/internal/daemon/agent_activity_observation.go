package daemon

import (
	"errors"
	"fmt"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type agentActivityProjection struct {
	activityKind      string
	detailKind        string
	processInstanceID string
	entries           []protocol.AgentActivityEntry
	preserveCurrent   bool
}

// Observe is the only typed Message/runtime-fact to Activity presentation
// mapping. It requires an existing managed launch and never manufactures
// lifecycle identity from Message or runtime facts.
func (p *agentActivityProducer) Observe(observation AgentObservation) error {
	if p == nil {
		return errors.New("Activity producer is not configured")
	}
	if err := observation.Validate(); err != nil {
		return err
	}
	if p.daemonInstanceID == "" {
		return errors.New("Activity producer daemon instance identity is required")
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	return p.observeLocked(observation)
}

func (p *agentActivityProducer) observeLocked(observation AgentObservation) error {
	key, state, err := p.observationStateLocked(observation)
	if err != nil {
		return err
	}
	projection, err := projectAgentObservation(observation)
	if err != nil {
		return err
	}

	snapshot := state.snapshot
	snapshot.AgentID = key.agentID
	snapshot.LaunchID = key.launchID
	snapshot.DaemonInstanceID = p.daemonInstanceID
	if !projection.preserveCurrent || snapshot.ActivityKind == "" {
		snapshot.ActivityKind = projection.activityKind
		snapshot.DetailKind = projection.detailKind
		snapshot.ProcessInstanceID = projection.processInstanceID
	}
	// Raft 1.0.16 queueTrajectoryText coalesces streaming thinking/text
	// progress. Tool and lifecycle observations remain distinct timeline facts,
	// even when consecutive events have the same detail kind.
	if (observation.Kind == AgentObservationRuntimeThinking || observation.Kind == AgentObservationRuntimeWorking) &&
		state.snapshot.ActivityKind == snapshot.ActivityKind &&
		state.snapshot.DetailKind == snapshot.DetailKind &&
		state.snapshot.ActivityKind != "" {
		return nil
	}
	snapshot.ClientSequence = 0
	snapshot.ProducerFactID = ""
	snapshot.ObservedAt = observation.At.UTC()
	snapshot.ProbeID = ""
	if err := p.publishLocked(snapshot, projection.entries); err != nil {
		return err
	}
	if data, ok := observation.Data.(AgentRuntimeObservationData); ok {
		state.session.ProviderSessionID = data.ProviderSessionID
		state.session.TurnID = data.TurnID
		state.session.RuntimeGeneration = data.RuntimeGeneration
	}
	switch observation.Kind {
	case AgentObservationRuntimeCompacting:
		clearAgentActivityCompaction(state)
		startedAt := observation.At.UTC()
		state.compaction = agentActivityCompactionState{
			active:    true,
			startedAt: startedAt,
			runtime:   observation.Data.(AgentRuntimeStageObservationData),
		}
		state.compaction.cancelStale = p.schedule(compactionStaleTimeout, func() {
			p.markCompactionStale(key, startedAt)
		})
	case AgentObservationRuntimeCompacted, AgentObservationError:
		clearAgentActivityCompaction(state)
	}
	return nil
}

// CompleteCompactionIfActive emits the missing provider finish before the first
// resumed Message-runtime observation. It is scoped to one concrete launch so
// a replacement process cannot inherit stale compaction state.
func (p *agentActivityProducer) CompleteCompactionIfActive(agentID, launchID string, data AgentRuntimeStageObservationData, at time.Time) (bool, error) {
	if p == nil {
		return false, errors.New("Activity producer is not configured")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	state := p.states[agentActivityProducerKey{agentID: agentID, launchID: launchID}]
	if state == nil || !state.compaction.active {
		return false, nil
	}
	observation := AgentObservation{AgentID: agentID, LaunchID: launchID, Kind: AgentObservationRuntimeCompacted, Data: data, At: at}
	if err := observation.Validate(); err != nil {
		return false, err
	}
	if err := p.observeLocked(observation); err != nil {
		return false, err
	}
	return true, nil
}

func (p *agentActivityProducer) InterruptCompactionIfActive(agentID, launchID string) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	state := p.states[agentActivityProducerKey{agentID: agentID, launchID: launchID}]
	if state == nil || !state.compaction.active {
		return false
	}
	clearAgentActivityCompaction(state)
	return true
}

func (p *agentActivityProducer) markCompactionStale(key agentActivityProducerKey, startedAt time.Time) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	state := p.states[key]
	if state == nil || !state.compaction.active || state.compaction.stale || !state.compaction.startedAt.Equal(startedAt) {
		return
	}
	state.compaction.cancelStale = nil
	observation := AgentObservation{
		AgentID: key.agentID, LaunchID: key.launchID,
		Kind: AgentObservationRuntimeCompactionStale, Data: state.compaction.runtime,
		At: p.now().UTC(),
	}
	if observation.Validate() == nil && p.observeLocked(observation) == nil {
		state.compaction.stale = true
	}
}

func (p *agentActivityProducer) observationStateLocked(observation AgentObservation) (agentActivityProducerKey, *agentActivityProducerState, error) {
	key := agentActivityProducerKey{agentID: observation.AgentID, launchID: observation.LaunchID}
	state := p.states[key]
	if state == nil {
		return agentActivityProducerKey{}, nil, errors.New("Activity is not managed for this Agent launch")
	}
	return key, state, nil
}

func projectAgentObservation(observation AgentObservation) (agentActivityProjection, error) {
	projection := agentActivityProjection{}
	var entry protocol.AgentActivityEntry
	var err error

	switch observation.Kind {
	case AgentObservationRuntimeReady:
		data := observation.Data.(AgentRuntimeObservationData)
		projection.activityKind, projection.detailKind, projection.processInstanceID = protocol.ActivityKindOnline, "idle", data.ProcessInstanceID
		entry, err = activityNarrativeEntry(projection.detailKind, "Online")
	case AgentObservationRuntimeStarting:
		projection.activityKind, projection.detailKind = protocol.ActivityKindWorking, "starting"
		entry, err = activityNarrativeEntry(projection.detailKind, "Starting…")
	case AgentObservationRuntimeWorking:
		projection.activityKind, projection.detailKind = protocol.ActivityKindWorking, "model_response_started"
		entry, err = activityNarrativeEntry(projection.detailKind, "Working")
	case AgentObservationRuntimeThinking:
		projection.activityKind, projection.detailKind = protocol.ActivityKindThinking, "thinking_started"
		entry, err = activityNarrativeEntry(projection.detailKind, "Thinking")
	case AgentObservationRuntimeTool:
		data := observation.Data.(AgentRuntimeStageObservationData)
		projection.activityKind = protocol.ActivityKindWorking
		var narrative string
		projection.detailKind, narrative = toolActivityFact(data.ToolName, data.ToolInput)
		if narrative != "" {
			entry, err = activityNarrativeEntry(projection.detailKind, narrative)
		}
	case AgentObservationRuntimeCompacting:
		projection.activityKind, projection.detailKind = protocol.ActivityKindWorking, "compacting_context"
		entry, err = activityNarrativeEntry(projection.detailKind, "Compacting context")
	case AgentObservationRuntimeCompacted:
		projection.activityKind, projection.detailKind = protocol.ActivityKindWorking, "compaction_finished"
		entry, err = activityNarrativeEntry(projection.detailKind, "Context compaction finished")
	case AgentObservationRuntimeCompactionStale:
		projection.activityKind, projection.detailKind = protocol.ActivityKindWorking, "compaction_stale"
		entry, err = activityNarrativeEntry(projection.detailKind, "Context compaction still running; no finish event observed")
	case AgentObservationRuntimeIdle:
		projection.activityKind, projection.detailKind = protocol.ActivityKindOnline, "idle"
		entry, err = activityNarrativeEntry(projection.detailKind, "Idle")
	case AgentObservationRuntimeDiagnostic:
		projection.activityKind, projection.detailKind, projection.preserveCurrent = protocol.ActivityKindOnline, "idle", true
		entry, err = activitySystemEntry("Runtime warning", "Provider reported a warning")
	case AgentObservationMessageBodyAccepted:
		// Raft 1.0.16 shows "Message received" when an ordinary inbox
		// body is accepted. Keep the presentation detail the UI already
		// maps; do not wait for native write completion.
		projection.activityKind, projection.detailKind = protocol.ActivityKindWorking, "message_received"
		entry, err = activityNarrativeEntry(projection.detailKind, "Message received")
	case AgentObservationFreshnessHeld:
		data := observation.Data.(AgentFreshnessHoldObservationData)
		projection.activityKind, projection.detailKind, projection.preserveCurrent = protocol.ActivityKindOnline, "idle", true
		entry, err = activitySystemEntry(messageSendHoldTitle(), messageSendHoldSubtext(int64(data.NewMessageCount)))
	case AgentObservationDraftSent:
		data := observation.Data.(AgentDraftSentObservationData)
		projection.activityKind, projection.detailKind, projection.preserveCurrent = protocol.ActivityKindOnline, "idle", true
		entry, err = activitySystemEntry(messageSendDraftSentTitle(), messageSendDraftSentSubtext(data.Target, data.Anyway))
	case AgentObservationError:
		data := observation.Data.(AgentErrorObservationData)
		projection.activityKind, projection.detailKind, projection.processInstanceID = protocol.ActivityKindError, "runtime_error", data.ProcessInstanceID
		entry, err = activityNarrativeEntry(projection.detailKind, "Agent execution failed")
	case AgentObservationOffline:
		data := observation.Data.(AgentErrorObservationData)
		projection.activityKind = protocol.ActivityKindOffline
		if data.ReasonCode == "stopped" {
			projection.detailKind = "stopped"
			entry, err = activityNarrativeEntry(projection.detailKind, "Agent stopped by user")
		} else {
			projection.detailKind = "runtime_unavailable"
			entry, err = activityNarrativeEntry(projection.detailKind, "Offline")
		}
	default:
		return agentActivityProjection{}, fmt.Errorf("unknown Agent Observation kind %q", observation.Kind)
	}
	if err != nil {
		return agentActivityProjection{}, err
	}
	if entry.Kind != "" {
		projection.entries = []protocol.AgentActivityEntry{entry}
	}
	return projection, nil
}

// messageSendHoldTitle and messageSendHoldSubtext are Activity presentation,
// so they live with the producer mapping rather than the Credential Proxy.
func messageSendHoldTitle() string {
	return "Message held — review newer messages before sending"
}

func messageSendHoldSubtext(newer int64) string {
	if newer > 0 {
		return fmt.Sprintf("%d newer messages available — review then resend", newer)
	}
	return "Send held — review the channel before resending"
}

func messageSendDraftSentTitle() string {
	return "Send draft sent"
}

func messageSendDraftSentSubtext(target string, anyway bool) string {
	if anyway {
		return fmt.Sprintf("target: %s\nfreshness updates: not checked (--anyway)\ndecision: saved draft sent with explicit freshness override", target)
	}
	return fmt.Sprintf("target: %s\nfreshness updates: 0 newer messages\ndecision: saved draft freshness check passed when sent", target)
}
