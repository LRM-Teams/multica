import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import type { AgentTaskFeedCursor } from "../types";

export const agentTaskSnapshotKeys = {
  all: (wsId: string) => ["workspaces", wsId, "agent-task-snapshot"] as const,
  list: (wsId: string) => [...agentTaskSnapshotKeys.all(wsId), "list"] as const,
};

export const agentActivityKeys = {
  all: (wsId: string) => ["workspaces", wsId, "agent-activity"] as const,
  last30d: (wsId: string) => [...agentActivityKeys.all(wsId), "30d"] as const,
};

export const agentRunCountsKeys = {
  all: (wsId: string) => ["workspaces", wsId, "agent-run-counts"] as const,
  last30d: (wsId: string) => [...agentRunCountsKeys.all(wsId), "30d"] as const,
};

// Workspace-scoped agent task snapshot — every active task plus each agent's
// most recent terminal task. This is the single shared source of truth that
// powers per-agent presence derivation across the app. One fetch per
// workspace; all agent dots / hover cards / list rows derive presence from
// this cache with zero additional network traffic.
//
// The 30s staleTime is a safety net only; the primary freshness signal is
// WS task events, which invalidate this query immediately. Without WS,
// presence still updates within 30s on focus / mount.
export function agentTaskSnapshotOptions(wsId: string) {
  return queryOptions({
    queryKey: agentTaskSnapshotKeys.list(wsId),
    queryFn: () => api.getAgentTaskSnapshot(),
    staleTime: 30 * 1000,
    gcTime: 5 * 60 * 1000,
    refetchOnWindowFocus: true,
  });
}

export const agentTaskFeedKeys = {
  all: (wsId: string) => ["workspaces", wsId, "agent-task-feed"] as const,
  list: (wsId: string) => [...agentTaskFeedKeys.all(wsId), "list"] as const,
};

// Workspace-wide completed/failed/total agent-task counts for the overview
// "tasks done" KPI. Channel replies are completed chat tasks, so they're
// counted here too — consistent with the agent activity feed.
export function agentTaskStatsOptions(wsId: string) {
  return queryOptions({
    queryKey: ["workspaces", wsId, "agent-task-stats"] as const,
    queryFn: () => api.getAgentTaskStats(),
    enabled: !!wsId,
    staleTime: 30 * 1000,
  });
}

// Workspace-wide, cursor-paginated feed of terminal agent tasks (one row per
// completed/failed/cancelled task), newest first. Infinite query — the overview
// timeline fetches the next (older) page as the user scrolls.
export function agentTaskFeedOptions(wsId: string, limit = 30) {
  return infiniteQueryOptions({
    queryKey: agentTaskFeedKeys.list(wsId),
    queryFn: ({ pageParam }) => api.listAgentTaskFeed({ before: pageParam, limit }),
    initialPageParam: null as AgentTaskFeedCursor | null,
    getNextPageParam: (lastPage) =>
      lastPage.has_more ? lastPage.next_cursor ?? undefined : undefined,
    enabled: !!wsId,
    staleTime: 30 * 1000,
  });
}

// Workspace-wide daily task activity for the last 30 days, anchored on
// completed_at. One fetch backs both the Agents-list sparkline (which
// only uses the trailing 7 buckets via `summarizeActivityWindow`) and
// the agent detail "Last 30 days" panel. WS task lifecycle events
// invalidate this query in useRealtimeSync; the staleTime is a
// tab-focus safety net.
export function agentActivity30dOptions(wsId: string) {
  return queryOptions({
    queryKey: agentActivityKeys.last30d(wsId),
    queryFn: () => api.getWorkspaceAgentActivity30d(),
    staleTime: 60 * 1000,
    gcTime: 5 * 60 * 1000,
    refetchOnWindowFocus: true,
  });
}

// Workspace-wide 30-day run counts for the Agents-list RUNS column. Same
// single-fetch / WS-invalidate pattern as activity24hOptions.
export function agentRunCounts30dOptions(wsId: string) {
  return queryOptions({
    queryKey: agentRunCountsKeys.last30d(wsId),
    queryFn: () => api.getWorkspaceAgentRunCounts(),
    staleTime: 60 * 1000,
    gcTime: 5 * 60 * 1000,
    refetchOnWindowFocus: true,
  });
}

export const agentMemoryKeys = {
  all: (wsId: string) => ["workspaces", wsId, "agent-memories"] as const,
  list: (wsId: string, agentId: string) =>
    [...agentMemoryKeys.all(wsId), agentId] as const,
};

export function agentMemoryOptions(wsId: string, agentId: string) {
  return queryOptions({
    queryKey: agentMemoryKeys.list(wsId, agentId),
    queryFn: () => api.listAgentMemories(agentId),
    enabled: !!wsId && !!agentId,
    staleTime: 30 * 1000,
    gcTime: 5 * 60 * 1000,
    refetchOnWindowFocus: true,
  });
}

export const generatedSkillDeliveryKeys = {
  all: (wsId: string) => ["workspaces", wsId, "generated-skill-deliveries"] as const,
  list: (wsId: string, agentId: string) =>
    [...generatedSkillDeliveryKeys.all(wsId), agentId] as const,
};

export function generatedSkillDeliveryOptions(wsId: string, agentId: string) {
  return queryOptions({
    queryKey: generatedSkillDeliveryKeys.list(wsId, agentId),
    queryFn: () => api.listAgentGeneratedSkillDeliveries(agentId),
    enabled: !!wsId && !!agentId,
    staleTime: 15 * 1000,
    gcTime: 5 * 60 * 1000,
    refetchOnWindowFocus: true,
  });
}

export const evolutionMemoryDeliveryKeys = {
  all: (wsId: string) => ["workspaces", wsId, "evolution-memory-deliveries"] as const,
  list: (wsId: string, agentId: string) =>
    [...evolutionMemoryDeliveryKeys.all(wsId), agentId] as const,
};

export function evolutionMemoryDeliveryOptions(wsId: string, agentId: string) {
  return queryOptions({
    queryKey: evolutionMemoryDeliveryKeys.list(wsId, agentId),
    queryFn: () => api.listAgentEvolutionMemoryDeliveries(agentId),
    enabled: !!wsId && !!agentId,
    staleTime: 15 * 1000,
    gcTime: 5 * 60 * 1000,
    refetchOnWindowFocus: true,
  });
}

export const agentTasksKeys = {
  all: (wsId: string) => ["workspaces", wsId, "agent-tasks"] as const,
  detail: (wsId: string, agentId: string) =>
    [...agentTasksKeys.all(wsId), agentId] as const,
};

// All tasks for a single agent (the agent detail page consumer). Powers both
// the inspector's 7-day throughput stats and the Tasks tab list — shared so
// they don't fetch twice. WS task events invalidate this via the existing
// task-prefix invalidation in useRealtimeSync.
export function agentTasksOptions(wsId: string, agentId: string) {
  return queryOptions({
    queryKey: agentTasksKeys.detail(wsId, agentId),
    queryFn: () => api.listAgentTasks(agentId),
    staleTime: 30 * 1000,
    gcTime: 5 * 60 * 1000,
    refetchOnWindowFocus: true,
  });
}

// Agent templates are workspace-independent: a static catalog served from
// the server's embedded JSON. Cache effectively forever — the only way the
// list / detail change is a server deploy, and a hard reload picks that up.
export const agentTemplateKeys = {
  all: () => ["agent-templates"] as const,
  list: () => [...agentTemplateKeys.all(), "list"] as const,
  detail: (slug: string) => [...agentTemplateKeys.all(), "detail", slug] as const,
};

export function agentTemplateListOptions() {
  return queryOptions({
    queryKey: agentTemplateKeys.list(),
    queryFn: () => api.listAgentTemplates(),
    staleTime: Infinity,
    gcTime: 30 * 60 * 1000,
  });
}

export function agentTemplateDetailOptions(slug: string) {
  return queryOptions({
    queryKey: agentTemplateKeys.detail(slug),
    queryFn: () => api.getAgentTemplate(slug),
    staleTime: Infinity,
    gcTime: 30 * 60 * 1000,
  });
}
