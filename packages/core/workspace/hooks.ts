"use client";

import { useCallback, useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { agentFleetRankingsOptions } from "../agents/fleet-queries";
import { useWorkspaceId } from "../hooks";
import { memberListOptions, agentListOptions } from "./queries";
import { resolvePublicFileUrl } from "./avatar-url";
import { resolveActorDisplayName, resolveActorHandle } from "../identity";
import type { MemberRole } from "../types/workspace";
import type { HonorSnapshot } from "../types/honor";
import type { AgentFleetRank } from "../types/agent-fleet";

export function useActorName() {
  const wsId = useWorkspaceId();
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: fleetRankings = [] } = useQuery(agentFleetRankingsOptions(wsId));

  // Each resolver takes an optional fallback used when the id isn't in the
  // workspace cache — e.g. a mention to someone who left the workspace, or an
  // agent-authored mention whose link label is the only name we have. Falling
  // back to that label beats rendering a bare "Unknown".
  const getMemberName = useCallback((userId: string, fallback?: string) => {
    const m = members.find((m) => m.user_id === userId);
    return resolveActorDisplayName(m, fallback ?? "Unknown");
  }, [members]);

  const getMemberHandle = useCallback((userId: string, fallback?: string) => {
    const m = members.find((m) => m.user_id === userId);
    return resolveActorHandle(m, fallback);
  }, [members]);

  // LRM-232: bubble chrome looks up workspace role by user id (owner/admin
  // muted label). Missing members return null so ordinary/unknown stay quiet.
  const getMemberRole = useCallback((userId: string): MemberRole | null => {
    return members.find((m) => m.user_id === userId)?.role ?? null;
  }, [members]);

  const getMemberHonor = useCallback((userId: string): HonorSnapshot | undefined => {
    return members.find((m) => m.user_id === userId)?.honor;
  }, [members]);

  const fleetByAgentId = useMemo(() => {
    const m = new Map<string, AgentFleetRank>();
    for (const row of fleetRankings) m.set(row.agent_id, row);
    return m;
  }, [fleetRankings]);

  const getAgentFleetRank = useCallback(
    (agentId: string): AgentFleetRank | undefined => fleetByAgentId.get(agentId),
    [fleetByAgentId],
  );

  const getAgentName = useCallback((agentId: string, fallback?: string) => {
    const a = agents.find((a) => a.id === agentId);
    return resolveActorDisplayName(a, fallback ?? "Unknown Agent");
  }, [agents]);

  const getAgentHandle = useCallback((agentId: string, fallback?: string) => {
    const a = agents.find((a) => a.id === agentId);
    return resolveActorHandle(a, fallback);
  }, [agents]);

  const getActorName = useCallback((type: string, id: string, fallback?: string) => {
    if (type === "member") return getMemberName(id, fallback);
    if (type === "agent") return getAgentName(id, fallback);
    // Squads are retired (Frank 2026-07-28). Historical assignee=squad rows
    // still render — degraded to a read-only "former" label, never re-selectable
    // (the composer @ picker already excludes squad, #600/#446). English-only
    // to match this core hook's hardcoded sentinels; a bilingual surface would
    // i18n it at the component layer.
    if (type === "squad") return fallback ?? "Former squad assignment";
    if (type === "system") return "Multica";
    return fallback ?? "System";
  }, [getAgentName, getMemberName]);

  const getActorHandle = useCallback((type: string, id: string, fallback?: string) => {
    if (type === "member") return getMemberHandle(id, fallback);
    if (type === "agent") return getAgentHandle(id, fallback);
    return fallback ?? "";
  }, [getAgentHandle, getMemberHandle]);

  const getActorInitials = useCallback((type: string, id: string) => {
    // LRM-201: avatar discs use a single glyph (not gray two-letter initials).
    const name = getActorName(type, id).trim();
    const c = name.charAt(0);
    if (!c) return "?";
    return /[a-z]/i.test(c) ? c.toUpperCase() : c;
  }, [getActorName]);

  const getActorAvatarUrl = useCallback((type: string, id: string): string | null => {
    if (type === "member") return resolvePublicFileUrl(members.find((m) => m.user_id === id)?.avatar_url);
    // Missing values fall back to the actor's initials in the avatar
    // component; never derive a second display truth from the mutable pool.
    if (type === "agent") return resolvePublicFileUrl(agents.find((a) => a.id === id)?.avatar_url);
    return null;
  }, [agents, members]);

  return useMemo(
    () => ({
      getMemberName,
      getMemberHandle,
      getMemberRole,
      getMemberHonor,
      getAgentFleetRank,
      getAgentName,
      getAgentHandle,
      getActorName,
      getActorHandle,
      getActorInitials,
      getActorAvatarUrl,
    }),
    [
      getActorAvatarUrl,
      getActorHandle,
      getActorInitials,
      getActorName,
      getAgentHandle,
      getAgentName,
      getMemberHandle,
      getMemberName,
      getMemberRole,
      getMemberHonor,
      getAgentFleetRank,
    ],
  );
}
