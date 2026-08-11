package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func activityNarrativeEntry(activityKind, detailKind, text string) (protocol.AgentActivityEntry, error) {
	body, err := json.Marshal(protocol.AgentActivityNarrativeBody{
		Text:         text,
		ActivityKind: activityKind,
		DetailKind:   detailKind,
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

const agentActivityHeartbeatInterval = time.Minute

// agentActivityProducer is a best-effort daemon-side Activity publisher. It
// intentionally has no acknowledgement, durable outbox, HTTP upload, or
// coupling to Message, Task, billing, recovery, or terminal outcomes.
type agentActivityProducer struct {
	mu sync.Mutex

	now                 func() time.Time
	newID               func() string
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
	status             protocol.AgentStatusPayload
	session            protocol.AgentSessionPayload
	connected          bool
	lastHeartbeatAt    time.Time
	lastClientSequence int64
}

type agentActivityReconnectFrame struct {
	EventType string
	Payload   any
}

func newAgentActivityProducer(daemonInstanceID string, now func() time.Time, send func(protocol.AgentActivityPayload)) *agentActivityProducer {
	if now == nil {
		now = time.Now
	}
	return &agentActivityProducer{daemonInstanceID: daemonInstanceID, now: now, newID: func() string { return uuid.NewString() }, send: send, states: make(map[agentActivityProducerKey]*agentActivityProducerState)}
}

// Close releases activity sequence and managed-launch state with the owning
// Workspace Runner. Reconnects deliberately use DetachTransport instead.
func (p *agentActivityProducer) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
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
	// make PublishForManagedAgent choose a launch nondeterministically.
	for existing := range p.states {
		if existing.agentID == status.AgentID && existing.launchID != status.LaunchID {
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
	delete(p.states, agentActivityProducerKey{agentID: agentID, launchID: launchID})
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
			frames = append(frames, agentActivityReconnectFrame{EventType: protocol.EventAgentActivity, Payload: protocol.AgentActivityPayload{Snapshot: state.snapshot}})
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

// Publish emits an immediately useful Snapshot when connected and otherwise
// retains only the latest Snapshot. Entries observed during an outage are
// deliberately discarded rather than replayed as invented narrative history.
func (p *agentActivityProducer) Publish(snapshot protocol.AgentActivitySnapshot, entries []protocol.AgentActivityEntry) error {
	if p == nil {
		return errors.New("Activity producer is not configured")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.publishLocked(snapshot, entries)
}

// PublishForManagedAgent reports a fact from a local Manager-owned boundary
// such as concrete canonical Message handoff. The caller supplies no launch
// identity: the producer takes it only from the currently managed launch.
func (p *agentActivityProducer) PublishForManagedAgent(agentID, daemonInstanceID, activityKind, detailKind string, entries []protocol.AgentActivityEntry) error {
	if p == nil || agentID == "" || daemonInstanceID == "" {
		return errors.New("Activity managed Agent identity is required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var snapshot protocol.AgentActivitySnapshot
	for key, state := range p.states {
		if key.agentID == agentID {
			snapshot = state.snapshot
			snapshot.AgentID = agentID
			snapshot.LaunchID = key.launchID
			break
		}
	}
	if snapshot.LaunchID == "" {
		return errors.New("Activity is not managed for this Agent")
	}
	snapshot.DaemonInstanceID = daemonInstanceID
	snapshot.ActivityKind = activityKind
	snapshot.DetailKind = detailKind
	snapshot.ProbeID = ""
	// This call observes a new Manager fact for the same launch. Reusing the
	// prior fact identity would make publishLocked reject every transition after
	// the first one as a client-sequence regression.
	snapshot.ClientSequence = 0
	snapshot.ProducerFactID = ""
	snapshot.ObservedAt = time.Time{}
	return p.publishLocked(snapshot, entries)
}

// PublishEntryForManagedAgent appends a timeline fact without replacing the
// current lifecycle meaning. If no observation exists yet, Online is the
// conservative baseline used by the runtime diagnostic contract.
func (p *agentActivityProducer) PublishEntryForManagedAgent(agentID, daemonInstanceID string, entries []protocol.AgentActivityEntry) error {
	if p == nil || agentID == "" || daemonInstanceID == "" {
		return errors.New("Activity managed Agent identity is required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var snapshot protocol.AgentActivitySnapshot
	for key, state := range p.states {
		if key.agentID == agentID {
			snapshot = state.snapshot
			snapshot.AgentID = agentID
			snapshot.LaunchID = key.launchID
			break
		}
	}
	if snapshot.LaunchID == "" {
		return errors.New("Activity is not managed for this Agent")
	}
	if snapshot.ActivityKind == "" {
		snapshot.ActivityKind = protocol.ActivityKindOnline
		snapshot.DetailKind = "idle"
	}
	snapshot.DaemonInstanceID = daemonInstanceID
	snapshot.ClientSequence = 0
	snapshot.ProducerFactID = ""
	snapshot.ObservedAt = time.Time{}
	snapshot.ProbeID = ""
	return p.publishLocked(snapshot, entries)
}

// PublishHoldEntry projects a soft-held send onto the Agent's Activity timeline
// for BOTH managed and not-yet-managed Agents, aligning with Raft's semantics
// that the daemon still reports an Activity fact even when the Agent has no
// locally managed launch (the server reconciles a fragment whose launch/client-
// seq bookkeeping is not tied to a managed client sequence). It is fail-soft:
// a missing transport drops the fragment and never influences the send outcome.
//
// When the Agent has a managed launch the identity and ordering are inherited
// from the managed snapshot, so the hold entry stays inside the managed
// client-sequence stream (managed main path untouched). When the Agent is not
// managed, a standalone fragment Snapshot is synthesized (fresh launch identity,
// client-sequence=1, Online baseline) and pushed without touching the states map
// or client-seq bookkeeping, so it cannot perturb a later managed launch.
func (p *agentActivityProducer) PublishHoldEntry(agentID, daemonInstanceID string, entries []protocol.AgentActivityEntry) error {
	if p == nil || agentID == "" || daemonInstanceID == "" {
		return errors.New("Activity publisher Agent identity is required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, state := range p.states {
		if key.agentID == agentID {
			snapshot := state.snapshot
			snapshot.AgentID = agentID
			snapshot.LaunchID = key.launchID
			snapshot.DaemonInstanceID = daemonInstanceID
			if snapshot.ActivityKind == "" {
				snapshot.ActivityKind = protocol.ActivityKindOnline
				snapshot.DetailKind = "idle"
			}
			snapshot.ClientSequence = 0 // publishLocked assigns the next value
			snapshot.ProducerFactID = ""
			snapshot.ObservedAt = time.Time{}
			snapshot.ProbeID = ""
			return p.publishLocked(snapshot, entries)
		}
	}
	// A hold before managed launch has no authoritative launch identity. Drop
	// this best-effort presentation fact rather than inventing one.
	return nil
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
		snapshot.ProducerFactID = p.newID()
	}
	payload := protocol.AgentActivityPayload{Snapshot: snapshot, Entries: entries}
	if err := payload.Validate(); err != nil {
		return err
	}
	if snapshot.ClientSequence <= state.lastClientSequence {
		return fmt.Errorf("Activity sequence regression: %d <= %d", snapshot.ClientSequence, state.lastClientSequence)
	}
	state.snapshot = snapshot
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
		heartbeat.ProducerFactID = p.newID()
		heartbeat.ObservedAt = now
		state.snapshot = heartbeat
		state.lastClientSequence = heartbeat.ClientSequence
		state.lastHeartbeatAt = now
		if state.connected && p.send != nil {
			p.send(protocol.AgentActivityPayload{Snapshot: heartbeat})
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
	payload := protocol.AgentActivityPayload{Snapshot: snapshot}
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
			frames = append(frames, agentActivityReconnectFrame{EventType: protocol.EventAgentActivity, Payload: protocol.AgentActivityPayload{Snapshot: state.snapshot}})
		}
	}
	return frames
}
