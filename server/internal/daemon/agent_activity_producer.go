package daemon

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func activityStatusEntry(detailKind, text string) (protocol.AgentActivityEntry, error) {
	body, err := json.Marshal(protocol.AgentActivityStatusBody{
		Activity:   activityKindFromDetailKind(detailKind),
		Detail:     text,
		DetailKind: detailKind,
	})
	if err != nil {
		return protocol.AgentActivityEntry{}, err
	}
	return protocol.AgentActivityEntry{Kind: "status", Body: body}, nil
}

func activityToolStartEntry(toolName, toolInput string) (protocol.AgentActivityEntry, error) {
	toolInput = truncateRunes(toolInput, maxActivityCommandRunes)
	body, err := json.Marshal(protocol.AgentActivityToolStartBody{
		ToolName:  toolName,
		ToolInput: toolInput,
	})
	if err != nil {
		return protocol.AgentActivityEntry{}, err
	}
	return protocol.AgentActivityEntry{Kind: "tool_start", Body: body}, nil
}

func activitySystemEntry(title, text string) (protocol.AgentActivityEntry, error) {
	body, err := json.Marshal(protocol.AgentActivitySystemBody{Title: title, Text: text})
	if err != nil {
		return protocol.AgentActivityEntry{}, err
	}
	return protocol.AgentActivityEntry{Kind: "system", Body: body}, nil
}

var diagnosticSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+\-/=]+`),
	regexp.MustCompile(`(?i)\b(?:sk|ghp|github_pat|xox[baprs])-[-A-Za-z0-9_]+`),
	regexp.MustCompile(`(?i)\b(?:api[_-]?key|token|password|secret|authorization)\s*[:=]\s*["']?[^\s"']+`),
}

func sanitizeDiagnosticText(value string) string {
	value = strings.TrimSpace(value)
	for _, pattern := range diagnosticSecretPatterns {
		value = pattern.ReplaceAllString(value, "[REDACTED]")
	}
	return value
}

func activityDiagnosticEntry(source, reference, title, kind, text string) (protocol.AgentActivityEntry, error) {
	source = sanitizeDiagnosticText(source)
	reference = sanitizeDiagnosticText(reference)
	title = sanitizeDiagnosticText(title)
	kind = sanitizeDiagnosticText(kind)
	text = sanitizeDiagnosticText(text)
	if title == "" {
		title = "Runtime warning"
	}
	if kind != "" && !strings.Contains(strings.ToLower(title), strings.ToLower(kind)) {
		title += " (" + kind + ")"
	}
	if text == "" {
		text = "Provider reported a warning"
	}
	body, err := json.Marshal(protocol.AgentActivitySystemBody{Title: title, Text: text, Source: source, Reference: reference})
	if err != nil {
		return protocol.AgentActivityEntry{}, err
	}
	return protocol.AgentActivityEntry{Kind: "system", Body: body}, nil
}

const (
	compactionStaleTimeout = 5 * time.Minute
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
	agentID         string
	agentInstanceID string
}

type agentActivityProducerState struct {
	snapshot       protocol.AgentActivitySnapshot
	detail         string
	latestActivity protocol.AgentActivityPayload
	status         protocol.AgentStatusPayload
	session        protocol.AgentSessionPayload
	connected      bool
	compaction     agentActivityCompactionState
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

// Close releases Activity and managed-launch state with the owning
// WorkspaceDaemon. Reconnects deliberately use DetachTransport instead.
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

func (p *agentActivityProducer) SetManaged(agentInstanceID string, status protocol.AgentStatusPayload, session protocol.AgentSessionPayload) error {
	if p == nil {
		return errors.New("Activity producer is not configured")
	}
	if err := status.Validate(); err != nil {
		return err
	}
	if err := session.Validate(); err != nil {
		return err
	}
	if status.AgentID != session.AgentID {
		return errors.New("Activity status and session identities do not match")
	}
	if strings.TrimSpace(agentInstanceID) == "" {
		return errors.New("Activity local Agent instance identity is required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	// One Agent has one current managed launch. A server-commanded launch may
	// replace a resident Message launch (or vice versa); retaining both would
	// make launch-free observations ambiguous.
	for existing := range p.states {
		if existing.agentID == status.AgentID && existing.agentInstanceID != agentInstanceID {
			clearAgentActivityCompaction(p.states[existing])
			delete(p.states, existing)
		}
	}
	key := agentActivityProducerKey{agentID: status.AgentID, agentInstanceID: agentInstanceID}
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
func (p *agentActivityProducer) UpdateProviderSession(agentInstanceID string, session protocol.AgentSessionPayload) (bool, error) {
	if p == nil {
		return false, errors.New("Activity producer is not configured")
	}
	if err := session.Validate(); err != nil {
		return false, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	state := p.states[agentActivityProducerKey{agentID: session.AgentID, agentInstanceID: agentInstanceID}]
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

func (p *agentActivityProducer) SetConnected(agentID, agentInstanceID string, connected bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if state := p.states[agentActivityProducerKey{agentID: agentID, agentInstanceID: agentInstanceID}]; state != nil {
		state.connected = connected
	}
}

// RemoveManaged forgets a stopped launch so a later reconnect cannot report a
// stale active status, session, or Snapshot for it.
func (p *agentActivityProducer) RemoveManaged(agentID, agentInstanceID string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	key := agentActivityProducerKey{agentID: agentID, agentInstanceID: agentInstanceID}
	clearAgentActivityCompaction(p.states[key])
	delete(p.states, key)
}

// AttachTransport makes a newly established WorkspaceDaemon connection the
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
			frames = append(frames, agentActivityReconnectFrame{EventType: protocol.EventAgentActivity, Payload: state.latestActivity})
		}
	}
	return p.transportGeneration, frames
}

// DetachTransport only disconnects the lease that owns it. A late defer from
// a replaced connection must not silence the current WorkspaceDaemon.
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
func (p *agentActivityProducer) publishLocked(key agentActivityProducerKey, snapshot protocol.AgentActivitySnapshot, broadcast activityBroadcast) error {
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
	detail := broadcast.detail
	if snapshot.DetailKind == state.snapshot.DetailKind {
		if detail == "" {
			detail = state.detail
		}
	}
	for _, entry := range broadcast.trajectory {
		if entry.Kind != "status" {
			continue
		}
		var body protocol.AgentActivityStatusBody
		if json.Unmarshal(entry.Body, &body) == nil && body.Detail != "" {
			detail = body.Detail
			break
		}
	}
	if detail == "" {
		detail = defaultAgentActivityDetail(snapshot.DetailKind)
	}
	summary := projectActivitySummary(snapshot)
	timeline := make([]protocol.AgentActivityTimelineRow, 0, len(broadcast.trajectory))
	for _, entry := range broadcast.trajectory {
		timeline = append(timeline, projectActivityTimelineEntry(entry, summary))
	}
	payload := protocol.AgentActivityPayload{Snapshot: snapshot, Summary: summary, Timeline: timeline}
	if err := payload.Validate(); err != nil {
		return err
	}
	state.snapshot = snapshot
	state.detail = detail
	state.latestActivity = payload
	if state.connected && p.send != nil {
		p.send(payload)
	}
	return nil
}

// ReconnectFrames reports current status, session, and Raft's latest complete
// Activity frame for every managed Agent. Replaying the complete frame keeps
// its Timeline Entry attached to the Snapshot across a connection loss.
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
			frames = append(frames, agentActivityReconnectFrame{EventType: protocol.EventAgentActivity, Payload: state.latestActivity})
		}
	}
	return frames
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
