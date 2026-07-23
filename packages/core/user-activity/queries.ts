import { queryOptions, useQuery } from "@tanstack/react-query";
import { api } from "../api";
import type { UserActivityTab } from "../types";

export const userActivityKeys = {
  all: (wsId: string) => ["user-activity", wsId] as const,
  list: (wsId: string, tab: UserActivityTab) =>
    [...userActivityKeys.all(wsId), "list", tab] as const,
};

export function userActivityListOptions(wsId: string, tab: UserActivityTab) {
  return queryOptions({
    queryKey: userActivityKeys.list(wsId, tab),
    queryFn: () => api.listUserActivity({ tab }),
  });
}

/** Sidebar badge: count activity rows with unread_count > 0 (threads + inbox). */
export function useUserActivityUnreadCount(wsId: string | null | undefined): number {
  const { data } = useQuery({
    ...userActivityListOptions(wsId ?? "", "all"),
    enabled: !!wsId,
    select: (response) =>
      response.items.filter((item) => item.unread_count > 0).length,
  });
  return data ?? 0;
}
