import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { CreateSandboxRequest, UpdateSandboxRequest } from "../types";
import { sandboxKeys } from "./queries";

function useInvalidateSandboxes(wsId: string) {
  const queryClient = useQueryClient();
  return () => queryClient.invalidateQueries({ queryKey: sandboxKeys.all(wsId) });
}

export function useCreateSandboxMutation(wsId: string) {
  const invalidate = useInvalidateSandboxes(wsId);
  return useMutation({
    mutationFn: (data: CreateSandboxRequest) => api.createSandbox(data),
    onSuccess: invalidate,
  });
}

export function useUpdateSandboxMutation(wsId: string, instanceId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: UpdateSandboxRequest) => api.updateSandbox(instanceId, data),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: sandboxKeys.all(wsId) });
    },
  });
}

export function useStopSandboxMutation(wsId: string) {
  const invalidate = useInvalidateSandboxes(wsId);
  return useMutation({
    mutationFn: (id: string) => api.stopSandbox(id),
    onSuccess: invalidate,
  });
}

export function useResumeSandboxMutation(wsId: string) {
  const invalidate = useInvalidateSandboxes(wsId);
  return useMutation({
    mutationFn: (id: string) => api.resumeSandbox(id),
    onSuccess: invalidate,
  });
}

export function useDeleteSandboxMutation(wsId: string) {
  const invalidate = useInvalidateSandboxes(wsId);
  return useMutation({
    mutationFn: (id: string) => api.deleteSandbox(id),
    onSuccess: invalidate,
  });
}
