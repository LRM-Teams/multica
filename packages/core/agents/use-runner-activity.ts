"use client";

import { useCallback } from "react";
import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useWSEvent, useWSReconnect } from "../realtime";
import type { RunnerActivityRealtimePayload } from "../types";
import { runnerActivityKeys, runnerActivityOptions } from "./queries";

// The sole client integration for server-projected Runner Activity. A realtime
// payload replaces the complete canonical presentation first, then invalidates
// it for a durable REST reconciliation. There is deliberately no reducer of
// task, presence, provider, session, or elapsed-time facts here.
export function useRunnerActivity(wsId: string | undefined, agentId: string | undefined) {
  const queryClient = useQueryClient();
  const enabled = !!wsId && !!agentId;
  const query = useQuery({
    ...runnerActivityOptions(wsId ?? "", agentId ?? ""),
    enabled,
  });
  const reconcile = useCallback(() => {
    if (!wsId || !agentId) return;
    void queryClient.invalidateQueries({ queryKey: runnerActivityKeys.all(wsId, agentId) });
  }, [agentId, queryClient, wsId]);

  useWSEvent("agent:activity", useCallback((payload: unknown) => {
    applyRunnerActivityRealtime(queryClient, wsId, agentId, payload);
  }, [agentId, queryClient, wsId]));
  useWSReconnect(reconcile);

  return {
    ...query,
    isLoading: enabled && query.isPending && !query.isError,
  };
}

export function applyRunnerActivityRealtime(
  queryClient: QueryClient,
  wsId: string | undefined,
  agentId: string | undefined,
  payload: unknown,
): void {
  if (!isRunnerActivityRealtimePayload(payload) || !wsId || !agentId || payload.agent_id !== agentId) return;
  const key = runnerActivityKeys.all(wsId, agentId);
  queryClient.setQueryData(key, payload.activity);
  void queryClient.invalidateQueries({ queryKey: key });
}

function isRunnerActivityRealtimePayload(value: unknown): value is RunnerActivityRealtimePayload {
  if (!value || typeof value !== "object") return false;
  const payload = value as Partial<RunnerActivityRealtimePayload>;
  return typeof payload.agent_id === "string"
    && !!payload.activity
    && typeof payload.activity === "object"
    && Array.isArray(payload.activity.timeline);
}
