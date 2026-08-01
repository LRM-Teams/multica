import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import type { Agent, Workspace } from "../types";

export const workspaceKeys = {
  all: (wsId: string) => ["workspaces", wsId] as const,
  list: () => ["workspaces", "list"] as const,
  members: (wsId: string) => ["workspaces", wsId, "members"] as const,
  memberPresence: (wsId: string) => ["workspaces", wsId, "member-presence"] as const,
  memberProfile: (wsId: string, memberType: "user" | "agent", memberId: string) =>
    ["workspaces", wsId, "member-profiles", memberType, memberId] as const,
  invitations: (wsId: string) => ["workspaces", wsId, "invitations"] as const,
  myInvitations: () => ["invitations", "mine"] as const,
  agents: (wsId: string) => ["workspaces", wsId, "agents"] as const,
  skills: (wsId: string) => ["workspaces", wsId, "skills"] as const,
  assigneeFrequency: (wsId: string) => ["workspaces", wsId, "assignee-frequency"] as const,
};

export function workspaceListOptions() {
  return queryOptions({
    queryKey: workspaceKeys.list(),
    queryFn: () => api.listWorkspaces(),
  });
}

/** Resolves the workspace whose slug matches, from the cached workspace list. */
export function workspaceBySlugOptions(slug: string) {
  return queryOptions({
    ...workspaceListOptions(),
    select: (list: Workspace[]) => list.find((w) => w.slug === slug) ?? null,
  });
}

export function memberListOptions(wsId: string) {
  return queryOptions({
    queryKey: workspaceKeys.members(wsId),
    queryFn: () => api.listMembers(wsId),
  });
}

export function memberPresenceOptions(wsId: string) {
  return queryOptions({
    queryKey: workspaceKeys.memberPresence(wsId),
    queryFn: () => api.listMemberPresence(wsId),
    staleTime: 15_000,
  });
}

export function memberProfileOptions(
  wsId: string,
  memberType: "user" | "agent" | null | undefined,
  memberId: string | null | undefined,
) {
  const type = memberType ?? "user";
  const id = memberId ?? "";
  return queryOptions({
    queryKey: workspaceKeys.memberProfile(wsId, type, id),
    // React Query forbids undefined; coalesce missing profiles to null.
    queryFn: async () => (await api.getMemberProfile(type, id)) ?? null,
    enabled: !!wsId && !!memberType && !!memberId,
    staleTime: 30 * 1000,
  });
}

/**
 * Workspace agent directory. Default excludes archived agents (LRM-409/410) —
 * Members / Graph / invite·@ / hiring lists treat archived as absent.
 *
 * Pass `includeArchived: true` only for explicit archive manage/restore
 * surfaces (Agents page Archived tab). That uses a separate cache key so it
 * cannot leak into the default directory key shared by list pickers.
 */
export function agentListOptions(wsId: string, opts?: { includeArchived?: boolean }) {
  const includeArchived = !!opts?.includeArchived;
  const baseKey = workspaceKeys.agents(wsId);
  const queryKey = includeArchived ? ([...baseKey, "include_archived"] as const) : baseKey;
  return queryOptions({
    queryKey,
    queryFn: () =>
      api.listAgents({
        workspace_id: wsId,
        ...(includeArchived ? { include_archived: true } : {}),
      }),
  });
}

export function skillListOptions(wsId: string) {
  return queryOptions({
    queryKey: workspaceKeys.skills(wsId),
    queryFn: () => api.listSkills(),
  });
}

export function skillDetailOptions(wsId: string, skillId: string) {
  return queryOptions({
    queryKey: [...workspaceKeys.skills(wsId), skillId] as const,
    queryFn: () => api.getSkill(skillId),
    enabled: !!skillId,
  });
}

/** LRM-954 — promotion audit list for a skill detail page. */
export function skillPromotionsOptions(wsId: string, skillId: string) {
  return queryOptions({
    queryKey: [...workspaceKeys.skills(wsId), skillId, "promotions"] as const,
    queryFn: () => api.listSkillPromotions(skillId),
    enabled: !!skillId,
    // BE route may not be on `dev` yet — soft-fail to empty.
    retry: false,
  });
}

/**
 * Builds a `Map<skillId, Agent[]>` from the cached agent list. The server
 * already returns each agent with its full skill list inline, so no extra
 * request is needed — "which agents use skill X" is pure client-side fold.
 *
 * Exposed as a plain helper rather than a `queryOptions` with `select` so
 * the Map's identity is stable across unrelated agent-cache rerenders —
 * callers wrap this in `useMemo(..., [agents])` and only re-fold when the
 * agent array identity actually changes. Previously this was `{ select }`,
 * which returned a new Map every subscription tick and triggered cascading
 * re-renders on every `agent:updated` WS event.
 */
export function selectSkillAssignments(
  agents: Agent[] | undefined,
): Map<string, Agent[]> {
  const map = new Map<string, Agent[]>();
  if (!agents) return map;
  for (const a of agents) {
    if (a.archived_at) continue;
    for (const s of a.skills ?? []) {
      const existing = map.get(s.id);
      if (existing) existing.push(a);
      else map.set(s.id, [a]);
    }
  }
  return map;
}

export function invitationListOptions(wsId: string) {
  return queryOptions({
    queryKey: workspaceKeys.invitations(wsId),
    queryFn: () => api.listWorkspaceInvitations(wsId),
  });
}

export function myInvitationListOptions() {
  return queryOptions({
    queryKey: workspaceKeys.myInvitations(),
    queryFn: () => api.listMyInvitations(),
  });
}

export function assigneeFrequencyOptions(wsId: string) {
  return queryOptions({
    queryKey: workspaceKeys.assigneeFrequency(wsId),
    queryFn: () => api.getAssigneeFrequency(),
  });
}
