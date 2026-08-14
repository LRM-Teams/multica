import type { RuntimeTokenStats } from "./chat";
import type { MessagePart } from "./message-part";

/** LRM-748 / LRM-769 — four-level per-channel notify preference.
 *  `"default"` follows the workspace-global notification settings. */
export type ChannelNotifyLevel = "default" | "all" | "mentions" | "muted";

export type ChannelGoalStatus = "active" | "paused" | "completed" | "cancelled";

export interface ChannelGoalWorkGraphSummary {
  id: string;
  version: number;
  status: "active" | "paused" | "deliverable" | "completed" | "cancelled" | "failed";
  total: number;
  completed: number;
  running: number;
  waiting: number;
  stale: number;
}

export type ChannelGoalExecutionAdmission =
  | "direct"
  | "project_required"
  | "git_required"
  | "issues_required"
  | "ready"
  | "acceptance_required"
  | "unavailable";

export interface ChannelGoalCoordinationSummary {
  project_id?: string;
  git_repository_bound: boolean;
  agent_member_count: number;
  channel_issue_total: number;
  channel_project_issue_total: number;
  project_issue_total: number;
  open_project_issue_total: number;
  in_review_project_issue_total: number;
  subgoal_total: number;
  open_subgoal_total: number;
  execution_admission: ChannelGoalExecutionAdmission;
}

export interface WorkGraphNode {
  id: string; issue_id: string; role: string; context_policy: string;
  execution_status: string; validity_status: string; review_status: string;
  completion_authority: "issue_status" | "kernel_evidence";
  effective_completion: "pending" | "satisfied" | "stale" | "revoked";
  objective: string; completion_contract: string[]; based_on_graph_version: number;
}
export interface WorkGraphEdge { id: string; from_node_id: string; to_node_id: string; edge_type: string; required: boolean; }
export interface WorkGraphDetail {
  id: string; workspace_id: string; anchor_kind: string; anchor_id: string; status: string;
  current_version: number; admission_decision: "GRAPH" | "PROPOSE_GRAPH";
  nodes: WorkGraphNode[]; edges: WorkGraphEdge[];
}

export interface ChannelGoal {
  id: string;
  workspace_id: string;
  channel_id: string;
  title: string;
  objective: string;
  success_criteria: string[];
  status: ChannelGoalStatus;
  version: number;
  progress_summary: string;
  current_step: string;
  blocker: string;
  evidence_refs: string[];
  completed_criteria: string[];
  created_by_type: "user" | "agent";
  created_by_id: string;
  updated_by_type: "user" | "agent";
  updated_by_id: string;
  created_at: string;
  updated_at: string;
  completed_at?: string;
  work_graph?: ChannelGoalWorkGraphSummary;
  coordination?: ChannelGoalCoordinationSummary;
}

export interface ChannelGoalEnvelope {
  goal: ChannelGoal | null;
}

export interface CreateChannelGoalRequest {
  title: string;
  objective: string;
  success_criteria: string[];
}

export interface UpdateChannelGoalRequest {
  expected_version: number;
  title?: string;
  objective?: string;
  success_criteria?: string[];
  status?: ChannelGoalStatus;
  progress_summary?: string;
  current_step?: string;
  blocker?: string;
  evidence_refs?: string[];
  completed_criteria?: string[];
}

/** LRM-931/932 — per-manager process Markdown under the channel's single goal. */
export interface ChannelGoalProcessMarkdown {
  id: string;
  workspace_id: string;
  channel_id: string;
  goal_id: string;
  manager_agent_id: string;
  content: string;
  version: number;
  updated_by_type: "user" | "agent";
  updated_by_id: string;
  created_at: string;
  updated_at: string;
}

export interface ChannelGoalProcessEnvelope {
  process: ChannelGoalProcessMarkdown | null;
}

export interface ChannelGoalProcessListEnvelope {
  goal_id: string;
  processes: ChannelGoalProcessMarkdown[];
}

export interface UpsertChannelGoalProcessRequest {
  content: string;
  expected_version: number;
}

export interface ChannelLastMessage {
  type: "user" | "agent" | "lark" | "system";
  author_name: string;
  content: string;
  parts?: MessagePart[];
  created_at: string;
}

export interface ChannelMemberBrief {
  member_type: "user" | "agent";
  member_id: string;
  /** Stable handle. */
  name: string;
  /** Human-facing member label. */
  display_name: string;
  avatar_url?: string | null;
  runtime_stats?: RuntimeTokenStats | null;
}

export interface Channel {
  id: string;
  workspace_id: string;
  name: string;
  kind: "group" | "dm";
  description: string | null;
  lark_chat_id: string | null;
  /** Custom group avatar (uploaded-file link). Absent/null → `#` landmark. */
  avatar_url?: string | null;
  created_by: string;
  created_at: string;
  updated_at: string;
  archived_at?: string | null;
  archived_by?: string | null;
  /** List-only enrichments (absent on create/update/get responses). */
  unread_count?: number;
  /** True unread count excluding the manually-unread boost. Added by BE task #24.
   *  Falls back to unread_count when absent so old API responses still work. */
  real_unread_count?: number;
  manually_unread?: boolean;
  /** Number of unread messages in this channel that @-mention the viewer. */
  mention_unread_count?: number;
  /** Viewer's read cursor for this conversation (`conversation_member.last_read_seq`).
   *  List/detail enrichment; drives the "N new messages" divider pinned on entry. */
  last_read_seq?: number;
  pinned_at?: string | null;
  muted_at?: string | null;
  muted?: boolean;
  /** LRM-748 / LRM-769 — viewer's per-channel notify level. Contract: the API
   *  always returns one of the four literals (`NULL` in DB → `"default"`).
   *  Optional only until LRM-769 ships; before that, callers derive the
   *  backfill mapping (`muted_at` set → `"mentions"`, else `"default"`). */
  notify_level?: ChannelNotifyLevel;
  last_message?: ChannelLastMessage | null;
  /** Bounded avatar stack for list rows; full roster loads via the channel-members query. */
  members?: ChannelMemberBrief[];
  /** #642 — server-owned identity for an immutable system channel (e.g.
   *  "general"). Absent/unknown values must degrade to a normal channel,
   *  never a blank screen — this is a capability marker, not a display
   *  name or a second source of the channel's `name`. */
  system_key?: string | null;
}

export interface ChannelMember {
  member_type: "user" | "agent";
  member_id: string;
  /** Stable handle. */
  name: string;
  /** Human-facing member label. */
  display_name: string;
  avatar_url?: string | null;
  runtime_stats?: RuntimeTokenStats | null;
  created_at: string;
  /**
   * Group membership role (group management, BE #1284). `owner` is the human
   * group owner; `manager` is elevated (group manager for agents, admin for
   * humans); `member` is ordinary. Optional — older backends omit it; treat a
   * missing value as `member` (least-privileged fallback).
   */
  role?: ChannelMemberRole;
}

/** LRM-622 — invite picker row from GET /api/channels/:id/invite-candidates. */
export interface ChannelInviteCandidate {
  member_type: "user" | "agent";
  member_id: string;
  name: string;
  display_name: string;
  email?: string;
  avatar_url?: string | null;
  role?: string;
}

export interface ChannelInviteCandidatesResponse {
  candidates: ChannelInviteCandidate[];
}

/** GET /api/channels/:id/mention-candidates row. */
export interface ChannelMentionCandidate {
  type: "member" | "agent";
  id: string;
  handle: string;
  label: string;
  avatar_url?: string | null;
}

export interface ChannelMentionCandidatesResponse {
  in_channel: ChannelMentionCandidate[];
  not_in_channel: ChannelMentionCandidate[];
  has_more: boolean;
  next_offset?: number | null;
}

export type ChannelMemberRole = "owner" | "manager" | "member";

/**
 * LRM-872 / LRM-879 — GET /api/channels/:id/member-management-capabilities.
 * Server-authored per-row gates (WS admin + inviter can_remove for self-added
 * agents). FE must prefer these over local channel-role heuristics.
 */
export interface ChannelMemberManagementCapabilityTarget {
  member_type: "user" | "agent";
  member_id: string;
  display_name: string;
  avatar_url?: string | null;
  role: ChannelMemberRole | string;
  can_remove: boolean;
  can_promote_to_manager: boolean;
  can_demote_to_member: boolean;
  can_transfer_ownership: boolean;
}

export interface ChannelMemberManagementCapabilities {
  channel_id: string;
  name: string;
  kind: string;
  archived: boolean;
  can_add_members: boolean;
  can_remove_members: boolean;
  can_leave: boolean;
  targets: ChannelMemberManagementCapabilityTarget[];
}

export interface ChannelReaction {
  id: string;
  channel_id: string;
  message_id: string;
  actor_type: string;
  actor_id: string;
  emoji: string;
  created_at: string;
}

export interface ChannelThreadParticipant {
  key: string;
  member_type: string;
  member_id: string;
  name: string;
  display_name: string;
  followed: boolean;
}

/**
 * Alice #1984 / Raft-aligned undelivered @ on public groups.
 * Message is stored; delivery withheld until the sender invites.
 * Omitted from list/history responses when empty.
 */
export interface UndeliveredMention {
  /** `member` (user) or `agent`. */
  type: "member" | "agent" | string;
  id: string;
  handle?: string;
  label?: string;
  /** e.g. `not_channel_member` */
  reason: string;
  /** e.g. `["invite"]` — `notify` reserved. */
  actions: string[];
}

export interface ChannelMessage {
  id: string;
  channel_id: string;
  workspace_id: string;
  seq: number;
  type: "user" | "agent" | "lark" | "system";
  author_id: string | null;
  author_name: string;
  content: string;
  parts?: MessagePart[];
  source: "multica" | "lark";
  external_message_id: string | null;
  client_message_id: string | null;
  reply_to_message_id?: string | null;
  reply_to?: ChannelMessageReply | null;
  quote_message_id?: string | null;
  quote?: ChannelMessageQuote | null;
  thread_root_message_id?: string | null;
  thread_root?: ChannelMessageReply | null;
  thread_reply_count?: number;
  thread_last_reply_at?: string | null;
  thread_unread_count?: number;
  thread_followed?: boolean;
  thread_participants?: ChannelThreadParticipant[];
  thread_id?: string | null;
  trigger_depth?: number;
  reactions?: ChannelReaction[];
  /**
   * File resources referenced by this message through the canonical
   * many-to-many association. Populated by ListChannelMessages. The bubble
   * renders these as file/image cards; the markdown URL inline in `content`
   * may carry an expiring signature, while this metadata is stable.
   */
  attachments?: import("./attachment").Attachment[];
  /**
   * Structured @ targets who are not channel members yet (group send only).
   * Present on the send ACK when BE #1984 is deployed; omit when empty.
   */
  undelivered_mentions?: UndeliveredMention[];
  created_at: string;
  /** Set once the author has edited the message; drives the "(edited)" label. */
  edited_at?: string | null;
  /**
   * Set once the message is soft-deleted. Thread roots with live replies render
   * a tombstone placeholder; deleted messages without replies can be omitted.
   */
  deleted_at?: string | null;
  /**
   * Client-only send lifecycle for optimistic bubbles (LRM-222). Never set by
   * the API — pending until HTTP/WS ACK replaces the temp id, or failed with
   * one-click retry reusing `client_message_id`.
   */
  local_send_status?: "pending" | "failed" | null;
}

export interface ChannelMessagesCursor {
  seq: number;
  created_at: string;
  id: string;
}

export interface ChannelMessagesPage {
  messages: ChannelMessage[];
  limit: number;
  has_more: boolean;
  next_cursor?: ChannelMessagesCursor | null;

  // around_seq mode only (task #340). Absent for the default/before-cursor page.
  /**
   * Index in `messages` (ascending) of the last already-read message — the
   * anchor. The first UNREAD row is `anchor_index + 1` (what an unread cold-open
   * pins to the top). `-1` when the window has no read message (whole window is
   * unread) → pin the first row. Only meaningful when the request used
   * `around_seq`; the caller decides based on the request it made (the server
   * omits the field entirely, and its `0` value, outside around mode).
   */
  anchor_index?: number;
  /** More messages exist NEWER than the window (around_seq mode). */
  has_more_after?: boolean;
  /** Cursor to page toward NEWER messages, mirroring `next_cursor` (around_seq mode). */
  after_cursor?: ChannelMessagesCursor | null;
  /**
   * True unread total for the "N new messages" divider (around_seq mode) —
   * messages with `seq > around_seq`, main-timeline-visible, authored by someone
   * other than the viewer. Computed server-side in the SAME around query, so it
   * is a snapshot of the entry moment by construction (the response is fetched
   * once). This is the PREFERRED divider count: the loaded window holds only
   * ~limit/2 messages past the anchor, so counting within it undercounts large
   * unread. Absent on older servers → fall back to the entry-frozen list count,
   * then the window count.
   */
  unread_total?: number;
}

/** Response of `POST /channels/{id}/read`. `previous_last_read_seq` echoes the
 *  member's read cursor *before* this call advanced it — the entry-moment read
 *  point, used to pin the "N new messages" divider race-free (BE #301). */
export interface MarkChannelReadResult {
  ok: boolean;
  previous_last_read_seq: number | null;
}

export interface ChannelThreadMessagesCursor {
  before_seq?: number;
  before: string;
  before_id: string;
}

export interface ChannelThreadMessagesPage {
  messages: ChannelMessage[];
  next_cursor: ChannelThreadMessagesCursor | null;
}

export interface ChannelMessageReply {
  id: string;
  type: "user" | "agent" | "lark" | "system";
  author_id: string | null;
  author_name: string;
  content: string;
  parts?: MessagePart[];
  created_at: string;
}

export interface ChannelMessageQuoteSnapshot {
  type: "user" | "agent" | "lark" | "system";
  authorId?: string | null;
  authorName: string;
  content: string;
  parts?: MessagePart[];
  createdAt: string;
  selectedText?: string | null;
}

export interface ChannelMessageQuote {
  messageId: string;
  snapshot?: ChannelMessageQuoteSnapshot | null;
  status: "active" | "deleted" | "inaccessible" | (string & {});
}

export interface ChannelMessageQuoteInput {
  messageId: string;
  selectedText?: string;
}

export interface ChannelMessageSearchResult {
  message_id: string;
  channel_id: string;
  thread_root_message_id?: string | null;
  /** True when the hit is a thread reply (thread_root_message_id set). */
  in_thread?: boolean;
  type: "user" | "agent" | "lark" | "system";
  author_id: string | null;
  author_name: string;
  content: string;
  parts?: MessagePart[];
  created_at: string;
}

export interface ChannelMessageSearchParams {
  q?: string;
  /** Filter to a member or agent author (from:@). Requires author_type. */
  author_id?: string;
  author_type?: "user" | "agent";
  /** Default true: include thread replies. false = mainline only. */
  include_thread?: boolean;
  limit?: number;
  offset?: number;
}

export interface ChannelMessageSearchResponse {
  query: string;
  total: number;
  include_thread?: boolean;
  author_type?: string;
  author_id?: string;
  scope?: string;
  results: ChannelMessageSearchResult[];
}

export interface ChannelAuthorStat {
  author_type: "user" | "agent" | "lark" | "system";
  author_id: string | null;
  author_name: string;
  count: number;
}

export interface ChannelStats {
  total_messages: number;
  file_count: number;
  member_count: number;
  by_author: ChannelAuthorStat[];
}

/** One entry in a project workdir listing; `path` is relative to the workdir
 *  root (forward slashes). The tree is rebuilt client-side from these paths. */
export interface ChannelProjectFile {
  path: string;
  is_dir: boolean;
  size?: number;
}

export type ChannelProjectFilesStatus =
  | "ok"
  | "no_project"
  | "offline"
  | "missing"
  | "github_unlinked"
  | "error";

export interface ChannelProjectFiles {
  project_id: string;
  status: ChannelProjectFilesStatus;
  nodes: ChannelProjectFile[];
  truncated: boolean;
}

/** A single project file's preview content (read from the daemon workdir). For
 *  text, `content` is UTF-8 and `encoding` is empty; for media it's base64 with
 *  `encoding` "base64" and `mime_type` set so the client can render it. */
export interface ChannelProjectFileContent {
  content: string;
  encoding: string;
  mime_type: string;
  truncated: boolean;
  too_large: boolean;
  binary: boolean;
}

export interface ChannelTypingPayload {
  channel_id: string;
  actor_type: "user" | "agent" | "lark" | "system";
  actor_id?: string;
  actor_name: string;
  is_typing: boolean;
  expires_in_ms?: number;
}


/** LRM-1004 / LRM-1005 — subgoal statuses. */
export type ChannelGoalSubgoalStatus =
  | "captured"
  | "in_progress"
  | "waiting"
  | "resolved"
  | "cancelled";

export interface ChannelGoalSubgoalActor {
  type: "agent" | "member";
  id: string;
}

export interface ChannelGoalSubgoalWaitingOn {
  kind: "member" | "issue" | "pr" | "lock" | "external";
  target_id?: string;
  note?: string;
}

export interface ChannelGoalSubgoal {
  id: string;
  workspace_id: string;
  channel_id: string;
  goal_id: string;
  title: string;
  purpose: string;
  completion_boundary: string;
  brief: string;
  current_conclusion: string;
  status: ChannelGoalSubgoalStatus;
  version: number;
  responsible_type: "agent" | "member" | string;
  responsible_id: string;
  participants: ChannelGoalSubgoalActor[];
  depends_on: string[];
  waiting_on: ChannelGoalSubgoalWaitingOn | null;
  artifact_refs: string[];
  activity_delta: string[];
  /** LRM-1007: optional same-channel message for "jump back"; omit when unset. */
  source_message_id?: string;
  created_by_type: string;
  created_by_id: string;
  updated_by_type: string;
  updated_by_id: string;
  created_at: string;
  updated_at: string;
  resolved_at?: string;
}

export interface ChannelGoalSubgoalListEnvelope {
  subgoals: ChannelGoalSubgoal[];
}

export interface ChannelGoalSubgoalEnvelope {
  subgoal: ChannelGoalSubgoal | null;
}

export interface CreateChannelGoalSubgoalRequest {
  title: string;
  purpose: string;
  completion_boundary?: string;
  brief?: string;
  responsible: ChannelGoalSubgoalActor;
  participants?: ChannelGoalSubgoalActor[];
  depends_on?: string[];
  artifact_refs?: string[];
  /** Same-channel message id; omit when no source. */
  source_message_id?: string;
}

export interface UpdateChannelGoalSubgoalRequest {
  expected_version: number;
  title?: string;
  purpose?: string;
  completion_boundary?: string;
  brief?: string;
  current_conclusion?: string;
  status?: ChannelGoalSubgoalStatus;
  responsible?: ChannelGoalSubgoalActor;
  participants?: ChannelGoalSubgoalActor[];
  depends_on?: string[];
  artifact_refs?: string[];
  activity_delta?: string[];
  waiting_on?: ChannelGoalSubgoalWaitingOn | null;
  /** Omit = unchanged; empty string = clear; UUID = set (must be same channel). */
  source_message_id?: string;
}

export interface ResolveChannelGoalSubgoalRequest {
  expected_version: number;
  current_conclusion: string;
}

export interface ClearChannelGoalSubgoalWaitingOnRequest {
  expected_version: number;
  verification: {
    kind: string;
    target_id?: string;
    issue_status_ok?: boolean;
    acknowledged?: boolean;
    released?: boolean;
    external_ok?: boolean;
  };
}
