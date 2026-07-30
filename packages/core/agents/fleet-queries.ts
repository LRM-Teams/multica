import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import { workspaceKeys } from "../workspace/queries";

export function agentFleetRankingsOptions(wsId: string) {
  return queryOptions({
    queryKey: [...workspaceKeys.agents(wsId), "fleet-rankings"] as const,
    queryFn: () => api.getAgentFleetRankings(),
  });
}

export function agentFleetRankOptions(wsId: string, agentId: string) {
  return queryOptions({
    queryKey: [...workspaceKeys.agents(wsId), "fleet-rank", agentId] as const,
    queryFn: () => api.getAgentFleetRank(agentId),
  });
}
