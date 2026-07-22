import { useMutation, useQueryClient } from "@tanstack/react-query";
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
      qc.setQueriesData<UserActivityListResponse>(
        { queryKey: userActivityKeys.all(wsId) },
        (old) =>
          old
            ? {
                ...old,
                items: old.items.map((item) => markActivityItemRead(item)),
              }
            : old,
      );
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

function markActivityItemRead(item: UserActivityItem): UserActivityItem {
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
