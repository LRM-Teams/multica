"use client";

import { useQuery } from "@tanstack/react-query";
import { runnerActivityOptions } from "./queries";

// Runner Activity is read from the shared Query cache. The centralized
// realtime sync owns WS updates and reconnect reconciliation so every consumer
// observes one subscription and one canonical cache entry.
export function useRunnerActivity(wsId: string | undefined, agentId: string | undefined) {
  const enabled = !!wsId && !!agentId;
  const query = useQuery({
    ...runnerActivityOptions(wsId ?? "", agentId ?? ""),
    enabled,
  });

  return {
    ...query,
    isLoading: enabled && query.isPending && !query.isError,
  };
}
