import type { QueryClient } from "@tanstack/react-query";

/**
 * LRM-1264 — conversation message caches that are not for the active channel
 * should leave the JS heap promptly. Channel lists / members / stats stay;
 * only the heavy message/thread/search families are dropped.
 *
 * Safe with WS upserts: `upsertChannelMessageInCache` refuses to seed a page
 * for a channel the user has never opened, so eviction does not create a
 * "fresh empty window" hole on the next visit.
 */
const CHANNEL_MESSAGE_CACHE_ROOTS = new Set([
  "channel-messages",
  "channel-messages-page",
  "channel-message-thread",
  "channel-message-search",
]);

/** Soft GC for inactive channel message queries (overrides default 10m). */
export const CHANNEL_MESSAGE_GC_TIME_MS = 2 * 60 * 1000;

export function evictInactiveChannelMessageCaches(
  qc: QueryClient,
  activeChannelId: string | null | undefined,
): number {
  const keep = activeChannelId ?? "";
  const doomed = qc.getQueryCache().getAll().filter((query) => {
    const key = query.queryKey;
    if (!Array.isArray(key) || key.length < 2) return false;
    if (typeof key[0] !== "string" || !CHANNEL_MESSAGE_CACHE_ROOTS.has(key[0])) {
      return false;
    }
    return typeof key[1] === "string" && key[1] !== keep;
  });
  for (const query of doomed) {
    qc.removeQueries({ queryKey: query.queryKey, exact: true });
  }
  return doomed.length;
}
