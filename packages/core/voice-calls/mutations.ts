import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type {
  CreateVoiceCallRequest,
  GetVoiceCallResponse,
} from "../types";
import { voiceCallKeys } from "./queries";

export function useCreateVoiceCall(workspaceId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationKey: [...voiceCallKeys.all(workspaceId), "create"],
    mutationFn: (input: CreateVoiceCallRequest) =>
      api.createVoiceCall(workspaceId, input),
    onSuccess: (created) => {
      queryClient.setQueryData<GetVoiceCallResponse>(
        voiceCallKeys.detail(workspaceId, created.call.id),
        { call: created.call },
      );
    },
  });
}

export function useStopVoiceCall(workspaceId: string, callId: string) {
  const queryClient = useQueryClient();
  const queryKey = voiceCallKeys.detail(workspaceId, callId);
  return useMutation({
    mutationKey: [...queryKey, "stop"],
    mutationFn: () => api.stopVoiceCall(workspaceId, callId),
    onMutate: async () => {
      await queryClient.cancelQueries({ queryKey });
      const previous = queryClient.getQueryData<GetVoiceCallResponse>(queryKey);
      queryClient.setQueryData<GetVoiceCallResponse>(
        queryKey,
        (current) => current
          ? {
            call: {
              ...current.call,
              status: "ending",
              updated_at: new Date().toISOString(),
            },
          }
          : current,
      );
      return { previous };
    },
    onError: (_error, _variables, context) => {
      if (context?.previous) {
        queryClient.setQueryData(queryKey, context.previous);
      }
    },
    onSuccess: (stopped) => {
      queryClient.setQueryData(queryKey, stopped);
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey });
    },
  });
}
