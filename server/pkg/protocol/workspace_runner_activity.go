package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Workspace Runner frames intentionally use their Raft names. They are a
// daemon-to-server protocol boundary, not an HTTP API, so their field names
// remain camelCase like agent:deliver.
const (
	EventWorkspaceRunnerReady = "ready"
	EventWorkspaceRunnerPing  = "ping"
	EventWorkspaceRunnerPong  = "pong"
	EventAgentStartAck        = "agent:start:ack"
	EventAgentActivity        = "agent:activity"
	EventAgentActivityProbe   = "agent:activity_probe"
	EventAgentSession         = "agent:session"

	AgentStatusActive   = "active"
	AgentStatusInactive = "inactive"

	AgentStartQueueQueued   = "queued"
	AgentStartQueueStarting = "starting"
	AgentStartQueueRunning  = "running"
	AgentStartQueueRebound  = "rebound"

	ActivityKindOnline   = "online"
	ActivityKindThinking = "thinking"
	ActivityKindWorking  = "working"
	ActivityKindError    = "error"
	ActivityKindOffline  = "offline"
)

const (
	maxWorkspaceRunnerIdentityLength = 200
	maxActivityDetailKindLength      = 120
	maxActivityEntryKindLength       = 120
	maxActivityEntryBytes            = 64 << 10
	maxActivityEntriesPerFrame       = 64
)

// WorkspaceRunnerReadyPayload establishes one daemon-instance/Workspace
// connection. It says that the local Manager has initialized and reconciled;
// it deliberately says nothing about any individual Agent being ready.
type WorkspaceRunnerReadyPayload struct {
	WorkspaceID      string `json:"workspaceId"`
	DaemonInstanceID string `json:"daemonInstanceId"`
}

// WorkspaceRunnerPingPayload and WorkspaceRunnerPongPayload are connection
// liveness only. They are not runtime heartbeats and must not project Agent
// Activity.
type WorkspaceRunnerPingPayload struct {
	PingID string `json:"pingId"`
}

type WorkspaceRunnerPongPayload struct {
	PingID string `json:"pingId"`
}

// WorkspaceRunnerAgentStartPayload is the server command accepted by a local
// Agent Process Manager. StartDispatchID is stable across retries; LaunchID is
// assigned by the Manager and returned in AgentStartAckPayload.
type WorkspaceRunnerAgentStartPayload struct {
	AgentID         string `json:"agentId"`
	RuntimeID       string `json:"runtimeId"`
	StartDispatchID string `json:"startDispatchId"`
}

// AgentStartAckPayload is an idempotent acceptance receipt. QueueState never
// claims spawn, runtime readiness, or initial activation delivery.
type AgentStartAckPayload struct {
	AgentID         string `json:"agentId"`
	LaunchID        string `json:"launchId"`
	StartDispatchID string `json:"startDispatchId"`
	QueueState      string `json:"queueState"`
	QueueDepth      int    `json:"queueDepth"`
	QueueAgeMS      int64  `json:"queueAgeMs"`
}

type WorkspaceRunnerAgentStopPayload struct {
	AgentID  string `json:"agentId"`
	LaunchID string `json:"launchId"`
}

// AgentStatusPayload is lifecycle management state, not a user Activity
// label. active includes an idle, no-process but wakeable managed launch.
type AgentStatusPayload struct {
	AgentID  string `json:"agentId"`
	LaunchID string `json:"launchId"`
	Status   string `json:"status"`
}

// AgentSessionPayload reports a provider session independently from process,
// launch, turn, and runtime-state generation identities.
type AgentSessionPayload struct {
	AgentID           string `json:"agentId"`
	LaunchID          string `json:"launchId"`
	ProviderSessionID string `json:"providerSessionId,omitempty"`
	TurnID            string `json:"turnId,omitempty"`
	RuntimeGeneration int64  `json:"runtimeGeneration"`
}

// AgentActivitySnapshot is replaceable current presentation evidence. Entries
// are narrative facts and are intentionally separate so an outage may retain
// only this latest Snapshot without inventing an Entry backlog.
type AgentActivitySnapshot struct {
	AgentID           string    `json:"agentId"`
	LaunchID          string    `json:"launchId"`
	DaemonInstanceID  string    `json:"daemonInstanceId"`
	ClientSequence    int64     `json:"clientSequence"`
	ProducerFactID    string    `json:"producerFactId"`
	ObservedAt        time.Time `json:"observedAt"`
	ActivityKind      string    `json:"activityKind"`
	DetailKind        string    `json:"detailKind,omitempty"`
	ProbeID           string    `json:"probeId,omitempty"`
	ProcessInstanceID string    `json:"processInstanceId,omitempty"`
}

// AgentActivityEntry keeps its body as an open envelope. The transport only
// requires a bounded JSON object; server presentation owns known-kind parsing
// and the generic fallback used for future kinds.
type AgentActivityEntry struct {
	Kind     string          `json:"kind"`
	Position int             `json:"position"`
	Body     json.RawMessage `json:"body"`
}

// AgentActivityNarrativeBody retains the lifecycle state that was current
// when a narrative fact occurred. The latest Snapshot is replaceable, so a
// historical Entry cannot safely borrow its state during presentation.
type AgentActivityNarrativeBody struct {
	Text         string `json:"text"`
	ActivityKind string `json:"activity_kind,omitempty"`
	DetailKind   string `json:"detail_kind,omitempty"`
}

// AgentActivitySystemBody is a bounded, user-visible runtime diagnostic. It
// is a timeline fact and does not replace the current lifecycle Snapshot.
type AgentActivitySystemBody struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

type AgentActivityPayload struct {
	Snapshot AgentActivitySnapshot `json:"snapshot"`
	Entries  []AgentActivityEntry  `json:"entries,omitempty"`
}

// AgentActivityProbePayload asks a Manager for an actual current observation.
// Receiving this frame must not change local Activity by itself.
type AgentActivityProbePayload struct {
	AgentID  string `json:"agentId"`
	LaunchID string `json:"launchId"`
	ProbeID  string `json:"probeId"`
}

func (p WorkspaceRunnerReadyPayload) Validate() error {
	return validateRequiredIDs(p.WorkspaceID, p.DaemonInstanceID)
}

func (p WorkspaceRunnerPingPayload) Validate() error { return validateRequiredIDs(p.PingID) }
func (p WorkspaceRunnerPongPayload) Validate() error { return validateRequiredIDs(p.PingID) }

func (p WorkspaceRunnerAgentStartPayload) Validate() error {
	return validateRequiredIDs(p.AgentID, p.RuntimeID, p.StartDispatchID)
}

func (p AgentStartAckPayload) Validate() error {
	if err := validateRequiredIDs(p.AgentID, p.LaunchID, p.StartDispatchID); err != nil {
		return err
	}
	if !isOneOf(p.QueueState, AgentStartQueueQueued, AgentStartQueueStarting, AgentStartQueueRunning, AgentStartQueueRebound) {
		return fmt.Errorf("invalid agent start queue state %q", p.QueueState)
	}
	if p.QueueDepth < 0 || p.QueueAgeMS < 0 {
		return fmt.Errorf("agent start acknowledgement has negative queue metadata")
	}
	return nil
}

func (p WorkspaceRunnerAgentStopPayload) Validate() error {
	return validateRequiredIDs(p.AgentID, p.LaunchID)
}

func (p AgentStatusPayload) Validate() error {
	if err := validateRequiredIDs(p.AgentID, p.LaunchID); err != nil {
		return err
	}
	if !isOneOf(p.Status, AgentStatusActive, AgentStatusInactive) {
		return fmt.Errorf("invalid agent status %q", p.Status)
	}
	return nil
}

func (p AgentSessionPayload) Validate() error {
	if err := validateRequiredIDs(p.AgentID, p.LaunchID); err != nil {
		return err
	}
	if p.RuntimeGeneration < 0 {
		return fmt.Errorf("runtime generation must not be negative")
	}
	return validateOptionalIDs(p.ProviderSessionID, p.TurnID)
}

func (p AgentActivityPayload) Validate() error {
	if err := p.Snapshot.Validate(); err != nil {
		return err
	}
	if len(p.Entries) > maxActivityEntriesPerFrame {
		return fmt.Errorf("too many activity entries: %d", len(p.Entries))
	}
	for index, entry := range p.Entries {
		if err := entry.Validate(index); err != nil {
			return err
		}
	}
	return nil
}

func (p AgentActivitySnapshot) Validate() error {
	if err := validateRequiredIDs(p.AgentID, p.LaunchID, p.DaemonInstanceID, p.ProducerFactID); err != nil {
		return err
	}
	if p.ClientSequence <= 0 {
		return fmt.Errorf("activity client sequence must be positive")
	}
	if p.ObservedAt.IsZero() {
		return fmt.Errorf("activity observation time is required")
	}
	if !isOneOf(p.ActivityKind, ActivityKindOnline, ActivityKindThinking, ActivityKindWorking, ActivityKindError, ActivityKindOffline) {
		return fmt.Errorf("invalid activity kind %q", p.ActivityKind)
	}
	if len(p.DetailKind) > maxActivityDetailKindLength {
		return fmt.Errorf("activity detail kind exceeds %d bytes", maxActivityDetailKindLength)
	}
	return validateOptionalIDs(p.DetailKind, p.ProbeID, p.ProcessInstanceID)
}

func (e AgentActivityEntry) Validate(expectedPosition int) error {
	if e.Position != expectedPosition {
		return fmt.Errorf("activity entry position %d, want %d", e.Position, expectedPosition)
	}
	if err := validateRequiredIDs(e.Kind); err != nil {
		return err
	}
	if len(e.Kind) > maxActivityEntryKindLength {
		return fmt.Errorf("activity entry kind exceeds %d bytes", maxActivityEntryKindLength)
	}
	if len(e.Body) == 0 || len(e.Body) > maxActivityEntryBytes || !json.Valid(e.Body) {
		return fmt.Errorf("invalid activity entry body")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(e.Body, &object); err != nil || object == nil {
		return fmt.Errorf("activity entry body must be a JSON object")
	}
	return nil
}

func (p AgentActivityProbePayload) Validate() error {
	return validateRequiredIDs(p.AgentID, p.LaunchID, p.ProbeID)
}

func validateRequiredIDs(values ...string) error {
	for _, value := range values {
		if strings.TrimSpace(value) == "" || len(value) > maxWorkspaceRunnerIdentityLength {
			return fmt.Errorf("invalid required protocol identity")
		}
	}
	return nil
}

func validateOptionalIDs(values ...string) error {
	for _, value := range values {
		if len(value) > maxWorkspaceRunnerIdentityLength {
			return fmt.Errorf("protocol identity exceeds %d bytes", maxWorkspaceRunnerIdentityLength)
		}
	}
	return nil
}

func isOneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
