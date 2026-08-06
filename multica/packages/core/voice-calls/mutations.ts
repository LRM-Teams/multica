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

export function useConnectVoiceCall(workspaceId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationKey: [...voiceCallKeys.all(workspaceId), "connect"],
    mutationFn: (callId: string) => api.connectVoiceCall(workspaceId, callId),
    onSuccess: (connected, callId) => {
      queryClient.setQueryData(
        voiceCallKeys.detail(workspaceId, callId),
        connected,
      );
    },
  });
}

export function useAnswerVoiceCall(workspaceId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationKey: [...voiceCallKeys.all(workspaceId), "answer"],
    mutationFn: (callId: string) => api.answerVoiceCall(workspaceId, callId),
    onSuccess: (answered, callId) => {
      queryClient.setQueryData(
        voiceCallKeys.detail(workspaceId, callId),
        answered,
      );
    },
  });
}

export function useStopVoiceCall(workspaceId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationKey: [...voiceCallKeys.all(workspaceId), "stop"],
    mutationFn: (callId: string) => api.stopVoiceCall(workspaceId, callId),
    onMutate: async (callId) => {
      const queryKey = voiceCallKeys.detail(workspaceId, callId);
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
      return { previous, queryKey };
    },
    onError: (_error, _variables, context) => {
      if (context?.previous && context.queryKey) {
        queryClient.setQueryData(context.queryKey, context.previous);
      }
    },
    onSuccess: (stopped, callId) => {
      queryClient.setQueryData(
        voiceCallKeys.detail(workspaceId, callId),
        stopped,
      );
    },
    onSettled: (_data, _error, callId) => {
      void queryClient.invalidateQueries({
        queryKey: voiceCallKeys.detail(workspaceId, callId),
      });
    },
  });
}

export function useStartVoiceCallDuplex(workspaceId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationKey: [...voiceCallKeys.all(workspaceId), "duplex"],
    mutationFn: (callId: string) =>
      api.startVoiceCallDuplex(workspaceId, callId),
    onSuccess: (started, callId) => {
      queryClient.setQueryData(
        voiceCallKeys.detail(workspaceId, callId),
        { call: started.call },
      );
    },
  });
}
