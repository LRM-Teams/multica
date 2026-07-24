import { useCallback, useEffect, useMemo, useState } from "react";
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import {
  agentActivityEventsKeys,
  agentActivityEventsOptions,
} from "@multica/core/agents";
import { useWSEvent, useWSReconnect } from "@multica/core/realtime";
import type { AgentActivityEventRealtimePayload } from "@multica/core/types";
import type { ActivityEvent } from "./activity-event";
import { projectLatestActivity, upsertActivityEvents } from "./activity-event-reducer";

// Cap the live-event buffer so a long-open panel can't grow it without bound.
// It only has to bridge the gap between "WS delivered an event" and "a REST
// page includes it"; the most-recent few hundred always cover the first-paint
// window (REST returns the latest ~50). Older rows beyond this fall to REST /
// the cursor-pagination follow-up. Recency-capped (never id-pruned) so a fresh
// WS aggregate for a recent id is never dropped in favour of a staler REST row.
const LIVE_EVENT_BUFFER_CAP = 500;

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
  /** First-paint REST failed with no cached rows (LRM-563 empty vs error). */
  isError: boolean;
  /** Retry the activity events query after a load failure. */
  refetch: () => void;
  /** Load the next (older) page — drives scroll-up history in the timeline. */
  loadOlder: () => void;
  /** More (older) pages remain beyond what's already fetched. */
  hasOlder: boolean;
  /** An older page is currently being fetched. */
  isLoadingOlder: boolean;
} {
  const queryClient = useQueryClient();
  const {
    data,
    isLoading,
    isError,
    refetch,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
  } = useInfiniteQuery({
    ...agentActivityEventsOptions(agentId),
    enabled: !!agentId,
  });
  // Flatten every fetched page's events, deduped by id. Pages are newest-first
  // and older pages append as the reader scrolls up; a boundary row can recur
  // across two pages, so dedupe here (the reducer's WS-merge only dedupes
  // `current` when the live buffer is non-empty). Order is irrelevant — the
  // reducer below sorts by `occurred_at`.
  const restEvents = useMemo(() => {
    if (!data) return [];
    const byId = new Map<string, ActivityEvent>();
    for (const page of data.pages) for (const event of page.events) byId.set(event.id, event);
    return Array.from(byId.values());
  }, [data]);

  // Live WS events are held in their OWN state, separate from the REST query
  // cache, and merged at read time below. Writing them straight into the query
  // cache (the old `setQueryData` path) lost the race: a WS event delivered
  // while the first-paint fetch is still in flight was clobbered the instant
  // that fetch resolved with a pre-event REST snapshot (the fetch REPLACES the
  // cache — the queryFn returns the whole page, it does not merge). That is
  // exactly why a `wake_attempt` ("Message received") fired at the very start of
  // a round vanished live yet reappeared on hard-refresh: REST always had it,
  // the live merge dropped it. Keeping WS events in a dedicated buffer means a
  // REST refetch can no longer drop a live event — it only replaces `data`.
  const [liveEvents, setLiveEvents] = useState<ActivityEvent[]>([]);

  // The buffer is per-agent; reset it when the viewed agent changes so a prior
  // agent's live rows never leak into the next one (the WS filter below only
  // guards what we ADD, not what a stale buffer still holds).
  useEffect(() => {
    setLiveEvents([]);
  }, [agentId]);

  // Live updates: the BE pushes the full hydrated event under a stable id
  // (`agent_activity:event`); upsert it into the live buffer by id (a WS push
  // for a known id simply replaces its row — e.g. a `thinking` aggregate that
  // grew). Only the degraded, `event`-less push (id only) falls back to
  // invalidating the REST query for a reload.
  const onActivityEvent = useCallback(
    (payload: unknown) => {
      const p = payload as AgentActivityEventRealtimePayload;
      if (!agentId || p.agent_id !== agentId) return;
      if (p.event) {
        const event = p.event;
        setLiveEvents((prev) => upsertActivityEvents(prev, event).slice(-LIVE_EVENT_BUFFER_CAP));
      } else {
        queryClient.invalidateQueries({
          queryKey: agentActivityEventsKeys.all(agentId),
        });
      }
    },
    [agentId, queryClient],
  );
  useWSEvent("agent_activity:event", onActivityEvent);

  // Backfill on WS reconnect: any events emitted while the socket was down are
  // absent from BOTH the REST snapshot (`data`) and the live buffer, so refetch
  // the per-agent event query to reconcile the gap. The global reconnect
  // handler invalidates `agentActivityKeys` (workspace presence), NOT this
  // per-agent `agentActivityEventsKeys` stream, so — like the issue hooks — this
  // hook owns its own reconnect refetch.
  useWSReconnect(
    useCallback(() => {
      if (!agentId) return;
      queryClient.invalidateQueries({
        queryKey: agentActivityEventsKeys.all(agentId),
      });
    }, [agentId, queryClient]),
  );

  // Merge REST first-paint with the live buffer (WS applied last, so a fresher
  // WS aggregate wins for a shared id). Also normalizes order and dedupes.
  const events = useMemo(
    () => upsertActivityEvents(restEvents, liveEvents),
    [restEvents, liveEvents],
  );
  const latest = useMemo(() => projectLatestActivity(events), [events]);
  const loadOlder = useCallback(() => {
    if (hasNextPage && !isFetchingNextPage) fetchNextPage();
  }, [hasNextPage, isFetchingNextPage, fetchNextPage]);
  const handleRefetch = useCallback(() => {
    void refetch();
  }, [refetch]);

  return {
    events,
    latest,
    isLoading,
    isError,
    refetch: handleRefetch,
    loadOlder,
    hasOlder: hasNextPage,
    isLoadingOlder: isFetchingNextPage,
  };
}
