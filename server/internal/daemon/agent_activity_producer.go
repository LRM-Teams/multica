package daemon

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const agentActivityHeartbeatInterval = time.Minute

// agentActivityProducer is a best-effort daemon-side Activity publisher. It
// intentionally has no acknowledgement, durable outbox, HTTP upload, or
// coupling to Message, Task, billing, recovery, or terminal outcomes.
type agentActivityProducer struct {
	mu sync.Mutex

	now                 func() time.Time
	newID               func() string
	send                func(protocol.AgentActivityPayload)
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

func newAgentActivityProducer(now func() time.Time, send func(protocol.AgentActivityPayload)) *agentActivityProducer {
	if now == nil {
		now = time.Now
	}
	return &agentActivityProducer{now: now, newID: func() string { return uuid.NewString() }, send: send, states: make(map[agentActivityProducerKey]*agentActivityProducerState)}
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
