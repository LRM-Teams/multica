import { useMemo } from "react";
import type { ActivityEvent } from "./activity-event";
import { projectLatestActivity, upsertActivityEvents } from "./activity-event-reducer";

/**
 * The single shared read-model for an agent's Activity event stream (#302 FE
 * consumer). Per the #267 contract the BE aggregates server-side and hands each
 * event already tagged (`label`/`subtext`/`tone`/`visibility`) with a stable id
 * — the FE never derives those from raw text (the P1-8 heuristic trap), it only
 * **upserts by id** and reads latest-state.
 *
 * Both consumers eat this ONE hook's output — no per-surface reducer drift:
 *  - `events` → the #421 Activity timeline (full stream; it derives the directed
 *    3-state block itself from raw kind/inbox/wake facts).
 *  - `latest` → the DM/panel header activity word + hover card (latest-state).
 *
 * The BE query + WS subscription (signature pending Barry's #302 schema) aren't
 * wired yet, so `raw` is empty for now: the tab renders the timeline's empty
 * state and lights up the moment #302 lands. When it does, `raw` becomes the
 * REST first-paint aggregate and live WS pushes feed `upsertActivityEvents`
 * (by-id upsert) — the reducer/projection below and every consumer stay
 * unchanged.
 */
export function useAgentActivityEvents(_agentId: string): {
  events: ActivityEvent[];
  latest: ActivityEvent | null;
  isLoading: boolean;
} {
  // TODO(#302): replace `raw` with useQuery(agentActivityEventsOptions(agentId))
  // for REST first-paint + a WS subscription that calls upsertActivityEvents on
  // each pushed event, once Barry's #302 endpoint lands.
  const raw = useMemo<ActivityEvent[]>(() => [], []);
  const events = useMemo(() => upsertActivityEvents([], raw), [raw]);
  const latest = useMemo(() => projectLatestActivity(events), [events]);
  return { events, latest, isLoading: false };
}
