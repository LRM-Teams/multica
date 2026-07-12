import { useCallback, useMemo } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  agentActivityEventsKeys,
  agentActivityEventsOptions,
} from "@multica/core/agents";
import { useWSEvent } from "@multica/core/realtime";
import type { AgentActivityEventRealtimePayload } from "@multica/core/types";
import type { ActivityEvent } from "./activity-event";
import { projectLatestActivity, upsertActivityEvents } from "./activity-event-reducer";

/**
 * The single shared read-model for an agent's Activity event stream (#302 FE
 * consumer). Per the #267 contract the BE aggregates source-backed event facts
 * with a stable id; the FE reads REST first-paint, **upserts live WS events by
 * id**, and projects latest-state.
 *
 * Both consumers eat this ONE hook's output — no per-surface reducer drift:
 *  - `events` → the #421 Activity timeline (full stream; it derives the directed
 *    3-state block itself from raw kind/inbox/wake facts).
 *  - `latest` → the DM/panel header activity word + hover card (latest-state).
 */
export function useAgentActivityEvents(agentId: string): {
  events: ActivityEvent[];
  latest: ActivityEvent | null;
  isLoading: boolean;
} {
  const queryClient = useQueryClient();
  const { data = [], isLoading } = useQuery({
    ...agentActivityEventsOptions(agentId),
    enabled: !!agentId,
  });

  // Live updates: the BE pushes the full hydrated event under a stable id
  // (`agent_activity:event`), so upsert it straight into the query cache by id —
  // no refetch round-trip; a WS push for a known id simply replaces its row
  // (e.g. a `thinking` aggregate that grew). Only the degraded, `event`-less
  // push (id only) invalidates for a reload.
  const onActivityEvent = useCallback(
    (payload: unknown) => {
      const p = payload as AgentActivityEventRealtimePayload;
      if (!agentId || p.agent_id !== agentId) return;
      if (p.event) {
        const event = p.event;
        queryClient.setQueryData<ActivityEvent[]>(
          agentActivityEventsKeys.all(agentId),
          (prev = []) => upsertActivityEvents(prev, event),
        );
      } else {
        queryClient.invalidateQueries({
          queryKey: agentActivityEventsKeys.all(agentId),
        });
      }
    },
    [agentId, queryClient],
  );
  useWSEvent("agent_activity:event", onActivityEvent);

  // Normalize order defensively (REST is already ordered; this also dedupes if a
  // WS event raced the first paint).
  const events = useMemo(() => upsertActivityEvents([], data), [data]);
  const latest = useMemo(() => projectLatestActivity(events), [events]);
  return { events, latest, isLoading };
}
