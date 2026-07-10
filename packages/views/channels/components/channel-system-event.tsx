"use client";

import { Fragment, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import { agentListOptions, memberListOptions } from "@multica/core/workspace/queries";
import { resolveActorHandle } from "@multica/core/identity";
import { useAgentPanelStore } from "@multica/core/agents/stores";
import type { ChannelMessage } from "@multica/core/types";
import { ActorProfileTrigger } from "../../common/actor-profile-popover";
import { useOpenAgentPanel } from "../../common/agent-panel-context";
import { useT } from "../../i18n/use-t";

/**
 * Member-change system events emitted by the backend (#450). The BE writes a
 * `type=system` message carrying BOTH a canonical fallback `content` string and
 * a structured `parts:[{event, params}]` payload. The FE composes its own copy
 * from the structured params so it can render Raft/Slack-style quiet inline rows
 * with clickable @username tokens — the params only give ids + display names, so
 * the FE resolves username + actor type (agent vs member) from the workspace
 * caches (#369). Old messages / non-member system events carry no such part, so
 * callers fall back to the canonical `content`.
 */

const MEMBER_EVENTS = {
  added: "channel_member_added",
  removed: "channel_member_removed",
  left: "channel_member_left",
} as const;

type MemberSystemEventKind = (typeof MEMBER_EVENTS)[keyof typeof MEMBER_EVENTS];

const MEMBER_EVENT_KINDS = new Set<string>(Object.values(MEMBER_EVENTS));

export interface MemberSystemEvent {
  event: MemberSystemEventKind;
  actorId?: string;
  actorName?: string;
  targetId: string;
  targetName?: string;
}

/**
 * Extract the structured member-change event from a system message's parts.
 * Returns null for any message that isn't a member-change system event (older
 * messages without the part, channel archive/rename notices, etc.) so the caller
 * renders the plain canonical `content` instead. Lenient about the part's `type`
 * discriminator: matches on a JSON-parseable `text` field carrying a known
 * `event`, so a backend that omits/renames the part type still resolves.
 */
export function parseMemberSystemEvent(message: ChannelMessage): MemberSystemEvent | null {
  if (message.type !== "system" || !Array.isArray(message.parts)) return null;
  for (const part of message.parts) {
    const text = (part as { text?: unknown }).text;
    if (typeof text !== "string" || !text) continue;
    let parsed: unknown;
    try {
      parsed = JSON.parse(text);
    } catch {
      continue;
    }
    if (!parsed || typeof parsed !== "object") continue;
    const event = (parsed as { event?: unknown }).event;
    if (typeof event !== "string" || !MEMBER_EVENT_KINDS.has(event)) continue;
    const params = ((parsed as { params?: unknown }).params ?? {}) as Record<string, unknown>;
    const targetId = typeof params.target_id === "string" ? params.target_id : "";
    if (!targetId) continue;
    return {
      event: event as MemberSystemEventKind,
      actorId: typeof params.actor_id === "string" ? params.actor_id || undefined : undefined,
      actorName: typeof params.actor_name === "string" ? params.actor_name || undefined : undefined,
      targetId,
      targetName: typeof params.target_name === "string" ? params.target_name || undefined : undefined,
    };
  }
  return null;
}

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

/**
 * Renders the composed, tokenized copy for a member-change system event. Feeds
 * the row's text slot; the row owns the timestamp + layout.
 */
export function MemberSystemEventContent({ event }: { event: MemberSystemEvent }): ReactNode {
  const { t } = useT("channels");
  const wsId = useWorkspaceId();
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: members = [] } = useQuery(memberListOptions(wsId));

  const resolveActor = (id: string, fallbackName?: string): ResolvedActor => {
    const agent = agents.find((a) => a.id === id);
    if (agent) return { type: "agent", id, handle: resolveActorHandle(agent, fallbackName) };
    const member = members.find((m) => m.user_id === id);
    if (member) return { type: "user", id, handle: resolveActorHandle(member, fallbackName) };
    // Unknown id (e.g. removed member no longer in the cache): keep the sentence
    // intact with the backend-supplied name, non-interactive.
    return { type: null, id, handle: fallbackName ?? id };
  };

  const target = <SystemEventActorToken actor={resolveActor(event.targetId, event.targetName)} />;
  const actor = event.actorId ? (
    <SystemEventActorToken actor={resolveActor(event.actorId, event.actorName)} />
  ) : undefined;

  const template =
    event.event === MEMBER_EVENTS.added
      ? t(($) => $.message.system_event.member_added)
      : event.event === MEMBER_EVENTS.removed
        ? t(($) => $.message.system_event.member_removed)
        : t(($) => $.message.system_event.member_left);

  return interpolateSlots(template, { target, actor });
}
