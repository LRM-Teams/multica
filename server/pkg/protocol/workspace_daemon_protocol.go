package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// WorkspaceDaemon frames intentionally use their Raft names. They are a
// daemon-to-server protocol boundary, not an HTTP API, so their field names
// remain camelCase like agent:deliver.
const (
	EventWorkspaceDaemonReady = "ready"
	EventWorkspaceDaemonPing  = "ping"
	EventWorkspaceDaemonPong  = "pong"

	// Computer control uses Raft 1.0.16 names on the DaemonCore connect
	// socket. The child does not swap the machine binary; it forwards the
	// command to Computer Host through the injected Host callback.
	EventComputerUpgrade         = "computer:upgrade"
	EventComputerRestart         = "computer:restart"
	EventComputerUpgradeProgress = "computer:upgrade:progress"
	EventComputerUpgradeDone     = "computer:upgrade:done"
	EventComputerRestartDone     = "computer:restart:done"
	EventComputerWorkDigest      = "computer:work-digest"
	EventComputerWorkDigestDone  = "computer:work-digest:done"
	EventComputerWorkJournal     = "computer:work-journal"
	EventComputerWorkJournalDone = "computer:work-journal:done"
	EventAgentStartAck           = "agent:start:ack"
	EventAgentActivity           = "agent:activity"
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
	maxWorkspaceDaemonIdentityLength = 200
	maxActivityDetailKindLength      = 120
	maxActivityEntryKindLength       = 120
	maxActivityEntryBytes            = 64 << 10
	maxActivityEntriesPerFrame       = 64
)

// WorkspaceReadyPayload establishes one daemon-instance/Workspace
// connection. It says that the local Manager has initialized and reconciled;
// it deliberately says nothing about any individual Agent being ready.
type WorkspaceReadyPayload struct {
	WorkspaceID      string `json:"workspaceId"`
	DaemonInstanceID string `json:"daemonInstanceId"`
	DeviceName       string `json:"deviceName,omitempty"`
	OS               string `json:"os,omitempty"`
	CLIVersion       string `json:"cliVersion,omitempty"`
	// MachineID is the OS-level persistent machine fingerprint (e.g.
	// /etc/machine-id on Linux, IOPlatformUUID on macOS, MachineGuid on
	// Windows). It is an attribute of the Computer, not of any single Workspace
	// Runner, and is used as the authoritative same-machine proof for identity
	// reclaim and agent convergence. Empty when the daemon could not derive one.
	MachineID          string   `json:"machineId,omitempty"`
	ActiveCapabilities []string `json:"activeCapabilities,omitempty"`
	RunningAgents      []string `json:"runningAgents,omitempty"`
}

// WorkspacePingPayload and WorkspacePongPayload are connection
// liveness only. They are not runtime heartbeats and must not project Agent
// Activity.
type WorkspacePingPayload struct {
	PingID string `json:"pingId"`
}

// ComputerUpgradePayload is the Raft 1.0.16 connect-socket command for one
// machine-owned upgrade. RequestID is the operation the Host must execute.
type ComputerUpgradePayload struct {
	RequestID     string `json:"requestId"`
	OperationID   string `json:"operationId,omitempty"`
	TargetVersion string `json:"targetVersion,omitempty"`
}

func (p ComputerUpgradePayload) Operation() string {
	if id := strings.TrimSpace(p.OperationID); id != "" {
		return id
	}
	return strings.TrimSpace(p.RequestID)
}

func (p *ComputerUpgradePayload) Canonicalize() {
	if p == nil || strings.TrimSpace(p.RequestID) != "" {
		return
	}
	p.RequestID = strings.TrimSpace(p.OperationID)
}

func (p ComputerUpgradePayload) Validate() error {
	if p.Operation() == "" {
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

// ComputerWorkDigestPayload asks the Computer Host for one windowed Work
// Digest. It is a new control command; it must not reuse upgrade payloads.
type ComputerWorkDigestPayload struct {
	RequestID string    `json:"requestId"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
}

func (p ComputerWorkDigestPayload) Validate() error {
	if err := validateRequiredIDs(p.RequestID); err != nil {
		return fmt.Errorf("Computer work digest request identity is required")
	}
	if !p.End.After(p.Start) {
		return fmt.Errorf("Computer work digest window end must be after start")
	}
	return nil
}

func (p ComputerWorkDigestPayload) Window() WorkDigestWindow {
	return WorkDigestWindow{Start: p.Start, End: p.End}
}

// ComputerWorkDigestDonePayload is the Host harvest result on the same
// DaemonConnection that received computer:work-digest.
type ComputerWorkDigestDonePayload struct {
	RequestID string      `json:"requestId"`
	OK        bool        `json:"ok"`
	Digest    *WorkDigest `json:"digest,omitempty"`
	Error     string      `json:"error,omitempty"`
}

func (p ComputerWorkDigestDonePayload) Validate() error {
	return validateRequiredIDs(p.RequestID)
}

// ComputerWorkJournalPayload sets the Computer-local Machine Work Journal
// switch. Local state is authoritative; the server only projects the bit.
type ComputerWorkJournalPayload struct {
	RequestID string `json:"requestId"`
	Enabled   bool   `json:"enabled"`
}

func (p ComputerWorkJournalPayload) Validate() error {
	if err := validateRequiredIDs(p.RequestID); err != nil {
		return fmt.Errorf("Computer work journal request identity is required")
	}
	return nil
}

type ComputerWorkJournalDonePayload struct {
	RequestID string `json:"requestId"`
	OK        bool   `json:"ok"`
	Enabled   bool   `json:"enabled"`
	Error     string `json:"error,omitempty"`
}

func (p ComputerWorkJournalDonePayload) Validate() error {
	return validateRequiredIDs(p.RequestID)
}

type WorkspacePongPayload struct {
	PingID string `json:"pingId"`
}

// AgentStartConfig mirrors Raft's agent:start config boundary.
// A non-empty SessionID resumes that provider session; an omitted SessionID
// starts fresh.
type AgentStartConfig struct {
	SessionID string `json:"sessionId,omitempty"`
}

// AgentStartPayload is the server command accepted by a local Agent Process Manager.
type AgentStartPayload struct {
	AgentID   string           `json:"agentId"`
	RuntimeID string           `json:"runtimeId"`
	Config    AgentStartConfig `json:"config"`
}

// AgentStartAckPayload is an idempotent acceptance receipt. QueueState never
// claims spawn, runtime readiness, or initial activation delivery.
type AgentStartAckPayload struct {
	AgentID    string `json:"agentId"`
	QueueState string `json:"queueState"`
	QueueDepth int    `json:"queueDepth"`
	QueueAgeMS int64  `json:"queueAgeMs"`
}

type AgentStopPayload struct {
	AgentID string `json:"agentId"`
}

// AgentWorkspaceResetPayload mirrors Raft's
// agent:reset-workspace command. OperationID is Multica's correlation fence;
// Workspace identity remains owned by the authenticated Runner connection.
type AgentWorkspaceResetPayload struct {
	OperationID string `json:"operationId"`
	AgentID     string `json:"agentId"`
}

const (
	AgentResetWorkspaceSucceeded = "succeeded"
	AgentResetWorkspaceFailed    = "failed"
)

// AgentWorkspaceResetResultPayload is Multica's terminal
// receipt extension for Raft's fire-and-forget reset command. The server must
// observe success before it is allowed to issue the replacement agent:start.
type AgentWorkspaceResetResultPayload struct {
	OperationID string `json:"operationId"`
	AgentID     string `json:"agentId"`
	Status      string `json:"status"`
	ReasonCode  string `json:"reasonCode,omitempty"`
}

// AgentStatusPayload is lifecycle management state, not a user Activity
// label. active is reported only after a live provider process exists.
type AgentStatusPayload struct {
	AgentID string `json:"agentId"`
	Status  string `json:"status"`
}

// AgentSessionPayload reports a provider session independently from process,
// launch, turn, and runtime-state generation identities.
type AgentSessionPayload struct {
	AgentID           string `json:"agentId"`
	ProviderSessionID string `json:"providerSessionId,omitempty"`
	TurnID            string `json:"turnId,omitempty"`
	RuntimeGeneration int64  `json:"runtimeGeneration"`
}

// AgentActivitySnapshot is the daemon-owned current Activity presentation.
type AgentActivitySnapshot struct {
	AgentID          string    `json:"agentId"`
	DaemonInstanceID string    `json:"daemonInstanceId"`
	ObservedAt       time.Time `json:"observedAt"`
	ActivityKind     string    `json:"activityKind"`
	DetailKind       string    `json:"detailKind"`
}

// AgentActivityEntry is the daemon-local fact envelope used while producing
// display-ready presentation. It never crosses the WorkspaceDaemon wire.
type AgentActivityEntry struct {
	Kind string          `json:"-"`
	Body json.RawMessage `json:"-"`
}

// AgentActivityStatusBody matches Raft 1.0.17's non-tool timeline entry.
type AgentActivityStatusBody struct {
	Activity   string `json:"activity"`
	Detail     string `json:"detail"`
	DetailKind string `json:"detailKind"`
}

// AgentActivityToolStartBody matches Raft 1.0.17's tool-call timeline entry.
// toolInput is already the bounded display summary, never the raw provider
// input.
type AgentActivityToolStartBody struct {
	ToolName  string `json:"toolName"`
	ToolInput string `json:"toolInput"`
}

// AgentActivitySystemBody is a bounded, user-visible runtime diagnostic. It
// is a timeline fact and does not replace the current lifecycle Snapshot.
type AgentActivitySystemBody struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

type AgentActivitySummary struct {
	ActivityKind string `json:"activityKind"`
	DetailKind   string `json:"detailKind"`
	Label        string `json:"label"`
}

type AgentActivityTimelineRow struct {
	ActivityKind string `json:"activityKind"`
	DetailKind   string `json:"detailKind"`
	Title        string `json:"title"`
	Subtext      string `json:"subtext,omitempty"`
	BodyKind     string `json:"bodyKind"`
	Body         string `json:"body,omitempty"`
}

type AgentActivityPayload struct {
	Snapshot AgentActivitySnapshot      `json:"snapshot"`
	Summary  AgentActivitySummary       `json:"summary"`
	Timeline []AgentActivityTimelineRow `json:"timeline,omitempty"`
	Timing   *AgentActivityTiming       `json:"timing,omitempty"`
}

// AgentActivityTiming is optional, provider-neutral diagnostic evidence. It
// never drives lifecycle projection; the browser adds receive/cache marks.
type AgentActivityTiming struct {
	ColdStartAtMS      int64 `json:"coldStartAtMs,omitempty"`
	AcceptedAtMS       int64 `json:"acceptedAtMs,omitempty"`
	FirstACPUpdateAtMS int64 `json:"firstAcpUpdateAtMs,omitempty"`
	DaemonSentAtMS     int64 `json:"daemonSentAtMs,omitempty"`
}

	Snapshot AgentActivitySnapshot      `json:"snapshot"`
	Summary  AgentActivitySummary       `json:"summary"`
	Timeline []AgentActivityTimelineRow `json:"timeline,omitempty"`
}

func (p WorkspaceReadyPayload) Validate() error {
:server/pkg/protocol/workspace_daemon_protocol.go
	if err := validateRequiredIDs(p.WorkspaceID, p.DaemonInstanceID); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(p.ActiveCapabilities))
	for _, capability := range p.ActiveCapabilities {
		if err := validateRequiredIDs(capability); err != nil {
			return fmt.Errorf("invalid WorkspaceDaemon capability")
		}
		if _, duplicate := seen[capability]; duplicate {
			return fmt.Errorf("duplicate WorkspaceDaemon capability %q", capability)
		}
		seen[capability] = struct{}{}
	}
	if _, supported := seen[DaemonCapabilityWorkspaceDaemonAgentProcess]; !supported {
		return fmt.Errorf("WorkspaceDaemon Agent process capability is required")
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

func (p WorkspacePingPayload) Validate() error { return validateRequiredIDs(p.PingID) }
func (p WorkspacePongPayload) Validate() error { return validateRequiredIDs(p.PingID) }

func (p AgentStartPayload) Validate() error {
	return validateRequiredIDs(p.AgentID, p.RuntimeID)
}

func (p AgentStartAckPayload) Validate() error {
	if err := validateRequiredIDs(p.AgentID); err != nil {
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

func (p AgentStopPayload) Validate() error {
	return validateRequiredIDs(p.AgentID)
}

func (p AgentWorkspaceResetPayload) Validate() error {
	return validateRequiredIDs(p.OperationID, p.AgentID)
}

func (p AgentWorkspaceResetResultPayload) Validate() error {
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
	if err := validateRequiredIDs(p.AgentID); err != nil {
		return err
	}
	if !isOneOf(p.Status, AgentStatusActive, AgentStatusInactive) {
		return fmt.Errorf("invalid agent status %q", p.Status)
	}
	return nil
}

func (p AgentSessionPayload) Validate() error {
	if err := validateRequiredIDs(p.AgentID); err != nil {
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
	if strings.TrimSpace(p.Summary.ActivityKind) == "" || strings.TrimSpace(p.Summary.DetailKind) == "" || strings.TrimSpace(p.Summary.Label) == "" {
		return fmt.Errorf("activity summary facts and label are required")
	}
	if len(p.Summary.Label) > maxActivityEntryBytes {
		return fmt.Errorf("activity summary exceeds %d bytes", maxActivityEntryBytes)
	}
	if len(p.Timeline) > maxActivityEntriesPerFrame {
		return fmt.Errorf("too many activity timeline rows: %d", len(p.Timeline))
	}
	for _, row := range p.Timeline {
		if strings.TrimSpace(row.ActivityKind) == "" || strings.TrimSpace(row.DetailKind) == "" || strings.TrimSpace(row.Title) == "" || strings.TrimSpace(row.BodyKind) == "" {
			return fmt.Errorf("activity timeline presentation is required")
		}
		if len(row.Title)+len(row.Subtext)+len(row.Body) > maxActivityEntryBytes {
			return fmt.Errorf("activity timeline row exceeds %d bytes", maxActivityEntryBytes)
		}
	}
	return nil
}

func (p AgentActivitySnapshot) Validate() error {
	if err := validateRequiredIDs(p.AgentID, p.DaemonInstanceID); err != nil {
		return err
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
	return validateOptionalIDs(p.DetailKind)
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

func validateRequiredIDs(values ...string) error {
	for _, value := range values {
		if strings.TrimSpace(value) == "" || len(value) > maxWorkspaceDaemonIdentityLength {
			return fmt.Errorf("invalid required protocol identity")
		}
	}
	return nil
}

func validateOptionalIDs(values ...string) error {
	for _, value := range values {
		if len(value) > maxWorkspaceDaemonIdentityLength {
			return fmt.Errorf("protocol identity exceeds %d bytes", maxWorkspaceDaemonIdentityLength)
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
