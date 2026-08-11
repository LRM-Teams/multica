import type { MentionType, MentionPreviewResolver } from "./message-preview";
import {
  MEMBER_EVENTS,
  ISSUE_EVENTS,
  PROJECT_EVENTS,
  THREAD_EVENTS,
  parseMemberSystemEvent,
  parseIssueAggregateSystemEvent,
  parseIssueSystemEvent,
  parseProjectSystemEvent,
  parseThreadSystemEvent,
  type SystemEventSource,
} from "./channel-system-event";
import type { TFunction } from "i18next";

/**
 * The channel-list/DM-list preview row (#634) is a plain-text reading
 * surface, same reasoning as {@link import("./message-preview").projectReferencesToText}
 * — but for system rows it must draw from the SAME localized templates and
 * structured `system_event` facts that `channel-system-event-content.tsx`
 * composes for the full in-channel row, not the BE's hard-coded English
 * fallback `content` string (e.g. "LRM-191 created"). Without this, the
 * sidebar disagrees with the message itself: the row reads "QA Bot 创建了
 * Issue LRM-191" while the preview above it still says "system: LRM-191
 * created" in English regardless of locale.
 *
 * Mirrors each `*SystemEventContent` component's template selection, minus
 * the JSX/clickable-token machinery: slots are filled with plain resolved
 * names (via the same {@link MentionPreviewResolver} the rest of the preview
 * pipeline already uses — a synchronous directory lookup, not a query hook)
 * instead of `<ActorMention>`/`<IssueRefLink>` nodes.
 *
 * Returns null for any row that isn't a known system event (falls back to
 * the caller's existing raw-content preview path), matching every `parse*`
 * helper's own null-means-"not this kind" contract.
 */

type T = TFunction<"channels">;

// Mirrors channel-system-event-content.tsx's ISSUE_STATUS_KEYS (not exported
// there) — the set of statuses with a translated label.
type IssueStatusTextKey =
  | "backlog"
  | "todo"
  | "in_progress"
  | "in_review"
  | "done"
  | "blocked"
  | "cancelled";
const ISSUE_STATUS_TEXT_KEYS = new Set<string>([
  "backlog",
  "todo",
  "in_progress",
  "in_review",
  "done",
  "blocked",
  "cancelled",
]);
function isIssueStatusTextKey(value: string): value is IssueStatusTextKey {
  return ISSUE_STATUS_TEXT_KEYS.has(value);
}

function toMentionType(type: string | undefined): MentionType | null {
  if (type === "agent") return "agent";
  if (type === "human") return "member";
  return null;
}

function resolveActorText(
  actorId: string | undefined,
  actorType: string | undefined,
  resolveMention: MentionPreviewResolver,
  systemFallback: string,
): string {
  const mentionType = toMentionType(actorType);
  if (!actorId || !mentionType) return systemFallback;
  return `@${resolveMention(mentionType, actorId, actorId).replace(/^@+/, "")}`;
}

function fillSlots(template: string, slots: Record<string, string>): string {
  return template.replace(/\{(\w+)\}/g, (match, key: string) =>
    Object.prototype.hasOwnProperty.call(slots, key) ? (slots[key] ?? match) : match,
  );
}

function issueSubjectText(identifier: string, title: string | undefined): string {
  const trimmed = title?.trim();
  return trimmed || identifier;
}

function formatMemberEventPreview(
  event: NonNullable<ReturnType<typeof parseMemberSystemEvent>>,
  t: T,
  resolveMention: MentionPreviewResolver,
): string {
  const target = resolveActorText(event.targetId, event.targetType, resolveMention, event.targetId);
  const actor = event.actorId
    ? resolveActorText(event.actorId, event.actorType, resolveMention, event.actorId)
    : undefined;

  const template =
    event.event === MEMBER_EVENTS.added
      ? actor
        ? t(($) => $.message.system_event.member_added)
        : t(($) => $.message.system_event.member_added_no_actor)
      : event.event === MEMBER_EVENTS.removed
        ? actor
          ? t(($) => $.message.system_event.member_removed)
          : t(($) => $.message.system_event.member_removed_no_actor)
        : t(($) => $.message.system_event.member_left);

  return fillSlots(template, { target, actor: actor ?? "" });
}

function formatIssueEventPreview(
  event: NonNullable<ReturnType<typeof parseIssueSystemEvent>>,
  t: T,
  resolveMention: MentionPreviewResolver,
): string {
  const actor = resolveActorText(
    event.actorId,
    event.actorType,
    resolveMention,
    t(($) => $.message.system_event.issue.actor_system),
  );
  const issue = issueSubjectText(event.issueIdentifier, event.issueTitle);

  if (event.event === ISSUE_EVENTS.created) {
    return fillSlots(t(($) => $.message.system_event.issue.created), { actor, issue });
  }

  if (event.event === ISSUE_EVENTS.assigned) {
    if (event.targetId && toMentionType(event.targetType)) {
      const target = resolveActorText(event.targetId, event.targetType, resolveMention, event.targetId);
      return fillSlots(t(($) => $.message.system_event.issue.assigned), { actor, issue, target });
    }
    return fillSlots(t(($) => $.message.system_event.issue.assigned_unknown), { actor, issue });
  }

  if (event.event === ISSUE_EVENTS.completed || event.issueStatus === "done") {
    return fillSlots(t(($) => $.message.system_event.issue.done), { actor, issue });
  }

  if (event.issueStatus === "in_progress") {
    return fillSlots(t(($) => $.message.system_event.issue.in_progress), { actor, issue });
  }
  if (event.issueStatus === "in_review") {
    return fillSlots(t(($) => $.message.system_event.issue.in_review), { actor, issue });
  }

  // Same guard as the JSX version: never echo a raw/unrecognized status enum
  // — an unknown key degrades to the generic "updated" copy, not a leaked
  // internal string.
  if (!event.issueStatus || !isIssueStatusTextKey(event.issueStatus)) {
    return fillSlots(t(($) => $.message.system_event.issue.updated), { actor, issue });
  }
  const statusKey = event.issueStatus;
  const statusText = t(($) => $.message.system_event.issue_status[statusKey]);
  return fillSlots(t(($) => $.message.system_event.issue.status, { status: statusText }), {
    actor,
    issue,
  });
}

function formatIssueAggregateEventPreview(
  event: NonNullable<ReturnType<typeof parseIssueAggregateSystemEvent>>,
  t: T,
  resolveMention: MentionPreviewResolver,
): string {
  const actor = resolveActorText(
    event.actorId,
    event.actorType,
    resolveMention,
    t(($) => $.message.system_event.issue.actor_system),
  );
  const issues = event.items
    .map((item) => issueSubjectText(item.issueIdentifier, item.issueTitle))
    .join("、");

  const template =
    event.event === ISSUE_EVENTS.created
      ? t(($) => $.message.system_event.issue.aggregate_created)
      : event.event === ISSUE_EVENTS.assigned
        ? t(($) => $.message.system_event.issue.aggregate_assigned)
        : event.event === ISSUE_EVENTS.completed ||
            event.items.every((item) => item.issueStatus === "done" || !item.issueStatus)
          ? t(($) => $.message.system_event.issue.aggregate_done)
          : event.items.every((item) => item.issueStatus === "in_progress")
            ? t(($) => $.message.system_event.issue.aggregate_started)
            : event.items.every((item) => item.issueStatus === "in_review")
              ? t(($) => $.message.system_event.issue.aggregate_in_review)
              : t(($) => $.message.system_event.issue.aggregate_updated);

  return fillSlots(template, { actor, issues });
}

function formatProjectEventPreview(
  event: NonNullable<ReturnType<typeof parseProjectSystemEvent>>,
  t: T,
  resolveMention: MentionPreviewResolver,
): string {
  const actor = resolveActorText(
    event.actorId,
    event.actorType,
    resolveMention,
    t(($) => $.message.system_event.project.actor_system),
  );

  if (event.event === PROJECT_EVENTS.bound) {
    return fillSlots(t(($) => $.message.system_event.project.bound), {
      actor,
      project: event.projectTitle ?? "",
    });
  }
  if (event.event === PROJECT_EVENTS.changed) {
    return fillSlots(t(($) => $.message.system_event.project.changed), {
      actor,
      project: event.projectTitle ?? "",
      previous: event.previousProjectTitle ?? "",
    });
  }
  return fillSlots(t(($) => $.message.system_event.project.unbound), {
    actor,
    previous: event.previousProjectTitle ?? "",
  });
}

function formatThreadEventPreview(
  event: NonNullable<ReturnType<typeof parseThreadSystemEvent>>,
  t: T,
  resolveMention: MentionPreviewResolver,
): string {
  const actor = resolveActorText(
    event.actorId,
    event.actorType,
    resolveMention,
    event.actorHandle ? `@${event.actorHandle.replace(/^@+/, "")}` : "…",
  );
  const template =
    event.event === THREAD_EVENTS.followed
      ? t(($) => $.message.system_event.thread.followed)
      : t(($) => $.message.system_event.thread.unfollowed);
  return fillSlots(template, { actor });
}

export function formatSystemEventPreviewText(
  message: SystemEventSource,
  t: T,
  resolveMention: MentionPreviewResolver,
): string | null {
  if (message.type !== "system") return null;

  const issueAggregateEvent = parseIssueAggregateSystemEvent(message);
  if (issueAggregateEvent) return formatIssueAggregateEventPreview(issueAggregateEvent, t, resolveMention);

  const issueEvent = parseIssueSystemEvent(message);
  if (issueEvent) return formatIssueEventPreview(issueEvent, t, resolveMention);

  const memberEvent = parseMemberSystemEvent(message);
  if (memberEvent) return formatMemberEventPreview(memberEvent, t, resolveMention);

  const projectEvent = parseProjectSystemEvent(message);
  if (projectEvent) return formatProjectEventPreview(projectEvent, t, resolveMention);

  const threadEvent = parseThreadSystemEvent(message);
  if (threadEvent) return formatThreadEventPreview(threadEvent, t, resolveMention);

  return null;
}
