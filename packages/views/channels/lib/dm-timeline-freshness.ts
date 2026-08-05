import type { InfiniteData } from "@tanstack/react-query";
import type { ChannelMessagesPage } from "@multica/core/types";

/**
 * A DM row and its timeline are separate server projections. Realtime updates
 * can refresh the row preview while a previously visited timeline remains in
 * the session cache. Because message pages intentionally use
 * `staleTime: Infinity`, detect that mismatch explicitly on conversation open.
 */
export function isDmTimelineBehindPreview(
  data: InfiniteData<ChannelMessagesPage> | undefined,
  previewCreatedAt: string | null | undefined,
): boolean {
  if (!data?.pages.length || !previewCreatedAt) return false;

  const previewTime = Date.parse(previewCreatedAt);
  if (!Number.isFinite(previewTime)) return false;

  let newestTimelineTime = Number.NEGATIVE_INFINITY;
  for (const page of data.pages) {
    for (const message of page.messages ?? []) {
      const messageTime = Date.parse(message.created_at);
      if (Number.isFinite(messageTime)) {
        newestTimelineTime = Math.max(newestTimelineTime, messageTime);
      }
    }
  }

  return newestTimelineTime < previewTime;
}
