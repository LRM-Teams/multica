import type { Label } from "./label";
import type { MessagePart } from "./message-part";

export type IssueStatus =
  | "backlog"
  | "todo"
  | "in_progress"
  | "in_review"
  | "done"
  | "blocked"
  | "cancelled";

export type IssuePriority = "urgent" | "high" | "medium" | "low" | "none";

export type IssueAssigneeType = "member" | "agent";

export interface IssueReaction {
  id: string;
  issue_id: string;
  actor_type: string;
  actor_id: string;
  emoji: string;
  created_at: string;
}

/**
 * Per-issue metadata is a flat KV map agents use to record pipeline state
 * (PR number, pipeline_status, waiting_on, ...). Values are primitives only —
 * string / number / bool — enforced by both the API and the DB. Always
 * present in responses (empty object when unset) so reads don't need a
 * nil guard on the parent field.
 */
export type IssueMetadataValue = string | number | boolean;
export type IssueMetadata = Record<string, IssueMetadataValue>;

// Overview "pending human approval" KPI: issues awaiting human review and how
// long the oldest has waited (now − the oldest entered-in_review time).
export interface IssueReviewStats {
  count: number;
  longest_wait_seconds: number;
}

/**
 * Detail-only navigation back to the chat message that caused an agent to
 * create this issue (#466/#470). The server owns every display field, gates
 * visibility on the requester's channel membership, and canonicalizes a
 * reply-triggered anchor to its thread root — so `message_id` is always the
 * message to deep-link to. `channel_name` is present for group channels only.
 * Omitted entirely when there is no source or the caller can't see the channel.
 */
export interface IssueSourceMessageRef {
  channel_id: string;
  channel_name?: string;
  channel_kind: string;
  message_id: string;
  thread_root_message_id: string;
  excerpt: string;
  /** Reference parts whose UTF-16 spans are anchored to `excerpt`. */
  excerpt_parts?: MessagePart[];
}

export interface IssueSourceRefs {
  message?: IssueSourceMessageRef;
}

/**
 * The group (channel) an issue is associated with — 1:1 (#574). Distinct from
 * `source_refs.message`: an issue can be linked to a group without originating
 * from a specific message. Set/cleared via `PUT /api/issues/{id}/channel`.
 */
export interface IssueSourceChannelRef {
  channel_id: string;
  channel_name?: string | null;
  channel_kind: string;
}

/** A group (channel) bound to a project (#574 / #629). Returned by
 *  `GET /api/projects/{id}/channels` for the project detail page. */
export interface ProjectChannel {
  id: string;
  workspace_id: string;
  project_id: string;
  name: string;
  description?: string | null;
  kind: string;
  created_at: string;
  updated_at: string;
}

export interface Issue {
  id: string;
  workspace_id: string;
  number: number;
  identifier: string;
  title: string;
  description: string | null;
  status: IssueStatus;
  priority: IssuePriority;
  assignee_type: IssueAssigneeType | null;
  assignee_id: string | null;
  creator_type: IssueAssigneeType;
  creator_id: string;
  parent_issue_id: string | null;
  project_id: string | null;
  position: number;
  // Calendar days as date-only "YYYY-MM-DD" (no time, no timezone). Use the
  // helpers in @multica/core/issues/date to format/compare — never `new Date()`
  // + local formatting, which shifts the day by the viewer's offset.
  start_date: string | null;
  due_date: string | null;
  metadata: IssueMetadata;
  reactions?: IssueReaction[];
  labels?: Label[];
  // Present only on the single-issue GET (`api.getIssue`), never on list/search
  // responses. Absent when the issue has no chat origin or the viewer can't see
  // the source channel.
  source_refs?: IssueSourceRefs;
  /** The associated group (#574 / #629), 1:1. Present when the issue is linked
   *  to a group channel; edited in Issue Properties. */
  channel?: IssueSourceChannelRef | null;
  created_at: string;
  updated_at: string;
}
