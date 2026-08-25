package protocol

// Event types for WebSocket communication between server, web clients, and daemon.
const (
	// Issue events
	EventIssueCreated         = "issue:created"
	EventIssueUpdated         = "issue:updated"
	EventIssueDeleted         = "issue:deleted"
	EventIssueMetadataChanged = "issue_metadata:changed"

	// Comment events
	EventCommentCreated       = "comment:created"
	EventCommentUpdated       = "comment:updated"
	EventCommentDeleted       = "comment:deleted"
	EventCommentResolved      = "comment:resolved"
	EventCommentUnresolved    = "comment:unresolved"
	EventReactionAdded        = "reaction:added"
	EventReactionRemoved      = "reaction:removed"
	EventIssueReactionAdded   = "issue_reaction:added"
	EventIssueReactionRemoved = "issue_reaction:removed"

	// Agent events
	EventAgentStatus          = "agent:status"
	EventAgentPresence        = "agent:presence"
	EventAgentCreated         = "agent:created"
	EventAgentArchived        = "agent:archived"
	EventAgentRestored        = "agent:restored"
	EventAgentDeleted         = "agent:deleted"
	EventAgentReminderChanged = "agent_reminder:changed"

	// Task events (server <-> daemon).
	// Each event maps to a status transition on agent_inbox_event. Front-end
	// subscribes by `task:` prefix and invalidates the workspace task
	// snapshot, so the granularity here is "what does the user want to see
	// change" — not "every internal status flip".
	EventTaskQueued         = "task:queued"   // ∅ → queued (enqueue / retry create)
	EventTaskDispatch       = "task:dispatch" // queued → dispatched (daemon claim)
	EventTaskRunning        = "task:running"  // dispatched → running (daemon started)
	EventTaskProgress       = "task:progress"
	EventTaskCompleted      = "task:completed" // running → completed
	EventTaskFailed         = "task:failed"    // running → failed
	EventTaskMessage        = "task:message"
	EventAgentMemoryUpdated = "agent.memory_updated"
	EventTaskCancelled      = "task:cancelled" // * → cancelled

	// Inbox events
	EventInboxNew           = "inbox:new"
	EventInboxRead          = "inbox:read"
	EventInboxArchived      = "inbox:archived"
	EventInboxBatchRead     = "inbox:batch-read"
	EventInboxBatchArchived = "inbox:batch-archived"

	// Workspace events
	EventWorkspaceUpdated = "workspace:updated"
	EventWorkspaceDeleted = "workspace:deleted"

	// Member events
	EventMemberAdded    = "member:added"
	EventMemberUpdated  = "member:updated"
	EventMemberRemoved  = "member:removed"
	EventMemberPresence = "member:presence" // LRM-462: human online/offline from WS sessions

	// Honor events
	EventHonorBadgeUnlocked     = "honor:badge_unlocked"
	EventAgentHonorUnlocked     = "agent_honor:achievement_unlocked"
	EventAgentHonorLevelChanged = "agent_honor:level_changed"
	EventAgentFleetClassChanged = "agent_honor:fleet_class_changed"

	// Subscriber events
	EventSubscriberAdded   = "subscriber:added"
	EventSubscriberRemoved = "subscriber:removed"

	// Activity events
	EventActivityCreated = "activity:created"

	// Skill events
	EventSkillCreated = "skill:created"
	EventSkillUpdated = "skill:updated"
	EventSkillDeleted = "skill:deleted"

	// Chat events
	EventChatMessage            = "chat:message"
	EventChatDone               = "chat:done"
	EventChatSessionRead        = "chat:session_read"
	EventChatSessionDeleted     = "chat:session_deleted"
	EventChatSessionUpdated     = "chat:session_updated"
	EventChannelMessage         = "channel:message"
	EventChannelMessageUpdated  = "channel:message_updated"
	EventChannelTyping          = "channel:typing"
	EventChannelReactionAdded   = "channel_reaction:added"
	EventChannelReactionRemoved = "channel_reaction:removed"
	EventChannelUpdated         = "channel:updated"
	EventChannelDeleted         = "channel:deleted"

	// Voice call events
	EventVoiceCallUpdated = "voice_call:updated"

	// Project events
	EventProjectCreated         = "project:created"
	EventProjectUpdated         = "project:updated"
	EventProjectDeleted         = "project:deleted"
	EventProjectResourceCreated = "project_resource:created"
	EventProjectResourceUpdated = "project_resource:updated"
	EventProjectResourceDeleted = "project_resource:deleted"

	// Label events
	EventLabelCreated       = "label:created"
	EventLabelUpdated       = "label:updated"
	EventLabelDeleted       = "label:deleted"
	EventIssueLabelsChanged = "issue_labels:changed"

	// Pin events
	EventPinCreated   = "pin:created"
	EventPinDeleted   = "pin:deleted"
	EventPinReordered = "pin:reordered"

	// Invitation events
	EventInvitationCreated  = "invitation:created"
	EventInvitationAccepted = "invitation:accepted"
	EventInvitationDeclined = "invitation:declined"
	EventInvitationRevoked  = "invitation:revoked"

	// Autopilot events
	EventAutopilotCreated  = "autopilot:created"
	EventAutopilotUpdated  = "autopilot:updated"
	EventAutopilotDeleted  = "autopilot:deleted"
	EventAutopilotRunStart = "autopilot:run_start"
	EventAutopilotRunDone  = "autopilot:run_done"

	// Computer events
	EventComputerStatus = "computer:status"

	// Daemon events
	EventDaemonHeartbeat      = "daemon:heartbeat"
	EventDaemonHeartbeatAck   = "daemon:heartbeat_ack"
	EventDaemonLivenessProbe  = "daemon:liveness_probe"
	EventDaemonLivenessAck    = "daemon:liveness_ack"
	EventDaemonRegister       = "daemon:register"
	EventDaemonRuntimeUpdated = "daemon:runtime_updated"
	EventDaemonTaskAvailable  = "daemon:task_available"
	// EventAgentDeliver is the at-least-once server-to-machine transport for a
	// canonical Message. Its acknowledgement is strictly a per-Agent acceptance
	// receipt: the provider accepted the body, Pending retained it, or the local
	// boundary deduplicated it. It is never a provider-turn completion receipt.
	EventAgentDeliver               = "agent:deliver"
	EventAgentDeliverAck            = "agent:deliver:ack"
	EventMixedRunActivityTransition = "mixed_run:activity_transition"
	EventMixedRunActivityAck        = "mixed_run:activity_transition:ack"
	EventDaemonAgentStart           = "agent:start"
	EventDaemonAgentStop            = "agent:stop"
	EventDaemonAgentResetWorkspace  = "agent:reset-workspace"
	EventAgentResetWorkspaceResult  = "agent:reset-workspace:result"
	EventReminderUpsert             = "reminder.upsert"
	EventReminderCancel             = "reminder.cancel"
	EventReminderSnapshotRequest    = "reminder.snapshot.request"
	EventReminderSnapshot           = "reminder.snapshot"
	EventReminderFireRequest        = "reminder.fire_request"
	EventReminderFireRequestResult  = "reminder.fire_request.result"
	EventReminderFireReceiptAck     = "reminder.fire_receipt.ack"

	// Sandbox events. Node-facing events wake shared sandbox infrastructure;
	// instance events are broadcast to workspace clients for UI refresh.
	EventSandboxJobAvailable    = "sandbox:job_available"
	EventSandboxInstanceCreated = "sandbox:instance_created"
	EventSandboxInstanceUpdated = "sandbox:instance_updated"
	// Agent Workspace file-tree RPC over the Computer socket. These event names
	// match Raft's Computer protocol: the server asks for a local workspace tree
	// and the Computer replies on the same persistent connection. Correlated by
	// RequestID. See protocol.ListWorkdirFiles*.
	EventAgentWorkspaceList     = "agent:workspace:list"
	EventAgentWorkspaceFileTree = "agent:workspace:file_tree"
	// Agent Workspace single-file read RPC (preview), also matching Raft's
	// Computer protocol. Correlated by RequestID. See protocol.ReadWorkdirFile*.
	EventAgentWorkspaceRead        = "agent:workspace:read"
	EventAgentWorkspaceFileContent = "agent:workspace:file_content"
	EventAgentSkillsList           = "agent:skills:list"
	EventAgentSkillsListResult     = "agent:skills:list_result"
	// Workdir single-file write RPC: server pushes a bounded UTF-8 text write,
	// daemon writes inside the confined workdir root and replies with a content
	// hash or conflict/error. Correlated by RequestID.
	EventDaemonWriteFileRequest  = "daemon:write_file_request"
	EventDaemonWriteFileResponse = "daemon:write_file_response"
	// Workdir directory delete RPC: server asks the daemon to remove one
	// confined directory under WorkspacesRoot (used for agent workspace
	// cleanup, including orphan dirs). Correlated by RequestID.
	EventDaemonDeleteDirRequest  = "daemon:delete_dir_request"
	EventDaemonDeleteDirResponse = "daemon:delete_dir_response"
	// Agent initial-context seed RPC: server pushes Wendy-created notes/memory
	// metadata after agent creation; daemon initializes the agent root and appends
	// only whitelisted markdown files. Correlated by RequestID.
	EventDaemonSeedAgentContextRequest  = "daemon:seed_agent_context_request"
	EventDaemonSeedAgentContextResponse = "daemon:seed_agent_context_response"
	EventDaemonPreparePiRunRequest      = "daemon:prepare_pi_run_request"
	EventDaemonPreparePiRunResponse     = "daemon:prepare_pi_run_response"
	EventDaemonRevokePiRunRequest       = "daemon:revoke_pi_run_request"
	EventDaemonRevokePiRunResponse      = "daemon:revoke_pi_run_response"

	// GitHub integration events
	EventGitHubInstallationCreated = "github_installation:created"
	EventGitHubInstallationDeleted = "github_installation:deleted"
	EventPullRequestLinked         = "pull_request:linked"
	EventPullRequestUpdated        = "pull_request:updated"
	EventPullRequestUnlinked       = "pull_request:unlinked"

	// Lark integration events. `created` covers both first-install
	// (UNIQUE on (workspace_id, agent_id) means at most one row per
	// agent) and re-install via UpsertLarkInstallation — front-ends
	// treat both as a single "installation appeared / refreshed"
	// notification. `revoked` flips status to 'revoked' without
	// deleting the row; the audit trail is preserved.
	EventLarkInstallationCreated = "lark_installation:created"
	EventLarkInstallationRevoked = "lark_installation:revoked"
)

type VoiceCallUpdatedPayload struct {
	WorkspaceID string `json:"workspace_id"`
	CallID      string `json:"call_id"`
}
