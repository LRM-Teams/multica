"use client";

import { useQuery } from "@tanstack/react-query";
import { agentListOptions } from "@multica/core/workspace/queries";

export interface ResearchV6DirectorAssignmentInput {
  enabled: boolean;
  workspaceId: string;
  persistedAgentId: string | null;
}

/** Resolves the persisted Director identity for header and presence. */
export function useResearchV6DirectorAssignment({
  enabled,
  workspaceId,
  persistedAgentId,
}: ResearchV6DirectorAssignmentInput) {
  const { data: agents = [] } = useQuery({
    ...agentListOptions(workspaceId),
    enabled,
  });

  return {
    assignedAgentId: persistedAgentId,
    assignedAgent: agents.find((agent) => agent.id === persistedAgentId),
  };
}
