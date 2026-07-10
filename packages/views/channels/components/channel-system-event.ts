import type { ChannelMessage } from "@multica/core/types";

/**
 * Member-change system events emitted by the backend (#450). The BE writes a
 * `type=system` message carrying BOTH a canonical fallback `content` string and
 * a structured `parts:[{event, params}]` payload. The FE composes its own copy
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
