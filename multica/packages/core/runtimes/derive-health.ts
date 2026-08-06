// Pure derivation of a runtime's user-facing "health" state from the raw
// server fields (status + last_seen_at). Splitting the offline state into
// time-bucketed flavors lets the UI distinguish "just lost — likely
// transient" from "long gone — needs attention" with no schema change.

import type { AgentRuntime } from "../types";
import type { RuntimeHealth } from "./types";

// A runtime the server still reports as ONLINE is trusted only as far as its
// heartbeat is fresh. Past this window the heartbeat has lagged enough to call
// the connection "recently lost" (likely a transient wobble); past
// FIVE_MINUTES_MS it is treated as fully offline.
const HEARTBEAT_STALE_MS = 150_000; // 150s
// Exported: task #106 reuses this as the "still plausibly reachable" window
// for the LRM-248 running-task optimism in derive-presence.ts — same boundary,
// not a second freshness policy invented independently.
export const FIVE_MINUTES_MS = 5 * 60 * 1000;
// The runtime sweeper GCs runtimes that have been offline for 7 days. We
// flag the last 24 hours of that window so users can rescue a runtime
// before it disappears silently.
const ABOUT_TO_GC_THRESHOLD_MS = 6 * 24 * 3600 * 1000; // 6 days

export function deriveRuntimeHealth(
  runtime: Pick<AgentRuntime, "status" | "last_seen_at">,
  now: number,
): RuntimeHealth {
  const lastSeen = runtime.last_seen_at ? new Date(runtime.last_seen_at).getTime() : null;

  // EXPLICITLY offline: the server has marked this runtime disconnected. This
  // is a fact, not an inference from a stale heartbeat — so it reads Offline
  // immediately and NEVER "recently_lost" (that flavor is reserved for an
  // ONLINE runtime whose heartbeat merely lagged). It only escalates to
  // about_to_gc as it approaches the GC horizon. (#571: an explicit offline
  // must not masquerade as "Unstable" for the first 5 minutes.)
  if (runtime.status === "offline") {
    const offlineFor = lastSeen === null ? Number.POSITIVE_INFINITY : now - lastSeen;
    if (offlineFor > ABOUT_TO_GC_THRESHOLD_MS) return "about_to_gc";
    return "offline";
  }

  // status === "online": trust the flag only while the heartbeat is fresh.
  // A missing heartbeat can't be vouched for at all → offline. A lagging one
  // degrades through recently_lost (transient wobble → "Unstable") to offline.
  // This ONLINE-but-stale path is now the ONLY source of recently_lost.
  if (lastSeen === null) return "offline";
  const staleFor = now - lastSeen;
  if (staleFor < HEARTBEAT_STALE_MS) return "online";
  if (staleFor < FIVE_MINUTES_MS) return "recently_lost";
  return "offline";
}
