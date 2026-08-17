package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func activityNarrativeEntry(detailKind, text string) (protocol.AgentActivityEntry, error) {
	body, err := json.Marshal(protocol.AgentActivityNarrativeBody{
		Text:       text,
		DetailKind: detailKind,
	})
	if err != nil {
		return protocol.AgentActivityEntry{}, err
	}
	return protocol.AgentActivityEntry{Kind: "narrative", Position: 0, Body: body}, nil
}

func activitySystemEntry(title, text string) (protocol.AgentActivityEntry, error) {
	body, err := json.Marshal(protocol.AgentActivitySystemBody{Title: title, Text: text})
	if err != nil {
		return protocol.AgentActivityEntry{}, err
	}
	return protocol.AgentActivityEntry{Kind: "system", Position: 0, Body: body}, nil
}

const (
	agentActivityHeartbeatInterval = time.Minute
	compactionStaleTimeout         = 5 * time.Minute
)

// agentActivityProducer is a best-effort daemon-side Activity publisher. It
// intentionally has no acknowledgement, durable outbox, HTTP upload, or
// coupling to Message, Task, billing, recovery, or terminal outcomes.
type agentActivityProducer struct {
	mu sync.Mutex

	now                 func() time.Time
	schedule            func(time.Duration, func()) func()
	send                func(protocol.AgentActivityPayload)
	daemonInstanceID    string
	transportGeneration uint64
	states              map[agentActivityProducerKey]*agentActivityProducerState
}

type agentActivityProducerKey struct {
	agentID  string
	launchID string
}

type agentActivityProducerState struct {
	snapshot           protocol.AgentActivitySnapshot
	detail             string
	status             protocol.AgentStatusPayload
	session            protocol.AgentSessionPayload
	connected          bool
	lastHeartbeatAt    time.Time
	lastClientSequence int64
	compaction         agentActivityCompactionState
}

type agentActivityCompactionState struct {
	active      bool
	startedAt   time.Time
	stale       bool
	runtime     AgentRuntimeStageObservationData
	cancelStale func()
}

type agentActivityReconnectFrame struct {
	EventType string
	Payload   any
}

func newAgentActivityProducer(daemonInstanceID string, now func() time.Time, send func(protocol.AgentActivityPayload)) *agentActivityProducer {
	if now == nil {
		now = time.Now
	}
	return &agentActivityProducer{
		daemonInstanceID: daemonInstanceID,
		now:              now,
		schedule: func(delay time.Duration, callback func()) func() {
			timer := time.AfterFunc(delay, callback)
			return func() { timer.Stop() }
		},
		send:   send,
		states: make(map[agentActivityProducerKey]*agentActivityProducerState),
	}
}

// Close releases activity sequence and managed-launch state with the owning
// Workspace Runner. Reconnects deliberately use DetachTransport instead.
func (p *agentActivityProducer) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	for _, state := range p.states {
		clearAgentActivityCompaction(state)
	}
	p.send = nil
	p.states = nil
	p.mu.Unlock()
}

func (p *agentActivityProducer) SetManaged(status protocol.AgentStatusPayload, session protocol.AgentSessionPayload) error {
	if p == nil {
		return errors.New("Activity producer is not configured")
	}
	if err := status.Validate(); err != nil {
		return err
	}
	if err := session.Validate(); err != nil {
		return err
	}
	if status.AgentID != session.AgentID || status.LaunchID != session.LaunchID {
		return errors.New("Activity status and session identities do not match")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	// One Agent has one current managed launch. A server-commanded launch may
	// replace a resident Message launch (or vice versa); retaining both would
	// make launch-free observations ambiguous.
	for existing := range p.states {
		if existing.agentID == status.AgentID && existing.launchID != status.LaunchID {
			clearAgentActivityCompaction(p.states[existing])
			delete(p.states, existing)
		}
	}
	key := agentActivityProducerKey{agentID: status.AgentID, launchID: status.LaunchID}
	state := p.states[key]
	if state == nil {
		state = &agentActivityProducerState{connected: true}
		p.states[key] = state
	}
	state.status = status
	state.session = session
	return nil
}

// UpdateProviderSession advances the reconnect projection for an already
// managed launch. It returns true only when the provider identity changed so
// ordinary Messages do not repeat an identical agent:session frame.
func (p *agentActivityProducer) UpdateProviderSession(session protocol.AgentSessionPayload) (bool, error) {
	if p == nil {
		return false, errors.New("Activity producer is not configured")
	}
	if err := session.Validate(); err != nil {
		return false, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	state := p.states[agentActivityProducerKey{agentID: session.AgentID, launchID: session.LaunchID}]
	if state == nil {
		return false, nil
	}
	if state.session.ProviderSessionID == session.ProviderSessionID {
		return false, nil
	}
	session.TurnID = state.session.TurnID
	session.RuntimeGeneration = state.session.RuntimeGeneration
	state.session = session
	return true, nil
}

func (p *agentActivityProducer) SetConnected(agentID, launchID string, connected bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if state := p.states[agentActivityProducerKey{agentID: agentID, launchID: launchID}]; state != nil {
		state.connected = connected
	}
}

// RemoveManaged forgets a stopped launch so a later reconnect cannot report a
// stale active status, session, or Snapshot for it.
func (p *agentActivityProducer) RemoveManaged(agentID, launchID string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	key := agentActivityProducerKey{agentID: agentID, launchID: launchID}
	clearAgentActivityCompaction(p.states[key])
	delete(p.states, key)
}

// AttachTransport makes a newly established Workspace Runner connection the
// only destination for best-effort Activity. It returns the current managed
// state for ready reconciliation and a lease that prevents an older replaced
// socket from marking the new connection disconnected on teardown.
func (p *agentActivityProducer) AttachTransport(send func(protocol.AgentActivityPayload)) (uint64, []agentActivityReconnectFrame) {
	if p == nil {
		return 0, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.transportGeneration++
	p.send = send
	frames := make([]agentActivityReconnectFrame, 0, len(p.states)*3)
	for _, state := range p.states {
		state.connected = true
		frames = append(frames, agentActivityReconnectFrame{EventType: protocol.EventAgentStatus, Payload: state.status})
		frames = append(frames, agentActivityReconnectFrame{EventType: protocol.EventAgentSession, Payload: state.session})
		if !state.snapshot.ObservedAt.IsZero() {
			frames = append(frames, agentActivityReconnectFrame{EventType: protocol.EventAgentActivity, Payload: protocol.AgentActivityPayload{Snapshot: state.snapshot, Detail: state.detail}})
		}
	}
	return p.transportGeneration, frames
}

// DetachTransport only disconnects the lease that owns it. A late defer from
// a replaced connection must not silence the current Workspace Runner.
func (p *agentActivityProducer) DetachTransport(generation uint64) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if generation != p.transportGeneration {
		return
	}
	p.send = nil
	for _, state := range p.states {
		state.connected = false
	}
}

func clearAgentActivityCompaction(state *agentActivityProducerState) {
	if state == nil {
		return
	}
	if state.compaction.cancelStale != nil {
		state.compaction.cancelStale()
	}
	state.compaction = agentActivityCompactionState{}
}
func (p *agentActivityProducer) publishLocked(snapshot protocol.AgentActivitySnapshot, entries []protocol.AgentActivityEntry) error {
	key := agentActivityProducerKey{agentID: snapshot.AgentID, launchID: snapshot.LaunchID}
	state := p.states[key]
	if state == nil {
		return errors.New("Activity is not managed for this Agent launch")
	}
	if snapshot.DaemonInstanceID == "" {
		return errors.New("Activity daemon instance identity is required")
	}
	if snapshot.ObservedAt.IsZero() {
		snapshot.ObservedAt = p.now().UTC()
	}
	if snapshot.ClientSequence == 0 {
		snapshot.ClientSequence = state.lastClientSequence + 1
	}
	if snapshot.ProducerFactID == "" {
		snapshot.ProducerFactID = raftActivityProducerFactID(snapshot.AgentID, snapshot.LaunchID, snapshot.DaemonInstanceID, snapshot.ClientSequence)
	}
	detail := ""
	if snapshot.DetailKind == state.snapshot.DetailKind {
		detail = state.detail
	}
	for _, entry := range entries {
		if entry.Kind != "narrative" {
			continue
		}
		var body protocol.AgentActivityNarrativeBody
		if json.Unmarshal(entry.Body, &body) == nil && body.Text != "" {
			detail = body.Text
			break
		}
	}
	if detail == "" {
		detail = defaultAgentActivityDetail(snapshot.DetailKind)
	}
	payload := protocol.AgentActivityPayload{Snapshot: snapshot, Detail: detail, Entries: entries}
	if err := payload.Validate(); err != nil {
		return err
	}
	if snapshot.ClientSequence <= state.lastClientSequence {
		return fmt.Errorf("Activity sequence regression: %d <= %d", snapshot.ClientSequence, state.lastClientSequence)
	}
	state.snapshot = snapshot
	state.detail = detail
	state.lastClientSequence = snapshot.ClientSequence
	state.lastHeartbeatAt = snapshot.ObservedAt
	if state.connected && p.send != nil {
		p.send(payload)
	}
	return nil
}

// Tick emits heartbeat Snapshots only for working/thinking state. It never
// emits an Entry, so the timeline does not fill with synthetic progress rows.
func (p *agentActivityProducer) Tick() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now().UTC()
	for _, state := range p.states {
		kind := state.snapshot.ActivityKind
		if (kind != protocol.ActivityKindWorking && kind != protocol.ActivityKindThinking) || state.snapshot.ObservedAt.IsZero() || now.Sub(state.lastHeartbeatAt) < agentActivityHeartbeatInterval {
			continue
		}
		heartbeat := state.snapshot
		heartbeat.ClientSequence = state.lastClientSequence + 1
		heartbeat.ProducerFactID = raftActivityProducerFactID(heartbeat.AgentID, heartbeat.LaunchID, heartbeat.DaemonInstanceID, heartbeat.ClientSequence)
		heartbeat.ObservedAt = now
		state.snapshot = heartbeat
		state.lastClientSequence = heartbeat.ClientSequence
		state.lastHeartbeatAt = now
		if state.connected && p.send != nil {
			p.send(protocol.AgentActivityPayload{Snapshot: heartbeat, Detail: state.detail, IsHeartbeat: true})
		}
	}
}

// Probe returns the last actual observation with a matching probe identity.
// It does not advance sequence, alter current Activity, or fabricate Online.
func (p *agentActivityProducer) Probe(probe protocol.AgentActivityProbePayload) (protocol.AgentActivityPayload, error) {
	if p == nil {
		return protocol.AgentActivityPayload{}, errors.New("Activity producer is not configured")
	}
	if err := probe.Validate(); err != nil {
		return protocol.AgentActivityPayload{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	state := p.states[agentActivityProducerKey{agentID: probe.AgentID, launchID: probe.LaunchID}]
	if state == nil || state.snapshot.ObservedAt.IsZero() {
		return protocol.AgentActivityPayload{}, errors.New("no current Activity observation for probe")
	}
	snapshot := state.snapshot
	snapshot.ProbeID = probe.ProbeID
	payload := protocol.AgentActivityPayload{Snapshot: snapshot, Detail: state.detail}
	if err := payload.Validate(); err != nil {
		return protocol.AgentActivityPayload{}, err
	}
	return payload, nil
}

// ReconnectFrames reports only current status, session, and Snapshot for every
// managed Agent. It intentionally cannot replay intermediate Entries.
func (p *agentActivityProducer) ReconnectFrames() []agentActivityReconnectFrame {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	frames := make([]agentActivityReconnectFrame, 0, len(p.states)*3)
	for _, state := range p.states {
		state.connected = true
		frames = append(frames, agentActivityReconnectFrame{EventType: protocol.EventAgentStatus, Payload: state.status})
		frames = append(frames, agentActivityReconnectFrame{EventType: protocol.EventAgentSession, Payload: state.session})
		if !state.snapshot.ObservedAt.IsZero() {
			frames = append(frames, agentActivityReconnectFrame{EventType: protocol.EventAgentActivity, Payload: protocol.AgentActivityPayload{Snapshot: state.snapshot, Detail: state.detail}})
		}
	}
	return frames
}

// raftActivityProducerFactID is Raft 1.0.16's deterministic fact identity:
// daemon_activity:{agent}:{launch}:{daemonInstance}:{clientSeq}. Same process
// and seq replay the same fact; a new daemon instance starts a new identity.
func raftActivityProducerFactID(agentID, launchID, daemonInstanceID string, clientSeq int64) string {
	launch := strings.TrimSpace(launchID)
	if launch == "" {
		launch = "legacy"
	}
	generation := ""
	if instance := strings.TrimSpace(daemonInstanceID); instance != "" {
		generation = ":" + instance
	}
	return fmt.Sprintf("daemon_activity:%s:%s%s:%d", strings.TrimSpace(agentID), launch, generation, clientSeq)
}

func defaultAgentActivityDetail(detailKind string) string {
	switch detailKind {
	case "idle", "ready":
		return "Online"
	case "starting", "runtime_starting":
		return "Starting…"
	case "thinking_started":
		return "Thinking"
	case "model_request_started", "model_response_started", "runtime_progress":
		return "Working"
	case "message_received":
		return "Message received"
	case "runtime_error", "runtime_crashed", "runtime_stalled":
		return "Error"
	case "runtime_unavailable", "runtime_interrupted", "machine_disconnected":
		return "Offline"
	case "stopped":
		return "Stopped"
	default:
		return ""
	}
}
