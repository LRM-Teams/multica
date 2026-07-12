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
}

function optString(params: Record<string, unknown>, key: string): string | undefined {
  const value = params[key];
  return typeof value === "string" && value ? value : undefined;
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
    };
  }
  return null;
}
