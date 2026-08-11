package daemon

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type agentActivityProjection struct {
	activityKind      string
	detailKind        string
	processInstanceID string
	entries           []protocol.AgentActivityEntry
	preserveCurrent   bool
}

// Observe is the only typed execution-fact to Activity presentation mapping.
// It requires an existing managed launch and never manufactures lifecycle
// identity from Attachment, Message, or runtime facts.
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
	key, state, err := p.observationStateLocked(observation)
	if err != nil {
		return err
	}
	projection, err := projectAgentObservation(observation)
	if err != nil {
		return err
	}
	factID, err := agentObservationFactID(observation, key.launchID)
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
	snapshot.ClientSequence = 0
	snapshot.ProducerFactID = factID
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
	return nil
}

func (p *agentActivityProducer) observationStateLocked(observation AgentObservation) (agentActivityProducerKey, *agentActivityProducerState, error) {
	if observation.LaunchID != "" {
		key := agentActivityProducerKey{agentID: observation.AgentID, launchID: observation.LaunchID}
		state := p.states[key]
		if state == nil {
			return agentActivityProducerKey{}, nil, errors.New("Activity is not managed for this Agent launch")
		}
		return key, state, nil
	}
	for key, state := range p.states {
		if key.agentID == observation.AgentID {
			return key, state, nil
		}
	}
	return agentActivityProducerKey{}, nil, errors.New("Activity is not managed for this Agent")
}

func projectAgentObservation(observation AgentObservation) (agentActivityProjection, error) {
	projection := agentActivityProjection{}
	var entry protocol.AgentActivityEntry
	var err error

	switch observation.Kind {
	case AgentObservationAttached:
		projection.activityKind, projection.detailKind = protocol.ActivityKindOnline, "attached"
		entry, err = activityNarrativeEntry(projection.activityKind, projection.detailKind, "Agent attached")
	case AgentObservationLaunchAccepted:
		projection.activityKind, projection.detailKind = protocol.ActivityKindOnline, "launch_accepted"
		entry, err = activityNarrativeEntry(projection.activityKind, projection.detailKind, "Launch accepted")
	case AgentObservationRuntimeReady:
		data := observation.Data.(AgentRuntimeObservationData)
		projection.activityKind, projection.detailKind, projection.processInstanceID = protocol.ActivityKindOnline, "idle", data.ProcessInstanceID
		entry, err = activityNarrativeEntry(projection.activityKind, projection.detailKind, "Online")
	case AgentObservationRuntimeStarting:
		projection.activityKind, projection.detailKind = protocol.ActivityKindWorking, "starting"
		entry, err = activityNarrativeEntry(projection.activityKind, projection.detailKind, "Starting")
	case AgentObservationRuntimeWorking:
		data := observation.Data.(AgentRuntimeObservationData)
		projection.activityKind, projection.processInstanceID = protocol.ActivityKindWorking, data.ProcessInstanceID
		entry, err = activityNarrativeEntry(projection.activityKind, projection.detailKind, "Working")
	case AgentObservationRuntimeThinking:
		projection.activityKind = protocol.ActivityKindThinking
		entry, err = activityNarrativeEntry(projection.activityKind, projection.detailKind, "Thinking")
	case AgentObservationRuntimeTool:
		data := observation.Data.(AgentRuntimeStageObservationData)
		projection.activityKind = protocol.ActivityKindWorking
		var narrative string
		projection.detailKind, narrative = toolActivityFact(data.ToolName, data.ToolInput)
		if narrative != "" {
			entry, err = activityNarrativeEntry(projection.activityKind, projection.detailKind, narrative)
		}
	case AgentObservationRuntimeCompacting:
		projection.activityKind, projection.detailKind = protocol.ActivityKindWorking, "compacting_context"
		entry, err = activityNarrativeEntry(projection.activityKind, projection.detailKind, "Compacting context")
	case AgentObservationRuntimeCompacted:
		projection.activityKind, projection.detailKind = protocol.ActivityKindOnline, "idle"
		entry, err = activityNarrativeEntry(projection.activityKind, projection.detailKind, "Context compaction finished")
	case AgentObservationRuntimeIdle:
		projection.activityKind, projection.detailKind = protocol.ActivityKindOnline, "idle"
		entry, err = activityNarrativeEntry(projection.activityKind, projection.detailKind, "Idle")
	case AgentObservationRuntimeDiagnostic:
		projection.activityKind, projection.detailKind, projection.preserveCurrent = protocol.ActivityKindOnline, "idle", true
		entry, err = activitySystemEntry("Runtime warning", "Provider reported a warning")
	case AgentObservationMessageBodyAccepted:
		projection.activityKind, projection.detailKind = protocol.ActivityKindWorking, "message_received"
		entry, err = activityNarrativeEntry(projection.activityKind, projection.detailKind, "Message received")
	case AgentObservationFreshnessHeld:
		data := observation.Data.(AgentFreshnessHoldObservationData)
		projection.activityKind, projection.detailKind, projection.preserveCurrent = protocol.ActivityKindOnline, "idle", true
		entry, err = activitySystemEntry(messageSendHoldTitle(), messageSendHoldSubtext(int64(data.NewMessageCount)))
	case AgentObservationError:
		data := observation.Data.(AgentErrorObservationData)
		projection.activityKind, projection.detailKind, projection.processInstanceID = protocol.ActivityKindError, "runtime_error", data.ProcessInstanceID
		entry, err = activityNarrativeEntry(projection.activityKind, projection.detailKind, "Agent execution failed")
	case AgentObservationStopped:
		projection.activityKind, projection.detailKind = protocol.ActivityKindOffline, "stopped"
		entry, err = activityNarrativeEntry(projection.activityKind, projection.detailKind, "Stopped")
	case AgentObservationDetached:
		projection.activityKind, projection.detailKind = protocol.ActivityKindOffline, "detached"
		entry, err = activityNarrativeEntry(projection.activityKind, projection.detailKind, "Agent detached")
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

func agentObservationFactID(observation AgentObservation, launchID string) (string, error) {
	fact := struct {
		Observation AgentObservation `json:"observation"`
		LaunchID    string           `json:"resolved_launch_id"`
	}{Observation: observation, LaunchID: launchID}
	raw, err := json.Marshal(fact)
	if err != nil {
		return "", fmt.Errorf("encode Agent Observation fingerprint: %w", err)
	}
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("observation-%x", sum[:]), nil
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
