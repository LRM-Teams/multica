import { useMutation, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { inboxKeys } from "../inbox/queries";
import { useWorkspaceId } from "../hooks";
import { userActivityKeys } from "./queries";
import type { UserActivityItem, UserActivityListResponse } from "../types";

export function useMarkAllUserActivityRead() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: () => api.markAllUserActivityRead(),
    onMutate: async () => {
      await qc.cancelQueries({ queryKey: userActivityKeys.all(wsId) });
      const prev = qc.getQueriesData<UserActivityListResponse>({
        queryKey: userActivityKeys.all(wsId),
      });
      for (const [key, data] of prev) {
        if (!data) continue;
        const tab = activityTabFromQueryKey(key);
        const items = data.items.map((item) => markActivityItemRead(item));
        qc.setQueryData<UserActivityListResponse>(key, {
          ...data,
          items: tab === "unread" ? [] : items,
        });
      }
      return { prev };
    },
    onError: (_err, _vars, ctx) => {
      for (const [key, data] of ctx?.prev ?? []) {
        qc.setQueryData(key, data);
      }
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: userActivityKeys.all(wsId) });
      qc.invalidateQueries({ queryKey: inboxKeys.all(wsId) });
    },
  });
}

/** Snapshot + optimistic clear for one Activity thread row (by root message id). */
export async function optimisticallyMarkActivityThreadRead(
  qc: QueryClient,
  wsId: string,
  rootMessageId: string,
): Promise<Array<[readonly unknown[], UserActivityListResponse | undefined]>> {
  await qc.cancelQueries({ queryKey: userActivityKeys.all(wsId) });
  const prev = qc.getQueriesData<UserActivityListResponse>({
    queryKey: userActivityKeys.all(wsId),
  });
  for (const [key, data] of prev) {
    if (!data) continue;
    const tab = activityTabFromQueryKey(key);
    const items = data.items.map((item) =>
      isActivityThreadMatch(item, rootMessageId) ? markActivityItemRead(item) : item,
    );
    qc.setQueryData<UserActivityListResponse>(key, {
      ...data,
      items: tab === "unread" ? items.filter((item) => item.unread_count > 0) : items,
    });
  }
  return prev;
}

/** Snapshot + optimistic clear for one Activity inbox row (by inbox item id). */
export async function optimisticallyMarkActivityInboxRead(
  qc: QueryClient,
  wsId: string,
  inboxId: string,
): Promise<Array<[readonly unknown[], UserActivityListResponse | undefined]>> {
  await qc.cancelQueries({ queryKey: userActivityKeys.all(wsId) });
  const prev = qc.getQueriesData<UserActivityListResponse>({
    queryKey: userActivityKeys.all(wsId),
  });
  for (const [key, data] of prev) {
    if (!data) continue;
    const tab = activityTabFromQueryKey(key);
    const items = data.items.map((item) =>
      isActivityInboxMatch(item, inboxId) ? markActivityItemRead(item) : item,
    );
    qc.setQueryData<UserActivityListResponse>(key, {
      ...data,
      items: tab === "unread" ? items.filter((item) => item.unread_count > 0) : items,
    });
  }
  return prev;
}

export function restoreActivityQueries(
  qc: QueryClient,
  prev: Array<[readonly unknown[], UserActivityListResponse | undefined]> | undefined,
) {
  for (const [key, data] of prev ?? []) {
    qc.setQueryData(key, data);
  }
}

export function markActivityItemRead(item: UserActivityItem): UserActivityItem {
  if (item.unread_count <= 0) return item;
  if (item.kind === "inbox" && item.inbox) {
    return {
      ...item,
      unread_count: 0,
      inbox: { ...item.inbox, read: true },
    };
  }
  return { ...item, unread_count: 0 };
}

function activityTabFromQueryKey(key: readonly unknown[]): string | null {
  // ["user-activity", wsId, "list", tab]
  if (key.length < 4 || key[2] !== "list") return null;
  const tab = key[3];
  return typeof tab === "string" ? tab : null;
}

function isActivityThreadMatch(item: UserActivityItem, rootMessageId: string): boolean {
  if (item.kind !== "thread") return false;
  return item.id === rootMessageId || item.thread_root_message_id === rootMessageId;
}

function isActivityInboxMatch(item: UserActivityItem, inboxId: string): boolean {
  if (item.kind !== "inbox") return false;
  return item.id === inboxId || item.inbox?.id === inboxId;
}
