"use client";

import { Fragment, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import { agentListOptions, memberListOptions } from "@multica/core/workspace/queries";
import { resolveActorHandle } from "@multica/core/identity";
import { useAgentPanelStore } from "@multica/core/agents/stores";
import { useActorName } from "@multica/core/workspace/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { ActorProfileTrigger } from "../../common/actor-profile-popover";
import { useOpenAgentPanel } from "../../common/agent-panel-context";
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
 * this owns the localized, target-first passive copy with clickable @username
 * tokens. Usernames + actor type are resolved from the workspace caches by id
 * (the #450 params only carry ids + display names), so no BE change is needed.
 */

interface ResolvedActor {
  type: "agent" | "user" | null;
  id: string;
  handle: string;
}

/**
 * A clickable @username token inside a system row. Agents open the #349 side
 * panel on click (context in channels/DM, global store elsewhere — same wiring
 * as rendered @mentions in markdown.tsx); members open the profile popover.
 * An unresolved actor (left the workspace, cache miss) degrades to plain,
 * non-interactive text so the sentence never breaks.
 */
function SystemEventActorToken({ actor }: { actor: ResolvedActor }): ReactNode {
  const openAgentPanelFromContext = useOpenAgentPanel();
  const openAgentPanelFromStore = useAgentPanelStore((s) => s.open);
  const openAgentPanel = openAgentPanelFromContext ?? openAgentPanelFromStore;

  const label = `@${actor.handle}`;
  if (actor.type !== "agent" && actor.type !== "user") {
    return <span className="font-medium text-foreground/70">{label}</span>;
  }
  return (
    <ActorProfileTrigger
      memberType={actor.type}
      memberId={actor.id}
      triggerElement="span"
      className="cursor-pointer font-medium text-foreground/80 hover:underline"
      onClickCapture={
        actor.type === "agent" && openAgentPanel ? () => openAgentPanel(actor.id) : undefined
      }
    >
      {label}
    </ActorProfileTrigger>
  );
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
  const wsId = useWorkspaceId();
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: members = [] } = useQuery(memberListOptions(wsId));

  const resolveActor = (
    id: string,
    factType?: string,
    factHandle?: string,
    fallbackName?: string,
  ): ResolvedActor => {
    // #456 fact layer: trust the BE-emitted type + canonical handle. This works
    // even when the actor has left the channel (removed/left members no longer in
    // the workspace cache), so the @token stays clickable — the bridge's
    // cache-miss degradation to plain text is gone. `human` maps to the member
    // profile ("user"), `agent` to the #349 side panel.
    if (factHandle && (factType === "human" || factType === "agent")) {
      return { type: factType === "agent" ? "agent" : "user", id, handle: factHandle };
    }
    // Bridge fallback for older messages without the fact layer: resolve type +
    // handle from the workspace caches by id.
    const agent = agents.find((a) => a.id === id);
    if (agent) return { type: "agent", id, handle: resolveActorHandle(agent, fallbackName) };
    const member = members.find((m) => m.user_id === id);
    if (member) return { type: "user", id, handle: resolveActorHandle(member, fallbackName) };
    // Truly unknown (old message, actor gone): keep the sentence intact with the
    // backend-supplied name, non-interactive.
    return { type: null, id, handle: fallbackName ?? id };
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

// The FE-owned issue token is the ONLY interactive/blue element in the row
// (item #7口径). The actor, assignee and status are plain interpolated text, so
// they ride i18next's `{{ }}` interpolation and only the `{issue}` slot is split
// out for the React token.
function interpolateIssueSlot(template: string, issue: ReactNode): ReactNode {
  return template.split(/(\{issue\})/g).map((segment, index) => {
    if (segment === "{issue}") return <Fragment key={index}>{issue}</Fragment>;
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
// getActorName resolves against. Humans live in the member cache.
function toActorType(type: string | undefined): string {
  return type === "agent" ? "agent" : "member";
}

/**
 * Renders an issue-lifecycle backflow row (#497) as the frozen item #7 copy:
 * "任务" only, a localized action verb (标记为处理中 / 提交审核 / 完成任务 / 指派给 X),
 * and the issue identifier as the SOLE clickable blue token. The actor and
 * assignee are plain resolved names — never @tokens, never colored — and the
 * status is a localized label, never a raw enum or the word "issue". The row
 * itself owns the (simple) time and quiet centered layout.
 */
export function IssueSystemEventContent({
  event,
  sourceMessageId,
}: {
  event: IssueSystemEvent;
  sourceMessageId?: string;
}): ReactNode {
  const { t } = useT("channels");
  const { getActorName } = useActorName();

  const actor = event.actorId
    ? getActorName(toActorType(event.actorType), event.actorId)
    : t(($) => $.message.system_event.issue.actor_system);

  const issueToken = (
    <IssueRefLink
      issueId={event.issueId}
      text={event.issueIdentifier}
      source="anchor"
      sourceMessageId={sourceMessageId}
    />
  );

  // Creation (#610): a fixed "创建了这个 issue" verb, no status. Plain actor +
  // the issue token as the sole clickable object — same grammar as the other
  // issue rows.
  if (event.event === ISSUE_EVENTS.created) {
    return interpolateIssueSlot(
      t(($) => $.message.system_event.issue.created, { actor }),
      issueToken,
    );
  }

  // Assignment: resolve the assignee's plain name live (falling back to the
  // backend-supplied handle/name), then to a name-less "changed assignee" copy.
  if (event.event === ISSUE_EVENTS.assigned) {
    const targetName = event.targetId
      ? getActorName(toActorType(event.targetType), event.targetId, event.targetName)
      : event.targetName ?? event.targetHandle;
    const template = targetName
      ? t(($) => $.message.system_event.issue.assigned, { actor, target: targetName })
      : t(($) => $.message.system_event.issue.assigned_unknown, { actor });
    return interpolateIssueSlot(template, issueToken);
  }

  // Completion (BE emits this instead of a status→done row).
  if (event.event === ISSUE_EVENTS.completed || event.issueStatus === "done") {
    return interpolateIssueSlot(
      t(($) => $.message.system_event.issue.done, { actor }),
      issueToken,
    );
  }

  // Status change — dedicated action phrasing for the milestone transitions,
  // else a generic "marked as <localized status>" that still avoids raw enums.
  if (event.issueStatus === "in_progress") {
    return interpolateIssueSlot(
      t(($) => $.message.system_event.issue.in_progress, { actor }),
      issueToken,
    );
  }
  if (event.issueStatus === "in_review") {
    return interpolateIssueSlot(
      t(($) => $.message.system_event.issue.in_review, { actor }),
      issueToken,
    );
  }

  // A status the FE recognizes → "marked as <localized status>". A status it
  // does NOT recognize must NEVER echo the raw enum to the user face (Nash: no
  // internal-enum leak) — degrade to a generic, status-less localized action.
  const statusKey: IssueStatusKey | null =
    event.issueStatus && ISSUE_STATUS_KEYS.has(event.issueStatus)
      ? (event.issueStatus as IssueStatusKey)
      : null;
  if (!statusKey) {
    return interpolateIssueSlot(
      t(($) => $.message.system_event.issue.updated, { actor }),
      issueToken,
    );
  }
  return interpolateIssueSlot(
    t(($) => $.message.system_event.issue.status, {
      actor,
      status: t(($) => $.message.system_event.issue_status[statusKey]),
    }),
    issueToken,
  );
}

// The project name is the SOLE clickable object in a channel↔project row (#610,
// the same one-object rule the issue rows follow). Splits the localized template
// on the `{project}` / `{previous}` slots so word order stays per-locale while
// the FE owns the interactive node. The actor rides i18next's `{{ }}`
// interpolation as plain text — never a token.
function interpolateProjectSlots(
  template: string,
  slots: { project?: ReactNode; previous?: ReactNode },
): ReactNode {
  return template.split(/(\{project\}|\{previous\})/g).map((segment, index) => {
    if (segment === "{project}") return <Fragment key={index}>{slots.project}</Fragment>;
    if (segment === "{previous}") return <Fragment key={index}>{slots.previous}</Fragment>;
    if (!segment) return null;
    return <Fragment key={index}>{segment}</Fragment>;
  });
}

/**
 * Renders a channel↔project association row (#610) as localized copy whose SOLE
 * clickable object is a project name — actor stays plain display-text (never a
 * bare handle), matching the issue rows' one-object rule (Parker/Iris口径).
 *
 * Which project links follows the row's subject: `bound`/`changed` link the
 * CURRENT (new) project; `unbound` links the PREVIOUS one (its only object, per
 * Barry's contract — `previous_project_id` is a valid target). A name only links
 * when its id + the project route are available; otherwise it degrades to plain
 * text (e.g. before the #576 project page ships), never a dead/empty link.
 */
export function ProjectSystemEventContent({ event }: { event: ProjectSystemEvent }): ReactNode {
  const { t } = useT("channels");
  const { getActorName } = useActorName();
  const paths = useWorkspacePaths();
  const navigation = useOptionalNavigation();

  const resolvedActor = event.actorId
    ? getActorName(toActorType(event.actorType), event.actorId, event.actorName)
    : undefined;
  const actor =
    resolvedActor ?? event.actorName ?? t(($) => $.message.system_event.project.actor_system);

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
    return interpolateProjectSlots(t(($) => $.message.system_event.project.bound, { actor }), {
      project: projectNode(event.projectTitle, event.projectId, true),
    });
  }
  if (event.event === PROJECT_EVENTS.changed) {
    return interpolateProjectSlots(t(($) => $.message.system_event.project.changed, { actor }), {
      project: projectNode(event.projectTitle, event.projectId, true),
      previous: projectNode(event.previousProjectTitle, event.previousProjectId, false),
    });
  }
  return interpolateProjectSlots(t(($) => $.message.system_event.project.unbound, { actor }), {
    previous: projectNode(event.previousProjectTitle, event.previousProjectId, true),
  });
}
