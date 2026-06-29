"use client";

import { useCallback, useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "../hooks";
import { memberListOptions, agentListOptions, squadListOptions } from "./queries";
import { resolvePublicFileUrl } from "./avatar-url";

export function useActorName() {
  const wsId = useWorkspaceId();
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: squads = [] } = useQuery(squadListOptions(wsId));

  // Each resolver takes an optional fallback used when the id isn't in the
  // workspace cache — e.g. a mention to someone who left the workspace, or an
  // agent-authored mention whose link label is the only name we have. Falling
  // back to that label beats rendering a bare "Unknown".
  const getMemberName = useCallback((userId: string, fallback?: string) => {
    const m = members.find((m) => m.user_id === userId);
    return m ? (m.display_name || m.name || fallback || "Unknown") : fallback ?? "Unknown";
  }, [members]);

  const getMemberHandle = useCallback((userId: string, fallback?: string) => {
    const m = members.find((m) => m.user_id === userId);
    return m ? (m.name || fallback || "") : fallback ?? "";
  }, [members]);

  const getAgentName = useCallback((agentId: string, fallback?: string) => {
    const a = agents.find((a) => a.id === agentId);
    return a ? (a.display_name || a.name || fallback || "Unknown Agent") : fallback ?? "Unknown Agent";
  }, [agents]);

  const getAgentHandle = useCallback((agentId: string, fallback?: string) => {
    const a = agents.find((a) => a.id === agentId);
    return a ? (a.name || fallback || "") : fallback ?? "";
  }, [agents]);

  const getSquadName = useCallback((squadId: string, fallback?: string) => {
    const s = squads.find((s) => s.id === squadId);
    return s?.name ?? fallback ?? "Unknown Squad";
  }, [squads]);

  const getActorName = useCallback((type: string, id: string, fallback?: string) => {
    if (type === "member") return getMemberName(id, fallback);
    if (type === "agent") return getAgentName(id, fallback);
    if (type === "squad") return getSquadName(id, fallback);
    if (type === "system") return "Multica";
    return fallback ?? "System";
  }, [getAgentName, getMemberName, getSquadName]);

  const getActorHandle = useCallback((type: string, id: string, fallback?: string) => {
    if (type === "member") return getMemberHandle(id, fallback);
    if (type === "agent") return getAgentHandle(id, fallback);
    return fallback ?? "";
  }, [getAgentHandle, getMemberHandle]);

  const getActorInitials = useCallback((type: string, id: string) => {
    const name = getActorName(type, id);
    return name
      .split(" ")
      .map((w) => w[0])
      .join("")
      .toUpperCase()
      .slice(0, 2);
  }, [getActorName]);

  const getActorAvatarUrl = useCallback((type: string, id: string): string | null => {
    if (type === "member") return resolvePublicFileUrl(members.find((m) => m.user_id === id)?.avatar_url);
    if (type === "agent") return resolvePublicFileUrl(agents.find((a) => a.id === id)?.avatar_url);
    if (type === "squad") return resolvePublicFileUrl(squads.find((s) => s.id === id)?.avatar_url);
    return null;
  }, [agents, members, squads]);

  return useMemo(
    () => ({
      getMemberName,
      getMemberHandle,
      getAgentName,
      getAgentHandle,
      getSquadName,
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
      getSquadName,
    ],
  );
}
