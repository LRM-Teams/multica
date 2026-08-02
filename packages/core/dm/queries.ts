import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const dmKeys = {
  all: (wsId: string) => ["dm", wsId] as const,
  list: (wsId: string) => [...dmKeys.all(wsId), "list"] as const,
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
    // LRM-844: first-paint DM list must not sit paused when onlineManager is
    // falsely offline. Fail the fetch instead of hanging the skeleton forever;
    // WS invalidation still keeps the cache fresh after the first success.
    networkMode: "always",
  });
}
