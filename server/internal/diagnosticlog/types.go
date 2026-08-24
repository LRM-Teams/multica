// Package diagnosticlog owns Multica's machine-local diagnostic streams.
// It validates ownership, builds the versioned JSONL envelope, redacts
// untrusted evidence, bounds storage, and exposes sink health. Event producers
// never open files or construct paths themselves.
package diagnosticlog

import "time"

const (
	SchemaVersion  = 1
	MaxRecordBytes = 16 << 10
	MaxStderrBytes = 2 << 10
)

type Scope string

const (
	ScopeService Scope = "service"
	ScopeRunner  Scope = "runner"
)

type Level string

const (
	LevelDebug Level = "debug"
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

type Environment string

const (
	EnvironmentProduction Environment = "production"
	EnvironmentTest       Environment = "test"
)

type EventName string

const (
	EventComputerStateChanged        EventName = "computer_state_changed"
	EventEnvironmentStateChanged     EventName = "environment_state_changed"
	EventSessionStateChanged         EventName = "session_state_changed"
	EventWorkspaceDaemonStateChanged EventName = "workspace_runner_state_changed"
	EventUpgradeStateChanged         EventName = "upgrade_state_changed"
	EventGenerationFenced            EventName = "generation_fenced"
	EventRunnerLogSinkDegraded       EventName = "runner_log_sink_degraded"
	EventRunnerLogSinkRecovered      EventName = "runner_log_sink_recovered"
	EventDiagnosticStorageEvicted    EventName = "diagnostic_storage_evicted"

	EventRunnerStateChanged           EventName = "runner_state_changed"
	EventServerConnectionStateChanged EventName = "server_connection_state_changed"
	EventRuntimeDetected              EventName = "runtime_detected"
	EventAgentLifecycleRequested      EventName = "agent_lifecycle_requested"
	EventAgentProcessStateChanged     EventName = "agent_process_state_changed"
	EventDeliveryStateChanged         EventName = "delivery_state_changed"
	EventChatTurnCheckpoint           EventName = "chat_turn_checkpoint"
	EventTaskStateChanged             EventName = "task_state_changed"
	EventToolStateChanged             EventName = "tool_state_changed"
	EventProviderFailure              EventName = "provider_failure"
)

// Identity is the closed set of cross-subsystem identities a diagnostic event
// may carry. The stream owner supplies environment, Workspace, and generation
// identity; callers cannot override those routing fields.
type Identity struct {
	EventID           string
	AgentID           string
	RuntimeID         string
	LaunchID          string
	StartDispatchID   string
	ProcessInstanceID string
	InboxEventID      string
	TaskID            string
	SessionID         string
	MessageID         string
	DeliveryID        string
	RequestID         string
	TraceID           string
	ChannelID         string
	ChatSessionID     string
	ConversationID    string
	SourceMessageID   string
	ExecutionID       string
}

// Fields is the closed set of bounded state and outcome metadata accepted by
// the v1 diagnostic contract. Free-form maps are intentionally unsupported.
type Fields struct {
	From          string
	To            string
	Trigger       string
	ReasonCode    string
	Outcome       string
	Status        string
	Provider      string
	Model         string
	Phase         string
	FailureReason string
	ResponseMode  string
	ServiceOrigin string

	DurationMS        int64
	SeqFrom           int64
	SeqTo             int64
	AckedSeq          int64
	FoldedCount       int64
	AttemptCount      int64
	SuppressedCount   int64
	DroppedCount      int64
	OutageDurationMS  int64
	RuntimeEpoch      int64
	ProcessPID        int64
	ExitCode          int64
	Signal            string
	TerminationReason string
	ForceKilled       bool
}

// Evidence contains the only untrusted text accepted by the diagnostic API.
// It is centrally repaired, redacted, and bounded before encoding.
type Evidence struct {
	Detail     string
	StderrTail []byte
}

type Event struct {
	Name      EventName
	Level     Level
	Component string
	Identity  Identity
	Fields    Fields
	Evidence  Evidence
}

type ServiceOptions struct {
	ComputerID        string
	ServiceGeneration string
}

type RunnerOptions struct {
	Environment      Environment
	WorkspaceID      string
	DaemonInstanceID string
	// StartIdentity is retained as a compatibility alias for older callers.
	// New code should set DaemonInstanceID.
	StartIdentity     string
	ComputerID        string
	ServiceGeneration string
}

type Config struct {
	// Root is the diagnostic log root. An empty value uses
	// ~/.multica/computer/logs.
	Root   string
	Now    func() time.Time
	Limits Limits
}

type Limits struct {
	SegmentBytes int64
	SegmentAge   time.Duration
	Retention    time.Duration
	StreamBytes  int64
	GlobalBytes  int64
}

func DefaultLimits() Limits {
	return Limits{
		SegmentBytes: 16 << 20,
		SegmentAge:   24 * time.Hour,
		Retention:    30 * 24 * time.Hour,
		StreamBytes:  128 << 20,
		GlobalBytes:  1 << 30,
	}
}

func (l Limits) normalized() Limits {
	defaults := DefaultLimits()
	if l.SegmentBytes <= 0 || l.SegmentBytes > defaults.SegmentBytes {
		l.SegmentBytes = defaults.SegmentBytes
	}
	if l.SegmentAge <= 0 || l.SegmentAge > defaults.SegmentAge {
		l.SegmentAge = defaults.SegmentAge
	}
	if l.Retention <= 0 || l.Retention > defaults.Retention {
		l.Retention = defaults.Retention
	}
	if l.StreamBytes <= 0 || l.StreamBytes > defaults.StreamBytes {
		l.StreamBytes = defaults.StreamBytes
	}
	if l.GlobalBytes <= 0 || l.GlobalBytes > defaults.GlobalBytes {
		l.GlobalBytes = defaults.GlobalBytes
	}
	return l
}

type SinkState string

const (
	SinkHealthy  SinkState = "healthy"
	SinkDegraded SinkState = "degraded"
)

// Health is a safe operational projection. It deliberately stores an error
// class rather than an unrestricted error string.
type Health struct {
	Scope                 Scope
	Environment           Environment
	WorkspaceID           string
	Path                  string
	State                 SinkState
	Bytes                 int64
	OldestRetainedAt      time.Time
	NewestRetainedAt      time.Time
	DroppedRecords        uint64
	SuppressedRecords     uint64
	LastErrorClass        string
	LastErrorAt           time.Time
	LastSuccessfulWriteAt time.Time
}

type wireRecord struct {
	SchemaVersion int       `json:"schema_version"`
	At            string    `json:"at"`
	Level         Level     `json:"level"`
	Scope         Scope     `json:"scope"`
	Event         EventName `json:"event"`
	Component     string    `json:"component"`

	ComputerID        string      `json:"computerId,omitempty"`
	DaemonID          string      `json:"daemon_id,omitempty"`
	ServiceGeneration string      `json:"serviceGeneration,omitempty"`
	Environment       Environment `json:"environment,omitempty"`
	WorkspaceID       string      `json:"workspaceId,omitempty"`
	DaemonInstanceID  string      `json:"daemonInstanceId,omitempty"`
	StreamSeq         uint64      `json:"streamSeq"`

	EventID           string `json:"event_id,omitempty"`
	AgentID           string `json:"agent_id,omitempty"`
	RuntimeID         string `json:"runtime_id,omitempty"`
	LaunchID          string `json:"launch_id,omitempty"`
	StartDispatchID   string `json:"start_dispatch_id,omitempty"`
	ProcessInstanceID string `json:"process_instance_id,omitempty"`
	InboxEventID      string `json:"inbox_event_id,omitempty"`
	TaskID            string `json:"task_id,omitempty"`
	SessionID         string `json:"session_id,omitempty"`
	MessageID         string `json:"message_id,omitempty"`
	DeliveryID        string `json:"delivery_id,omitempty"`
	RequestID         string `json:"request_id,omitempty"`
	TraceID           string `json:"trace_id,omitempty"`
	ChannelID         string `json:"channel_id,omitempty"`
	ChatSessionID     string `json:"chat_session_id,omitempty"`
	ConversationID    string `json:"conversation_id,omitempty"`
	SourceMessageID   string `json:"source_message_id,omitempty"`
	ExecutionID       string `json:"execution_id,omitempty"`

	From          string `json:"from,omitempty"`
	To            string `json:"to,omitempty"`
	Trigger       string `json:"trigger,omitempty"`
	ReasonCode    string `json:"reason_code,omitempty"`
	Outcome       string `json:"outcome,omitempty"`
	Status        string `json:"status,omitempty"`
	Provider      string `json:"provider,omitempty"`
	Model         string `json:"model,omitempty"`
	Phase         string `json:"phase,omitempty"`
	FailureReason string `json:"failure_reason,omitempty"`
	ResponseMode  string `json:"response_mode,omitempty"`
	ServiceOrigin string `json:"service_origin,omitempty"`

	DurationMS        int64  `json:"duration_ms,omitempty"`
	SeqFrom           int64  `json:"seq_from,omitempty"`
	SeqTo             int64  `json:"seq_to,omitempty"`
	AckedSeq          int64  `json:"acked_seq,omitempty"`
	FoldedCount       int64  `json:"folded_count,omitempty"`
	AttemptCount      int64  `json:"attempt_count,omitempty"`
	SuppressedCount   int64  `json:"suppressed_count,omitempty"`
	DroppedCount      int64  `json:"dropped_count,omitempty"`
	OutageDurationMS  int64  `json:"outage_duration_ms,omitempty"`
	RuntimeEpoch      int64  `json:"runtime_epoch,omitempty"`
	ProcessPID        int64  `json:"pid,omitempty"`
	ExitCode          int64  `json:"exit_code,omitempty"`
	Signal            string `json:"signal,omitempty"`
	TerminationReason string `json:"termination_reason,omitempty"`
	ForceKilled       bool   `json:"force_killed,omitempty"`

	Diagnostic      string   `json:"diagnostic,omitempty"`
	StderrTail      string   `json:"stderr_tail,omitempty"`
	RedactionFailed bool     `json:"redaction_failed,omitempty"`
	TruncatedFields []string `json:"truncated_fields,omitempty"`
}
