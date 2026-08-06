"use client";

import { useCallback, useState } from "react";
import {
  queryOptions,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { api } from "../api";
import type {
  AgentLifecycleActionKind,
  AgentLifecycleOperation,
} from "../types";
import { isTerminalAgentLifecycleStatus } from "./agent-lifecycle";

export const agentLifecycleKeys = {
  preflight: (agentId: string) =>
    ["agents", agentId, "lifecycle", "preflight"] as const,
  operation: (agentId: string, operationId: string) =>
    ["agents", agentId, "lifecycle", "operation", operationId] as const,
};

/**
 * Server-authoritative per-action executability. Only fetched while the modal is
 * `open` — this is the sole source for enable/disable + immediate-vs-scheduled;
 * the FE never derives active/idle from `agent.status`.
 */
export function agentLifecyclePreflightOptions(agentId: string, enabled: boolean) {
  return queryOptions({
    queryKey: agentLifecycleKeys.preflight(agentId),
    queryFn: () => api.getAgentLifecyclePreflight(agentId),
    enabled: enabled && !!agentId,
    staleTime: 5 * 1000,
    gcTime: 60 * 1000,
    refetchOnWindowFocus: true,
  });
}

const LIFECYCLE_POLL_INTERVAL_MS = 2000;

/**
 * Drives the three-tier restart modal for one agent (#633). Starting an action
 * mints a fresh UUID `Idempotency-Key`, seeds the returned operation into cache,
 * and polls it until terminal (succeeded/failed) — #687 terminal-set discipline,
 * no forever-poll. The hook stays effect-free: the poll self-stops via
 * `refetchInterval`, and the caller invalidates surfaces on acknowledge via
 * `refreshAfterTerminal()` (keeps side effects out of an effect for react:doctor).
 */
export function useAgentLifecycle(agentId: string, open: boolean) {
  const qc = useQueryClient();
  const [operationId, setOperationId] = useState<string | null>(null);

  const preflight = useQuery(agentLifecyclePreflightOptions(agentId, open));

  const operation = useQuery({
    queryKey: operationId
      ? agentLifecycleKeys.operation(agentId, operationId)
      : ["agents", agentId, "lifecycle", "operation", "idle"],
    queryFn: () =>
      api.getAgentLifecycleOperation(agentId, operationId as string),
    enabled: !!operationId,
    refetchInterval: (query) =>
      isTerminalAgentLifecycleStatus(
        (query.state.data as AgentLifecycleOperation | undefined)?.status,
      )
        ? false
        : LIFECYCLE_POLL_INTERVAL_MS,
  });

  const start = useMutation({
    mutationFn: (actionKind: AgentLifecycleActionKind) =>
      api.startAgentLifecycleAction(agentId, actionKind, crypto.randomUUID()),
    onSuccess: (op) => {
      qc.setQueryData(agentLifecycleKeys.operation(agentId, op.id), op);
      setOperationId(op.id);
    },
  });

  const reset = useCallback(() => {
    setOperationId(null);
    start.reset();
  }, [start]);

  const refreshAfterTerminal = useCallback(() => {
    // Return the agent's surfaces to their new, real state (session/health facts
    // and per-action executability) after the op resolves.
    qc.invalidateQueries({ queryKey: agentLifecycleKeys.preflight(agentId) });
    qc.invalidateQueries({ queryKey: ["agents", agentId] });
  }, [qc, agentId]);

  const op = operation.data ?? null;
  return {
    preflight: preflight.data ?? null,
    preflightLoading: preflight.isLoading,
    start,
    operation: op,
    isTerminal: isTerminalAgentLifecycleStatus(op?.status),
    reset,
    refreshAfterTerminal,
  };
}
