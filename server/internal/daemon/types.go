package daemon

import (
	"encoding/json"
	"fmt"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// AgentEntry describes a single available agent CLI.
type AgentEntry struct {
	Path  string // path to CLI binary
	Model string // model override (optional)
}

// Runtime represents a registered daemon runtime.
type Runtime struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	Provider    string `json:"provider"`
	Status      string `json:"status"`
}

// ResidentAgentRuntimeConfig is the daemon-only, process-stable configuration
// of an Agent resident on one runtime. Message delivery is deliberately not
// represented here: it is handled exclusively by MessageCoordinator.
type ResidentAgentRuntimeConfig struct {
	WorkspaceID      string     `json:"workspace_id"`
	RuntimeID        string     `json:"runtime_id"`
	WorkspaceContext string     `json:"workspace_context,omitempty"`
	Agent            *AgentData `json:"agent"`
}

// Task represents a claimed task from the server.
// Agent data (name, skills) is populated by the claim endpoint.
type Task struct {
	ID          string `json:"id"`
	AgentID     string `json:"agent_id"`
	RuntimeID   string `json:"runtime_id"`
	Priority    int    `json:"priority,omitempty"`
	IssueID     string `json:"issue_id"`
	WorkspaceID string `json:"workspace_id"`
	// WorkspaceContext mirrors workspace.context (the per-workspace system
	// prompt set in Settings → General). Server populates this on every claim
	// regardless of task kind so the daemon can inject `## Workspace Context`
	// into the brief. Empty when the owner hasn't set one.
	WorkspaceContext string     `json:"workspace_context,omitempty"`
	ThreadName       string     `json:"thread_name,omitempty"` // semantic title for provider-native session/thread history
	Agent            *AgentData `json:"agent,omitempty"`
	ProjectID        string     `json:"project_id,omitempty"`   // issue's project, when present
	ChannelID        string     `json:"channel_id,omitempty"`   // exact DM/channel surface, when present
	ChannelKind      string     `json:"channel_kind,omitempty"` // "dm" | "group" when ChannelID is set; drives personal-memory entry gate
	// ScopedSecrets are channel/project (and optionally agent) secrets injected
	// after filtering by the current task channel/project (LRM-953). Agent
	// custom_env remains separate and is treated as agent-scoped.
	ScopedSecrets            []ScopedSecret                     `json:"scoped_secrets,omitempty"`
	ChannelGoal              *protocol.ChannelGoalContext       `json:"channel_goal,omitempty"`                // active channel goal, refreshed on every claim
	ProjectTitle             string                             `json:"project_title,omitempty"`               // human-readable project title for context injection
	PriorSessionID           string                             `json:"prior_session_id,omitempty"`            // Claude session ID from a previous task on this issue
	PriorWorkDir             string                             `json:"prior_work_dir,omitempty"`              // work_dir from a previous task on this issue
	TriggerCommentID         string                             `json:"trigger_comment_id,omitempty"`          // comment that triggered this task
	TriggerThreadID          string                             `json:"trigger_thread_id,omitempty"`           // root comment ID for the triggering thread; falls back to trigger_comment_id on old servers
	TriggerCommentContent    string                             `json:"trigger_comment_content,omitempty"`     // content of the triggering comment
	TriggerAuthorType        string                             `json:"trigger_author_type,omitempty"`         // "agent" or "member" — author kind for the triggering comment
	TriggerAuthorName        string                             `json:"trigger_author_name,omitempty"`         // display name of the triggering comment author
	NewCommentCount          int                                `json:"new_comment_count,omitempty"`           // issue-wide comments since this agent's last run (excludes its own and the injected trigger); 0/omitted for old daemons or cold start
	NewCommentsSince         string                             `json:"new_comments_since,omitempty"`          // RFC3339 anchor (last run's started_at) the count is measured from; empty on cold start
	AssignmentSnapshot       *protocol.IssueAssignmentSnapshot  `json:"assignment_snapshot,omitempty"`         // assignment-time stable fields plus claim-time current status
	ChatSessionID            string                             `json:"chat_session_id,omitempty"`             // non-empty for chat tasks
	ChatMessage              string                             `json:"chat_message,omitempty"`                // user message content for chat tasks
	ChatContextSummary       string                             `json:"chat_context_summary,omitempty"`        // compact surface-scoped context handoff when native resume is skipped
	ChatMessageAttachments   []ChatAttachmentMeta               `json:"chat_message_attachments,omitempty"`    // attachments linked to the chat message; agent uses these to `multica attachment view --id <id> --output <path>`
	AutopilotRunID           string                             `json:"autopilot_run_id,omitempty"`            // non-empty for autopilot run_only tasks
	AutopilotID              string                             `json:"autopilot_id,omitempty"`                // autopilot that spawned this run
	AutopilotTitle           string                             `json:"autopilot_title,omitempty"`             // autopilot title used as task context
	AutopilotDescription     string                             `json:"autopilot_description,omitempty"`       // autopilot description used as task prompt
	AutopilotSource          string                             `json:"autopilot_source,omitempty"`            // manual, schedule, webhook, or api
	AutopilotTriggerPayload  json.RawMessage                    `json:"autopilot_trigger_payload,omitempty"`   // optional trigger payload for webhook/api runs
	QuickCreatePrompt        string                             `json:"quick_create_prompt,omitempty"`         // user's natural-language input for quick-create tasks
	QuickCreateAttachmentIDs []string                           `json:"quick_create_attachment_ids,omitempty"` // attachments uploaded in the quick-create prompt and bound by issue create
	QuickCreateSource        *protocol.QuickCreateSourceContext `json:"quick_create_source,omitempty"`         // bounded chat/thread source context for quick-create tasks
	AgentRadarPrompt         string                             `json:"agent_radar_prompt,omitempty"`          // full prompt for proactive radar tasks
	ParentIssueID            string                             `json:"parent_issue_id,omitempty"`             // for quick-create tasks opened from "Add sub issue" — UUID of the parent issue the new issue should be filed under
	ParentIssueIdentifier    string                             `json:"parent_issue_identifier,omitempty"`     // human-readable identifier (e.g. MUL-123) of the quick-create parent issue, used in prompt context
	// RequestingUserName + RequestingUserProfileDescription describe the human
	// the agent is working on behalf of. v1 sources them from the runtime
	// owner (the user who registered the daemon). Empty when the runtime has
	// no owner (cloud / system runtimes) or the user hasn't set a description.
	// Injected into the brief under `## Requesting User`; omitted entirely
	// when description is empty so the agent doesn't see a useless heading.
	RequestingUserName               string `json:"requesting_user_name,omitempty"`
	RequestingUserProfileDescription string `json:"requesting_user_profile_description,omitempty"`
	// Initiator* identify the actor who triggered THIS task (the real
	// requester behind the current comment/mention or chat message) as
	// distinct from the runtime owner whose credentials the agent runs with.
	// Comment-triggered tasks resolve to the triggering comment's author;
	// chat tasks resolve to the chat session creator. Empty for task kinds
	// with no attributable human initiator (on-assign, autopilot,
	// quick-create). InitiatorEmail is set only for member initiators. The
	// daemon emits these into the brief under `## Task Initiator` so a
	// workspace-visible agent can attribute the request per person. The
	// agent's effective credentials stay owner-scoped — this is an attested
	// identity, not a credential. See MUL-2645.
	InitiatorType  string `json:"initiator_type,omitempty"`
	InitiatorID    string `json:"initiator_id,omitempty"`
	InitiatorName  string `json:"initiator_name,omitempty"`
	InitiatorEmail string `json:"initiator_email,omitempty"`
	// ArealProxy carries the AReaL RL proxy provider config the server extracts
	// from a trained task's context.areal_proxy at claim time (§4.4). When set,
	// the daemon launches the runtime against the RL proxy instead of the
	// agent's normal provider so the trained agent's LLM traffic routes through
	// the bridge. Nil for non-trained tasks (the vast majority); omitempty so
	// old servers that never send it are handled transparently.
	ArealProxy *ArealProxy `json:"areal_proxy,omitempty"`
	// AuthToken is the bearer token the daemon writes into the spawned agent's
	// MULTICA_TOKEN_FILE wrapper. Legacy queue runs bind it to a task; legacy
	// inbox runs bind it to a single delivery. Credential-transport-capable
	// inbox runs leave this empty so the daemon provisions/reuses a durable
	// agent credential locally and exposes only a per-run token file copy.
	// Empty when the server-side runtime has no owning user.
	AuthToken string `json:"auth_token,omitempty"`

	// ExecutionConfig is the immutable run contract captured by the server.
	// Restricted profiles must fail closed when the local provider cannot
	// guarantee an isolated, tool-free invocation.
	ExecutionConfig *TaskExecutionConfig `json:"execution_config,omitempty"`

	ReferencedEntities           []protocol.ReferencedEntitySnapshot `json:"referenced_entities,omitempty"`             // bounded, permission-filtered snapshots for canonical references in this turn
	ReferencedEntityOmittedCount int                                 `json:"referenced_entity_omitted_count,omitempty"` // references beyond the hydration cap

	// InboxEvent is present when this work item came from the raft-like
	// agent inbox instead of legacy agent_inbox_event. Terminal callbacks must
	// use the inbox lease endpoints and must not call task start/complete/fail.
	// Primary lease of a conversation batch (agent output is reported on this
	// one). Folded same-conversation leases ride FoldedInboxEvents.
	InboxEvent *AgentInboxLease `json:"inbox_event,omitempty"`
	// FoldedInboxEvents are additional same-conversation inbox leases that were
	// drained with the primary for turn-fold (one exchange = one turn). They are
	// renewed for the turn's duration and acked/failed with the primary — never
	// parked across turns.
	FoldedInboxEvents []*AgentInboxLease `json:"-"`
}

type TaskExecutionConfig struct {
	Model             string `json:"model,omitempty"`
	ThinkingLevel     string `json:"thinking_level,omitempty"`
	ExecutionProfile  string `json:"execution_profile,omitempty"`
	ContextMessages   int    `json:"context_messages,omitempty"`
	MemoryBudgetBytes int    `json:"memory_budget_bytes,omitempty"`
	MaxOutputTokens   int    `json:"max_output_tokens,omitempty"`
	ToolsEnabled      bool   `json:"tools_enabled"`
	Snapshotted       bool   `json:"snapshotted,omitempty"`
}

type AgentInboxLease struct {
	ID              string `json:"id"`
	DeliveryID      string `json:"delivery_id"`
	ConversationID  string `json:"conversation_id,omitempty"`
	SourceMessageID string `json:"source_message_id,omitempty"`
	ResponseMode    string `json:"response_mode,omitempty"`
	LeaseToken      string `json:"lease_token"`
	LeaseExpiresAt  string `json:"lease_expires_at"`
	SeqFrom         int64  `json:"seq_from"`
	SeqTo           int64  `json:"seq_to"`
	RequiresWake    bool   `json:"requires_wake"`
	Reason          string `json:"reason,omitempty"`
	RuntimeID       string `json:"-"`
	ExecutionID     string `json:"-"`
}

// ChatAttachmentMeta is the structured attachment metadata the daemon
// hands to the agent for chat tasks. We pass id + filename + content_type
// so the chat prompt can list them explicitly and instruct the agent to
// run `multica attachment view --id <id> --output <path>` instead of
// guessing from a signed CDN URL (which expires).
type ChatAttachmentMeta struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type,omitempty"`
}

// ArealProxy is the daemon-side mirror of handler.ArealProxyData: the AReaL RL
// proxy provider config the server extracts from context.areal_proxy at claim
// time (§4.4). The JSON tags must stay in sync with the wire struct.
type ArealProxy struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	APIKey   string `json:"api_key"`
	BaseURL  string `json:"base_url"`
}

// String returns a log-safe representation. Task-scoped proxy credentials and
// route details are intentionally omitted so accidental formatting cannot leak
// them into logs or test output.
func (p *ArealProxy) String() string {
	if p == nil {
		return "<nil>"
	}
	return "ArealProxy{route_configured:true credentials:[REDACTED]}"
}

// Format ensures alternate fmt verbs such as %#v cannot bypass String and
// reflect the credential-bearing wire fields.
func (p *ArealProxy) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte(p.String()))
}

// ScopedSecret is one env entry with a hard scope boundary for injection.
type ScopedSecret struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	Scope     string `json:"scope,omitempty"` // agent | channel | project
	ChannelID string `json:"channel_id,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
}

// AgentData holds agent details returned by the claim endpoint.
type AgentData struct {
	ID              string                                `json:"id"`
	Name            string                                `json:"name"`
	ManagedRole     string                                `json:"managed_role,omitempty"`
	ManagerChannels []execenv.ManagerChannelContextForEnv `json:"manager_channels,omitempty"`
	Instructions    string                                `json:"instructions"`
	Skills          []SkillData                           `json:"skills"`
	Memories        []MemoryData                          `json:"memories,omitempty"`
	CustomEnv       map[string]string                     `json:"custom_env,omitempty"`
	CustomArgs      []string                              `json:"custom_args,omitempty"`
	McpConfig       json.RawMessage                       `json:"mcp_config,omitempty"`
	Model           string                                `json:"model,omitempty"`
	ThinkingLevel   string                                `json:"thinking_level,omitempty"`
}

type MemoryData struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Content     string `json:"content"`
	Scope       string `json:"scope"`
	SubjectType string `json:"subject_type,omitempty"`
	SubjectID   string `json:"subject_id,omitempty"`
	SyncKey     string `json:"sync_key"`
	ContentHash string `json:"content_hash,omitempty"`
}

// SkillData represents a structured skill for task execution.
type SkillData struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Content     string          `json:"content"`
	Files       []SkillFileData `json:"files,omitempty"`
}

// SkillFileData represents a supporting file within a skill.
type SkillFileData struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type SharedSkillSyncPayload struct {
	Skills      []SharedSkillBundle `json:"skills"`
	PresentKeys []string            `json:"present_keys"`
}

type SharedSkillBundle struct {
	Key         string          `json:"key"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Content     string          `json:"content"`
	SourcePath  string          `json:"source_path"`
	Provider    string          `json:"provider"`
	ContentHash string          `json:"content_hash,omitempty"`
	Files       []SkillFileData `json:"files,omitempty"`
}

type EvolutionSubmissionSyncPayload struct {
	Submissions []EvolutionSubmissionBundle `json:"submissions"`
}

type EvolutionSubmissionBundle struct {
	WorkspaceID    string          `json:"workspace_id,omitempty"`
	AgentID        string          `json:"agent_id"`
	UnitType       string          `json:"unit_type"`
	LocalUnitID    string          `json:"local_unit_id"`
	Title          string          `json:"title"`
	Summary        string          `json:"summary,omitempty"`
	Content        string          `json:"content,omitempty"`
	Payload        json.RawMessage `json:"payload,omitempty"`
	ContentHash    string          `json:"content_hash,omitempty"`
	BundleHash     string          `json:"bundle_hash,omitempty"`
	BundleRef      string          `json:"bundle_ref,omitempty"`
	Sensitivity    string          `json:"sensitivity,omitempty"`
	Confidence     string          `json:"confidence,omitempty"`
	SuggestedScope string          `json:"suggested_scope,omitempty"`
	SourceUserID   string          `json:"source_user_id,omitempty"`
	SubjectType    string          `json:"subject_type,omitempty"`
	SubjectID      string          `json:"subject_id,omitempty"`
	Evidence       json.RawMessage `json:"evidence,omitempty"`
	Applies        json.RawMessage `json:"applies,omitempty"`
	Tags           []string        `json:"tags,omitempty"`
	Tools          []string        `json:"tools,omitempty"`
	TaskTypes      []string        `json:"task_types,omitempty"`
	ProjectTypes   []string        `json:"project_types,omitempty"`
	Languages      []string        `json:"languages,omitempty"`
	Frameworks     []string        `json:"frameworks,omitempty"`
	SourceCreated  string          `json:"created_at,omitempty"`
	Files          []SkillFileData `json:"files,omitempty"`
}

type SharedSkillSyncConflict struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Skill  string `json:"skill_id,omitempty"`
	Reason string `json:"reason"`
}

type SharedSkillSyncItemError struct {
	Key   string `json:"key"`
	Name  string `json:"name,omitempty"`
	Error string `json:"error"`
}

type SharedSkillSyncResult struct {
	Status       string                     `json:"status"`
	Created      int                        `json:"created"`
	Updated      int                        `json:"updated"`
	Unchanged    int                        `json:"unchanged"`
	Deleted      int                        `json:"deleted"`
	Acknowledged []string                   `json:"acknowledged,omitempty"`
	Conflicts    []SharedSkillSyncConflict  `json:"conflicts,omitempty"`
	Errors       []SharedSkillSyncItemError `json:"errors,omitempty"`
}

// TaskUsageEntry represents token usage for a single model during a task execution.
type TaskUsageEntry struct {
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
}

// TaskResult is the outcome of executing a task.
type TaskResult struct {
	Status        string                        `json:"status"`
	Comment       string                        `json:"comment"`
	BranchName    string                        `json:"branch_name,omitempty"`
	Action        string                        `json:"action,omitempty"`
	Target        string                        `json:"target,omitempty"`
	Type          string                        `json:"type,omitempty"`
	Parts         []protocol.MessagePart        `json:"parts,omitempty"`
	Reaction      *protocol.ChatReactionPayload `json:"reaction,omitempty"`
	EnvType       string                        `json:"env_type,omitempty"`
	SessionID     string                        `json:"session_id,omitempty"` // Claude session ID for future resumption
	WorkDir       string                        `json:"work_dir,omitempty"`   // working directory used during execution
	FailureReason string                        `json:"-"`                    // classifier forwarded to FailTask on the blocked path; empty falls back to 'agent_error'
	Usage         []TaskUsageEntry              `json:"usage,omitempty"`      // per-model token usage
	RuntimeStats  *protocol.RuntimeTokenStats   `json:"runtime_stats,omitempty"`
	ExecutionID   string                        `json:"-"`
	// InternalOutput is the validated structured result for a restricted
	// execution. It is deliberately excluded from legacy task completion JSON;
	// protocol turn persistence consumes it through its own internal contract.
	InternalOutput         json.RawMessage `json:"-"`
	OutputSuppressedReason string          `json:"-"`
	TransportAttempted     bool            `json:"-"`
}
