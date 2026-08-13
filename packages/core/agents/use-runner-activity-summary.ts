"use client";

import { useQuery } from "@tanstack/react-query";
import { runnerActivitySummaryOptions } from "./queries";

// Compact Activity consumers share one Workspace query. The server owns the
// summary projection; absence from the sparse response means no observation.
export function useRunnerActivitySummary(
  wsId: string | undefined,
  agentId: string | undefined,
) {
  const enabled = !!wsId && !!agentId;
  return useQuery({
    ...runnerActivitySummaryOptions(wsId ?? ""),
    enabled,
    select: (data) =>
      data.items.find((item) => item.agent_id === agentId)?.summary ?? null,
  });
}

/** Same Workspace-batched summary query, without per-agent select. */
export function useRunnerActivitySummaries(wsId: string | undefined) {
  return useQuery({
    ...runnerActivitySummaryOptions(wsId ?? ""),
    enabled: !!wsId,
  });
}
