"use client";

import { Fragment, useState, type ReactNode } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import { useWorkspacePaths } from "@multica/core/paths";
import { useResolvedActorDisplayName } from "../../common/use-resolved-actor-display-name";
import { ActorMention } from "../../common/markdown";
import { useT } from "../../i18n/use-t";
import { AppLink } from "../../navigation/app-link";
import { useOptionalNavigation } from "../../navigation/context";
import { IssueRefLink } from "../../issues/components/issue-ref-link";
import {
  MEMBER_EVENTS,
  type MemberSystemEvent,
  ISSUE_EVENTS,
  type IssueSystemEvent,
  type IssueAggregateSystemEvent,
  PROJECT_EVENTS,
  type ProjectSystemEvent,
  THREAD_EVENTS,
  type ThreadSystemEvent,
} from "./channel-system-event";

/**
 * Renders the composed, tokenized copy for a member-change system event
 * (parsed by channel-system-event.ts). The row owns the timestamp + layout;
 * this owns the localized, target-first passive copy with clickable @display-name
 * mention tokens. Identity is read only from the structured event facts and the
 * existing actor cache; it never parses the fallback prose.
 */

interface ResolvedActor {
  type: "agent" | "member" | null;
  id: string;
  displayName: string;
}

/**
 * Reuse the ordinary @mention identity affordance: a clickable @display-name
 * token with the existing agent/member profile behavior (#603) — the same
 * chip rendered mentions get in message bodies, not a bespoke system-row
 * variant. Its hover card owns the avatar; activity rows intentionally do not
 * add an inline avatar slot. If an event can't identify the actor type (older
 * bridge rows, or a real cache miss), degrade to an honest plain label so the
 * sentence never fakes a clickable identity.
 */
function SystemEventActorToken({ actor }: { actor: ResolvedActor }): ReactNode {
  const label = `@${actor.displayName}`;
  if (!actor.type) {
    // LRM-561: plain unresolved labels stay muted like the system row — never
    // invent a louder ink than the surrounding ceremonial notice.
    return <span className="font-medium">{label}</span>;
  }
  return <ActorMention type={actor.type} id={actor.id} label={label} />;
}

// Interleave the localized template's `{target}` / `{actor}` slots with the
// resolved token nodes. Single-brace slots pass through i18next untouched (it
// only interpolates `{{ }}`), so the copy stays a normal translatable string
// while the FE owns the interactive tokens. Word order differs per locale
// (en "{target} … by {actor}" vs zh "{target} 被 {actor} …"); splitting on the
// slot markers keeps any order correct.
function interpolateSlots(
  template: string,
  slots: { target: ReactNode; actor?: ReactNode },
): ReactNode {
  return template.split(/(\{target\}|\{actor\})/g).map((segment, index) => {
    if (segment === "{target}") return <Fragment key={index}>{slots.target}</Fragment>;
    if (segment === "{actor}") return <Fragment key={index}>{slots.actor}</Fragment>;
    if (!segment) return null;
    return <Fragment key={index}>{segment}</Fragment>;
  });
}

export function MemberSystemEventContent({ event }: { event: MemberSystemEvent }): ReactNode {
  const { t } = useT("channels");

  // Prefer typed facts; older bridge rows may omit type — probe member then agent.
  const resolvedTargetType = toActorMentionType(event.targetType);
  const targetAsMember = useResolvedActorDisplayName(
    event.targetId,
    resolvedTargetType ?? "member",
  );
  const targetAsAgent = useResolvedActorDisplayName(
    resolvedTargetType ? undefined : event.targetId,
    resolvedTargetType ? null : "agent",
  );
  const targetDisplayName = resolvedTargetType
    ? targetAsMember
    : (targetAsMember ?? targetAsAgent);
  const targetMentionType: "agent" | "member" | null =
    resolvedTargetType ?? (targetAsAgent && !targetAsMember ? "agent" : targetAsMember ? "member" : null);

  const resolvedActorType = toActorMentionType(event.actorType);
  const actorAsMember = useResolvedActorDisplayName(
    event.actorId,
    resolvedActorType ?? "member",
  );
  const actorAsAgent = useResolvedActorDisplayName(
    resolvedActorType ? undefined : event.actorId,
    resolvedActorType ? null : "agent",
  );
  const actorDisplayName = resolvedActorType
    ? actorAsMember
    : (actorAsMember ?? actorAsAgent);
  const actorMentionType: "agent" | "member" | null =
    resolvedActorType ?? (actorAsAgent && !actorAsMember ? "agent" : actorAsMember ? "member" : null);

  const target = (
    <SystemEventActorToken
      actor={{
        type: targetMentionType,
        id: event.targetId,
        displayName: targetDisplayName ?? event.targetId,
      }}
    />
  );
  const actor = event.actorId ? (
    <SystemEventActorToken
      actor={{
        type: actorMentionType,
        id: event.actorId,
        displayName: actorDisplayName ?? event.actorId,
      }}
    />
  ) : undefined;

  // A real `actor` is the sole discriminator (not `source`): old rows predate
  // the `source` field, and a system-maintained row can't be told apart from a
  // manual one without it. An actor-less added/removed row is restructured to
  // drop the "by" clause entirely — never a fabricated "Workspace/System"
  // token (#661).
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

  return interpolateSlots(template, { target, actor });
}

// Issue rows carry structured actor, issue, and (for assignments) assignee
// facts (#603 / LRM-306). Keep relative word order per locale while making
// actor + assignee the same real @mention tokens used elsewhere — the issue
// identifier stays its own separately-hoverable anchored reference (Iris:
// each object owns its hover/link semantics, never one mixed click region).
function interpolateIssueSlots(
  template: string,
  slots: { actor: ReactNode; issue: ReactNode; target?: ReactNode },
): ReactNode {
  return template.split(/(\{actor\}|\{issue\}|\{target\})/g).map((segment, index) => {
    if (segment === "{actor}") return <Fragment key={index}>{slots.actor}</Fragment>;
    if (segment === "{issue}") return <Fragment key={index}>{slots.issue}</Fragment>;
    if (segment === "{target}") return <Fragment key={index}>{slots.target}</Fragment>;
    if (!segment) return null;
    return <Fragment key={index}>{segment}</Fragment>;
  });
}

/**
 * LRM-423 / LRM-508 / LRM-609 A' — main-line ink is **title only** (clickable).
 * Identifier stays out of the row (Frank: 别显示 LRM，显示 name); peek still
 * shows it. Missing title → identifier alone as honest interim (never invent
 * a title; LRM-238).
 */
function IssueEventSubject({
  issueId,
  identifier,
  title,
  sourceMessageId,
}: {
  issueId: string;
  identifier: string;
  title?: string;
  sourceMessageId?: string;
}): ReactNode {
  // LRM-609 SoT A': unfilled brand text + title-primary (no ▶ / fill / icon).
  const chipText = title?.trim() || identifier.trim() || issueId.slice(0, 8);
  return (
    <IssueRefLink
      issueId={issueId}
      text={chipText}
      source="anchor"
      sourceMessageId={sourceMessageId}
      appearance="systemChip"
    />
  );
}

type IssueStatusKey =
  | "backlog"
  | "todo"
  | "in_progress"
  | "in_review"
  | "done"
  | "blocked"
  | "cancelled";

const ISSUE_STATUS_KEYS = new Set<string>([
  "backlog",
  "todo",
  "in_progress",
  "in_review",
  "done",
  "blocked",
  "cancelled",
]);

// Public actor type ("human" | "agent") → the identity-cache actor type
// getActorName resolves against, or null for an unrecognized/missing type so
// a system-event actor token can fall back to an honest plain label instead
// of guessing "member" (#603 — a missing typed fact must never fabricate a
// clickable identity).
function toActorMentionType(type: string | undefined): "agent" | "member" | null {
  if (type === "agent") return "agent";
  if (type === "human") return "member";
  return null;
}

/**
 * Renders an issue-lifecycle backflow row (#497, #603, LRM-306) as the frozen
 * item #7 copy: "任务" only, a localized action verb (标记为处理中 / 提交审核 /
 * 完成任务 / 指派给 X), and the issue identifier as its anchored reference.
 * Structured actor and assignee are ordinary clickable @display-name mentions
 * (same SystemEventActorToken / ActorMention as member rows); status stays
 * plain localized text — never colored, never a raw enum. The row itself owns
 * the simple time and quiet centered layout.
 */
export function IssueSystemEventContent({
  event,
  sourceMessageId,
}: {
  event: IssueSystemEvent;
  sourceMessageId?: string;
}): ReactNode {
  const { t } = useT("channels");

  const actorType = toActorMentionType(event.actorType);
  const actorDisplayName = useResolvedActorDisplayName(event.actorId, actorType);
  const targetMentionType = toActorMentionType(event.targetType);
  const targetDisplayName = useResolvedActorDisplayName(event.targetId, targetMentionType);

  const actor =
    event.actorId && actorType && actorDisplayName ? (
      <SystemEventActorToken
        actor={{
          type: actorType,
          id: event.actorId,
          displayName: actorDisplayName,
        }}
      />
    ) : event.actorId && actorType ? (
      // Profile/list still loading or failed — keep a non-fake typed token with
      // the stable id until the DB-backed name arrives (never emit-time name).
      <SystemEventActorToken
        actor={{ type: actorType, id: event.actorId, displayName: event.actorId }}
      />
    ) : (
      t(($) => $.message.system_event.issue.actor_system)
    );

  const issueToken = (
    <IssueEventSubject
      issueId={event.issueId}
      identifier={event.issueIdentifier}
      title={event.issueTitle}
      sourceMessageId={sourceMessageId}
    />
  );

  // Creation (#610): a fixed "创建了这个 issue" verb, no status.
  if (event.event === ISSUE_EVENTS.created) {
    return interpolateIssueSlots(t(($) => $.message.system_event.issue.created), {
      actor,
      issue: issueToken,
    });
  }

  // Assignment (LRM-306): assignee is a clickable @mention from target_id +
  // target_type (same SystemEventActorToken as actor / member rows) — never
  // i18n {{target}} plain-text interpolation. Display name from directory /
  // member-profile (DB), never emit-time target_name (LRM-238). Missing typed
  // target facts → name-less "changed assignee" copy.
  if (event.event === ISSUE_EVENTS.assigned) {
    if (event.targetId && targetMentionType) {
      const target = (
        <SystemEventActorToken
          actor={{
            type: targetMentionType,
            id: event.targetId,
            displayName: targetDisplayName ?? event.targetId,
          }}
        />
      );
      return interpolateIssueSlots(t(($) => $.message.system_event.issue.assigned), {
        actor,
        issue: issueToken,
        target,
      });
    }
    return interpolateIssueSlots(t(($) => $.message.system_event.issue.assigned_unknown), {
      actor,
      issue: issueToken,
    });
  }

  // Completion (BE emits this instead of a status→done row).
  if (event.event === ISSUE_EVENTS.completed || event.issueStatus === "done") {
    return interpolateIssueSlots(t(($) => $.message.system_event.issue.done), {
      actor,
      issue: issueToken,
    });
  }

  // Status change — dedicated action phrasing for the milestone transitions,
  // else a generic "marked as <localized status>" that still avoids raw enums.
  if (event.issueStatus === "in_progress") {
    return interpolateIssueSlots(t(($) => $.message.system_event.issue.in_progress), {
      actor,
      issue: issueToken,
    });
  }
  if (event.issueStatus === "in_review") {
    return interpolateIssueSlots(t(($) => $.message.system_event.issue.in_review), {
      actor,
      issue: issueToken,
    });
  }

  // A status the FE recognizes → "marked as <localized status>". A status it
  // does NOT recognize must NEVER echo the raw enum to the user face (Nash: no
  // internal-enum leak) — degrade to a generic, status-less localized action.
  const statusKey: IssueStatusKey | null =
    event.issueStatus && ISSUE_STATUS_KEYS.has(event.issueStatus)
      ? (event.issueStatus as IssueStatusKey)
      : null;
  if (!statusKey) {
    return interpolateIssueSlots(t(($) => $.message.system_event.issue.updated), {
      actor,
      issue: issueToken,
    });
  }
  return interpolateIssueSlots(
    t(($) => $.message.system_event.issue.status, {
      status: t(($) => $.message.system_event.issue_status[statusKey]),
    }),
    { actor, issue: issueToken },
  );
}

function interpolateAggregateSlots(
  template: string,
  slots: { actor: ReactNode; issues: ReactNode },
): ReactNode {
  return template.split(/(\{actor\}|\{issues\})/g).map((segment, index) => {
    if (segment === "{actor}") return <Fragment key={index}>{slots.actor}</Fragment>;
    if (segment === "{issues}") return <Fragment key={index}>{slots.issues}</Fragment>;
    if (!segment) return null;
    return <Fragment key={index}>{segment}</Fragment>;
  });
}

/**
 * Server-aggregated issue system row (LRM-418 / LRM-423): same title-primary
 * template as singles. N=2–3 inline all titles; N≥4 fold with +N expand.
 * Only renders when the BE stamped a valid `items` array — FE never invents
 * the group from consecutive singles.
 */
export function IssueAggregateSystemEventContent({
  event,
  sourceMessageId,
}: {
  event: IssueAggregateSystemEvent;
  sourceMessageId?: string;
}): ReactNode {
  const { t } = useT("channels");
  const [expanded, setExpanded] = useState(false);
  const count = event.items.length;
  const foldRest = count >= 4;
  const previewCount = foldRest && !expanded ? 1 : count;
  const hiddenCount = Math.max(0, count - previewCount);

  const actorType = toActorMentionType(event.actorType);
  const actorDisplayName = useResolvedActorDisplayName(event.actorId, actorType);

  const actor =
    event.actorId && actorType && actorDisplayName ? (
      <SystemEventActorToken
        actor={{
          type: actorType,
          id: event.actorId,
          displayName: actorDisplayName,
        }}
      />
    ) : event.actorId && actorType ? (
      <SystemEventActorToken
        actor={{ type: actorType, id: event.actorId, displayName: event.actorId }}
      />
    ) : (
      t(($) => $.message.system_event.issue.actor_system)
    );

  const summaryTemplate =
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

  const visibleItems = event.items.slice(0, previewCount);
  const issuesNode = (
    <span className="inline-flex max-w-full flex-wrap items-baseline justify-start gap-x-1.5 gap-y-0.5">
      {visibleItems.map((item, index) => (
        <Fragment key={item.issueId}>
          {index > 0 ? <span className="text-muted-foreground/60">·</span> : null}
          <IssueEventSubject
            issueId={item.issueId}
            identifier={item.issueIdentifier}
            title={item.issueTitle}
            sourceMessageId={sourceMessageId}
          />
        </Fragment>
      ))}
      {foldRest ? (
        <button
          type="button"
          data-testid="issue-aggregate-expand"
          aria-expanded={expanded}
          aria-label={
            expanded
              ? t(($) => $.message.system_event.issue.aggregate_collapse)
              : t(($) => $.message.system_event.issue.aggregate_expand)
          }
          onClick={() => setExpanded((open) => !open)}
          className="inline-flex items-center gap-0.5 rounded px-0.5 text-muted-foreground transition-colors hover:text-foreground"
        >
          {expanded ? (
            <ChevronDown className="size-3 shrink-0" aria-hidden />
          ) : (
            <>
              <span className="tabular-nums">+{hiddenCount}</span>
              <ChevronRight className="size-3 shrink-0" aria-hidden />
            </>
          )}
        </button>
      ) : null}
    </span>
  );

  return (
    <span className="inline-flex max-w-full flex-col items-start gap-1">
      <span className="inline-flex flex-wrap items-baseline justify-start gap-x-1">
        {interpolateAggregateSlots(summaryTemplate, {
          actor,
          issues: issuesNode,
        })}
      </span>
      {foldRest && expanded ? (
        <ul
          data-testid="issue-aggregate-items"
          className="m-0 flex list-none flex-col items-start gap-0.5 p-0 text-xs"
        >
          {event.items.slice(1).map((item) => (
            <li key={item.issueId} className="min-w-0">
              <IssueEventSubject
                issueId={item.issueId}
                identifier={item.issueIdentifier}
                title={item.issueTitle}
                sourceMessageId={sourceMessageId}
              />
            </li>
          ))}
        </ul>
      ) : null}
    </span>
  );
}

// The project name is its own clickable object in a channel↔project row (#610,
// the same one-object-per-thing rule the issue rows follow) — independent from
// the actor's own @mention token (#603): each owns its own hover/link
// semantics, never a merged click region. Splits the localized template on the
// `{actor}` / `{project}` / `{previous}` slots so word order stays per-locale
// while the FE owns the interactive nodes.
function interpolateProjectSlots(
  template: string,
  slots: { actor: ReactNode; project?: ReactNode; previous?: ReactNode },
): ReactNode {
  return template.split(/(\{actor\}|\{project\}|\{previous\})/g).map((segment, index) => {
    if (segment === "{actor}") return <Fragment key={index}>{slots.actor}</Fragment>;
    if (segment === "{project}") return <Fragment key={index}>{slots.project}</Fragment>;
    if (segment === "{previous}") return <Fragment key={index}>{slots.previous}</Fragment>;
    if (!segment) return null;
    return <Fragment key={index}>{segment}</Fragment>;
  });
}

/**
 * Renders a channel↔project association row (#610) as localized copy with two
 * independent interactive objects: the actor (#603 — the same @mention token
 * issue/member rows use, once a typed actor fact exists) and the project name
 * (#610's own one-object rule: `bound`/`changed` link the CURRENT project,
 * `unbound` links the PREVIOUS one — Barry's contract). Neither shares the
 * other's hover/click region. A project name only links when its id + the
 * project route are available; otherwise it degrades to plain text (e.g.
 * before the #576 project page ships), never a dead/empty link. A missing
 * actor fact degrades the same honest way the issue rows do.
 */
export function ProjectSystemEventContent({ event }: { event: ProjectSystemEvent }): ReactNode {
  const { t } = useT("channels");
  const paths = useWorkspacePaths();
  const navigation = useOptionalNavigation();

  const actorType = toActorMentionType(event.actorType);
  const actorDisplayName = useResolvedActorDisplayName(event.actorId, actorType);
  const actor =
    event.actorId && actorType && actorDisplayName ? (
      <SystemEventActorToken
        actor={{
          type: actorType,
          id: event.actorId,
          displayName: actorDisplayName,
        }}
      />
    ) : event.actorId && actorType ? (
      <SystemEventActorToken
        actor={{ type: actorType, id: event.actorId, displayName: event.actorId }}
      />
    ) : (
      t(($) => $.message.system_event.project.actor_system)
    );

  // A project name → clickable token when we can route to it, else plain text.
  const projectNode = (title?: string, id?: string, linkable?: boolean): ReactNode => {
    if (!title) return null;
    if (linkable && id && typeof paths.projectDetail === "function") {
      const href = paths.projectDetail(id);
      const className = "text-brand hover:underline";
      return navigation ? (
        <AppLink href={href} className={className}>
          {title}
        </AppLink>
      ) : (
        <a href={href} className={className}>
          {title}
        </a>
      );
    }
    return <span className="text-foreground/80">{title}</span>;
  };

  if (event.event === PROJECT_EVENTS.bound) {
    return interpolateProjectSlots(t(($) => $.message.system_event.project.bound), {
      actor,
      project: projectNode(event.projectTitle, event.projectId, true),
    });
  }
  if (event.event === PROJECT_EVENTS.changed) {
    return interpolateProjectSlots(t(($) => $.message.system_event.project.changed), {
      actor,
      project: projectNode(event.projectTitle, event.projectId, true),
      previous: projectNode(event.previousProjectTitle, event.previousProjectId, false),
    });
  }
  return interpolateProjectSlots(t(($) => $.message.system_event.project.unbound), {
    actor,
    previous: projectNode(event.previousProjectTitle, event.previousProjectId, true),
  });
}

function interpolateActorSlot(template: string, actor: ReactNode): ReactNode {
  return template.split(/(\{actor\})/g).map((segment, index) => {
    if (segment === "{actor}") return <Fragment key={index}>{actor}</Fragment>;
    if (!segment) return null;
    return <Fragment key={index}>{segment}</Fragment>;
  });
}

/**
 * Thread unfollow/follow system row (LRM-540). Actor ink is the same
 * SystemEventActorToken / ActorMention path as member rows — live
 * display_name primary, handle peek / gray unresolved, never slug-as-primary
 * from the BE fallback content and never a bare uuid (LRM-515 / LRM-238).
 */
export function ThreadSystemEventContent({ event }: { event: ThreadSystemEvent }): ReactNode {
  const { t } = useT("channels");

  const resolvedActorType = toActorMentionType(event.actorType);
  const actorAsMember = useResolvedActorDisplayName(
    event.actorId,
    resolvedActorType ?? "member",
  );
  const actorAsAgent = useResolvedActorDisplayName(
    resolvedActorType ? undefined : event.actorId,
    resolvedActorType ? null : "agent",
  );
  const actorDisplayName = resolvedActorType
    ? actorAsMember
    : (actorAsMember ?? actorAsAgent);
  const actorMentionType: "agent" | "member" | null =
    resolvedActorType ?? (actorAsAgent && !actorAsMember ? "agent" : actorAsMember ? "member" : null);

  // Unresolved ink must be @handle (gray), never uuid — pass handle into the
  // ActorMention label so useActorMentionChipLabel's miss path stays honest.
  const actor = (
    <SystemEventActorToken
      actor={{
        type: actorMentionType,
        id: event.actorId,
        displayName: actorDisplayName ?? event.actorHandle ?? "…",
      }}
    />
  );

  const template =
    event.event === THREAD_EVENTS.followed
      ? t(($) => $.message.system_event.thread.followed)
      : t(($) => $.message.system_event.thread.unfollowed);

  return interpolateActorSlot(template, actor);
}
