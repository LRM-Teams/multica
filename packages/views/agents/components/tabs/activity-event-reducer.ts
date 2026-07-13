import type { ActivityEvent } from "./activity-event";

/**
 * Pure read-model reducer for the agent Activity stream (#302 FE consumer).
 *
 * The BE (#302 / `agent_activity_event`) aggregates server-side and hands the FE
 * a stable-`id` ActivityEvent per row; the FE just **upserts by id** (Ronan's
 * contract: REST first-paint aggregate, then WS pushes the current
 * aggregate/coalesced event under the SAME id). The FE never rebuilds durable
 * state from transient chunks — a later WS push for an id simply replaces the
 * earlier row (e.g. a `thinking` event whose aggregate text grew).
 *
 * Kept pure + framework-free so the single shared hook (`useAgentActivityEvents`)
 * and its WS subscription share one reducer — no per-consumer (timeline vs
 * header/hover) drift. Field names beyond `id`/`occurred_at` are finalized by
 * Barry's #302 schema; this logic only depends on those two stable anchors.
 */

/** Ascending by `occurred_at` (ISO-8601), stable-ties by `id`. */
function compareEvents(a: ActivityEvent, b: ActivityEvent): number {
  if (a.occurred_at !== b.occurred_at) {
    return a.occurred_at < b.occurred_at ? -1 : 1;
  }
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
}

/**
 * Upsert one-or-many incoming events into the current list by `id`, returning a
 * new chronologically-ordered array. An incoming event with a known id REPLACES
 * the existing one (the BE re-sent a fresher aggregate); an unknown id is added.
 * Idempotent: re-applying the same event yields an equal list.
 */
export function upsertActivityEvents(
  current: readonly ActivityEvent[],
  incoming: ActivityEvent | readonly ActivityEvent[],
): ActivityEvent[] {
  const incomingList = Array.isArray(incoming) ? incoming : [incoming];
  // Still normalize order on the empty-incoming path — this function's contract
  // is "chronologically-ordered output", and `current` may arrive in the REST
  // wire order (DESC). Returning it unsorted was the #500 regression: the hook
  // computes `events = upsertActivityEvents(data, liveEvents)`, so before any
  // live event arrives (`liveEvents === []`) the timeline rendered raw-DESC
  // (newest on top), the compact card `slice(-5)` took the OLDEST five, and
  // `projectLatestActivity` (last element) picked the OLDEST as "latest".
  if (incomingList.length === 0) return current.slice().sort(compareEvents);

  const byId = new Map<string, ActivityEvent>();
  for (const event of current) byId.set(event.id, event);
  // Last write wins within a batch too, so a batch carrying two rows for the
  // same id (delta then coalesced) settles on the latest.
  for (const event of incomingList) byId.set(event.id, event);

  return Array.from(byId.values()).sort(compareEvents);
}

/**
 * The latest event in the stream — the latest-state read powering the DM/panel
 * header activity word + hover card (Parker's "same stream, latest-state view").
 * Assumes `events` is chronologically ordered (as `upsertActivityEvents` returns);
 * returns null for an empty stream.
 */
export function projectLatestActivity(
  events: readonly ActivityEvent[],
): ActivityEvent | null {
  return events.length > 0 ? (events[events.length - 1] ?? null) : null;
}
