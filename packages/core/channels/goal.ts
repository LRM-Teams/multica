import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api";
import type { CreateChannelGoalRequest, UpdateChannelGoalRequest } from "../types";
import type { ChannelGoalEnvelope } from "../types";

export const channelGoalKeys = {
  all: () => ["channel-goal"] as const,
  detail: (channelId: string) => [...channelGoalKeys.all(), channelId] as const,
  processes: (channelId: string) =>
    [...channelGoalKeys.detail(channelId), "process"] as const,
  process: (channelId: string, managerAgentId: string) =>
    [...channelGoalKeys.processes(channelId), managerAgentId] as const,
};

export function channelGoalOptions(channelId: string) {
  return queryOptions({
    queryKey: channelGoalKeys.detail(channelId),
    // Some embedded/test hosts inject a deliberately partial API adapter.
    // A missing optional surface must render as "no goal", never poison the
    // shared Query cache with undefined.
    queryFn: async () =>
      typeof api.getChannelGoal === "function"
        ? (await api.getChannelGoal(channelId)) ?? { goal: null }
        : { goal: null },
    enabled: !!channelId,
  });
}

export function channelGoalProcessesOptions(channelId: string) {
  return queryOptions({
    queryKey: channelGoalKeys.processes(channelId),
    queryFn: async () =>
      typeof api.listChannelGoalProcesses === "function"
        ? await api.listChannelGoalProcesses(channelId)
        : { goal_id: "", processes: [] },
    enabled: !!channelId,
  });
}

export function channelGoalProcessOptions(channelId: string, managerAgentId: string) {
  return queryOptions({
    queryKey: channelGoalKeys.process(channelId, managerAgentId),
    queryFn: async () => {
      if (typeof api.getChannelGoalProcess !== "function") return { process: null };
      try {
        return await api.getChannelGoalProcess(channelId, managerAgentId);
      } catch (error) {
        // 404 = manager has not written process md yet (explicit empty, not failure).
        if (error instanceof ApiError && error.status === 404) {
          return { process: null };
        }
        throw error;
      }
    },
    enabled: !!channelId && !!managerAgentId,
  });
}

export function useCreateChannelGoal(channelId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateChannelGoalRequest) => api.createChannelGoal(channelId, input),
    onSuccess: (data) => queryClient.setQueryData(channelGoalKeys.detail(channelId), data),
    onSettled: () => queryClient.invalidateQueries({ queryKey: channelGoalKeys.detail(channelId) }),
  });
}

export function useUpdateChannelGoal(channelId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: UpdateChannelGoalRequest) => api.updateChannelGoal(channelId, input),
    onMutate: async (input) => {
      const key = channelGoalKeys.detail(channelId);
      await queryClient.cancelQueries({ queryKey: key });
      const previous = queryClient.getQueryData<ChannelGoalEnvelope>(key);
      if (previous?.goal && previous.goal.version === input.expected_version) {
        const { expected_version: _expectedVersion, ...patch } = input;
        queryClient.setQueryData<ChannelGoalEnvelope>(key, {
          goal: {
            ...previous.goal,
            ...patch,
            version: previous.goal.version + 1,
          },
        });
      }
      return { previous };
    },
    onError: (_error, _input, context) => {
      if (context?.previous) {
        queryClient.setQueryData(channelGoalKeys.detail(channelId), context.previous);
      }
    },
    onSuccess: (data) => queryClient.setQueryData(channelGoalKeys.detail(channelId), data),
    onSettled: () => queryClient.invalidateQueries({ queryKey: channelGoalKeys.detail(channelId) }),
  });
}
