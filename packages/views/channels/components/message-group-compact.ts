import type { ChannelMessage } from "@multica/core/types";
import { channelMessageListItemKey } from "@multica/core/channels";
import { startsNewLocalDay } from "../../i18n/use-message-time";

/** Default Slack-style grouping window — consecutive same-author messages. */
export const MESSAGE_GROUP_MAX_GAP_MS = 5 * 60 * 1000;

export function isGroupableChannelMessage(message: ChannelMessage): boolean {
  return message.type !== "system" && !message.deleted_at;
}

function findPreviousVisibleMessage(
  messages: readonly ChannelMessage[],
  index: number,
  foldedIds: ReadonlySet<string>,
): ChannelMessage | null {
  for (let i = index - 1; i >= 0; i -= 1) {
    const candidate = messages[i];
    if (!candidate || foldedIds.has(candidate.id)) continue;
    return candidate;
  }
  return null;
}

/**
 * True when `curr` should render as the lead row of a visual group (avatar +
 * display name + inline timestamp). System rows, tombstones, and date-divider
 * boundaries always lead; otherwise same author within the time window groups.
 */
export function shouldStartMessageGroup(
  prev: ChannelMessage | null,
  curr: ChannelMessage,
  options: {
    hasDateDivider?: boolean;
    tz?: string;
    maxGapMs?: number;
  } = {},
): boolean {
  if (!isGroupableChannelMessage(curr)) return true;
  if (options.hasDateDivider) return true;
  if (!prev || !isGroupableChannelMessage(prev)) return true;
  if (prev.author_id !== curr.author_id) return true;

  const prevMs = Date.parse(prev.created_at);
  const currMs = Date.parse(curr.created_at);
  if (Number.isNaN(prevMs) || Number.isNaN(currMs)) return true;

  const maxGapMs = options.maxGapMs ?? MESSAGE_GROUP_MAX_GAP_MS;
  if (currMs - prevMs > maxGapMs) return true;

  const tz = options.tz ?? "UTC";
  if (startsNewLocalDay(currMs, prevMs, tz)) return true;

  return false;
}

/** Maps each visible message id to whether it renders as a compact continuation. */
export function buildMessageGroupCompactMap(
  messages: readonly ChannelMessage[],
  options: {
    foldedIds?: ReadonlySet<string>;
    dateDividerIds?: ReadonlySet<string>;
    tz?: string;
    maxGapMs?: number;
  } = {},
): Map<string, boolean> {
  const foldedIds = options.foldedIds ?? new Set<string>();
  const dateDividerIds = options.dateDividerIds ?? new Set<string>();
  const map = new Map<string, boolean>();

  for (let i = 0; i < messages.length; i += 1) {
    const msg = messages[i];
    if (!msg || foldedIds.has(msg.id)) continue;

    if (!isGroupableChannelMessage(msg)) {
      map.set(channelMessageListItemKey(msg), false);
      continue;
    }

    const prev = findPreviousVisibleMessage(messages, i, foldedIds);
    const compact =
      prev !== null &&
      !shouldStartMessageGroup(prev, msg, {
        hasDateDivider: dateDividerIds.has(msg.id) || dateDividerIds.has(channelMessageListItemKey(msg)),
        tz: options.tz,
        maxGapMs: options.maxGapMs,
      });
    map.set(channelMessageListItemKey(msg), compact);
  }

  return map;
}

/**
 * Maps each visible message id to whether it is the last row of its visual
 * group.
 *
 * LRM-1227 (freeze from LRM-1233, shell granularity **G**): every message is its
 * own virtual row, so a group has no wrapping DOM node to draw one bubble around.
 * The joined shell is painted per row instead — lead row draws the top edge and
 * top corners, middle rows only the side edges, last row the bottom edge and
 * bottom corners. `groupEnd` is that last-row flag, and it is derived from the
 * compact map so the two can never disagree: a row ends its group when the next
 * visible row is not a compact continuation. System rows and tombstones are never
 * compact, so they can neither be joined into a neighbouring group nor swallow
 * the previous group's bottom edge.
 */
export function buildMessageGroupEndMap(
  messages: readonly ChannelMessage[],
  compactMap: ReadonlyMap<string, boolean>,
  options: { foldedIds?: ReadonlySet<string> } = {},
): Map<string, boolean> {
  const foldedIds = options.foldedIds ?? new Set<string>();
  const map = new Map<string, boolean>();
  const visible: ChannelMessage[] = [];
  for (const msg of messages) {
    if (!msg || foldedIds.has(msg.id)) continue;
    visible.push(msg);
  }

  for (let i = 0; i < visible.length; i += 1) {
    const key = channelMessageListItemKey(visible[i]!);
    const next = visible[i + 1];
    if (!next) {
      map.set(key, true);
      continue;
    }
    const nextKey = channelMessageListItemKey(next);
    const nextCompact = compactMap.get(nextKey) ?? compactMap.get(next.id) ?? false;
    map.set(key, !nextCompact);
  }

  return map;
}
