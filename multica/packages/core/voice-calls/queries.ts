import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const voiceCallKeys = {
  all: (workspaceId: string) => ["voice-calls", workspaceId] as const,
  detail: (workspaceId: string, callId: string) => [
    ...voiceCallKeys.all(workspaceId),
    "detail",
    callId,
  ] as const,
};

export function voiceCallOptions(workspaceId: string, callId: string) {
  return queryOptions({
    queryKey: voiceCallKeys.detail(workspaceId, callId),
    queryFn: () => api.getVoiceCall(workspaceId, callId),
    enabled: Boolean(workspaceId && callId),
    staleTime: Infinity,
  });
}
