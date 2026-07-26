import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const dmKeys = {
  all: (wsId: string) => ["dm", wsId] as const,
  list: (wsId: string) => [...dmKeys.all(wsId), "list"] as const,
  /** #692 workspace-level agent↔agent DM global control (pause-all state). */
  a2aGlobal: (wsId: string) => [...dmKeys.all(wsId), "a2a-global"] as const,
};

/**
 * Unified DM list — kind='dm' channels the caller is in unioned with the
 * caller's unbound legacy chat_sessions, deduped by peer and recency-sorted by
 * the server. `staleTime: Infinity` mirrors the chat sessions list: the cache
 * is kept fresh by WS invalidation (channel:message / chat:message), not by
 * polling.
 */
export function dmListOptions(wsId: string) {
  return queryOptions({
    queryKey: dmKeys.list(wsId),
    queryFn: () => api.listDMs(),
    enabled: !!wsId,
    staleTime: Infinity,
  });
}

/**
 * #692 workspace-level agent↔agent DM global control state (the "pause all
 * agent DMs" toggle). Independent of any DM channel — read from
 * `/api/dm/a2a-control`. Kept fresh by explicit invalidation after the owner
 * toggles it, not by polling.
 */
export function agentDMGlobalControlOptions(wsId: string) {
  return queryOptions({
    queryKey: dmKeys.a2aGlobal(wsId),
    queryFn: () => api.getAgentDMGlobalControl(),
    enabled: !!wsId,
    staleTime: Infinity,
  });
}
