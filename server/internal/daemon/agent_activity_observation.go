package daemon

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// activityBroadcast mirrors Raft's broadcastActivity arguments. The
// processInstanceID is local launch fencing; the remaining fields are the
// activity state and timeline fact.
type activityBroadcast struct {
	activityKind string
	detail       string
	detailKind   string
	trajectory   []protocol.AgentActivityEntry
	timing       protocol.AgentActivityTiming
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
	broadcast, err := activityBroadcastForObservation(observation)
	if err != nil {
		return err
	}
	if duplicateRuntimeActivity(state.snapshot, broadcast, observation.Kind) ||
		duplicateIdleActivity(state.snapshot, broadcast, observation.Kind) {
		return nil
	}

	snapshot := state.snapshot
	snapshot.AgentID = observation.AgentID
	snapshot.DaemonInstanceID = p.daemonInstanceID
	if broadcast.activityKind != "" {
		snapshot.ActivityKind = broadcast.activityKind
		snapshot.DetailKind = broadcast.detailKind
	} else if snapshot.ActivityKind == "" {
		snapshot.ActivityKind = protocol.ActivityKindOnline
		snapshot.DetailKind = broadcast.detailKind
	}
	snapshot.ObservedAt = observation.At.UTC()
	if err := p.publishLocked(key, snapshot, broadcast); err != nil {
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

// duplicateRuntimeActivity mirrors Raft's alreadyLive guard: progress does
// not repaint an already-working/thinking runtime.
func duplicateRuntimeActivity(previous protocol.AgentActivitySnapshot, broadcast activityBroadcast, kind AgentObservationKind) bool {
	progress := kind == AgentObservationRuntimeThinking || kind == AgentObservationRuntimeWorking
	return progress && previous.ActivityKind != "" &&
		previous.ActivityKind == broadcast.activityKind &&
		previous.DetailKind == broadcast.detailKind
}

// duplicateIdleActivity is the lifecycle-specific guard for the resident
// readiness path. Raft avoids this at its lifecycle call site; keeping it
// separate from the runtime-progress guard prevents ready/idle from becoming
// a generic suppressible category.
func duplicateIdleActivity(previous protocol.AgentActivitySnapshot, broadcast activityBroadcast, kind AgentObservationKind) bool {
	return (kind == AgentObservationRuntimeReady || kind == AgentObservationRuntimeIdle) &&
		previous.ActivityKind == broadcast.activityKind &&
		previous.DetailKind == broadcast.detailKind
}

// CompleteCompactionIfActive emits the missing provider finish before the first
// resumed Message-runtime observation. It is scoped to one concrete launch so
// a replacement process cannot inherit stale compaction state.
func (p *agentActivityProducer) CompleteCompactionIfActive(agentID, agentInstanceID string, data AgentRuntimeStageObservationData, at time.Time) (bool, error) {
	if p == nil {
		return false, errors.New("Activity producer is not configured")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	state := p.states[agentActivityProducerKey{agentID: agentID, agentInstanceID: agentInstanceID}]
	if state == nil || !state.compaction.active {
		return false, nil
	}
	observation := AgentObservation{AgentID: agentID, AgentInstanceID: agentInstanceID, Kind: AgentObservationRuntimeCompacted, Data: data, At: at}
	if err := observation.Validate(); err != nil {
		return false, err
	}
	if err := p.observeLocked(observation); err != nil {
		return false, err
	}
	return true, nil
}

func (p *agentActivityProducer) InterruptCompactionIfActive(agentID, agentInstanceID string) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	state := p.states[agentActivityProducerKey{agentID: agentID, agentInstanceID: agentInstanceID}]
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
		AgentID: key.agentID, AgentInstanceID: key.agentInstanceID, Kind: AgentObservationRuntimeCompactionStale, Data: state.compaction.runtime,
		At: p.now().UTC(),
	}
	if observation.Validate() == nil && p.observeLocked(observation) == nil {
		state.compaction.stale = true
	}
}

func (p *agentActivityProducer) observationStateLocked(observation AgentObservation) (agentActivityProducerKey, *agentActivityProducerState, error) {
	key := agentActivityProducerKey{agentID: observation.AgentID, agentInstanceID: observation.AgentInstanceID}
	state := p.states[key]
	if state == nil {
		return agentActivityProducerKey{}, nil, errors.New("Activity is not managed for this Agent launch")
	}
	return key, state, nil
}

func activityBroadcastForObservation(observation AgentObservation) (activityBroadcast, error) {
	broadcast := activityBroadcast{}
	var entry protocol.AgentActivityEntry
	var err error

	switch observation.Kind {
	case AgentObservationRuntimeReady:
		broadcast.activityKind, broadcast.detailKind, broadcast.detail = protocol.ActivityKindOnline, "idle", "Online"
		entry, err = activityStatusEntry(broadcast.detailKind, broadcast.detail)
	case AgentObservationRuntimeStarting:
		broadcast.activityKind, broadcast.detailKind, broadcast.detail = protocol.ActivityKindWorking, "starting", "Starting…"
		broadcast.timing.ColdStartAtMS = observation.At.UnixMilli()
		entry, err = activityStatusEntry(broadcast.detailKind, broadcast.detail)
	case AgentObservationRuntimeWorking:
		// Runtime text advances Raft's current model-response state, but the
		// final reply belongs to Chat and does not create a generic Timeline row.
		broadcast.activityKind, broadcast.detailKind, broadcast.detail = protocol.ActivityKindWorking, "model_response_started", "Working"
		if data := observation.Data.(AgentRuntimeStageObservationData); !data.ProviderEventAt.IsZero() {
			broadcast.timing.FirstACPUpdateAtMS = data.ProviderEventAt.UnixMilli()
		}
	case AgentObservationRuntimeThinking:
		broadcast.activityKind, broadcast.detailKind, broadcast.detail = protocol.ActivityKindThinking, "thinking_started", "Thinking"
		if data := observation.Data.(AgentRuntimeStageObservationData); !data.ProviderEventAt.IsZero() {
			broadcast.timing.FirstACPUpdateAtMS = data.ProviderEventAt.UnixMilli()
		}
		entry, err = activityStatusEntry(broadcast.detailKind, broadcast.detail)
	case AgentObservationRuntimeTool:
		data := observation.Data.(AgentRuntimeStageObservationData)
		broadcast.activityKind = protocol.ActivityKindWorking
		var summary string
		broadcast.detailKind, summary = toolActivityFact(data.ToolName, data.ToolInput)
		toolName := data.ToolName
		toolInput := summary
		if semantic, summary, ok := resolveMulticaCLIInvocation(data.ToolName, data.ToolInput); ok {
			toolName, toolInput = semantic, summary
		} else if semantic, ok := canonicalToolSemantic(data.ToolName); ok {
			toolName = semantic
		}
		broadcast.detail = summary
		if !data.ProviderEventAt.IsZero() {
			broadcast.timing.FirstACPUpdateAtMS = data.ProviderEventAt.UnixMilli()
		}
		entry, err = activityToolStartEntry(toolName, toolInput)
	case AgentObservationRuntimeCompacting:
		broadcast.activityKind, broadcast.detailKind, broadcast.detail = protocol.ActivityKindWorking, "compacting_context", "Compacting context"
		entry, err = activityStatusEntry(broadcast.detailKind, broadcast.detail)
	case AgentObservationRuntimeCompacted:
		broadcast.activityKind, broadcast.detailKind, broadcast.detail = protocol.ActivityKindWorking, "compaction_finished", "Context compaction finished"
		entry, err = activityStatusEntry(broadcast.detailKind, broadcast.detail)
	case AgentObservationRuntimeCompactionStale:
		broadcast.activityKind, broadcast.detailKind, broadcast.detail = protocol.ActivityKindWorking, "compaction_stale", "Context compaction still running; no finish event observed"
		entry, err = activityStatusEntry(broadcast.detailKind, broadcast.detail)
	case AgentObservationRuntimeStalled:
		data := observation.Data.(AgentRuntimeStageObservationData)
		broadcast.activityKind, broadcast.detailKind = protocol.ActivityKindError, "runtime_stalled"
		staleMinutes := int(data.StaleFor / time.Minute)
		if staleMinutes < 1 {
			staleMinutes = 1
		}
		broadcast.detail = fmt.Sprintf("Runtime stalled: no runtime events for %dm", staleMinutes)
		entry, err = activityStatusEntry(broadcast.detailKind, broadcast.detail)
	case AgentObservationRuntimeIdle:
		broadcast.activityKind, broadcast.detailKind, broadcast.detail = protocol.ActivityKindOnline, "idle", "Idle"
		entry, err = activityStatusEntry(broadcast.detailKind, broadcast.detail)
	case AgentObservationRuntimeDiagnostic:
		data := observation.Data.(AgentRuntimeDiagnosticObservationData)
		broadcast.detailKind = "idle"
		entry, err = formatActivityTimelineEntry(data.Source, data.Reference, data.Name, data.Kind, data.Detail)
	case AgentObservationMessageBodyAccepted:
		// Raft 1.0.16 shows "Message received" when an ordinary inbox
		// body is accepted. Keep the presentation detail the UI already
		// maps; do not wait for native write completion.
		broadcast.activityKind, broadcast.detailKind, broadcast.detail = protocol.ActivityKindWorking, "message_received", "Message received"
		data := observation.Data.(AgentMessageAcceptanceObservationData)
		acceptedAt := data.AcceptedAt
		if acceptedAt.IsZero() {
			acceptedAt = observation.At
		}
		broadcast.timing.AcceptedAtMS = acceptedAt.UnixMilli()
		entry, err = activityStatusEntry(broadcast.detailKind, broadcast.detail)
	case AgentObservationFreshnessHeld:
		data := observation.Data.(AgentFreshnessHoldObservationData)
		broadcast.detailKind = "idle"
		entry, err = activitySystemEntry(messageSendHoldTitle(), messageSendHoldSubtext(int64(data.NewMessageCount)))
	case AgentObservationDraftSent:
		data := observation.Data.(AgentDraftSentObservationData)
		broadcast.detailKind = "idle"
		entry, err = activitySystemEntry(messageSendDraftSentTitle(), messageSendDraftSentSubtext(data.Target, data.Anyway))
	case AgentObservationError:
		data := observation.Data.(AgentErrorObservationData)
		broadcast.activityKind, broadcast.detailKind, broadcast.detail = protocol.ActivityKindError, "runtime_error", strings.TrimSpace(data.Message)
		entry, err = activityStatusEntry(broadcast.detailKind, broadcast.detail)
	case AgentObservationOffline:
		data := observation.Data.(AgentErrorObservationData)
		broadcast.activityKind = protocol.ActivityKindOffline
		if data.ReasonCode == "stopped" {
			broadcast.detailKind, broadcast.detail = "stopped", "Agent stopped by user"
			entry, err = activityStatusEntry(broadcast.detailKind, broadcast.detail)
		} else {
			broadcast.detailKind, broadcast.detail = "runtime_unavailable", "Offline"
			entry, err = activityStatusEntry(broadcast.detailKind, broadcast.detail)
		}
	default:
		return activityBroadcast{}, fmt.Errorf("unknown Agent Observation kind %q", observation.Kind)
	}
	if err != nil {
		return activityBroadcast{}, err
	}
	if entry.Kind != "" {
		broadcast.trajectory = []protocol.AgentActivityEntry{entry}
	}
	return broadcast, nil
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
