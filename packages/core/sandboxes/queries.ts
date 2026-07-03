import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const sandboxKeys = {
  all: (wsId: string) => ["sandboxes", wsId] as const,
  list: (wsId: string) => [...sandboxKeys.all(wsId), "list"] as const,
  detail: (wsId: string, instanceId: string) => [...sandboxKeys.all(wsId), "detail", instanceId] as const,
  bindings: (wsId: string) => [...sandboxKeys.all(wsId), "bindings"] as const,
  nodes: () => ["sandboxes", "nodes"] as const,
};

export function sandboxListOptions(wsId: string) {
  return queryOptions({
    queryKey: sandboxKeys.list(wsId),
    queryFn: () => api.listSandboxes(),
    enabled: !!wsId,
    refetchInterval: 2000,
  });
}

export function sandboxDetailOptions(wsId: string, instanceId: string) {
  return queryOptions({
    queryKey: sandboxKeys.detail(wsId, instanceId),
    queryFn: () => api.getSandbox(instanceId),
    enabled: !!wsId && !!instanceId,
    refetchInterval: 2000,
  });
}

export function sandboxBindingListOptions(wsId: string) {
  return queryOptions({
    queryKey: sandboxKeys.bindings(wsId),
    queryFn: () => api.listSandboxBindings(wsId),
    enabled: !!wsId,
    refetchInterval: 5000,
  });
}

export function sandboxNodeListOptions() {
  return queryOptions({
    queryKey: sandboxKeys.nodes(),
    queryFn: () => api.listSandboxNodes(),
  });
}
