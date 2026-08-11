import type { ChannelMessage } from "@multica/core/types";

/**
 * The parsers below only ever read `type` + `parts` — narrowing to this shape
 * (rather than requiring a full `ChannelMessage`) lets the same parsers run
 * against a channel/DM list row's lighter `ChannelLastMessage` too (#634), so
 * the sidebar preview can share one source of truth with the in-channel row
 * instead of re-deriving its own copy from the raw fallback `content`.
 */
export type SystemEventSource = Pick<ChannelMessage, "type" | "parts">;

/**
 * Member-change system events emitted by the backend (#450). The BE writes a
 * `type=system` message carrying BOTH a canonical fallback `content` string and
 * a typed `parts:[{type:"system_event", event, event_params}]` payload. The FE composes its own copy
 * from the structured params (see channel-system-event-content.tsx) so it can
 * render Raft/Slack-style quiet inline rows with clickable @username tokens.
 *
 * Pure (no JSX) so it can live alongside the component file without tripping
 * Fast Refresh's component-file-only-exports rule.
 */

export const MEMBER_EVENTS = {
  added: "channel_member_added",
  removed: "channel_member_removed",
  left: "channel_member_left",
} as const;

export type MemberSystemEventKind = (typeof MEMBER_EVENTS)[keyof typeof MEMBER_EVENTS];

const MEMBER_EVENT_KINDS = new Set<string>(Object.values(MEMBER_EVENTS));

export interface MemberSystemEvent {
  event: MemberSystemEventKind;
  actorId?: string;
  /** #456 fact layer: "human" | "agent". Absent on older/bridge messages. */
  actorType?: string;
  /** #456: canonical @handle (username). Absent on older messages. */
  actorHandle?: string;
  actorName?: string;
  targetId: string;
  /** #456 fact layer: "human" | "agent". Absent on older/bridge messages. */
  targetType?: string;
  /** #456: canonical @handle (username). Absent on older messages. */
  targetHandle?: string;
  targetName?: string;
  /**
   * #661: why the membership row changed — "manual" for an authenticated
   * actor's own action, "system_invariant" for rows the backend maintains on
   * its own (e.g. the immutable #general roster sync). Absent on rows older
   * than this field; the content projection falls back to `actorId` presence
   * either way so no row can render a dangling "by" with no actor.
   */
  source?: string;
}

/**
 * Issue-lifecycle backflow events (#497). The BE mirrors a narrow set of issue
 * facts into the originating discussion as `type=system` rows carrying a
 * `system_event` part (the factual transition) alongside an anchored `issue-ref`
 * reference part (the token). The FE composes its OWN user-facing copy from the
 * structured params — "任务" only, localized action verbs, never a raw status
 * enum or the internal word "issue" (item #7 frozen口径).
 */
export const ISSUE_EVENTS = {
  created: "issue_created",
  assigned: "issue_assigned",
  statusChanged: "issue_status_changed",
  completed: "issue_completed",
} as const;

export type IssueSystemEventKind = (typeof ISSUE_EVENTS)[keyof typeof ISSUE_EVENTS];

const ISSUE_EVENT_KINDS = new Set<string>(Object.values(ISSUE_EVENTS));

export interface IssueSystemEvent {
  event: IssueSystemEventKind;
  /** Issue uuid — the ref_id the inline token links/hovers on. */
  issueId: string;
  /** Canonical identifier ("LRM-137") — muted secondary when title is present. */
  issueIdentifier: string;
  /**
   * Human title from BE (`issue_title`, LRM-422/496). Primary ink when present;
   * never invent client-side (LRM-238 / LRM-423).
   */
  issueTitle?: string;
  /**
   * New status enum (never rendered raw; drives the localized verb). Absent for
   * `issue_created` (#610), whose verb is fixed ("创建了这个 issue") and carries no
   * status — every other issue event derives its verb from this field.
   */
  issueStatus?: string;
  previousStatus?: string;
  actorId?: string;
  /** Public actor type: "human" | "agent" (see channelMemberSystemEventPublicType). */
  actorType?: string;
  /** Canonical @handle (username). Present on new backflow rows. */
  actorHandle?: string;
  /** Optional emit-time display name (diagnostics only — FE must not fallback; LRM-281). */
  actorName?: string;
  targetId?: string;
  targetType?: string;
  targetHandle?: string;
  targetName?: string;
}

/**
 * One constituent transition inside a server-aggregated issue system event
 * (LRM-418 / LRM-422 / LRM-423). The BE merges same-actor + same-type events
 * across issues inside a 5-minute window into a single `system_event` row whose
 * `event_params.items` lists every surviving fact — FE must NEVER invent this
 * list client-side (LRM-238).
 */
export interface IssueAggregateSystemEventItem {
  issueId: string;
  issueIdentifier: string;
  /** Optional BE stamp (`issue_title`) — title-primary when present (LRM-423). */
  issueTitle?: string;
  issueStatus?: string;
  previousStatus?: string;
  targetId?: string;
  targetType?: string;
  /** ISO timestamp of the original transition when the BE stamps it. */
  occurredAt?: string;
}

/**
 * Server-authored aggregate of N same-kind issue transitions. Present only when
 * `event_params.items` is a non-empty array of valid items — missing/empty/
 * malformed `items` is NOT treated as an aggregate (no silent fake merge).
 */
export interface IssueAggregateSystemEvent {
  event: IssueSystemEventKind;
  items: IssueAggregateSystemEventItem[];
  actorId?: string;
  actorType?: string;
  actorHandle?: string;
  actorName?: string;
}

function optString(params: Record<string, unknown>, key: string): string | undefined {
  const value = params[key];
  return typeof value === "string" && value ? value : undefined;
}

function parseIssueAggregateItems(
  params: Record<string, unknown>,
): IssueAggregateSystemEventItem[] | null {
  const raw = params.items;
  if (!Array.isArray(raw) || raw.length === 0) return null;
  const items: IssueAggregateSystemEventItem[] = [];
  for (const entry of raw) {
    if (!entry || typeof entry !== "object") return null;
    const row = entry as Record<string, unknown>;
    const issueId = optString(row, "issue_id");
    const issueIdentifier = optString(row, "issue_identifier");
    // Every item must carry a resolvable issue token — refuse the whole
    // aggregate rather than silently drop a fact (LRM-238).
    if (!issueId || !issueIdentifier) return null;
    items.push({
      issueId,
      issueIdentifier,
      issueTitle: optString(row, "issue_title"),
      issueStatus: optString(row, "issue_status"),
      previousStatus: optString(row, "previous_status"),
      targetId: optString(row, "target_id"),
      targetType: optString(row, "target_type"),
      occurredAt: optString(row, "occurred_at"),
    });
  }
  // LRM-422 stamps occurred_at per item — expand list stays chronological.
  items.sort((a, b) => {
    if (a.occurredAt && b.occurredAt && a.occurredAt !== b.occurredAt) {
      return a.occurredAt < b.occurredAt ? -1 : 1;
    }
    if (a.occurredAt && !b.occurredAt) return -1;
    if (!a.occurredAt && b.occurredAt) return 1;
    return a.issueId < b.issueId ? -1 : a.issueId > b.issueId ? 1 : 0;
  });
  return items;
}

/**
 * Extract a server-aggregated issue-lifecycle event (LRM-423). Returns null
 * unless the BE stamped a non-empty, fully-valid `items` array on a known issue
 * event — FE does not fold consecutive single-issue rows into a fake group.
 */
export function parseIssueAggregateSystemEvent(
  message: SystemEventSource,
): IssueAggregateSystemEvent | null {
  if (message.type !== "system" || !Array.isArray(message.parts)) return null;
  for (const part of message.parts) {
    if (part.type !== "system_event" || !ISSUE_EVENT_KINDS.has(part.event)) continue;
    const params = part.event_params;
    const items = parseIssueAggregateItems(params);
    if (!items) continue;
    return {
      event: part.event as IssueSystemEventKind,
      items,
      actorId: optString(params, "actor_id"),
      actorType: optString(params, "actor_type"),
      actorHandle: optString(params, "actor_handle"),
      actorName: optString(params, "actor_name"),
    };
  }
  return null;
}

/**
 * Extract the structured issue-lifecycle event from a system message's parts.
 * Returns null for anything that isn't one (member changes, archive notices,
 * or old backflow rows that predate the `system_event` part and carry only the
 * reference — those keep the raw-content projection). Identity comes entirely
 * from the `system_event` params, so the projection never parses the fallback
 * `content` string.
 *
 * Server aggregates (non-empty `items`) are owned by
 * {@link parseIssueAggregateSystemEvent} — this parser deliberately skips them
 * so a single row never projects both a summary and a lone issue sentence.
 */
export function parseIssueSystemEvent(message: SystemEventSource): IssueSystemEvent | null {
  if (message.type !== "system" || !Array.isArray(message.parts)) return null;
  for (const part of message.parts) {
    if (part.type !== "system_event" || !ISSUE_EVENT_KINDS.has(part.event)) continue;
    const params = part.event_params;
    // Aggregate rows carry `items`; leave them to the aggregate parser.
    if (Array.isArray(params.items)) continue;
    const issueId = optString(params, "issue_id");
    const issueIdentifier = optString(params, "issue_identifier");
    const issueStatus = optString(params, "issue_status");
    // Every row needs the id + identifier to project the "任务 <ref> <verb>"
    // sentence with a clickable token. `issue_created` (#610) has a fixed verb
    // and carries no status; every other issue event derives its verb FROM the
    // status, so a non-created row missing it can't be projected — fall back to
    // the raw-content path.
    if (!issueId || !issueIdentifier) continue;
    if (part.event !== ISSUE_EVENTS.created && !issueStatus) continue;
    return {
      event: part.event as IssueSystemEventKind,
      issueId,
      issueIdentifier,
      issueTitle: optString(params, "issue_title"),
      issueStatus,
      previousStatus: optString(params, "previous_status"),
      actorId: optString(params, "actor_id"),
      actorType: optString(params, "actor_type"),
      actorHandle: optString(params, "actor_handle"),
      actorName: optString(params, "actor_name"),
      targetId: optString(params, "target_id"),
      targetType: optString(params, "target_type"),
      targetHandle: optString(params, "target_handle"),
      targetName: optString(params, "target_name"),
    };
  }
  return null;
}

/**
 * The equivalence class two consecutive issue events must share to fold into one
 * row. A `completed` event and a `status→done` event for the SAME issue collapse
 * to the same key (both are "this task is done"), so a defensive double-emit —
 * or the BE's completed-instead-of-status pair — never double-renders. Distinct
 * verbs on the same task (…→in_progress then …→in_review) keep DIFFERENT keys and
 * both show; only exact repeats merge.
 */
function issueEventFoldKey(event: IssueSystemEvent): string {
  const kind =
    event.event === ISSUE_EVENTS.created
      ? "created"
      : event.event === ISSUE_EVENTS.assigned
        ? "assigned"
        : event.event === ISSUE_EVENTS.completed || event.issueStatus === "done"
          ? "done"
          : `status:${event.issueStatus}`;
  return `${event.issueId}::${kind}`;
}

/**
 * Ids of issue system rows that should NOT render because an immediately
 * preceding row already conveys the same fact (see {@link issueEventFoldKey}).
 * Pure over the ordered message window — the caller keeps the FIRST occurrence
 * and skips the redundant repeats. Deliberately derives a Set instead of
 * filtering the array so the virtualized list's data/indices stay untouched.
 */
export function foldedIssueEventIds(
  messages: readonly ChannelMessage[],
): Set<string> {
  const suppressed = new Set<string>();
  let prevKey: string | null = null;
  for (const message of messages) {
    const event = parseIssueSystemEvent(message);
    if (!event) {
      prevKey = null;
      continue;
    }
    const key = issueEventFoldKey(event);
    if (key === prevKey) {
      suppressed.add(message.id);
    } else {
      prevKey = key;
    }
  }
  return suppressed;
}

/**
 * Extract the structured member-change event from a system message's parts.
 * Returns null for any message that isn't a member-change system event (channel
 * archive/rename notices, etc.) so the caller renders the plain canonical
 * `content` instead. System events have a single durable wire shape; migration
 * 178 converts historical text-JSON payloads rather than retaining a legacy
 * reader here.
 */
export function parseMemberSystemEvent(message: SystemEventSource): MemberSystemEvent | null {
  if (message.type !== "system" || !Array.isArray(message.parts)) return null;
  for (const part of message.parts) {
    if (part.type !== "system_event" || !MEMBER_EVENT_KINDS.has(part.event)) continue;
    const event = part.event;
    const params = part.event_params;
    const targetId = optString(params, "target_id");
    if (!targetId) continue;
    return {
      event: event as MemberSystemEventKind,
      actorId: optString(params, "actor_id"),
      actorType: optString(params, "actor_type"),
      actorHandle: optString(params, "actor_handle"),
      actorName: optString(params, "actor_name"),
      targetId,
      targetType: optString(params, "target_type"),
      targetHandle: optString(params, "target_handle"),
      targetName: optString(params, "target_name"),
      source: optString(params, "source"),
    };
  }
  return null;
}

/**
 * Channel↔project association events (#610). The BE emits one when a channel is
 * bound to a project, moved between projects, or unbound. The FE projects each
 * into a localized row whose SOLE clickable object is the project name (the same
 * one-object rule the issue rows follow — actor stays plain text). `bound`
 * carries the current project; `unbound` carries only the previous one; `changed`
 * carries both.
 */
export const PROJECT_EVENTS = {
  bound: "channel_project_bound",
  changed: "channel_project_changed",
  unbound: "channel_project_unbound",
} as const;

export type ProjectSystemEventKind = (typeof PROJECT_EVENTS)[keyof typeof PROJECT_EVENTS];

const PROJECT_EVENT_KINDS = new Set<string>(Object.values(PROJECT_EVENTS));

export interface ProjectSystemEvent {
  event: ProjectSystemEventKind;
  /** Current association's project uuid — absent on `unbound`. */
  projectId?: string;
  /** Current project title — absent on `unbound`. */
  projectTitle?: string;
  /** Prior association's project uuid — present on `changed` + `unbound`. */
  previousProjectId?: string;
  previousProjectTitle?: string;
  actorId?: string;
  /** Public actor type: "human" | "agent". */
  actorType?: string;
  actorName?: string;
}

/**
 * Extract the structured channel↔project association event (#610). Returns null
 * for any other system message so the caller renders the plain canonical
 * `content`. A row that names neither the current nor the previous project can't
 * be projected into the "把本群关联到项目「X」" sentence — fall back to raw content.
 * Identity is entirely from params — never the fallback `content` string.
 */
export function parseProjectSystemEvent(message: SystemEventSource): ProjectSystemEvent | null {
  if (message.type !== "system" || !Array.isArray(message.parts)) return null;
  for (const part of message.parts) {
    if (part.type !== "system_event" || !PROJECT_EVENT_KINDS.has(part.event)) continue;
    const params = part.event_params;
    const projectTitle = optString(params, "project_title");
    const previousProjectTitle = optString(params, "previous_project_title");
    if (!projectTitle && !previousProjectTitle) continue;
    return {
      event: part.event as ProjectSystemEventKind,
      projectId: optString(params, "project_id"),
      projectTitle,
      previousProjectId: optString(params, "previous_project_id"),
      previousProjectTitle,
      actorId: optString(params, "actor_id"),
      actorType: optString(params, "actor_type"),
      actorName: optString(params, "actor_name"),
    };
  }
  return null;
}

/**
 * Thread attention system events (LRM-540). Today the BE emits only
 * `thread_unfollowed` when an agent explicitly unfollows (#329: re-follow is
 * silent). The fallback `content` stamps `@handle unfollowed this thread` and
 * the companion mention reference often lacks UTF-16 spans — so projecting
 * from `content` paints the slug. FE owns the localized row + @display_name
 * token from structured params (same contract as member/issue system rows).
 */
export const THREAD_EVENTS = {
  unfollowed: "thread_unfollowed",
  followed: "thread_followed",
} as const;

export type ThreadSystemEventKind = (typeof THREAD_EVENTS)[keyof typeof THREAD_EVENTS];

const THREAD_EVENT_KINDS = new Set<string>(Object.values(THREAD_EVENTS));

export interface ThreadSystemEvent {
  event: ThreadSystemEventKind;
  actorId: string;
  /** Public actor type: "human" | "agent". Absent on sparse/legacy params. */
  actorType?: string;
  /** Canonical @handle — unresolved-ink fallback (LRM-515); never uuid. */
  actorHandle?: string;
  /** Emit-time name (diagnostics only — FE must not paint this; LRM-281). */
  actorName?: string;
}

/**
 * Extract a thread follow/unfollow system event. Requires a resolvable actor
 * id (`actor_id`, or legacy `agent_id`). Returns null so the caller can fall
 * back to canonical `content` when facts are missing — never invent an actor.
 */
export function parseThreadSystemEvent(message: SystemEventSource): ThreadSystemEvent | null {
  if (message.type !== "system" || !Array.isArray(message.parts)) return null;
  for (const part of message.parts) {
    if (part.type !== "system_event" || !THREAD_EVENT_KINDS.has(part.event)) continue;
    const params = part.event_params;
    // Current emit stamps both; older/normalized fixtures may only carry actor_id
    // or the agent-only agent_id key from early unfollow payloads.
    const actorId = optString(params, "actor_id") ?? optString(params, "agent_id");
    if (!actorId) continue;
    // Prefer stamped actor_type; agent_id-only legacy payloads are agent rows.
    const actorType =
      optString(params, "actor_type") ?? (optString(params, "agent_id") ? "agent" : undefined);
    return {
      event: part.event as ThreadSystemEventKind,
      actorId,
      actorType,
      actorHandle: optString(params, "actor_handle"),
      actorName: optString(params, "actor_name") ?? optString(params, "agent_name"),
    };
  }
  return null;
}
