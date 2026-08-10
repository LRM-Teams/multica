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

export const agentHealthKeys = {
  all: (wsId: string) => ["workspaces", wsId, "agent-health"] as const,
  detail: (wsId: string, agentId: string) =>
    [...agentHealthKeys.all(wsId), agentId] as const,
};

// Workspace-scoped agent task snapshot — every active task plus each agent's
// most recent terminal task. This is Task workload/capacity data only; Agent
// Presence has its own server-owned Workspace query and never reads this cache.
//
// Freshness (post-step②): WS task events PATCH this cache in place
// (`patchAgentTaskSnapshotStatus`) rather than invalidating it; the 30s
// staleTime + refetchOnWindowFocus is the bounded resync that heals any missed
// event. A whole-workspace refetch now happens only for a brand-new task
// (coalesced) or on the staleTime/focus path — no longer once per task event.

/**
 * step② measurement hook: cumulative count of agent-task-snapshot network
 * fetches, exposed read-only on `globalThis` so a scripted task burst can be
 * measured before/after on a real deploy (the whole point of #1 is driving this
 * toward ~0 for transition bursts). Cheap and side-effect-free in production.
 */
function countSnapshotFetch(): void {
  const g = globalThis as { __multicaSnapshotFetches?: number };
  g.__multicaSnapshotFetches = (g.__multicaSnapshotFetches ?? 0) + 1;
}

export function agentTaskSnapshotOptions(wsId: string) {
  return queryOptions({
    queryKey: agentTaskSnapshotKeys.list(wsId),
    queryFn: () => {
      countSnapshotFetch();
      return api.getAgentTaskSnapshot();
    },
    staleTime: 30 * 1000,
    gcTime: 5 * 60 * 1000,
    refetchOnWindowFocus: true,
  });
}

// Runner Activity is server-projected presentation keyed by both Workspace and
// Agent. It intentionally does not share a cache with the historical raw fact
// timeline, so the hard cut can delete the latter without a compatibility path.
export const runnerActivityKeys = {
  root: (wsId: string) => ["workspaces", wsId, "runner-activity"] as const,
  all: (wsId: string, agentId: string) =>
    [...runnerActivityKeys.root(wsId), agentId] as const,
};

export const runnerActivitySummaryKeys = {
  all: (wsId: string) => ["workspaces", wsId, "runner-activity-summaries"] as const,
};

export function runnerActivityOptions(wsId: string, agentId: string) {
  return queryOptions({
    queryKey: runnerActivityKeys.all(wsId, agentId),
    queryFn: () => api.getRunnerActivity(agentId),
    staleTime: 30 * 1000,
    gcTime: 5 * 60 * 1000,
  });
}

export function runnerActivitySummaryOptions(wsId: string) {
  return queryOptions({
    queryKey: runnerActivitySummaryKeys.all(wsId),
    queryFn: () => api.getRunnerActivitySummaries(),
    staleTime: 30 * 1000,
    gcTime: 5 * 60 * 1000,
  });
}

// #656 Agent Card Reminders tab (V2 spec:
// docs/superpowers/specs/2026-07-22-raft-reminder-parity.md). The visible
// definition list is a small, non-paginated "scheduled" query: active rows are
// ordered by next_fire_at and the same response may include one dormant
// managed patrol without a next fire. Fired human history remains
// cursor-paginated occurrences,
// newest-first. Both invalidate on the `agent_reminder:changed` WS event (see
// `use-agent-reminders-realtime.ts`) — the 30s staleTime below is just a
// safety net, not the live-refresh mechanism.
export const agentRemindersKeys = {
  all: (agentId: string) => ["agent-reminders", agentId] as const,
  upcoming: (agentId: string) => [...agentRemindersKeys.all(agentId), "scheduled"] as const,
  history: (agentId: string) => [...agentRemindersKeys.all(agentId), "fired"] as const,
};

export function agentRemindersUpcomingOptions(agentId: string) {
  return queryOptions({
    queryKey: agentRemindersKeys.upcoming(agentId),
    queryFn: () => api.getAgentReminders(agentId, { status: "scheduled" }),
    enabled: !!agentId,
    staleTime: 30 * 1000,
  });
}

export function agentRemindersHistoryOptions(agentId: string) {
  return infiniteQueryOptions({
    queryKey: agentRemindersKeys.history(agentId),
    queryFn: ({ pageParam }) =>
      api.getAgentReminders(agentId, { status: "fired", cursor: pageParam ?? undefined }),
    initialPageParam: null as string | null,
    // `has_more` is the locked authority, not just "did a cursor come back" —
    // a stale/residual `next_cursor` alongside `has_more: false` must not
    // surface a Load more affordance for a page that has nothing after it.
    getNextPageParam: (lastPage) => (lastPage.has_more ? (lastPage.next_cursor ?? undefined) : undefined),
    enabled: !!agentId,
    staleTime: 30 * 1000,
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

export function agentHealthOptions(wsId: string, agentId: string) {
  return queryOptions({
    queryKey: agentHealthKeys.detail(wsId, agentId),
    queryFn: () => api.getAgentHealth(agentId),
    enabled: !!wsId && !!agentId,
    staleTime: 15 * 1000,
    gcTime: 5 * 60 * 1000,
    refetchOnWindowFocus: true,
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

export const agentSkillSuggestionKeys = {
  all: (wsId: string) => ["workspaces", wsId, "agent-skill-suggestions"] as const,
  list: (wsId: string, agentId: string) =>
    [...agentSkillSuggestionKeys.all(wsId), agentId] as const,
};

export function agentSkillSuggestionOptions(wsId: string, agentId: string) {
  return queryOptions({
    queryKey: agentSkillSuggestionKeys.list(wsId, agentId),
    queryFn: () => api.listAgentSkillSuggestions(agentId),
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

export const agentDetailKeys = {
  all: (wsId: string) => ["workspaces", wsId, "agent"] as const,
  detail: (wsId: string, agentId: string) =>
    [...agentDetailKeys.all(wsId), agentId] as const,
};

/**
 * Authoritative single-agent fetch by id (LRM-292).
 *
 * Panel hosts always use this for body data. ListAgents remains the
 * workspace directory / invite discovery surface only (LRM-233 still hides
 * group managers / channel-only agents from that list) — never the open gate.
 */
export function agentDetailOptions(wsId: string, agentId: string) {
  return queryOptions({
    queryKey: agentDetailKeys.detail(wsId, agentId),
    queryFn: () => api.getAgent(agentId),
    enabled: !!wsId && !!agentId,
    staleTime: 30 * 1000,
    gcTime: 5 * 60 * 1000,
    refetchOnWindowFocus: true,
    retry: false,
  });
}

export const memberProfileKeys = {
  all: (wsId: string) => ["workspaces", wsId, "member-profile"] as const,
  detail: (wsId: string, memberType: "user" | "agent", memberId: string) =>
    [...memberProfileKeys.all(wsId), memberType, memberId] as const,
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

export function memberProfileOptions(
  wsId: string,
  memberType: "user" | "agent",
  memberId: string,
) {
  return queryOptions({
    queryKey: memberProfileKeys.detail(wsId, memberType, memberId),
    queryFn: () => api.getMemberProfile(memberType, memberId),
    enabled: !!wsId && !!memberId,
    staleTime: 30 * 1000,
    gcTime: 5 * 60 * 1000,
    refetchOnWindowFocus: true,
    retry: false,
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
