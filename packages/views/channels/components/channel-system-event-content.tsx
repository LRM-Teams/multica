"use client";

import { Fragment, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import { agentListOptions, memberListOptions } from "@multica/core/workspace/queries";
import { resolveActorHandle } from "@multica/core/identity";
import { useAgentPanelStore } from "@multica/core/agents/stores";
import { ActorProfileTrigger } from "../../common/actor-profile-popover";
import { useOpenAgentPanel } from "../../common/agent-panel-context";
import { useT } from "../../i18n/use-t";
import { MEMBER_EVENTS, type MemberSystemEvent } from "./channel-system-event";

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
