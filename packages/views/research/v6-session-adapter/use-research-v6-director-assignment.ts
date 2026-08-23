"use client";

import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { researchKeys } from "@multica/core/research";
import { agentListOptions } from "@multica/core/workspace/queries";

export interface ResearchV6DirectorAssignmentInput {
  enabled: boolean;
  workspaceId: string;
  runId: string;
  persistedAgentId: string | null;
  expectedStateVersion: number;
}

/** Owns Director roster loading and optimistic assignment reconciliation. */
export function useResearchV6DirectorAssignment({
  enabled,
  workspaceId,
  runId,
  persistedAgentId,
  expectedStateVersion,
}: ResearchV6DirectorAssignmentInput) {
  const queryClient = useQueryClient();
  const { data: agents = [] } = useQuery({
    ...agentListOptions(workspaceId),
    enabled,
  });
  const [assignedAgentId, setAssignedAgentId] = useState<string | null>(
    persistedAgentId,
  );
  useEffect(() => {
    // react-doctor-disable-next-line react-doctor/no-derived-state -- assignment changes optimistically and reconciles from the persisted snapshot.
    setAssignedAgentId(persistedAgentId);
  }, [persistedAgentId]);
  const assignment = useMutation({
    mutationFn: ({ agentId, reason }: { agentId: string; reason: string }) =>
      api.replaceResearchV6Director(workspaceId, runId, {
        directorAgentId: agentId,
        expectedStateVersion,
        reason,
        clientRequestId: crypto.randomUUID(),
      }),
    onSuccess: (updated) => {
      if (!updated) return;
      setAssignedAgentId(updated.directorAgentId);
      void queryClient.invalidateQueries({
        queryKey: researchKeys.snapshot(workspaceId, runId),
      });
    },
  });

  return {
    agents,
    assignedAgentId,
    assignedAgent: agents.find((agent) => agent.id === assignedAgentId),
    assignment,
  };
}
