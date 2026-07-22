import type { ChannelMessage } from "@multica/core/types";

/** Slack-aligned default: same author within 5 minutes stays one visual group. */
export const MESSAGE_GROUP_WINDOW_MS = 5 * 60 * 1000;

/**
 * True when `curr` should render as a Slack-style continuation of `prev`:
 * no avatar, no name row — body aligned under the lead's content column.
 *
 * Breaks on: missing prev, system messages, author/type change, local-day
 * boundary (`startsNewDay`), or a gap larger than `windowMs`.
 *
 * Visual-only — does not merge storage or change the message model.
 */
export function isCompactContinuation(
  prev: ChannelMessage | null | undefined,
  curr: ChannelMessage,
  options?: {
    windowMs?: number;
    /** True when a date divider precedes `curr` (cross-day / first-of-day). */
    startsNewDay?: boolean;
  },
): boolean {
  if (!prev) return false;
  if (options?.startsNewDay) return false;
  // System rows (and anything that isn't a normal author bubble) never compact.
  if (curr.type === "system" || prev.type === "system") return false;
  if (!curr.author_id || !prev.author_id) return false;
  if (curr.author_id !== prev.author_id) return false;
  // Guard against colliding ids across user/agent/lark namespaces.
  if (curr.type !== prev.type) return false;

  const currMs = Date.parse(curr.created_at);
  const prevMs = Date.parse(prev.created_at);
  if (Number.isNaN(currMs) || Number.isNaN(prevMs)) return false;

  const windowMs = options?.windowMs ?? MESSAGE_GROUP_WINDOW_MS;
  if (currMs - prevMs > windowMs) return false;

  return true;
}

/**
 * Message ids that should render in compact (continuation) mode for a
 * chronological list. `dayDividerIds` is the set of ids that open a new local
 * day (from `useMessageDayDividers`) — those rows always start a new group.
 */
export function computeCompactMessageIds(
  messages: readonly ChannelMessage[],
  options?: {
    windowMs?: number;
    dayDividerIds?: ReadonlySet<string> | ReadonlyMap<string, unknown>;
  },
): Set<string> {
  const compactIds = new Set<string>();
  const windowMs = options?.windowMs ?? MESSAGE_GROUP_WINDOW_MS;
  const dayDividerIds = options?.dayDividerIds;

  for (let i = 1; i < messages.length; i += 1) {
    const prev = messages[i - 1];
    const curr = messages[i];
    if (!prev || !curr) continue;
    if (
      isCompactContinuation(prev, curr, {
        windowMs,
        startsNewDay: dayDividerIds?.has(curr.id) ?? false,
      })
    ) {
      compactIds.add(curr.id);
    }
  }
  return compactIds;
}
