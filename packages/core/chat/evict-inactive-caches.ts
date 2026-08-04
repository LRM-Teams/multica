import type { QueryClient } from "@tanstack/react-query";

/**
 * LRM-1264 — drop message caches for chat sessions that are not currently
 * open (floating window and/or DM bubbles). Session list / task transcripts
 * stay; only per-session message + pending-task rows are eligible.
 */
const CHAT_SESSION_CACHE_KINDS = new Set([
  "messages",
  "messages-page",
  "pending-task",
]);

/** Soft GC for inactive chat session message queries (overrides default 10m). */
export const CHAT_MESSAGE_GC_TIME_MS = 2 * 60 * 1000;

export function evictInactiveChatMessageCaches(
  qc: QueryClient,
  keepSessionIds: Iterable<string | null | undefined>,
): number {
  const keep = new Set<string>();
  for (const id of keepSessionIds) {
    if (id) keep.add(id);
  }
  const doomed = qc.getQueryCache().getAll().filter((query) => {
    const key = query.queryKey;
    // Shape: ["chat", "messages"|"messages-page"|"pending-task", sessionId]
    if (!Array.isArray(key) || key.length < 3) return false;
    if (key[0] !== "chat") return false;
    if (typeof key[1] !== "string" || !CHAT_SESSION_CACHE_KINDS.has(key[1])) {
      return false;
    }
    return typeof key[2] === "string" && !keep.has(key[2]);
  });
  for (const query of doomed) {
    qc.removeQueries({ queryKey: query.queryKey, exact: true });
  }
  return doomed.length;
}
