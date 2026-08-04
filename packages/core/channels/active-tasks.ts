import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import type { ChannelActiveTask } from "../types";

export const activeChannelTasksKeys = {
  all: (channelId: string) => ["channel-active-tasks", channelId] as const,
};

// Query-authoritative view of which agents are mid-task in a channel. Unlike the
// transient `channel:typing` broadcast (which fires once at mention and once at
// completion, and shows nothing if a tab misses the event), this recovers the
// agent's lifecycle stage on every fetch. Polls so queued→running transitions
// surface even when no WS event accompanies them; WS handlers also invalidate
// the key for prompt start/end updates.
export function activeChannelTasksOptions(channelId: string) {
  return queryOptions({
    queryKey: activeChannelTasksKeys.all(channelId),
    queryFn: async ({ signal }): Promise<ChannelActiveTask[]> => {
      const res = await api.listChannelActiveTasks(channelId, { signal });
      return res.tasks ?? [];
    },
    enabled: !!channelId,
    refetchInterval: 3000,
  });
}
