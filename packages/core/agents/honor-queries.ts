import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import { workspaceKeys } from "../workspace/queries";

export const agentHonorKeys = {
  rules: (workspaceId: string) =>
    [...workspaceKeys.agents(workspaceId), "honor", "rules"] as const,
  dashboard: (workspaceId: string, agentId: string) =>
    [...workspaceKeys.agents(workspaceId), "honor", agentId] as const,
  audit: (workspaceId: string, agentId?: string) =>
    [...workspaceKeys.agents(workspaceId), "honor", "audit", agentId ?? "all"] as const,
};

export function agentHonorRulesOptions(workspaceId: string) {
  return queryOptions({
    queryKey: agentHonorKeys.rules(workspaceId),
    queryFn: () => api.getAgentHonorRules(),
  });
}

export function agentHonorOptions(workspaceId: string, agentId: string) {
  return queryOptions({
    queryKey: agentHonorKeys.dashboard(workspaceId, agentId),
    queryFn: () => api.getAgentHonor(agentId),
  });
}

export function agentHonorAuditOptions(workspaceId: string, agentId?: string) {
  return queryOptions({
    queryKey: agentHonorKeys.audit(workspaceId, agentId),
    queryFn: () => api.getAgentHonorAdminAudit(agentId),
  });
}
