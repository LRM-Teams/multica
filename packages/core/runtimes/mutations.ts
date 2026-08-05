import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { runtimeKeys } from "./queries";
import { workspaceKeys } from "../workspace/queries";
import { agentTaskSnapshotKeys } from "../agents/queries";

export function useDeleteRuntime(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (runtimeId: string) => api.deleteRuntime(runtimeId),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
      qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
      qc.invalidateQueries({ queryKey: agentTaskSnapshotKeys.all(wsId) });
    },
  });
}

// Computer / host one-click delete (LRM-438). Prefer this over looping
// useDeleteRuntime — per-row DELETE is explicitly not the product path.
export function useDeleteRuntimesByDaemon(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      daemonId,
      runtimeMode,
    }: {
      daemonId: string;
      runtimeMode?: string;
    }) => api.deleteRuntimesByDaemon(daemonId, { runtimeMode }),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
    },
  });
}

export function useRemoveAgentsByDaemon(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      daemonId,
      runtimeMode,
      expectedActiveAgentIds,
    }: {
      daemonId: string;
      runtimeMode?: string;
      expectedActiveAgentIds: string[];
    }) => api.removeAgentsByDaemon(daemonId, expectedActiveAgentIds, { runtimeMode }),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
      qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
      qc.invalidateQueries({ queryKey: agentTaskSnapshotKeys.all(wsId) });
    },
  });
}

// Cascade-mode counterpart to useDeleteRuntime. The dialog routes here when
// the strict DELETE refused with `runtime_has_active_agents` (or when the
// caller already knows the runtime has active agents and wants to skip the
// pre-flight refusal). Mutation fn returns the server-reported counts so
// the caller can render a richer success toast.
//
// Invalidates runtimes (the list / detail), workspace agents (the cascade
// archives them) and the agent presence snapshot (cascade also cancels
// queued/running tasks). Without the agent-side invalidation the Agents
// page would keep showing the just-archived rows as live until a refetch.
export function useArchiveAgentsAndDeleteRuntime(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      runtimeId,
      expectedActiveAgentIds,
    }: {
      runtimeId: string;
      expectedActiveAgentIds: string[];
    }) => api.archiveAgentsAndDeleteRuntime(runtimeId, expectedActiveAgentIds),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
      qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
      qc.invalidateQueries({ queryKey: agentTaskSnapshotKeys.all(wsId) });
    },
  });
}

// useUpdateRuntime patches the user-editable runtime display name.
// Invalidates runtime and agent projections that render the machine label.
export function useUpdateRuntime(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      runtimeId,
      patch,
    }: {
      runtimeId: string;
      patch: {
        display_name?: string | null;
      };
    }) => api.updateRuntime(runtimeId, patch),
    onSettled: () => {
      // Keep Profile/create runtime pickers in sync when display_name changes
      // on the Runtimes page (LRM-925).
      qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
      qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
    },
  });
}

/** LRM-810 — on-demand scan of agent workspace directories on a runtime host. */
export function useRuntimeAgentWorkspaces(
  runtimeId: string | null | undefined,
  enabled: boolean,
) {
  return useQuery({
    queryKey: ["runtimes", runtimeId, "agent-workspaces"],
    queryFn: () => api.listRuntimeAgentWorkspaces(runtimeId!),
    enabled: enabled && !!runtimeId,
  });
}

export function useDeleteRuntimeAgentWorkspace(runtimeId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (dirName: string) =>
      api.deleteRuntimeAgentWorkspace(runtimeId, dirName),
    onSettled: () => {
      qc.invalidateQueries({
        queryKey: ["runtimes", runtimeId, "agent-workspaces"],
      });
    },
  });
}
