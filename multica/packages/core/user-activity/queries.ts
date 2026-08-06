import { keepPreviousData, queryOptions, useQuery } from "@tanstack/react-query";
import { api } from "../api";
import type { UserActivityTab } from "../types";

export const userActivityKeys = {
  all: (wsId: string) => ["user-activity", wsId] as const,
  list: (wsId: string, tab: UserActivityTab) =>
    [...userActivityKeys.all(wsId), "list", tab] as const,
};

/**
 * Activity feed query (LRM-424):
 * - `refetchOnMount: "always"` — re-enter shows cache immediately, then silent refresh
 * - `placeholderData: keepPreviousData` — tab switches keep prior rows instead of blanking
 */
export function userActivityListOptions(wsId: string, tab: UserActivityTab) {
  return queryOptions({
    queryKey: userActivityKeys.list(wsId, tab),
    queryFn: () => api.listUserActivity({ tab }),
    placeholderData: keepPreviousData,
    refetchOnMount: "always",
  });
}

/** Sidebar badge: count activity rows with unread_count > 0 (threads + inbox). */
export function useUserActivityUnreadCount(wsId: string | null | undefined): number {
  const { data } = useQuery({
    ...userActivityListOptions(wsId ?? "", "all"),
    enabled: !!wsId,
    // Badge only needs a stable count — avoid remount churn from Activity's
    // refetchOnMount:always when the sidebar is already observing the same key.
    refetchOnMount: true,
    select: (response) =>
      response.items.filter((item) => item.unread_count > 0).length,
  });
  return data ?? 0;
}
