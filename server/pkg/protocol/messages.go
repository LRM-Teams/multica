package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	MessagePartTypeText    = "text"
	MessagePartTypeSticker = "sticker"
)

type MessagePart struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	PackID    string `json:"pack_id,omitempty"`
	StickerID string `json:"sticker_id,omitempty"`
	Alt       string `json:"alt,omitempty"`
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
	Missing   bool              `json:"missing,omitempty"`
	Truncated bool              `json:"truncated,omitempty"`
	Error     string            `json:"error,omitempty"`
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
	RequestID string `json:"request_id"`
	Content   string `json:"content,omitempty"`
	Encoding  string `json:"encoding,omitempty"`
	MimeType  string `json:"mime_type,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	TooLarge  bool   `json:"too_large,omitempty"`
	Binary    bool   `json:"binary,omitempty"`
	Missing   bool   `json:"missing,omitempty"`
	Error     string `json:"error,omitempty"`
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
	Options                *ChatOutputOptions   `json:"options,omitempty"`
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
	DaemonCapabilityChannelOutputActions = "channel_output_actions"
)

const (
	ChannelOutputSuppressedReasonDaemonOutdated       = "daemon_outdated"
	ChannelOutputSuppressedReasonLegacyProtocolOutput = "legacy_protocol_output"
	ChannelOutputSuppressedReasonInvalidOutput        = "invalid_output"
	ChannelOutputSuppressedReasonInvalidAction        = "invalid_action"
	ChannelOutputSuppressedReasonInvalidType          = "invalid_type"
	ChannelOutputSuppressedReasonEmptyMessage         = "empty_message"
	ChannelOutputSuppressedReasonInvalidReaction      = "invalid_reaction"
	ChannelOutputSuppressedReasonMessageMissingAction = "message_missing_action"
	ChannelOutputSuppressedReasonInvalidTarget        = "invalid_target"
)

type ChatOutputOptions struct {
	ShowInChannel     *bool `json:"show_in_channel,omitempty"`
	AlsoSendToChannel *bool `json:"also_send_to_channel,omitempty"` // legacy #252 name; not part of the new contract.
}

func (o *ChatOutputOptions) HasChannelDisplayOption() bool {
	return o != nil && (o.ShowInChannel != nil || o.AlsoSendToChannel != nil)
}

func (o *ChatOutputOptions) ShowInChannelValue() bool {
	if o == nil {
		return false
	}
	if o.ShowInChannel != nil {
		return *o.ShowInChannel
	}
	return o.AlsoSendToChannel != nil && *o.AlsoSendToChannel
}

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
	TaskID    string         `json:"task_id"`
	IssueID   string         `json:"issue_id,omitempty"`
	Seq       int            `json:"seq"`
	Type      string         `json:"type"`              // "text", "tool_use", "tool_result", "error"
	Tool      string         `json:"tool,omitempty"`    // tool name for tool_use/tool_result
	Content   string         `json:"content,omitempty"` // text content
	Input     map[string]any `json:"input,omitempty"`   // tool input (tool_use only)
	Output    string         `json:"output,omitempty"`  // tool output (tool_result only)
	CreatedAt string         `json:"created_at,omitempty"`
}

// DaemonRegisterPayload is sent from daemon to server on connection.
type DaemonRegisterPayload struct {
	DaemonID string        `json:"daemon_id"`
	AgentID  string        `json:"agent_id"`
	Runtimes []RuntimeInfo `json:"runtimes"`
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
	Options                *ChatOutputOptions   `json:"options,omitempty"`
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
	RuntimeID           string `json:"runtime_id"`
	SupportsBatchImport bool   `json:"supports_batch_import,omitempty"`
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

// DaemonHeartbeatPendingModelList describes a request for the daemon to
// enumerate the runtime's supported models.
type DaemonHeartbeatPendingModelList struct {
	ID string `json:"id"`
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
