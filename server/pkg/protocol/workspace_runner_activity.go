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

	// Computer control uses Raft 1.0.16 names on the DaemonCore connect
	// socket. The child does not swap the machine binary; it forwards the
	// command to Computer Host through the injected Host callback.
	EventComputerUpgrade         = "computer:upgrade"
	EventComputerRestart         = "computer:restart"
	EventComputerUpgradeProgress = "computer:upgrade:progress"
	EventComputerUpgradeDone     = "computer:upgrade:done"
	EventComputerRestartDone     = "computer:restart:done"
	EventAgentStartAck           = "agent:start:ack"
	EventAgentActivity           = "agent:activity"
	EventAgentActivityProbe      = "agent:activity_probe"
	EventAgentSession            = "agent:session"

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

// agentActivityFactDetailKinds is Raft v1.0.16's fact vocabulary plus the
// Multica tool facts that use the same contract. Deliberately excluded Raft
// display/diagnostic placeholders (none, daemon_activity, external_activity,
// slock_action, other) must never cross the agent:activity boundary.
var agentActivityFactDetailKinds = map[string]struct{}{
	"message_received": {}, "freshness_hold": {}, "starting": {}, "runtime_starting": {},
	"idle": {}, "running_command": {}, "checking_messages": {}, "compacting_context": {},
	"compaction_finished": {}, "compaction_stale": {}, "reviewing_changes": {},
	"review_finished": {}, "review_stale": {}, "runtime_reconnecting": {},
	"runtime_error": {}, "runtime_crashed": {}, "runtime_unavailable": {},
	"runtime_stalled": {}, "stalled_recovery": {}, "stopped": {}, "ready": {},
	"runtime_interrupted": {}, "machine_disconnected": {}, "computer_started": {},
	"computer_restarted": {}, "computer_upgraded": {}, "computer_operation_failed": {},
	"synthetic_repair": {}, "system_message": {}, "runtime_progress": {},
	"model_request_started": {}, "model_response_started": {}, "tool_started": {},
	"tool_end": {}, "thinking_started": {}, "thinking_end": {}, "subagent_activity": {},

	"reading_file": {}, "writing_file": {}, "editing_file": {}, "searching_files": {},
	"searching_code": {}, "fetching_url": {}, "searching_web": {}, "updating_tasks": {},
	"sending_message": {}, "waiting_for_message": {}, "reading_history": {},
	"searching_messages": {}, "listing_server": {}, "listing_tasks": {},
	"creating_tasks": {}, "claiming_task": {}, "unclaiming_task": {},
	"updating_task_status": {}, "adding_channel_member": {}, "joining_channel": {},
	"leaving_channel": {}, "uploading_file": {}, "viewing_file": {},
	"listing_issues": {}, "getting_issue": {}, "searching_issues": {},
	"listing_issue_comments": {}, "commenting_issue": {}, "deleting_issue_comment": {},
	"scheduling_reminder": {}, "listing_reminders": {}, "canceling_reminder": {},
	"snoozing_reminder": {}, "updating_reminder": {}, "logging_reminder": {},
	"collaborating": {},
}

func IsAgentActivityFactDetailKind(detailKind string) bool {
	_, ok := agentActivityFactDetailKinds[detailKind]
	return ok
}

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
	WorkspaceID        string   `json:"workspaceId"`
	DaemonInstanceID   string   `json:"daemonInstanceId"`
	ActiveCapabilities []string `json:"activeCapabilities,omitempty"`
	RunningAgents      []string `json:"runningAgents,omitempty"`
}

// WorkspaceRunnerPingPayload and WorkspaceRunnerPongPayload are connection
// liveness only. They are not runtime heartbeats and must not project Agent
// Activity.
type WorkspaceRunnerPingPayload struct {
	PingID string `json:"pingId"`
}

// ComputerUpgradePayload is the Raft 1.0.16 connect-socket command for one
// machine-owned upgrade. RequestID correlates the command, successor marker,
// progress, and completion.
type ComputerUpgradePayload struct {
	RequestID     string `json:"requestId"`
	TargetVersion string `json:"targetVersion,omitempty"`
}

func (p ComputerUpgradePayload) Validate() error {
	if strings.TrimSpace(p.RequestID) == "" {
		return fmt.Errorf("Computer upgrade request identity is required")
	}
	return nil
}

// ComputerRestartPayload is the Raft 1.0.16 connect-socket restart command.
type ComputerRestartPayload struct {
	RequestID   string `json:"requestId"`
	OperationID string `json:"operationId,omitempty"`
}

func (p ComputerRestartPayload) Operation() string {
	if id := strings.TrimSpace(p.OperationID); id != "" {
		return id
	}
	return strings.TrimSpace(p.RequestID)
}

func (p ComputerRestartPayload) Validate() error {
	if p.Operation() == "" {
		return fmt.Errorf("Computer restart request identity is required")
	}
	return nil
}

// ComputerUpgradeProgressPayload and ComputerUpgradeDonePayload are emitted on
// the same DaemonConnection that received the command. Host owns the swap;
// the child only writes these frames.
type ComputerUpgradeProgressPayload struct {
	RequestID string `json:"requestId"`
	Phase     string `json:"phase,omitempty"`
	Message   string `json:"message,omitempty"`
	Percent   *int   `json:"percent,omitempty"`
}

func (p ComputerUpgradeProgressPayload) Validate() error {
	return validateRequiredIDs(p.RequestID)
}

type ComputerUpgradeDonePayload struct {
	RequestID  string `json:"requestId"`
	OK         bool   `json:"ok"`
	NewVersion string `json:"newVersion,omitempty"`
	Error      string `json:"error,omitempty"`
	RolledBack bool   `json:"rolledBack,omitempty"`
}

func (p ComputerUpgradeDonePayload) Validate() error {
	return validateRequiredIDs(p.RequestID)
}

type WorkspaceRunnerPongPayload struct {
	PingID string `json:"pingId"`
}

// WorkspaceRunnerAgentStartConfig mirrors Raft's agent:start config boundary.
// A non-empty SessionID resumes that provider session; an omitted SessionID
// starts fresh.
type WorkspaceRunnerAgentStartConfig struct {
	SessionID string `json:"sessionId,omitempty"`
}

// WorkspaceRunnerAgentStartPayload is the server command accepted by a local
// Agent Process Manager. LaunchID is the server-owned launch epoch and remains
// stable when the same desired launch is retried after reconnect.
type WorkspaceRunnerAgentStartPayload struct {
	AgentID         string                          `json:"agentId"`
	RuntimeID       string                          `json:"runtimeId"`
	LaunchID        string                          `json:"launchId"`
	StartDispatchID string                          `json:"startDispatchId"`
	Config          WorkspaceRunnerAgentStartConfig `json:"config"`
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

// WorkspaceRunnerAgentResetWorkspacePayload mirrors Raft's
// agent:reset-workspace command. OperationID is Multica's correlation fence;
// Workspace identity remains owned by the authenticated Runner connection.
type WorkspaceRunnerAgentResetWorkspacePayload struct {
	OperationID string `json:"operationId"`
	AgentID     string `json:"agentId"`
}

const (
	AgentResetWorkspaceSucceeded = "succeeded"
	AgentResetWorkspaceFailed    = "failed"
)

// WorkspaceRunnerAgentResetWorkspaceResultPayload is Multica's terminal
// receipt extension for Raft's fire-and-forget reset command. The server must
// observe success before it is allowed to issue the replacement agent:start.
type WorkspaceRunnerAgentResetWorkspaceResultPayload struct {
	OperationID string `json:"operationId"`
	AgentID     string `json:"agentId"`
	Status      string `json:"status"`
	ReasonCode  string `json:"reasonCode,omitempty"`
}

// AgentStatusPayload is lifecycle management state, not a user Activity
// label. active is reported only after a live provider process exists.
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

// AgentActivitySnapshot is the server's replaceable current presentation
// evidence. The daemon keeps the same shape internally for heartbeat and
// reconnect state, but AgentActivityPayload's JSON boundary sends only Raft
// execution facts; ActivityKind is derived by the server from DetailKind.
type AgentActivitySnapshot struct {
	AgentID           string    `json:"-"`
	LaunchID          string    `json:"-"`
	DaemonInstanceID  string    `json:"-"`
	ClientSequence    int64     `json:"-"`
	ProducerFactID    string    `json:"-"`
	ObservedAt        time.Time `json:"-"`
	ActivityKind      string    `json:"-"`
	DetailKind        string    `json:"-"`
	ProbeID           string    `json:"-"`
	ProcessInstanceID string    `json:"-"`
}

// AgentActivityEntry keeps its body as an open envelope. Like Raft, its array
// order is its position; the server derives the persistence ordinal instead of
// carrying a duplicate position field on the wire. Server presentation owns
// known-kind parsing and the generic fallback used for future kinds.
type AgentActivityEntry struct {
	Kind string          `json:"kind"`
	Body json.RawMessage `json:"body"`
}

// AgentActivityNarrativeBody carries only the event-local fact. The server
// derives its lifecycle presentation from DetailKind; the daemon must not
// serialize a competing ActivityKind into timeline history.
type AgentActivityNarrativeBody struct {
	Text       string `json:"text"`
	DetailKind string `json:"detail_kind"`
}

// AgentActivitySystemBody is a bounded, user-visible runtime diagnostic. It
// is a timeline fact and does not replace the current lifecycle Snapshot.
type AgentActivitySystemBody struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

type AgentActivityPayload struct {
	Snapshot    AgentActivitySnapshot `json:"-"`
	Detail      string                `json:"-"`
	Entries     []AgentActivityEntry  `json:"-"`
	IsHeartbeat bool                  `json:"-"`
}

// agentActivityWirePayload is Raft v1.0.16's fact-only agent:activity body.
// It intentionally has no snapshot, activityKind, or processInstanceId.
type agentActivityWirePayload struct {
	AgentID          string               `json:"agentId"`
	Detail           string               `json:"detail"`
	DetailKind       string               `json:"detailKind"`
	Entries          []AgentActivityEntry `json:"entries,omitempty"`
	LaunchID         string               `json:"launchId,omitempty"`
	DaemonInstanceID string               `json:"daemonInstanceId,omitempty"`
	ProbeID          string               `json:"probeId,omitempty"`
	ClientSeq        int64                `json:"clientSeq,omitempty"`
	ProducerFactID   string               `json:"producerFactId,omitempty"`
	ObservedAtMS     *int64               `json:"observedAtMs,omitempty"`
	IsHeartbeat      bool                 `json:"isHeartbeat"`
}

func (p AgentActivityPayload) MarshalJSON() ([]byte, error) {
	var observedAtMS *int64
	if !p.Snapshot.ObservedAt.IsZero() {
		value := p.Snapshot.ObservedAt.UnixMilli()
		observedAtMS = &value
	}
	return json.Marshal(agentActivityWirePayload{
		AgentID:          p.Snapshot.AgentID,
		Detail:           p.Detail,
		DetailKind:       p.Snapshot.DetailKind,
		Entries:          p.Entries,
		LaunchID:         p.Snapshot.LaunchID,
		DaemonInstanceID: p.Snapshot.DaemonInstanceID,
		ProbeID:          p.Snapshot.ProbeID,
		ClientSeq:        p.Snapshot.ClientSequence,
		ProducerFactID:   p.Snapshot.ProducerFactID,
		ObservedAtMS:     observedAtMS,
		IsHeartbeat:      p.IsHeartbeat,
	})
}

func (p *AgentActivityPayload) UnmarshalJSON(data []byte) error {
	var wire agentActivityWirePayload
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	observedAt := time.Time{}
	if wire.ObservedAtMS != nil {
		observedAt = time.UnixMilli(*wire.ObservedAtMS).UTC()
	}
	p.Snapshot = AgentActivitySnapshot{
		AgentID:          wire.AgentID,
		LaunchID:         wire.LaunchID,
		DaemonInstanceID: wire.DaemonInstanceID,
		ClientSequence:   wire.ClientSeq,
		ProducerFactID:   wire.ProducerFactID,
		ObservedAt:       observedAt,
		DetailKind:       wire.DetailKind,
		ProbeID:          wire.ProbeID,
	}
	p.Detail = wire.Detail
	p.Entries = wire.Entries
	p.IsHeartbeat = wire.IsHeartbeat
	return nil
}

// AgentActivityProbePayload asks a Manager for an actual current observation.
// Receiving this frame must not change local Activity by itself.
type AgentActivityProbePayload struct {
	AgentID  string `json:"agentId"`
	LaunchID string `json:"launchId"`
	ProbeID  string `json:"probeId"`
}

func (p WorkspaceRunnerReadyPayload) Validate() error {
	if err := validateRequiredIDs(p.WorkspaceID, p.DaemonInstanceID); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(p.ActiveCapabilities))
	for _, capability := range p.ActiveCapabilities {
		if err := validateRequiredIDs(capability); err != nil {
			return fmt.Errorf("invalid Workspace Runner capability")
		}
		if _, duplicate := seen[capability]; duplicate {
			return fmt.Errorf("duplicate Workspace Runner capability %q", capability)
		}
		seen[capability] = struct{}{}
	}
	if _, supported := seen[DaemonCapabilityWorkspaceRunnerAgentProcess]; !supported {
		return fmt.Errorf("Workspace Runner Agent process capability is required")
	}
	running := make(map[string]struct{}, len(p.RunningAgents))
	for _, agentID := range p.RunningAgents {
		if err := validateRequiredIDs(agentID); err != nil {
			return fmt.Errorf("invalid running Agent identity")
		}
		if _, duplicate := running[agentID]; duplicate {
			return fmt.Errorf("duplicate running Agent %q", agentID)
		}
		running[agentID] = struct{}{}
	}
	return nil
}

func (p WorkspaceRunnerPingPayload) Validate() error { return validateRequiredIDs(p.PingID) }
func (p WorkspaceRunnerPongPayload) Validate() error { return validateRequiredIDs(p.PingID) }

func (p WorkspaceRunnerAgentStartPayload) Validate() error {
	return validateRequiredIDs(p.AgentID, p.RuntimeID, p.LaunchID, p.StartDispatchID)
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

func (p WorkspaceRunnerAgentResetWorkspacePayload) Validate() error {
	return validateRequiredIDs(p.OperationID, p.AgentID)
}

func (p WorkspaceRunnerAgentResetWorkspaceResultPayload) Validate() error {
	if err := validateRequiredIDs(p.OperationID, p.AgentID); err != nil {
		return err
	}
	if !isOneOf(p.Status, AgentResetWorkspaceSucceeded, AgentResetWorkspaceFailed) {
		return fmt.Errorf("invalid Agent workspace reset status %q", p.Status)
	}
	if p.Status == AgentResetWorkspaceSucceeded && strings.TrimSpace(p.ReasonCode) != "" {
		return fmt.Errorf("successful Agent workspace reset must not include a reason code")
	}
	if p.Status == AgentResetWorkspaceFailed && strings.TrimSpace(p.ReasonCode) == "" {
		return fmt.Errorf("failed Agent workspace reset requires a reason code")
	}
	return validateOptionalIDs(p.ReasonCode)
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
	if len(p.Detail) > maxActivityEntryBytes {
		return fmt.Errorf("activity detail exceeds %d bytes", maxActivityEntryBytes)
	}
	if len(p.Entries) > maxActivityEntriesPerFrame {
		return fmt.Errorf("too many activity entries: %d", len(p.Entries))
	}
	for _, entry := range p.Entries {
		if err := entry.Validate(); err != nil {
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
	if p.ActivityKind != "" && !isOneOf(p.ActivityKind, ActivityKindOnline, ActivityKindThinking, ActivityKindWorking, ActivityKindError, ActivityKindOffline) {
		return fmt.Errorf("invalid activity kind %q", p.ActivityKind)
	}
	if strings.TrimSpace(p.DetailKind) == "" {
		return fmt.Errorf("activity detail kind is required")
	}
	if len(p.DetailKind) > maxActivityDetailKindLength {
		return fmt.Errorf("activity detail kind exceeds %d bytes", maxActivityDetailKindLength)
	}
	if !IsAgentActivityFactDetailKind(p.DetailKind) {
		return fmt.Errorf("invalid or non-fact activity detail kind %q", p.DetailKind)
	}
	return validateOptionalIDs(p.DetailKind, p.ProbeID, p.ProcessInstanceID)
}

func (e AgentActivityEntry) Validate() error {
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
