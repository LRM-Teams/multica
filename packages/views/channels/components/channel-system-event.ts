import type { ChannelMessage } from "@multica/core/types";

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
}

function optString(params: Record<string, unknown>, key: string): string | undefined {
  const value = params[key];
  return typeof value === "string" && value ? value : undefined;
}

/**
 * Extract the structured member-change event from a system message's parts.
 * Returns null for any message that isn't a member-change system event (channel
 * archive/rename notices, etc.) so the caller renders the plain canonical
 * `content` instead. System events have a single durable wire shape; migration
 * 178 converts historical text-JSON payloads rather than retaining a legacy
 * reader here.
 */
export function parseMemberSystemEvent(message: ChannelMessage): MemberSystemEvent | null {
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
    };
  }
  return null;
}
