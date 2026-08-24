import type { Issue, IssueMetadata, IssueReaction } from "./issue";
import type { Agent, AgentRuntime } from "./agent";
import type { InboxItem } from "./inbox";
import type { Comment, Reaction } from "./comment";
import type { TimelineEntry } from "./activity";
import type { Workspace, MemberWithUser, Invitation } from "./workspace";
import type { Project } from "./project";
import type { Label } from "./label";
import type { ChannelMessage, ChannelReaction, ChannelTypingPayload } from "./channel";
import type { MessagePart } from "./message-part";
import type { AgentAchievement } from "./agent-honor";

// WebSocket event types (matching Go server protocol/events.go)
export type WSEventType =
  | "issue:created"
  | "issue:updated"
  | "issue:deleted"
  | "comment:created"
  | "comment:updated"
  | "comment:deleted"
  | "comment:resolved"
  | "comment:unresolved"
  | "agent:status"
  | "agent:activity"
  | "agent:presence"
  | "agent:created"
  | "agent:archived"
  | "agent:restored"
  | "agent:deleted"
  | "agent.memory_updated"
  | "task:queued"
  | "task:dispatch"
  | "task:running"
  | "task:progress"
  | "task:completed"
  | "task:failed"
  | "task:message"
  | "agent_reminder:changed"
  | "task:cancelled"
  | "inbox:new"
  | "inbox:read"
  | "inbox:archived"
  | "inbox:batch-read"
  | "inbox:batch-archived"
  | "workspace:updated"
  | "workspace:deleted"
  | "member:added"
  | "member:updated"
  | "member:removed"
  | "member:presence"
  | "honor:badge_unlocked"
  | "agent_honor:achievement_unlocked"
  | "agent_honor:level_changed"
  | "agent_honor:fleet_class_changed"
  | "daemon:heartbeat"
  | "daemon:register"
  | "daemon:runtime_updated"
  | "computer:updated"
  | "computer:upgrade:progress"
  | "computer:upgrade:done"
  | "skill:created"
  | "skill:updated"
  | "skill:deleted"
  | "subscriber:added"
  | "subscriber:removed"
  | "activity:created"
  | "reaction:added"
  | "reaction:removed"
  | "issue_reaction:added"
  | "issue_reaction:removed"
  | "chat:message"
  | "chat:done"
  | "chat:session_read"
  | "chat:session_deleted"
  | "chat:session_updated"
  | "channel:message"
  | "channel:message_updated"
  | "channel:typing"
  | "channel_reaction:added"
  | "channel_reaction:removed"
  | "channel:updated"
  | "channel:deleted"
  | "voice_call:updated"
  | "project:created"
  | "project:updated"
  | "project:deleted"
  | "label:created"
  | "label:updated"
  | "label:deleted"
  | "issue_labels:changed"
  | "issue_metadata:changed"
  | "pin:created"
  | "pin:deleted"
  | "pin:reordered"
  | "invitation:created"
  | "invitation:accepted"
  | "invitation:declined"
  | "invitation:revoked"
  | "github_installation:created"
  | "github_installation:deleted"
  | "pull_request:linked"
  | "pull_request:updated"
  | "pull_request:unlinked"
  | "sandbox:instance_created"
  | "sandbox:instance_updated"
  | "research_session:graph_updated"
  | "research_session:presence"
  | "research_session:sources_updated"
  | "research_session:report_updated"
  | "research_session:message"
  | "research_session:stage_eval"
  | "research_session:status_changed"
  | "research_session:product_round"
  | "research_projection_v6:delta";

export interface WSMessage<T = unknown> {
  type: WSEventType;
  payload: T;
  actor_id?: string;
  actor_type?: string;
}

export interface IssueCreatedPayload {
  issue: Issue;
}

export interface IssueUpdatedPayload {
  issue: Issue;
}

export interface IssueDeletedPayload {
  issue_id: string;
}

export interface IssueLabelsChangedPayload {
  issue_id: string;
  labels: Label[];
}

export interface IssueMetadataChangedPayload {
  issue_id: string;
  metadata: IssueMetadata;
}

export interface AgentStatusPayload {
  agent: Agent;
}

export interface AgentCreatedPayload {
  agent: Agent;
}

export interface AgentArchivedPayload {
  agent: Agent;
}

export interface AgentRestoredPayload {
  agent: Agent;
}

/** Fired when an agent persists a governed memory file (Phase① XP feedback). */
export interface AgentMemoryUpdatedPayload {
  agent_id: string;
  scope_type: string;
  file_key: string;
  count: number;
}

export interface DaemonRuntimeUpdatedPayload {
  runtime: AgentRuntime;
}

export interface ComputerUpdatedPayload {
  computer_id: string;
}

export interface ComputerUpgradeProgressPayload {
  computer_id: string;
  requestId: string;
  phase?: string;
  message?: string;
  percent?: number;
}

export interface ComputerUpgradeDonePayload {
  computer_id: string;
  requestId: string;
  ok: boolean;
  newVersion?: string;
  error?: string;
  rolledBack?: boolean;
}

export interface VoiceCallUpdatedPayload {
  workspace_id: string;
  call_id: string;
}

export interface InboxNewPayload {
  item: InboxItem;
}

export interface InboxReadPayload {
  item_id: string;
  recipient_id: string;
}

export interface InboxArchivedPayload {
  item_id: string;
  recipient_id: string;
}

export interface InboxBatchReadPayload {
  recipient_id: string;
  count: number;
}

export interface InboxBatchArchivedPayload {
  recipient_id: string;
  count: number;
}

export interface CommentCreatedPayload {
  comment: Comment;
}

export interface CommentUpdatedPayload {
  comment: Comment;
}

export interface CommentDeletedPayload {
  comment_id: string;
  issue_id: string;
}

export interface CommentResolvedPayload {
  comment: Comment;
}

export interface CommentUnresolvedPayload {
  comment: Comment;
}

export interface WorkspaceUpdatedPayload {
  workspace: Workspace;
}

export interface WorkspaceDeletedPayload {
  workspace_id: string;
}

export interface MemberUpdatedPayload {
  member: MemberWithUser;
}

export interface MemberAddedPayload {
  member: MemberWithUser;
  workspace_id: string;
  workspace_name?: string;
}

export interface MemberRemovedPayload {
  member_id: string;
  user_id: string;
  workspace_id: string;
}

export interface SubscriberAddedPayload {
  issue_id: string;
  user_type: string;
  user_id: string;
  reason: string;
}

export interface SubscriberRemovedPayload {
  issue_id: string;
  user_type: string;
  user_id: string;
}

export interface ActivityCreatedPayload {
  issue_id: string;
  entry: TimelineEntry;
}

export interface TaskMessagePayload {
  task_id: string;
  issue_id: string;
  chat_session_id?: string;
  seq: number;
  type: "text" | "thinking" | "tool_use" | "tool_result" | "error";
  tool?: string;
  content?: string;
  input?: Record<string, unknown>;
  output?: string;
  visibility?: "user_facing" | "diagnostic_only";
  created_at?: string;
}

/** `agent_reminder:changed` — a pure invalidate signal (schedule/snooze/update/cancel/fire/terminalize, emitted post-commit). Minimal on purpose: no title/anchor/reminder data broadcast, just the scope to refetch. */
export interface AgentReminderChangedPayload {
  agentId: string;
}

// The Workspace Runner Activity read-model is presentation-safe: callers must
// display these fields as supplied and never infer runtime state.
export interface RunnerActivitySummary {
  label: string;
  tone: string;
  visibility: string;
}

export interface RunnerActivityTimelineRow {
  id: string;
  occurred_at: string;
  title: string;
  subtext?: string;
  tone: string;
  body_kind: string;
  body?: string;
}

export interface RunnerActivityResponse {
  summary: RunnerActivitySummary | null;
  timeline: RunnerActivityTimelineRow[];
}

export interface RunnerActivitySummaryItem {
  agent_id: string;
  summary: RunnerActivitySummary;
}

export interface RunnerActivitySummariesResponse {
  items: RunnerActivitySummaryItem[];
}

export interface RunnerActivityRealtimePayload {
  agent_id: string;
  activity: RunnerActivityResponse;
}

export interface AgentPresenceRealtimePayload {
  agent_id: string;
  presence: "online" | "offline";
}

export interface TaskQueuedPayload {
  task_id: string;
  agent_id: string;
  issue_id: string;
  chat_session_id?: string;
  status: string;
}

export interface TaskDispatchPayload {
  task_id: string;
  agent_id: string;
  issue_id: string;
  runtime_id: string;
  chat_session_id?: string;
}

export interface TaskRunningPayload {
  task_id: string;
  agent_id: string;
  issue_id: string;
  chat_session_id?: string;
  status: string;
}

export interface TaskCompletedPayload {
  task_id: string;
  agent_id: string;
  issue_id: string;
  chat_session_id?: string;
  status: string;
}

export interface TaskFailedPayload {
  task_id: string;
  agent_id: string;
  issue_id: string;
  chat_session_id?: string;
  status: string;
}

export interface TaskCancelledPayload {
  task_id: string;
  agent_id: string;
  issue_id: string;
  chat_session_id?: string;
  status: string;
}

export interface ReactionAddedPayload {
  reaction: Reaction;
  issue_id: string;
}

export interface ReactionRemovedPayload {
  comment_id: string;
  issue_id: string;
  emoji: string;
  actor_type: string;
  actor_id: string;
}

export interface IssueReactionAddedPayload {
  reaction: IssueReaction;
  issue_id: string;
}

export interface IssueReactionRemovedPayload {
  issue_id: string;
  emoji: string;
  actor_type: string;
  actor_id: string;
}

export interface ChatMessageEventPayload {
  chat_session_id: string;
  message_id: string;
  role: "user" | "assistant";
  content: string;
  parts?: MessagePart[];
  task_id?: string;
  created_at: string;
}

export interface ChatDonePayload {
  chat_session_id: string;
  task_id: string;
  /**
   * Server populates these from the freshly-persisted assistant ChatMessage
   * row so the WS handler can write it into the messages cache inline. Older
   * servers (pre-#2123) only sent chat_session_id + task_id; treat every field
   * below as optional and fall back to a refetch when absent.
   */
  message_id?: string;
  content?: string;
  parts?: MessagePart[];
  elapsed_ms?: number;
  created_at?: string;
}

export interface ChatSessionReadPayload {
  chat_session_id: string;
}

export interface ChatSessionDeletedPayload {
  chat_session_id: string;
}

export interface ProjectCreatedPayload {
  project: Project;
}

export interface ProjectUpdatedPayload {
  project: Project;
}

export interface ProjectDeletedPayload {
  project_id: string;
}

export interface InvitationCreatedPayload {
  invitation: Invitation;
  workspace_name?: string;
}

export interface InvitationAcceptedPayload {
  invitation_id: string;
  member: MemberWithUser;
}

export interface InvitationDeclinedPayload {
  invitation_id: string;
  invitee_email: string;
}

export interface InvitationRevokedPayload {
  invitation_id: string;
  invitee_email: string;
}

export interface DaemonRuntimeUpdatedPayload {
  runtime: AgentRuntime;
}

/** LRM-462: human member online/offline from WS sessions. */
export interface MemberPresencePayload {
  user_id: string;
  status: "online" | "offline";
  observed_at?: string;
  workspace_id?: string;
}

export interface HonorBadgeUnlockedPayload {
  user_id: string;
  badge: {
    id: string;
    title: string;
    description: string;
    svg_key: string;
  };
  unlock_pct?: number;
}

export interface AgentHonorUnlockedPayload {
  agent_id: string;
  /** Added by newer servers; older event payloads may omit it. */
  agent_name?: string;
  achievement: AgentAchievement;
}

export interface AgentHonorLevelChangedPayload {
  agent_id: string;
  agent_name: string;
  previous_level: number;
  level: number;
}

export interface AgentFleetClassChangedPayload {
  agent_id: string;
  /** Added by newer servers; older servers require the cached agent-directory fallback. */
  agent_name?: string;
  previous_class_id: string;
  class_id: string;
  fleet_score: number;
}

/**
 * Maps every WSEventType to its payload interface. Events whose payload
 * shape isn't formally typed (server emits an object the client doesn't
 * meaningfully consume yet) fall back to `unknown` — callers must narrow
 * before access.
 *
 * Use via `WSEventPayload<E>` rather than indexing the map directly:
 *   const handler = (payload: WSEventPayload<"issue:created">) => { ... };
 *
 * Adding a new event: extend WSEventType first (above), then append a key
 * here. TS will compile-error every WSClient.on("new:event", …) site that
 * forgets the payload shape — that's the whole point.
 */
export interface WSEventPayloadMap {
  "issue:created": IssueCreatedPayload;
  "issue:updated": IssueUpdatedPayload;
  "issue:deleted": IssueDeletedPayload;
  "issue_labels:changed": IssueLabelsChangedPayload;
  "issue_reaction:added": IssueReactionAddedPayload;
  "issue_reaction:removed": IssueReactionRemovedPayload;
  "comment:created": CommentCreatedPayload;
  "comment:updated": CommentUpdatedPayload;
  "comment:deleted": CommentDeletedPayload;
  "comment:resolved": CommentResolvedPayload;
  "comment:unresolved": CommentUnresolvedPayload;
  "reaction:added": ReactionAddedPayload;
  "reaction:removed": ReactionRemovedPayload;
  "agent:status": AgentStatusPayload;
  "agent:created": AgentCreatedPayload;
  "agent:archived": AgentArchivedPayload;
  "agent:restored": AgentRestoredPayload;
  "agent:deleted": { agent_id: string };
  "agent.memory_updated": AgentMemoryUpdatedPayload;
  "task:queued": TaskQueuedPayload;
  "task:dispatch": TaskDispatchPayload;
  "task:running": TaskRunningPayload;
  "task:completed": TaskCompletedPayload;
  "task:failed": TaskFailedPayload;
  "task:message": TaskMessagePayload;
  "agent:activity": RunnerActivityRealtimePayload;
  "agent:presence": AgentPresenceRealtimePayload;
  "agent_reminder:changed": AgentReminderChangedPayload;
  "task:cancelled": TaskCancelledPayload;
  "task:progress": unknown;
  "inbox:new": InboxNewPayload;
  "inbox:read": InboxReadPayload;
  "inbox:archived": InboxArchivedPayload;
  "inbox:batch-read": InboxBatchReadPayload;
  "inbox:batch-archived": InboxBatchArchivedPayload;
  "workspace:updated": WorkspaceUpdatedPayload;
  "workspace:deleted": WorkspaceDeletedPayload;
  "member:added": MemberAddedPayload;
  "member:updated": MemberUpdatedPayload;
  "member:removed": MemberRemovedPayload;
  "member:presence": MemberPresencePayload;
  "honor:badge_unlocked": HonorBadgeUnlockedPayload;
  "agent_honor:achievement_unlocked": AgentHonorUnlockedPayload;
  "agent_honor:level_changed": AgentHonorLevelChangedPayload;
  "agent_honor:fleet_class_changed": AgentFleetClassChangedPayload;
  "subscriber:added": SubscriberAddedPayload;
  "subscriber:removed": SubscriberRemovedPayload;
  "activity:created": ActivityCreatedPayload;
  "chat:message": ChatMessageEventPayload;
  "chat:done": ChatDonePayload;
  "chat:session_read": ChatSessionReadPayload;
  "chat:session_deleted": ChatSessionDeletedPayload;
  "chat:session_updated": unknown;
  "channel:message": ChannelMessage;
  "channel:message_updated": ChannelMessage;
  "channel:typing": ChannelTypingPayload;
  "channel_reaction:added": { reaction: ChannelReaction; channel_id: string; message_id: string };
  "channel_reaction:removed": { channel_id: string; message_id: string; emoji: string; actor_type: string; actor_id: string };
  "channel:updated": unknown;
  "channel:deleted": { id: string };
  "voice_call:updated": VoiceCallUpdatedPayload;
  "project:created": ProjectCreatedPayload;
  "project:updated": ProjectUpdatedPayload;
  "project:deleted": ProjectDeletedPayload;
  "invitation:created": InvitationCreatedPayload;
  "invitation:accepted": InvitationAcceptedPayload;
  "invitation:declined": InvitationDeclinedPayload;
  "invitation:revoked": InvitationRevokedPayload;
  // No formal payload interfaces yet — server emits domain objects clients
  // currently consume as opaque triggers (refetch on receipt).
  "daemon:heartbeat": unknown;
  "daemon:register": unknown;
  "daemon:runtime_updated": DaemonRuntimeUpdatedPayload;
  "computer:updated": ComputerUpdatedPayload;
  "computer:upgrade:progress": ComputerUpgradeProgressPayload;
  "computer:upgrade:done": ComputerUpgradeDonePayload;
  "skill:created": unknown;
  "skill:updated": unknown;
  "skill:deleted": unknown;
  "label:created": unknown;
  "label:updated": unknown;
  "label:deleted": unknown;
  "pin:created": unknown;
  "pin:deleted": unknown;
  "pin:reordered": unknown;
  "github_installation:created": unknown;
  "github_installation:deleted": unknown;
  "pull_request:linked": unknown;
  "pull_request:updated": unknown;
  "pull_request:unlinked": unknown;
  "research_session:graph_updated": unknown;
  "research_session:presence": unknown;
  "research_session:sources_updated": unknown;
  "research_session:report_updated": unknown;
  "research_session:message": unknown;
  "research_session:stage_eval": unknown;
  "research_session:status_changed": unknown;
  "research_projection_v6:delta": {
    run_id: string;
    through_sequence?: number;
    delta?: unknown;
  };
}

/**
 * Payload type for a given event. Lookup against WSEventPayloadMap with
 * `unknown` as the safety net — if a future WSEventType is added without
 * a map entry, callers see `unknown` (forced narrow) rather than `any`
 * (silent unsafe access).
 */
export type WSEventPayload<E extends WSEventType> =
  E extends keyof WSEventPayloadMap ? WSEventPayloadMap[E] : unknown;
