package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	MessagePartTypeText        = "text"
	MessagePartTypeSticker     = "sticker"
	MessagePartTypeAttachment  = "attachment"
	MessagePartTypeReference   = "reference"
	MessagePartTypeSystemEvent = "system_event"
	MessagePartTypeVoice       = "voice"
	MessagePartTypeChoice      = "choice"
	MessagePartTypeChoiceReply = "choice_reply"
	// MessagePartTypeNoteBrief is a collapsible note snapshot embedded in a
	// Messages timeline (e.g. Note Worker 「按这篇做」). RefID is the note page
	// id, Label is the title, Text is the note body at dispatch time.
	MessagePartTypeNoteBrief = "note_brief"
	// MessagePartTypeNoteWrite is a human-confirm product-note proposal on an
	// agent Message. The Server constructs it from `message send --note-write`.
	// RefID is optional: empty means create a new page; set it to target an
	// existing note_page. Label may carry a suggested title.
	MessagePartTypeNoteWrite = "note_write"
	// MessagePartTypePeriodBriefInsert is the two-button insert card on a
	// Notes-bubble Period Brief result (append below the issuing page, or
	// create a child page). RefID is the run id; SelectedOptionID is set
	// after the human picks append or child.
	MessagePartTypePeriodBriefInsert = "period_brief_insert"
	// MessagePartTypeConfirmation is the structured acknowledgement part (LRM-1523
	// L1). A pure confirmation carries no new information, no @-directive and no
	// action, and must not wake any agent.
	MessagePartTypeConfirmation = "confirmation"
)

// ChannelMessageKind classifies a channel_message row on top of its author and
// content (LRM-1523 L1 + LRM-1529). confirmation / status / system_reminder
// carry no-wake (observe-only) semantics; everything else is ordinary content
// that may wake.
const (
	ChannelMessageKindContent        = "content"
	ChannelMessageKindConfirmation   = "confirmation"
	ChannelMessageKindStatus         = "status"
	ChannelMessageKindHandoff        = "handoff"
	ChannelMessageKindDelegation     = "delegation"
	ChannelMessageKindReview         = "review"
	ChannelMessageKindDeliverable    = "deliverable"
	ChannelMessageKindSystemReminder = "system_reminder"
)

// ChannelMessageKindSource records how kind was derived (LRM-1529).
// Priority: structured → system → lexicon → default.
const (
	ChannelMessageKindSourceStructured = "structured"
	ChannelMessageKindSourceSystem     = "system"
	ChannelMessageKindSourceLexicon    = "lexicon"
	ChannelMessageKindSourceDefault    = "default"
)

// AgentOutputEnvelope is the machine-readable agent output contract (LRM-1529).
// When present on a send, Kind wins over the legacy confirmation lexicon.
type AgentOutputEnvelope struct {
	Kind                string `json:"kind"`
	Intent              string `json:"intent,omitempty"`
	RequiresPublicReply *bool  `json:"requires_public_reply,omitempty"`
	AdvancesWork        *bool  `json:"advances_work,omitempty"`
	Summary             string `json:"summary,omitempty"`
}

// NormalizeChannelMessageKind returns a canonical kind or "" when unknown.
func NormalizeChannelMessageKind(kind string) string {
	switch strings.TrimSpace(strings.ToLower(kind)) {
	case ChannelMessageKindContent:
		return ChannelMessageKindContent
	case ChannelMessageKindConfirmation:
		return ChannelMessageKindConfirmation
	case ChannelMessageKindStatus:
		return ChannelMessageKindStatus
	case ChannelMessageKindHandoff:
		return ChannelMessageKindHandoff
	case ChannelMessageKindDelegation:
		return ChannelMessageKindDelegation
	case ChannelMessageKindReview:
		return ChannelMessageKindReview
	case ChannelMessageKindDeliverable:
		return ChannelMessageKindDeliverable
	case ChannelMessageKindSystemReminder:
		return ChannelMessageKindSystemReminder
	default:
		return ""
	}
}

// ChannelMessageKindIsObserveOnly reports whether kind must not wake agents.
func ChannelMessageKindIsObserveOnly(kind string) bool {
	switch NormalizeChannelMessageKind(kind) {
	case ChannelMessageKindConfirmation, ChannelMessageKindStatus, ChannelMessageKindSystemReminder:
		return true
	default:
		return false
	}
}

const (
	ChoiceLayoutBinary = "binary"
	ChoiceLayoutList   = "list"
)

// ChoiceOption is one selectable item inside a choice MessagePart.
type ChoiceOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

const VoiceTranscriptMaxRunes = 4096

const (
	VoiceTranscriptionPending   = "pending"
	VoiceTranscriptionCompleted = "completed"
	VoiceTranscriptionFailed    = "failed"
	VoiceSynthesisPending       = "pending"
	VoiceSynthesisCompleted     = "completed"
	VoiceSynthesisFailed        = "failed"
)

type MessagePart struct {
	Type       string `json:"type"`
	Text       string `json:"text,omitempty"`
	RefType    string `json:"ref_type,omitempty"`
	RefSubType string `json:"ref_subtype,omitempty"`
	RefID      string `json:"ref_id,omitempty"`
	Label      string `json:"label,omitempty"`
	// ContentStartUTF16 and ContentEndUTF16 anchor a reference to the exact
	// UTF-16 code-unit range in the message's canonical content. They are
	// pointers so zero is representable and a missing pair is unambiguous.
	ContentStartUTF16 *int            `json:"content_start_utf16,omitempty"`
	ContentEndUTF16   *int            `json:"content_end_utf16,omitempty"`
	Event             string          `json:"event,omitempty"`
	EventParams       json.RawMessage `json:"event_params,omitempty"`
	Params            json.RawMessage `json:"params,omitempty"`
	PackID            string          `json:"pack_id,omitempty"`
	StickerID         string          `json:"sticker_id,omitempty"`
	Alt               string          `json:"alt,omitempty"`
	AttachmentID      string          `json:"attachment_id,omitempty"`
	Filename          string          `json:"filename,omitempty"`
	ContentType       string          `json:"content_type,omitempty"`
	SizeBytes         int64           `json:"size_bytes,omitempty"`
	DurationMS        int64           `json:"duration_ms,omitempty"`
	// TranscriptionStatus is exposed only for recorded human voice messages.
	// Agent TTS parts have no attachment and leave it empty.
	TranscriptionStatus string `json:"transcription_status,omitempty"`
	// SynthesisStatus is server-owned lifecycle state for Agent TTS output.
	// Recorded human voice messages leave it empty.
	SynthesisStatus string `json:"synthesis_status,omitempty"`

	// Choice card fields (type=choice). SelectedOptionID is set server-side
	// after the user picks an answer. SelectCount tracks how many picks:
	// 1 = first choice (one reselect still allowed), 2 = locked after reselect.
	ChoiceID         string         `json:"choice_id,omitempty"`
	Prompt           string         `json:"prompt,omitempty"`
	Layout           string         `json:"layout,omitempty"`
	Options          []ChoiceOption `json:"options,omitempty"`
	AllowDismiss     *bool          `json:"allow_dismiss,omitempty"`
	ExpiresAt        string         `json:"expires_at,omitempty"`
	SelectedOptionID string         `json:"selected_option_id,omitempty"`
	SelectCount      int            `json:"select_count,omitempty"`

	// Choice reply fields (type=choice_reply) — user-visible answer after click.
	OptionID string `json:"option_id,omitempty"`
}

// Message is the envelope for all WebSocket messages.
type Message struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// TaskDispatchPayload is sent from server to daemon when a task is assigned.
type TaskDispatchPayload struct {
	TaskID      string `json:"task_id"`
	IssueID     string `json:"issue_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// TaskAvailablePayload is sent from server to daemon as a wakeup hint. The
// daemon still claims work through the existing HTTP claim endpoint.
type TaskAvailablePayload struct {
	RuntimeID string `json:"runtime_id"`
	TaskID    string `json:"task_id,omitempty"`
}

// AgentSkillsListPayload asks a daemon to report the skills visible to one
// agent. Global skills come from the daemon's provider home; workspace skills
// come from the agent workspace.
type AgentSkillsListPayload struct {
	AgentID   string `json:"agentId"`
	Runtime   string `json:"runtime,omitempty"`
	RequestID string `json:"requestId,omitempty"`
}

type AgentSkillSummary struct {
	Name                   string `json:"name"`
	Description            string `json:"description"`
	Path                   string `json:"path"`
	Source                 string `json:"source"`
	Type                   string `json:"type,omitempty"`
	DisableModelInvocation bool   `json:"disableModelInvocation,omitempty"`
	IsSubSkill             bool   `json:"isSubSkill,omitempty"`
}

type AgentSkillsListResultPayload struct {
	AgentID   string              `json:"agentId"`
	RequestID string              `json:"requestId,omitempty"`
	Global    []AgentSkillSummary `json:"global"`
	Workspace []AgentSkillSummary `json:"workspace"`
}

// AgentMessageProjection is the canonical Message data the coordinator may
// hand to a runtime. Target is the internal Context Boundary key;
// ReplyTarget is the recipient-relative CLI target exposed to the runtime.
// The projection deliberately has no delivery or read-state fields.
type AgentMessageProjection struct {
	ID            string                         `json:"id"`
	ChannelID     string                         `json:"channel_id,omitempty"`
	Target        string                         `json:"target"`
	ReplyTarget   string                         `json:"reply_target,omitempty"`
	Seq           int64                          `json:"seq"`
	Content       string                         `json:"content"`
	Parts         []MessagePart                  `json:"parts,omitempty"`
	ChannelKind   string                         `json:"channel_kind,omitempty"`
	ProjectID     string                         `json:"project_id,omitempty"`
	InitiatorType string                         `json:"initiator_type,omitempty"`
	InitiatorID   string                         `json:"initiator_id,omitempty"`
	InitiatorName string                         `json:"initiator_name,omitempty"`
	Memories      []AgentMessageMemoryProjection `json:"memories,omitempty"`
	// Directed marks an explicit @mention of a managed Graph Memory Agent.
	// The daemon may deliver it through a provider-native in-flight steering
	// boundary; ordinary Messages remain queued through the coordinator.
	Directed bool `json:"directed,omitempty"`
	// GraphMemoryTools authorizes projection of the five managed Graph
	// operations into this turn. It is set only from server-side managed
	// membership and is recomputed during redelivery.
	GraphMemoryTools bool `json:"graph_memory_tools,omitempty"`
	// RuntimeContext is daemon-local, freshly rendered immediately before a
	// resident turn. It must never be persisted in coordinator state or sent
	// by the Server because it may contain scoped memory.
	RuntimeContext string `json:"-"`
	// Daemon-local lifecycle metadata is copied from the authenticated delivery
	// envelope and never serialized as part of canonical message content.
	RunID      string `json:"-"`
	RunAgentID string `json:"-"`
	DeliveryID string `json:"-"`
}

// AgentMessageMemoryProjection is a server-filtered memory item applicable to
// one canonical Message. In particular, user scope is selected against that
// Message's attested member identity before it reaches the daemon.
type AgentMessageMemoryProjection struct {
	Name        string `json:"name"`
	Content     string `json:"content"`
	Scope       string `json:"scope"`
	SubjectType string `json:"subject_type,omitempty"`
	SubjectID   string `json:"subject_id,omitempty"`
}

const (
	MixedRunActivityActiveTurn             = "active_turn"
	MixedRunActivityQueuedMessage          = "queued_message"
	MixedRunActivityInflightTool           = "inflight_tool"
	MixedRunActivityUnfinishedCaptureBatch = "unfinished_capture_batch"
)

// MixedRunActivityTransitionPayload reports one durable lifecycle edge. The
// server owns pending-delivery accounting from obligation rows; the daemon
// reports the other four dimensions with stable transition IDs so reconnect or
// retry cannot double-apply a counter delta.
type MixedRunActivityTransitionPayload struct {
	AgentID      string `json:"agent_id"`
	RuntimeID    string `json:"runtime_id"`
	RunID        string `json:"run_id"`
	RunAgentID   string `json:"run_agent_id"`
	TransitionID string `json:"transition_id"`
	Dimension    string `json:"dimension"`
	Delta        int    `json:"delta"`
}

func (p MixedRunActivityTransitionPayload) Validate() error {
	if strings.TrimSpace(p.AgentID) == "" || strings.TrimSpace(p.RuntimeID) == "" ||
		strings.TrimSpace(p.RunID) == "" || strings.TrimSpace(p.RunAgentID) == "" || strings.TrimSpace(p.TransitionID) == "" {
		return errors.New("mixed-run activity transition identity is incomplete")
	}
	if p.Delta != -1 && p.Delta != 1 {
		return errors.New("mixed-run activity transition delta must be -1 or 1")
	}
	switch p.Dimension {
	case MixedRunActivityActiveTurn, MixedRunActivityQueuedMessage, MixedRunActivityInflightTool, MixedRunActivityUnfinishedCaptureBatch:
		return nil
	default:
		return errors.New("invalid mixed-run activity transition dimension")
	}
}

// MixedRunActivityTransitionAckPayload confirms the server committed a
// transition (or recognized the same already-committed payload). The daemon
// may remove its durable outbox entry only after receiving this frame.
type MixedRunActivityTransitionAckPayload struct {
	RunID        string `json:"run_id"`
	TransitionID string `json:"transition_id"`
}

func (p MixedRunActivityTransitionAckPayload) Validate() error {
	if strings.TrimSpace(p.RunID) == "" || strings.TrimSpace(p.TransitionID) == "" {
		return errors.New("mixed-run activity transition acknowledgement identity is incomplete")
	}
	return nil
}

// AgentDeliverPayload is an at-least-once transfer attempt to a single local
// Agent coordinator. DeliveryID identifies the attempt lineage; Message.ID
// plus its target sequence identify the canonical Message being projected.
type AgentDeliverPayload struct {
	AgentID     string                 `json:"agentId"`
	Target      string                 `json:"target"`
	Seq         int64                  `json:"seq"`
	DeliveryID  string                 `json:"deliveryId"`
	Message     AgentMessageProjection `json:"message"`
	Traceparent string                 `json:"traceparent,omitempty"`
	RunID       string                 `json:"runId,omitempty"`
	RunAgentID  string                 `json:"runAgentId,omitempty"`
	// MemoryType / ExploreAgents / ExploreMaxRounds carry the workspace's
	// effective graph memory profile at delivery time (spec §10). The daemon
	// caches them per workspace for the resident-message memory path; empty
	// means "no workspace profile" (env defaults apply).
	MemoryType       string `json:"memory_type,omitempty"`
	ExploreAgents    int    `json:"explore_agents,omitempty"`
	ExploreMaxRounds int    `json:"explore_max_rounds,omitempty"`
}

// AgentDeliverAckPayload confirms only per-Agent provider acceptance, Pending
// retention, or deduplication. It is intentionally not a read or completion
// receipt; the outcome stays daemon-local diagnostic state.
type AgentDeliverAckPayload struct {
	AgentID     string `json:"agentId"`
	Seq         int64  `json:"seq"`
	DeliveryID  string `json:"deliveryId"`
	Traceparent string `json:"traceparent,omitempty"`
}

// SandboxJobAvailablePayload is sent from server to a shared sandbox node as a
// wakeup hint. The node still claims work through the HTTP claim endpoint.
type SandboxJobAvailablePayload struct {
	NodeID string `json:"node_id"`
	JobID  string `json:"job_id,omitempty"`
}

// ListWorkdirFilesRequestPayload is pushed server→daemon to ask a specific
// runtime's daemon to enumerate a project's local working directory. The
// daemon replies with ListWorkdirFilesResponsePayload carrying the same
// RequestID. RelPath is the workdir path relative to the daemon's workspace
// root (the daemon joins it onto its own root — the server never sends an
// absolute host path).
type ListWorkdirFilesRequestPayload struct {
	RequestID  string `json:"request_id"`
	RuntimeID  string `json:"runtime_id"`
	RelPath    string `json:"rel_path"`
	MaxEntries int    `json:"max_entries,omitempty"`
	MaxDepth   int    `json:"max_depth,omitempty"`
	// DirPath is a subdirectory of RelPath. Empty or "." lists RelPath itself.
	// Only used when OneLevel is true (agent Files tab).
	DirPath string `json:"dir_path,omitempty"`
	// OneLevel lists only immediate children of RelPath/DirPath using Raft
	// visibility rules. The recursive 2000/12 walk stays for machine scan.
	OneLevel bool `json:"one_level,omitempty"`
	// HideDotfiles skips files/directories whose basename starts with ".".
	// Project file browsing leaves this false; agent config browsing toggles it
	// from the UI's "show hidden files" eye button.
	HideDotfiles bool `json:"hide_dotfiles,omitempty"`
}

// WorkdirFileNode is one entry in a flat workdir listing. Path is relative to
// the workdir root using forward slashes; the frontend rebuilds the tree from
// the path segments. Size is bytes for files (0 for directories).
type WorkdirFileNode struct {
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size,omitempty"`
}

// ListWorkdirFilesResponsePayload is the daemon→server reply for a workdir
// listing. Missing is true when the workdir does not exist on that daemon (a
// normal case — the project may not have run there). Truncated is true when
// the entry/depth caps were hit.
type ListWorkdirFilesResponsePayload struct {
	RequestID string            `json:"request_id"`
	Nodes     []WorkdirFileNode `json:"nodes,omitempty"`
	// RootPath is the daemon-absolute path of RelPath (the agent root for
	// Files). Always the workdir root, not the currently listed DirPath.
	RootPath  string `json:"root_path,omitempty"`
	Missing   bool   `json:"missing,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Error     string `json:"error,omitempty"`
}

// ReadWorkdirFileRequestPayload is pushed server→daemon to read one file from a
// project workdir for preview. RelPath is the workdir root (relative to
// WorkspacesRoot); FilePath is the file relative to that root.
type ReadWorkdirFileRequestPayload struct {
	RequestID string `json:"request_id"`
	RuntimeID string `json:"runtime_id"`
	RelPath   string `json:"rel_path"`
	FilePath  string `json:"file_path"`
	MaxBytes  int    `json:"max_bytes,omitempty"`
}

// ReadWorkdirFileResponsePayload is the daemon→server reply for a file read.
// For text files Content is UTF-8 and Encoding is empty/"utf8". For media files
// (image/video/audio/pdf, by extension) Content is base64 and Encoding is
// "base64" with MimeType set, so the client can render it directly. Binary is
// set (no Content) for non-text files that aren't a known media type; TooLarge
// when over the byte cap; Truncated when text was cut to the cap.
type ReadWorkdirFileResponsePayload struct {
	RequestID   string `json:"request_id"`
	Content     string `json:"content,omitempty"`
	Encoding    string `json:"encoding,omitempty"`
	MimeType    string `json:"mime_type,omitempty"`
	ContentHash string `json:"content_hash,omitempty"`
	Truncated   bool   `json:"truncated,omitempty"`
	TooLarge    bool   `json:"too_large,omitempty"`
	Binary      bool   `json:"binary,omitempty"`
	Missing     bool   `json:"missing,omitempty"`
	Error       string `json:"error,omitempty"`
}

// WriteWorkdirFileRequestPayload is pushed server→daemon to replace one UTF-8
// text file inside a confined workdir root. ExpectedContentHash, when present,
// must match the current file hash or the daemon returns Conflict without
// modifying the file. Create, when true, allows the daemon to create a missing
// file (and parent directories) instead of returning Missing; agent-file
// callers leave it false so the RPC stays edit-only.
type WriteWorkdirFileRequestPayload struct {
	RequestID           string `json:"request_id"`
	RuntimeID           string `json:"runtime_id"`
	RelPath             string `json:"rel_path"`
	FilePath            string `json:"file_path"`
	Content             string `json:"content"`
	ExpectedContentHash string `json:"expected_content_hash,omitempty"`
	MaxBytes            int    `json:"max_bytes,omitempty"`
	Create              bool   `json:"create,omitempty"`
}

// WriteWorkdirFileResponsePayload is the daemon→server reply for a text write.
type WriteWorkdirFileResponsePayload struct {
	RequestID   string `json:"request_id"`
	ContentHash string `json:"content_hash,omitempty"`
	Conflict    bool   `json:"conflict,omitempty"`
	TooLarge    bool   `json:"too_large,omitempty"`
	Binary      bool   `json:"binary,omitempty"`
	Missing     bool   `json:"missing,omitempty"`
	Error       string `json:"error,omitempty"`
}

// DeleteWorkdirDirRequestPayload asks the daemon to remove one directory under
// WorkspacesRoot. RelPath is relative to the daemon workspaces root (never an
// absolute host path). The daemon rejects paths that escape the root.
type DeleteWorkdirDirRequestPayload struct {
	RequestID string `json:"request_id"`
	RuntimeID string `json:"runtime_id"`
	RelPath   string `json:"rel_path"`
}

// DeleteWorkdirDirResponsePayload is the daemon→server reply for a directory
// delete. Missing is true when the path does not exist (idempotent success for
// callers that only care that the dir is gone).
type DeleteWorkdirDirResponsePayload struct {
	RequestID string `json:"request_id"`
	Missing   bool   `json:"missing,omitempty"`
	Error     string `json:"error,omitempty"`
}

// SeedAgentContextRequestPayload asks the daemon that owns RuntimeID to create
// the target Multica agent root (if missing) and append Wendy-provided initial
// notes/memory into whitelisted markdown files under RelPath.
type SeedAgentContextRequestPayload struct {
	RequestID     string            `json:"request_id"`
	RuntimeID     string            `json:"runtime_id"`
	RelPath       string            `json:"rel_path"`
	InitialNotes  map[string]string `json:"initial_notes,omitempty"`
	InitialMemory map[string]string `json:"initial_memory,omitempty"`
	MaxBytes      int               `json:"max_bytes,omitempty"`
}

// PreparePiRunRequestPayload asks the daemon owning RuntimeID to create the
// resident Pi backend, bind the durable mixed-run identity, and start the
// native process without accepting any conversation input.
type PreparePiRunRequestPayload struct {
	RequestID  string `json:"request_id"`
	RuntimeID  string `json:"runtime_id"`
	AgentID    string `json:"agent_id"`
	RunID      string `json:"run_id"`
	RunAgentID string `json:"run_agent_id"`
}

type PreparePiRunResponsePayload struct {
	RequestID       string `json:"request_id"`
	SessionID       string `json:"session_id,omitempty"`
	CaptureBoundary string `json:"capture_boundary,omitempty"`
	Error           string `json:"error,omitempty"`
}

type RevokePiRunRequestPayload struct {
	RequestID  string `json:"request_id"`
	RuntimeID  string `json:"runtime_id"`
	AgentID    string `json:"agent_id"`
	RunID      string `json:"run_id"`
	RunAgentID string `json:"run_agent_id"`
}

type RevokePiRunResponsePayload struct {
	RequestID string `json:"request_id"`
	Error     string `json:"error,omitempty"`
}

// SeedAgentContextResponsePayload is the daemon reply for initial context seeding.
type SeedAgentContextResponsePayload struct {
	RequestID string   `json:"request_id"`
	Written   []string `json:"written,omitempty"`
	TooLarge  bool     `json:"too_large,omitempty"`
	Error     string   `json:"error,omitempty"`
}

// TaskProgressPayload is sent from daemon to server during task execution.
type TaskProgressPayload struct {
	TaskID  string `json:"task_id"`
	Summary string `json:"summary"`
	Step    int    `json:"step,omitempty"`
	Total   int    `json:"total,omitempty"`
}

// TaskCompletedPayload is sent from daemon to server when a task finishes.
type TaskCompletedPayload struct {
	TaskID                 string               `json:"task_id"`
	PRURL                  string               `json:"pr_url,omitempty"`
	Action                 string               `json:"action,omitempty"`
	Target                 string               `json:"target,omitempty"`
	Type                   string               `json:"type,omitempty"`
	Output                 string               `json:"output,omitempty"`
	Parts                  []MessagePart        `json:"parts,omitempty"`
	Reaction               *ChatReactionPayload `json:"reaction,omitempty"`
	OutputSuppressedReason string               `json:"output_suppressed_reason,omitempty"`
}

const (
	ChatOutputKindMessage  = "message"
	ChatOutputKindNoReply  = "no_reply"
	ChatOutputKindReaction = "reaction"
)

const (
	ChatOutputActionMessageSend  = "message_send"
	ChatOutputActionMessageReact = "message_react"
	ChatOutputActionSendReaction = ChatOutputActionMessageReact
	ChatOutputActionNoReply      = "no_reply"
)

const (
	ChannelOnboardingReason  = "channel_onboarding"
	ChannelRoleChangedReason = "channel_role_changed"
)

const (
	DaemonCapabilityChannelOutputActions     = "channel_output_actions"
	DaemonCapabilityAgentCLITransport        = "agent_cli_transport"
	DaemonCapabilityAgentCredentialTransport = "agent_credential_transport_v1"
	DaemonCapabilityMemoryCuration           = "memory_curation_v1"
	DaemonCapabilityMemoryCrossDeviceSync    = "memory_cross_device_sync_v2"
	DaemonCapabilityRestrictedExecution      = "restricted_execution_profiles_v1"
	DaemonCapabilityReminderVersionedCache   = "reminder_versioned_cache_v1"
	DaemonCapabilityReminderFireRequest      = "reminder:fire-request-v2"
	// DaemonCapabilityWorkspaceDaemonAgentReset gates Raft's discrete
	// agent:reset-workspace command plus Multica's terminal reset receipt.
	DaemonCapabilityWorkspaceDaemonAgentReset = "workspace_daemon_agent_reset_workspace_v1"
	// DaemonCapabilityMachineUpgrade gates the machine-scoped upgrade
	// operation protocol. Older daemons continue to receive no machine action
	// and therefore cannot accidentally claim or complete an operation.
	DaemonCapabilityMachineUpgrade = "machine_upgrade_v1"
	// DaemonCapabilityWorkspaceDaemonAgentProcess selects the Raft-shaped
	// agent:start / agent:stop process-control boundary.
	DaemonCapabilityWorkspaceDaemonAgentProcess = "workspace_daemon_agent_process_v1"
	// DaemonCapabilityMemoryExploreV2 advertises the agent-native Explore
	// v2 protocol generation. The capability is necessary but never
	// sufficient: the server additionally requires its memory_explore_v2
	// phase gate to be green before generation 2 is negotiated.
	DaemonCapabilityMemoryExploreV2 = "memory_explore_v2"
	// DaemonCapabilityWorkspaceDaemonControlPlane selects the current ready
	// WorkspaceDaemon as the sole carrier for heartbeat actions belonging to
	// that Workspace. Runtime-multiplexed WS and HTTP heartbeats remain legacy
	// adapters for older daemons and must not execute actions for this Runner.
	DaemonCapabilityWorkspaceDaemonControlPlane = "workspace_daemon_control_plane_v1"
)

// ReminderTimerJob is the complete server-owned timer projection cached by
// the owner daemon. Version is monotonic per Reminder definition.
type ReminderTimerJob struct {
	ReminderID   string `json:"reminder_id"`
	OwnerAgentID string `json:"owner_agent_id"`
	Version      int64  `json:"version"`
	FireAt       string `json:"fire_at"`
	Title        string `json:"title"`
}

type ReminderUpsertPayload struct {
	RuntimeID string           `json:"runtime_id"`
	AgentID   string           `json:"agent_id"`
	Reminder  ReminderTimerJob `json:"reminder"`
}

type ReminderCancelPayload struct {
	RuntimeID  string `json:"runtime_id"`
	AgentID    string `json:"agent_id"`
	ReminderID string `json:"reminder_id"`
	Version    int64  `json:"version"`
}

type ReminderSnapshotRequestPayload struct {
	RuntimeID string `json:"runtime_id"`
}

type ReminderSnapshotPayload struct {
	RuntimeID string             `json:"runtime_id"`
	Reminders []ReminderTimerJob `json:"reminders"`
}

type ReminderFireRequestPayload struct {
	AgentID       string `json:"agentId"`
	ReminderID    string `json:"reminderId"`
	Version       int64  `json:"version"`
	RequestID     string `json:"requestId"`
	FiredAtClient string `json:"firedAtClient"`
}

type ReminderFireRequestResultPayload struct {
	AgentID      string `json:"agentId"`
	ReminderID   string `json:"reminderId"`
	Version      int64  `json:"version"`
	RequestID    string `json:"requestId"`
	Outcome      string `json:"outcome"`
	Fired        bool   `json:"fired,omitempty"`
	Catchup      bool   `json:"catchup,omitempty"`
	RetryAfterMS int64  `json:"retryAfterMs,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

type ReminderFireReceiptAckPayload struct {
	AgentID    string `json:"agentId"`
	ReminderID string `json:"reminderId"`
	Version    int64  `json:"version"`
}

const (
	ChannelOutputSuppressedReasonDaemonOutdated             = "daemon_outdated"
	ChannelOutputSuppressedReasonLegacyProtocolOutput       = "legacy_protocol_output"
	ChannelOutputSuppressedReasonNoReplyRationale           = "no_reply_rationale"
	ChannelOutputSuppressedReasonToolTransportOutput        = "tool_transport_output"
	ChannelOutputSuppressedReasonInvalidOutput              = "invalid_output"
	ChannelOutputSuppressedReasonInvalidAction              = "invalid_action"
	ChannelOutputSuppressedReasonInvalidType                = "invalid_type"
	ChannelOutputSuppressedReasonEmptyMessage               = "empty_message"
	ChannelOutputSuppressedReasonInvalidReaction            = "invalid_reaction"
	ChannelOutputSuppressedReasonMessageMissingAction       = "message_missing_action"
	ChannelOutputSuppressedReasonInvalidTarget              = "invalid_target"
	ChannelOutputSuppressedReasonUnsentFinalOutput          = "unsent_final_output"
	ChannelOutputSuppressedReasonRestrictedExecutionProfile = "restricted_execution_profile"
)

type ChatReactionPayload struct {
	MessageID string `json:"message_id,omitempty"`
	Emoji     string `json:"emoji,omitempty"`
}

func NormalizeChatOutputAction(action string) (string, error) {
	normalized := strings.TrimSpace(strings.ToLower(action))
	switch normalized {
	case "":
		return "", nil
	case ChatOutputActionMessageSend, "send":
		return ChatOutputActionMessageSend, nil
	case ChatOutputActionMessageReact, "react", "react_message", "message_reaction", "send_reaction":
		return ChatOutputActionMessageReact, nil
	case ChatOutputActionNoReply, "stay_silent":
		return ChatOutputActionNoReply, nil
	default:
		return "", fmt.Errorf("invalid chat output action %q", normalized)
	}
}

func ChatOutputTypeForAction(action string) (string, error) {
	normalized, err := NormalizeChatOutputAction(action)
	if err != nil {
		return "", err
	}
	switch normalized {
	case "":
		return "", nil
	case ChatOutputActionMessageSend:
		return ChatOutputKindMessage, nil
	case ChatOutputActionMessageReact:
		return ChatOutputKindReaction, nil
	case ChatOutputActionNoReply:
		return ChatOutputKindNoReply, nil
	default:
		return "", fmt.Errorf("invalid chat output action %q", normalized)
	}
}

func NormalizeChatOutputType(outputType string, hasReplyBody, hasReactionPayload bool) (string, error) {
	normalized := strings.TrimSpace(strings.ToLower(outputType))
	switch normalized {
	case "":
		if hasReactionPayload {
			return ChatOutputKindReaction, nil
		}
		if hasReplyBody {
			return ChatOutputKindMessage, nil
		}
		return ChatOutputKindNoReply, nil
	case ChatOutputKindMessage, ChatOutputKindNoReply, ChatOutputKindReaction:
		return normalized, nil
	default:
		return "", ErrInvalidChatOutputType(normalized)
	}
}

func ErrInvalidChatOutputType(outputType string) error {
	return fmt.Errorf("invalid chat output type %q", outputType)
}

// TaskMessagePayload represents a single agent execution message (tool call, text, etc.)
type TaskMessagePayload struct {
	TaskID     string         `json:"task_id"`
	IssueID    string         `json:"issue_id,omitempty"`
	Seq        int            `json:"seq"`
	Type       string         `json:"type"`              // "text", "tool_use", "tool_result", "error"
	Tool       string         `json:"tool,omitempty"`    // tool name for tool_use/tool_result
	Content    string         `json:"content,omitempty"` // text content
	Input      map[string]any `json:"input,omitempty"`   // tool input (tool_use only)
	Output     string         `json:"output,omitempty"`  // tool output (tool_result only)
	Visibility string         `json:"visibility"`        // "user_facing" or "diagnostic_only"
	CreatedAt  string         `json:"created_at,omitempty"`
}

// GraphMemoryRecallRequest is the daemon's call to the server-authoritative
// recall endpoint (spec §1/§3/§14). Only trace_id, task_id, runtime_id and
// query are inputs; the workspace/daemon identity comes from the
// authenticated daemon capability. Every remaining field is a caller-side
// diagnostic hint and is never consulted for resolution (A14).
type GraphMemoryRecallRequest struct {
	TraceID   string `json:"trace_id"`
	TaskID    string `json:"task_id"`
	RuntimeID string `json:"runtime_id"`
	Query     string `json:"query"`

	GraphKind    string `json:"graph_kind,omitempty"`
	GraphOwnerID string `json:"graph_owner_id,omitempty"`
	GraphVersion int    `json:"graph_version,omitempty"`
	TrainingMode string `json:"training_mode,omitempty"`
	K            int    `json:"k,omitempty"`
}

// GraphMemoryRecallCitation identifies one graph node cited by a bounded
// server-side recall injection.
type GraphMemoryRecallCitation struct {
	NodeID    string `json:"node_id"`
	Level     int    `json:"level"`
	Epistemic string `json:"epistemic"`
}

// GraphMemoryRecallResponse is the bounded outcome returned by the daemon
// graph-memory recall endpoint.
type GraphMemoryRecallResponse struct {
	RecallID     string                      `json:"recall_id"`
	TraceID      string                      `json:"trace_id"`
	Status       string                      `json:"status"`
	Replayed     bool                        `json:"replayed"`
	K            int                         `json:"k"`
	GraphKind    string                      `json:"graph_kind"`
	GraphVersion int                         `json:"graph_version"`
	Found        bool                        `json:"found"`
	Summary      string                      `json:"summary"`
	Citations    []GraphMemoryRecallCitation `json:"citations"`
	Rounds       int                         `json:"rounds"`
	Injection    string                      `json:"injection"`
}

// DaemonRegisterPayload is sent from daemon to server on connection.
type DaemonRegisterPayload struct {
	DaemonID string        `json:"daemon_id"`
	AgentID  string        `json:"agent_id"`
	Runtimes []RuntimeInfo `json:"runtimes"`
}

// DaemonUpdateObservation is daemon-resolved update truth shared by register,
// HTTP heartbeat, and WebSocket heartbeat. SessionID changes for every daemon
// process. Revision starts at one and advances only when the semantic payload
// changes, so the server can ignore duplicate and out-of-order transports
// without writing on the ordinary heartbeat path.
type DaemonUpdateObservation struct {
	SessionID                  string  `json:"session_id"`
	Revision                   int64   `json:"revision"`
	ObservedAt                 string  `json:"observed_at"`
	AutoUpdateEffectiveEnabled bool    `json:"auto_update_effective_enabled"`
	ConfigSource               string  `json:"config_source"`
	IneligibleReason           string  `json:"ineligible_reason,omitempty"`
	CheckIntervalSeconds       int64   `json:"check_interval_seconds"`
	Phase                      string  `json:"phase"`
	AttemptSource              string  `json:"attempt_source,omitempty"`
	LastAttemptAt              string  `json:"last_attempt_at,omitempty"`
	LastOutcome                string  `json:"last_outcome"`
	TargetVersion              string  `json:"target_version,omitempty"`
	ErrorCode                  string  `json:"error_code,omitempty"`
	ErrorMessage               string  `json:"error_message,omitempty"`
	StagedVersion              string  `json:"staged_version,omitempty"`
	ActivationGeneration       *uint64 `json:"activation_generation,omitempty"`
}

// RuntimeInfo describes an available agent runtime on the daemon's machine.
type RuntimeInfo struct {
	Type    string `json:"type"`
	Version string `json:"version"`
	Status  string `json:"status"`
}

// ChatMessagePayload is broadcast when a new chat message is created.
type ChatMessagePayload struct {
	ChatSessionID string        `json:"chat_session_id"`
	MessageID     string        `json:"message_id"`
	Role          string        `json:"role"`
	Content       string        `json:"content"`
	Parts         []MessagePart `json:"parts,omitempty"`
	TaskID        string        `json:"task_id,omitempty"`
	CreatedAt     string        `json:"created_at"`
}

// ChatDonePayload is broadcast when an agent finishes responding to a chat
// message. Carries the freshly-persisted assistant ChatMessage so the client
// can write it into the messages cache inline — avoids a refetch round-trip
// during the live-timeline → AssistantMessage handoff that previously caused
// a visible flicker (#2123).
type ChatDonePayload struct {
	ChatSessionID          string               `json:"chat_session_id"`
	TaskID                 string               `json:"task_id"`
	Type                   string               `json:"type,omitempty"`
	Target                 string               `json:"target,omitempty"`
	MessageID              string               `json:"message_id,omitempty"`
	Content                string               `json:"content,omitempty"`
	Parts                  []MessagePart        `json:"parts,omitempty"`
	Reaction               *ChatReactionPayload `json:"reaction,omitempty"`
	OutputSuppressedReason string               `json:"output_suppressed_reason,omitempty"`
	ElapsedMs              int64                `json:"elapsed_ms,omitempty"`
	CreatedAt              string               `json:"created_at,omitempty"`
}

// ChatSessionReadPayload is broadcast when the creator marks a session as read.
// Fires to other devices so their unread counts stay in sync.
type ChatSessionReadPayload struct {
	ChatSessionID string `json:"chat_session_id"`
}

// ChatSessionDeletedPayload is broadcast when a chat session is hard-deleted
// so other tabs/devices drop it from their session lists and reset the active
// pointer if it referenced the deleted session.
type ChatSessionDeletedPayload struct {
	ChatSessionID string `json:"chat_session_id"`
}

// ChatSessionUpdatedPayload is broadcast when a user-editable field on a
// chat session changes (today: title via inline rename). Other tabs/devices
// patch the session row in their cached list so the dropdown stays in sync
// without a full refetch.
type ChatSessionUpdatedPayload struct {
	ChatSessionID string `json:"chat_session_id"`
	Title         string `json:"title"`
	UpdatedAt     string `json:"updated_at"`
}

// ChannelTypingPayload is a transient group-chat typing indicator. It is not
// persisted; clients expire it locally if a matching stop event is missed.
type ChannelTypingPayload struct {
	ChannelID   string `json:"channel_id"`
	ActorType   string `json:"actor_type"`
	ActorID     string `json:"actor_id,omitempty"`
	ActorName   string `json:"actor_name"`
	IsTyping    bool   `json:"is_typing"`
	ExpiresInMS int    `json:"expires_in_ms,omitempty"`
}

// DaemonHeartbeatRequestPayload is sent from daemon to server over WebSocket
// to update last_seen_at and pull pending actions for a single runtime.
// Mirrors the body of POST /api/daemon/heartbeat so both transports share
// identical semantics.
type DaemonHeartbeatRequestPayload struct {
	RuntimeID                 string                   `json:"runtime_id"`
	SupportsBatchImport       bool                     `json:"supports_batch_import,omitempty"`
	SupportsMemoryCuration    bool                     `json:"supports_memory_curation,omitempty"`
	ActiveMemoryCurationRunID string                   `json:"active_memory_curation_run_id,omitempty"`
	UpdateObservation         *DaemonUpdateObservation `json:"auto_update,omitempty"`
}

// DaemonHeartbeatAckPayload is the server's reply to DaemonHeartbeatRequestPayload.
// JSON shape mirrors the HTTP heartbeat response so daemon code can decode either.
//
// RuntimeGone is the WebSocket replacement for the HTTP 404 "runtime not found"
// response. When the server discovers the runtime row was deleted (UI delete,
// 7-day offline GC), it sends back an ack with Status=HeartbeatStatusRuntimeGone
// and RuntimeGone=true rather than tearing down the connection with an error.
// The daemon reads this signal, prunes the stale runtime from its local state
// and re-registers; without it the dead UUID would keep heartbeating until the
// daemon process restarts.
type DaemonHeartbeatAckPayload struct {
	RuntimeID               string                                  `json:"runtime_id"`
	Status                  string                                  `json:"status"`
	RuntimeGone             bool                                    `json:"runtime_gone,omitempty"`
	PendingUpdate           *DaemonHeartbeatPendingUpdate           `json:"pending_update,omitempty"`
	PendingModelList        *DaemonHeartbeatPendingModelList        `json:"pending_model_list,omitempty"`
	PendingLocalSkills      *DaemonHeartbeatPendingLocalSkills      `json:"pending_local_skills,omitempty"`
	PendingLocalSkillImport *DaemonHeartbeatPendingLocalSkillImport `json:"pending_local_skill_import,omitempty"`
	// PendingLocalSkillImports carries multiple import requests in a single
	// heartbeat so the daemon can process them concurrently. Old daemons
	// that don't know this field silently ignore it (standard JSON behavior)
	// and fall back to the singular PendingLocalSkillImport above.
	PendingLocalSkillImports []DaemonHeartbeatPendingLocalSkillImport `json:"pending_local_skill_imports,omitempty"`
	PendingMemoryCuration    *DaemonHeartbeatPendingMemoryCuration    `json:"pending_memory_curation,omitempty"`
	// PendingRestart is a human-initiated "restart this daemon" request
	// (task #43), independent of any CLI update — e.g. a stuck/unresponsive
	// daemon a workspace admin restarts remotely from the web UI.
	PendingRestart *DaemonHeartbeatPendingRestart `json:"pending_restart,omitempty"`
	// ReleaseManifestBaseURL, when non-empty, is the server's current opinion
	// of where the daemon should download CLI update artifacts from. It takes
	// precedence over the daemon's own MULTICA_RELEASE_MANIFEST_BASE_URL env
	// var and compile-time default (see internal/cli.releaseManifestBaseURLWithOverride),
	// so a blocked/changed download domain can be fixed with a server-side
	// config change instead of touching every machine. Empty means "server has
	// no opinion" — the daemon falls through to its own precedence.
	ReleaseManifestBaseURL string `json:"release_manifest_base_url,omitempty"`
}

// HeartbeatStatusRuntimeGone is the ack Status used when the runtime row no
// longer exists server-side. Companion to DaemonHeartbeatAckPayload.RuntimeGone.
const HeartbeatStatusRuntimeGone = "runtime_gone"

// DaemonHeartbeatPendingUpdate describes a CLI-update action the daemon
// should run for the runtime.
type DaemonHeartbeatPendingUpdate struct {
	ID                   string `json:"id"`
	TargetVersion        string `json:"target_version"`
	SupportsReadyToApply bool   `json:"supports_ready_to_apply,omitempty"`
}

// DaemonHeartbeatPendingRestart describes a human-initiated remote restart
// request the daemon should act on for this runtime.
type DaemonHeartbeatPendingRestart struct {
	ID string `json:"id"`
}

// DaemonHeartbeatPendingModelList describes a request for the daemon to
// enumerate the runtime's supported models.
type DaemonHeartbeatPendingModelList struct {
	ID          string            `json:"id"`
	Environment map[string]string `json:"environment,omitempty"`
}

// DaemonHeartbeatPendingLocalSkills describes a request for the runtime's
// local-skill inventory.
type DaemonHeartbeatPendingLocalSkills struct {
	ID string `json:"id"`
}

// DaemonHeartbeatPendingLocalSkillImport describes a request to import a
// specific runtime local skill.
type DaemonHeartbeatPendingLocalSkillImport struct {
	ID       string `json:"id"`
	SkillKey string `json:"skill_key"`
}

// DaemonHeartbeatPendingMemoryCuration is a server-owned run intent claimed by
// the explicitly configured runtime. The daemon executes it against local
// agent roots and reports the structured engine result back to the server.
type DaemonHeartbeatPendingMemoryCuration struct {
	ID                   string                               `json:"id"`
	ParentRunID          string                               `json:"parent_run_id,omitempty"`
	AgentRunID           string                               `json:"agent_run_id,omitempty"`
	WorkspaceID          string                               `json:"workspace_id"`
	Stage                string                               `json:"stage"`
	DateFrom             string                               `json:"date_from"`
	DateTo               string                               `json:"date_to"`
	AgentIDs             []string                             `json:"agent_ids"`
	CuratorAgentID       string                               `json:"curator_agent_id"`
	CuratorAgentRoot     string                               `json:"curator_agent_root,omitempty"`
	CuratorModel         string                               `json:"curator_model,omitempty"`
	CuratorThinkingLevel string                               `json:"curator_thinking_level,omitempty"`
	CuratorCustomArgs    []string                             `json:"curator_custom_args,omitempty"`
	CuratorMcpConfig     json.RawMessage                      `json:"curator_mcp_config,omitempty"`
	CuratorInstructions  string                               `json:"curator_instructions,omitempty"`
	Timezone             string                               `json:"timezone"`
	IncludeHistory       bool                                 `json:"include_history"`
	DryRun               bool                                 `json:"dry_run"`
	Force                bool                                 `json:"force"`
	ClaimToken           string                               `json:"claim_token"`
	Mode                 string                               `json:"mode"`
	ConfidenceThreshold  float64                              `json:"confidence_threshold"`
	DBEvidence           []DaemonMemoryCurationEvidenceBundle `json:"db_evidence,omitempty"`
}

// DaemonMemoryCurationEvidenceBundle carries bounded server-side evidence for
// one agent. The daemon cannot safely connect to the server database directly,
// so the server includes relevant evidence in the claimed run intent.
type DaemonMemoryCurationEvidenceBundle struct {
	AgentID string                             `json:"agent_id"`
	Items   []DaemonMemoryCurationEvidenceItem `json:"items"`
}

type DaemonMemoryCurationEvidenceItem struct {
	Kind      string          `json:"kind"`
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Snippet   string          `json:"snippet"`
	Scope     string          `json:"scope,omitempty"`
	SubjectID string          `json:"subject_id,omitempty"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
	CreatedAt string          `json:"created_at"`
}

// AgentMemoryWriteReport is sent by the daemon after a task when whitelisted
// agent-local memory files changed, or when a missed-write guard check is needed
// (explicit remember / durable feedback / co-emitted memory signal) even if no
// file changed.
type AgentMemoryWriteReport struct {
	AgentID     string                  `json:"agent_id"`
	RuntimeID   string                  `json:"runtime_id"`
	TaskID      string                  `json:"task_id,omitempty"`
	TriggerText string                  `json:"trigger_text,omitempty"`
	InitiatorID string                  `json:"initiator_id,omitempty"`
	Signals     []AgentMemorySignal     `json:"signals,omitempty"`
	Writes      []AgentMemoryWriteEntry `json:"writes"`
	Friction    *AgentFrictionVector    `json:"friction,omitempty"`
}

// AgentFrictionVector is the per-task friction episode count vector observed
// by the daemon (friction-gated memory spec). Counts are episodes, not raw
// events, and are observability input for server-side guards only.
type AgentFrictionVector struct {
	HumanCorrection int `json:"human_correction,omitempty"`
	ActionRejected  int `json:"action_rejected,omitempty"`
	RetryLoop       int `json:"retry_loop,omitempty"`
	Rework          int `json:"rework,omitempty"`
	SelfErrorStreak int `json:"self_error_streak,omitempty"`
}

// AgentMemorySignal is an optional co-emitted memory intent. It does not replace
// file writes; the platform uses it for observability and missed-write detection.
type AgentMemorySignal struct {
	Action     string `json:"action"`
	Kind       string `json:"kind,omitempty"`
	Scope      string `json:"scope,omitempty"`
	SubjectID  string `json:"subject_id,omitempty"`
	Topic      string `json:"topic,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Importance string `json:"importance,omitempty"`
}

type AgentMemoryWriteEntry struct {
	RelPath     string `json:"rel_path"`
	ScopeType   string `json:"scope_type"`
	FileKey     string `json:"file_key"`
	ContentHash string `json:"content_hash"`
	DeltaChars  int    `json:"delta_chars"`
}

// AgentMemoryUpdatedPayload is broadcast to workspace clients for avatar +N UI.
type AgentMemoryUpdatedPayload struct {
	AgentID   string `json:"agent_id"`
	ScopeType string `json:"scope_type"`
	FileKey   string `json:"file_key"`
	Count     int    `json:"count"`
}

// AgentMemoryCenterSyncReport uploads durable local memory atoms to the center store.
type AgentMemoryCenterSyncReport struct {
	AgentID             string                      `json:"agent_id"`
	RuntimeID           string                      `json:"runtime_id,omitempty"`
	TaskID              string                      `json:"task_id,omitempty"`
	MutationID          string                      `json:"mutation_id,omitempty"`
	Entries             []AgentMemoryCenterSyncAtom `json:"entries,omitempty"`
	DeletedIdentityKeys []string                    `json:"deleted_identity_keys,omitempty"`
}

// AgentMemoryCenterSyncAtom is one durable bullet/fact from a local memory file.
type AgentMemoryCenterSyncAtom struct {
	RelPath   string `json:"rel_path"`
	Scope     string `json:"scope,omitempty"`
	SubjectID string `json:"subject_id,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Topic     string `json:"topic,omitempty"`
	Content   string `json:"content"`
}

// AgentMemoryCenterSyncResponse summarizes upsert decisions.
type AgentMemoryCenterSyncResponse struct {
	ProtocolVersion        int      `json:"protocol_version,omitempty"`
	Accepted               int      `json:"accepted"`
	Updated                int      `json:"updated"`
	Conflicts              int      `json:"conflicts"`
	Deleted                int      `json:"deleted"`
	Skipped                int      `json:"skipped"`
	TombstonedIdentityKeys []string `json:"tombstoned_identity_keys,omitempty"`
}

// AgentMemoryHydrateRequest asks the center for durable memory to materialize locally.
type AgentMemoryHydrateRequest struct {
	AgentID   string `json:"agent_id"`
	RuntimeID string `json:"runtime_id,omitempty"`
	Cursor    int64  `json:"cursor,omitempty"`
}

// AgentMemoryHydrateResponse carries center changes newer than Cursor.
type AgentMemoryHydrateResponse struct {
	Active    []AgentMemoryHydrateEntry `json:"active"`
	Conflicts []AgentMemoryHydrateEntry `json:"conflicts"`
	Deleted   []AgentMemoryHydrateEntry `json:"deleted,omitempty"`
	Cursor    int64                     `json:"cursor,omitempty"`
}

// AgentMemoryHydrateEntry is one center memory row for local materialization.
type AgentMemoryHydrateEntry struct {
	ID          string `json:"id"`
	IdentityKey string `json:"identity_key"`
	Scope       string `json:"scope"`
	SubjectID   string `json:"subject_id,omitempty"`
	Kind        string `json:"kind"`
	Topic       string `json:"topic,omitempty"`
	RelPath     string `json:"rel_path"`
	Content     string `json:"content"`
	Status      string `json:"status"`
	ConflictOf  string `json:"conflict_of,omitempty"`
	ChangeSeq   int64  `json:"change_seq,omitempty"`
	DeletedAt   string `json:"deleted_at,omitempty"`
}

// TurnCaptureUpload is the daemon-to-server, agent-credential-authenticated
// record of one settled resident Pi turn. The daemon, rather than the
// workspace daemon, is the trust boundary which creates this payload.
type TurnCaptureUpload struct {
	AgentID        string                     `json:"agent_id"`
	RuntimeID      string                     `json:"runtime_id"`
	CaptureBatchID string                     `json:"capture_batch_id"`
	Turn           TurnCaptureTurn            `json:"turn"`
	ProviderCalls  []TurnCaptureProviderCall  `json:"provider_calls"`
	VisibleActions []TurnCaptureVisibleAction `json:"visible_actions,omitempty"`
	Consumptions   []TurnCaptureConsumption   `json:"consumptions,omitempty"`
	PayloadHash    string                     `json:"payload_hash"`
}

// TurnCaptureVisibleAction is a trusted, daemon-observed successful channel
// message or reaction associated with a provider call in the same batch.
type TurnCaptureVisibleAction struct {
	Kind           string `json:"kind"`
	CanonicalID    string `json:"canonical_id"`
	ProducerCallID string `json:"producer_call_id"`
	ActionOrdinal  int64  `json:"action_ordinal"`
	SucceededAt    string `json:"succeeded_at,omitempty"`
}

// TurnCaptureConsumption is concrete message acceptance/check evidence for a
// provider call in the same trusted batch.
type TurnCaptureConsumption struct {
	ChannelMessageID    string `json:"channel_message_id"`
	Source              string `json:"source"`
	EffectiveFromCallID string `json:"effective_from_call_id"`
	ConsumedAt          string `json:"consumed_at,omitempty"`
}

// TurnCaptureTurn identifies the logical resident turn that produced a
// capture batch. CaptureBoundary is the server-issued binding boundary and is
// used to reject a stale or cross-run upload.
type TurnCaptureTurn struct {
	TurnID          string `json:"turn_id"`
	RunAgentID      string `json:"run_agent_id"`
	PiSessionID     string `json:"pi_session_id"`
	CaptureBoundary string `json:"capture_boundary"`
	TurnOrdinal     int64  `json:"turn_ordinal"`
	Status          string `json:"status"`
	StartedAt       string `json:"started_at,omitempty"`
	CompletedAt     string `json:"completed_at,omitempty"`
}

// TurnCaptureProviderCall preserves the final provider request and typed
// final assistant message for one provider call. RawProviderRequest must have
// been redacted by the daemon before it reaches this transport DTO.
type TurnCaptureProviderCall struct {
	CallID                string          `json:"call_id"`
	CallOrdinal           int64           `json:"call_ordinal"`
	Provider              string          `json:"provider"`
	Model                 string          `json:"model"`
	APIKind               string          `json:"api_kind"`
	RawProviderRequest    json.RawMessage `json:"raw_provider_request"`
	FinalAssistantMessage json.RawMessage `json:"final_assistant_message"`
	Status                string          `json:"status"`
	StopReason            string          `json:"stop_reason,omitempty"`
	ResponseComplete      bool            `json:"response_complete"`
	AReaLSessionID        string          `json:"areal_session_id,omitempty"`
	AReaLCallID           string          `json:"areal_call_id,omitempty"`
	RequestHash           string          `json:"request_hash"`
	ResponseHash          string          `json:"response_hash"`
	StartedAt             string          `json:"started_at,omitempty"`
	CompletedAt           string          `json:"completed_at,omitempty"`
}

// TurnCaptureUploadResponse is returned when the server atomically accepted
// (or idempotently recognized) a capture batch. After freeze, Late and
// SnapshotID identify the immutable late-audit routing without claiming a
// snapshot mutation.
type TurnCaptureUploadResponse struct {
	Accepted           bool   `json:"accepted"`
	CaptureBatchID     string `json:"capture_batch_id,omitempty"`
	TurnID             string `json:"turn_id,omitempty"`
	ProviderCallCount  int    `json:"provider_call_count"`
	VisibleActionCount int    `json:"visible_action_count"`
	ConsumptionCount   int    `json:"consumption_count"`
	RunStatus          string `json:"run_status,omitempty"`
	Late               bool   `json:"late,omitempty"`
	SnapshotID         string `json:"snapshot_id,omitempty"`
}

// TurnCaptureGapReport records why a finished daemon turn cannot supply a
// trusted complete capture. The server may freeze or otherwise terminally
// account for the gap; callers must not silently decrement unfinished capture
// state before this report is acknowledged.
type TurnCaptureGapReport struct {
	AgentID         string `json:"agent_id"`
	RuntimeID       string `json:"runtime_id"`
	RunAgentID      string `json:"run_agent_id"`
	TurnID          string `json:"turn_id"`
	TurnOrdinal     int64  `json:"turn_ordinal"`
	CaptureBoundary string `json:"capture_boundary"`
	Reason          string `json:"reason"`
	OccurredAt      string `json:"occurred_at"`
}

// TurnCaptureGapResponse acknowledges durable accounting for a capture gap.
type TurnCaptureGapResponse struct {
	Accepted bool `json:"accepted"`
}
