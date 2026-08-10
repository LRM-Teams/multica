"use client";

import { useQuery } from "@tanstack/react-query";
import { api } from "../api";
import type { AgentPresence, AgentPresenceItem } from "../types";

export const agentPresenceKeys = {
  workspace: (wsId: string) => ["workspaces", wsId, "agent-presence"] as const,
};

const EMPTY_AGENT_PRESENCE_MAP: ReadonlyMap<string, AgentPresence> = new Map();

export function buildAgentPresenceMap(
  items: readonly AgentPresenceItem[],
): ReadonlyMap<string, AgentPresence> {
  return new Map(items.map((item) => [item.agent_id, item.presence]));
}

export function agentPresenceOptions(wsId: string) {
  return {
    queryKey: agentPresenceKeys.workspace(wsId),
    queryFn: async () => buildAgentPresenceMap((await api.getAgentPresence()).items),
    staleTime: 30 * 1000,
    gcTime: 5 * 60 * 1000,
  } as const;
}

export function useWorkspaceAgentPresence(wsId: string | undefined): {
  byAgent: ReadonlyMap<string, AgentPresence>;
  loading: boolean;
} {
  const query = useQuery({
    ...agentPresenceOptions(wsId ?? ""),
    enabled: !!wsId,
  });
  return {
    byAgent: query.data ?? EMPTY_AGENT_PRESENCE_MAP,
    loading: !wsId || query.isPending || query.isError,
  };
}

export function useAgentPresence(
  wsId: string | undefined,
  agentId: string | undefined,
): AgentPresence | "loading" {
  const query = useQuery({
    ...agentPresenceOptions(wsId ?? ""),
    enabled: !!wsId && !!agentId,
    select: (presence) => presence.get(agentId ?? ""),
  });
  if (!wsId || !agentId || query.isPending || query.isError || !query.data) {
    return "loading";
  }
  return query.data;
}
