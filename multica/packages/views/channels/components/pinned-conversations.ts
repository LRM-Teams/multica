import type { DMItem } from "@multica/core/dm";
import type { Channel } from "@multica/core/types";

export type PinnedConversationEntry =
  | { kind: "dm"; id: string; pinned_at: string; dm: DMItem }
  | { kind: "channel"; id: string; pinned_at: string; channel: Channel };

/**
 * Build a unified pinned list (DMs + channels), ordered by most-recently-pinned
 * first — Slack-style "Starred"/pinned section semantics rather than
 * float-to-top within each region.
 */
export function buildPinnedConversationEntries(
  dms: DMItem[],
  channels: Channel[],
): PinnedConversationEntry[] {
  const entries: PinnedConversationEntry[] = [];
  for (const dm of dms) {
    if (!dm.pinned_at) continue;
    entries.push({ kind: "dm", id: dm.id, pinned_at: dm.pinned_at, dm });
  }
  for (const channel of channels) {
    if (!channel.pinned_at) continue;
    entries.push({
      kind: "channel",
      id: channel.id,
      pinned_at: channel.pinned_at,
      channel,
    });
  }
  return entries.toSorted((a, b) => b.pinned_at.localeCompare(a.pinned_at));
}
