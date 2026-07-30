import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const sandboxKeys = {
  all: (wsId: string) => ["sandboxes", wsId] as const,
  list: (wsId: string) => [...sandboxKeys.all(wsId), "list"] as const,
  detail: (wsId: string, instanceId: string) => [...sandboxKeys.all(wsId), "detail", instanceId] as const,
  bindings: (wsId: string) => [...sandboxKeys.all(wsId), "bindings"] as const,
  nodes: () => ["sandboxes", "nodes"] as const,
  nodeTemplates: (nodeId: string) => ["sandboxes", "nodes", nodeId, "templates"] as const,
  nodeDockerImages: (nodeId: string) => ["sandboxes", "nodes", nodeId, "docker-images"] as const,
  nodeSnapshots: (nodeId: string) => ["sandboxes", "nodes", nodeId, "snapshots"] as const,
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
    refetchInterval: 5000,
  });
}

export function sandboxNodeTemplatesOptions(nodeId: string) {
  return queryOptions({
    queryKey: sandboxKeys.nodeTemplates(nodeId),
    queryFn: () => api.listSandboxNodeTemplates(nodeId),
    enabled: !!nodeId,
    refetchInterval: 10_000,
  });
}

export function sandboxNodeDockerImagesOptions(nodeId: string) {
  return queryOptions({
    queryKey: sandboxKeys.nodeDockerImages(nodeId),
    queryFn: () => api.listSandboxNodeDockerImages(nodeId),
    enabled: !!nodeId,
    refetchInterval: 10_000,
  });
}

export function sandboxNodeSnapshotsOptions(nodeId: string) {
  return queryOptions({
    queryKey: sandboxKeys.nodeSnapshots(nodeId),
    queryFn: () => api.listSandboxNodeSnapshots(nodeId),
    enabled: !!nodeId,
    // Only poll while healthy so a bad request does not spam the UI.
    refetchInterval: (query) => (query.state.status === "success" ? 5_000 : false),
  });
}
