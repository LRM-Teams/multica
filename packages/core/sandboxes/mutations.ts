import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { sandboxKeys } from "./queries";

function useInvalidateSandboxes(wsId: string) {
  const queryClient = useQueryClient();
  return () => queryClient.invalidateQueries({ queryKey: sandboxKeys.all(wsId) });
}

export function useCreateSandboxMutation(
  wsId: string,
  payload: () => { node_id?: string; runtime: Record<string, string> },
) {
  const invalidate = useInvalidateSandboxes(wsId);
  return useMutation({
    mutationFn: () => {
      const next = payload();
      return api.createSandbox({
        node_id: next.node_id,
        template: "default",
        runtime: next.runtime,
      });
    },
    onSuccess: invalidate,
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
