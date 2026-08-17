"use client";

import { useCallback } from "react";
import { queryOptions, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { AgentRestartMode } from "../types";

export const agentRestartKeys = {
  preflight: (agentId: string) =>
    ["agents", agentId, "reset", "preflight"] as const,
};

/**
 * Server-authoritative per-action executability. Only fetched while the modal is
 * `open` — this is the sole source for enable/disable;
 * the FE never derives active/idle from `agent.status`.
 */
export function agentRestartPreflightOptions(agentId: string, enabled: boolean) {
  return queryOptions({
    queryKey: agentRestartKeys.preflight(agentId),
    queryFn: () => api.getAgentRestartPreflight(agentId),
    enabled: enabled && !!agentId,
    staleTime: 5 * 1000,
    gcTime: 60 * 1000,
    refetchOnWindowFocus: true,
  });
}

/**
 * Drives Raft's three reset modes for one Agent. Each click performs one
 * `resetAgent(mode)` request. Progress belongs to normal Agent status
 * surfaces; the modal closes after acceptance.
 */
export function useAgentRestart(agentId: string, open: boolean) {
  const qc = useQueryClient();
  const preflight = useQuery(agentRestartPreflightOptions(agentId, open));

  const resetAgent = useMutation({
    mutationFn: async (mode: AgentRestartMode) => {
      const operation = await api.resetAgent(agentId, mode);
      if (!operation.id || operation.status === "failed") {
        throw new Error(
          operation.reason_code || "Agent Restart request was not accepted",
        );
      }
      return operation;
    },
  });

  const clear = useCallback(() => resetAgent.reset(), [resetAgent]);

  const refreshAfterRequest = useCallback(() => {
    // Return Agent surfaces to server-owned restart/session/health facts after
    // the request is accepted.
    qc.invalidateQueries({ queryKey: agentRestartKeys.preflight(agentId) });
    qc.invalidateQueries({ queryKey: ["agents", agentId] });
  }, [qc, agentId]);

  return {
    preflight: preflight.data ?? null,
    preflightLoading: preflight.isLoading,
    resetAgent,
    clear,
    refreshAfterRequest,
  };
}
