"use client";

import { Fragment, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { agentListOptions, memberListOptions } from "@multica/core/workspace/queries";
import { useActorName } from "@multica/core/workspace/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
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
  PROJECT_EVENTS,
  type ProjectSystemEvent,
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
    return <span className="font-medium text-foreground/70">{label}</span>;
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
  const { getActorName } = useActorName();
  const wsId = useWorkspaceId();
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: members = [] } = useQuery(memberListOptions(wsId));

  const resolveActor = (
    id: string,
    factType?: string,
    factHandle?: string,
    fallbackName?: string,
  ): ResolvedActor => {
    // The canonical handle wins over the display-name fallback: it's a stable
    // routable identity, while the display name is just prose the backend
    // happened to send with this particular event.
    const fallback = factHandle ?? fallbackName ?? id;
    // The typed fact is authoritative even when a former member has fallen out
    // of the live list. `human` maps to the standard member mention route.
    if (factType === "human" || factType === "agent") {
      const type = factType === "agent" ? "agent" : "member";
      return { type, id, displayName: getActorName(type, id, fallback) };
    }
    // Older bridge rows do not carry actor_type. Resolve only from identity
    // caches, never from prose; a real cache miss stays a non-interactive label.
    const agent = agents.find((a) => a.id === id);
    if (agent) return { type: "agent", id, displayName: getActorName("agent", id, fallback) };
    const member = members.find((m) => m.user_id === id);
    if (member) return { type: "member", id, displayName: getActorName("member", id, fallback) };
    return { type: null, id, displayName: fallback };
  };

  const target = (
    <SystemEventActorToken
      actor={resolveActor(event.targetId, event.targetType, event.targetHandle, event.targetName)}
    />
  );
  const actor = event.actorId ? (
    <SystemEventActorToken
      actor={resolveActor(event.actorId, event.actorType, event.actorHandle, event.actorName)}
    />
  ) : undefined;

  const template =
    event.event === MEMBER_EVENTS.added
      ? t(($) => $.message.system_event.member_added)
      : event.event === MEMBER_EVENTS.removed
        ? t(($) => $.message.system_event.member_removed)
        : t(($) => $.message.system_event.member_left);

  return interpolateSlots(template, { target, actor });
}

// Issue rows carry both structured actor and issue facts (#603). Keep their
// relative word order in each locale while making the actor the same real
// @mention token used elsewhere in the conversation — the issue identifier
// stays its own separately-hoverable anchored reference (Iris: actor and
// issue each own their hover/link semantics, never one mixed click region).
function interpolateIssueSlots(
  template: string,
  slots: { actor: ReactNode; issue: ReactNode },
): ReactNode {
  return template.split(/(\{actor\}|\{issue\})/g).map((segment, index) => {
    if (segment === "{actor}") return <Fragment key={index}>{slots.actor}</Fragment>;
    if (segment === "{issue}") return <Fragment key={index}>{slots.issue}</Fragment>;
    if (!segment) return null;
    return <Fragment key={index}>{segment}</Fragment>;
  });
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
 * Live display name from authoritative sources only:
 * 1. Workspace directory caches (ListAgents / members)
 * 2. GET /api/member-profiles/{type}/{id} when the directory misses
 *
 * Group managers and other private agents are hidden from ListAgents (LRM-233)
 * but remain resolvable via member-profiles (full or identity_only). Never pass
 * emit-time actor_name/actor_handle as a FE fallback (LRM-281 / Frank).
 *
 * Query key is identity-scoped (not workspace-scoped) so this hook does not
 * require useWorkspaceId — system rows can render in unit tests that only
 * provide a QueryClient + a mocked useActorName directory.
 */
function useLiveActorDisplayName(
  mentionType: "agent" | "member" | null,
  actorId: string | undefined,
): string {
  const { getActorName } = useActorName();
  const directoryName =
    mentionType && actorId ? getActorName(mentionType, actorId) : "";
  const directoryMiss =
    !!mentionType &&
    !!actorId &&
    (directoryName === "Unknown Agent" || directoryName === "Unknown");
  const profileType =
    mentionType === "agent" ? "agent" : mentionType === "member" ? "user" : null;
  const { data: profile } = useQuery({
    queryKey: ["actor-identity", profileType ?? "", actorId ?? ""],
    queryFn: () => api.getMemberProfile(profileType!, actorId!),
    enabled: directoryMiss && !!profileType && !!actorId,
    staleTime: 30 * 1000,
  });
  if (!directoryMiss) return directoryName;
  const live = profile?.display_name?.trim() || profile?.name?.trim();
  return live || directoryName;
}

/**
 * Renders an issue-lifecycle backflow row (#497, #603) as the frozen item #7
 * copy: "任务" only, a localized action verb (标记为处理中 / 提交审核 / 完成任务 /
 * 指派给 X), and the issue identifier as its anchored reference. A structured
 * actor is rendered as the ordinary clickable @display-name mention (#603);
 * assignee and status stay plain localized text — never colored, never a raw
 * enum. The row itself owns the simple time and quiet centered layout.
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
  const actorDisplayName = useLiveActorDisplayName(actorType, event.actorId);
  const targetMentionType = toActorMentionType(event.targetType);
  const targetDisplayName = useLiveActorDisplayName(targetMentionType, event.targetId);
  const actor =
    event.actorId && actorType ? (
      <SystemEventActorToken
        actor={{
          type: actorType,
          id: event.actorId,
          displayName: actorDisplayName,
        }}
      />
    ) : (
      t(($) => $.message.system_event.issue.actor_system)
    );

  const issueToken = (
    <IssueRefLink
      issueId={event.issueId}
      text={event.issueIdentifier}
      source="anchor"
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

  // Assignment: resolve the assignee live from directory / member-profile API.
  // No emit-time target_name fallback (same rule as actors — LRM-281).
  if (event.event === ISSUE_EVENTS.assigned) {
    const targetName = event.targetId ? targetDisplayName : undefined;
    const template =
      targetName && targetName !== "Unknown Agent" && targetName !== "Unknown"
        ? t(($) => $.message.system_event.issue.assigned, { target: targetName })
        : t(($) => $.message.system_event.issue.assigned_unknown);
    return interpolateIssueSlots(template, { actor, issue: issueToken });
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
  const { getActorName } = useActorName();
  const paths = useWorkspacePaths();
  const navigation = useOptionalNavigation();

  const actorType = toActorMentionType(event.actorType);
  const actor =
    event.actorId && actorType ? (
      <SystemEventActorToken
        actor={{
          type: actorType,
          id: event.actorId,
          displayName: getActorName(actorType, event.actorId, event.actorName),
        }}
      />
    ) : (
      event.actorName ?? t(($) => $.message.system_event.project.actor_system)
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
