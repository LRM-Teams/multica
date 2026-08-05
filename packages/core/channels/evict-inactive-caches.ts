import type { QueryClient } from "@tanstack/react-query";

/**
 * LRM-1363 / Frank product lock (2026-08-04): keep cross-channel message /
 * thread RQ caches for the session. Instant switch-back fill beats heap
 * convergence. Do **not** remove inactive channel message caches.
 *
 * LRM-1264 previously shortened GC + actively evicted; that caused cold loads
 * on every channel reopen. Eviction is now a documented no-op so accidental
 * call sites cannot revive the defect; prefer deleting callers instead.
 */
/** Session-scoped retention (pairs with staleTime: Infinity on message queries). */
export const CHANNEL_MESSAGE_GC_TIME_MS = Number.POSITIVE_INFINITY;

/**
 * @deprecated LRM-1363 — no-op. Callers should be removed; do not reintroduce
 * active eviction for heap pressure.
 */
export function evictInactiveChannelMessageCaches(
  _qc: QueryClient,
  _activeChannelId: string | null | undefined,
): number {
  return 0;
}
