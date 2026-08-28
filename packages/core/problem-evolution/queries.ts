import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import type { ProblemEvolutionRun, ProblemEvolutionSnapshot } from "./schemas";

export const problemEvolutionKeys = {
  all: (wsId: string) => ["problem-evolution", wsId] as const,
  runs: (wsId: string) => ["problem-evolution", wsId, "runs"] as const,
  snapshot: (wsId: string, runId: string) =>
    ["problem-evolution", wsId, "runs", runId, "snapshot"] as const,
  events: (wsId: string, runId: string) =>
    ["problem-evolution", wsId, "runs", runId, "events"] as const,
};

export function problemEvolutionRunListOptions(wsId: string) {
  return queryOptions<ProblemEvolutionRun[]>({
    queryKey: problemEvolutionKeys.runs(wsId),
    queryFn: () => api.listProblemEvolutionRuns(),
    enabled: !!wsId,
  });
}

export function problemEvolutionSnapshotOptions(wsId: string, runId: string) {
  return queryOptions<ProblemEvolutionSnapshot>({
    queryKey: problemEvolutionKeys.snapshot(wsId, runId),
    queryFn: () => api.getProblemEvolutionSnapshot(runId),
    enabled: !!wsId && !!runId,
    // WS `problem_evolution_run:*` events invalidate this key; polling is only
    // a backstop for a quiet socket while a run is executing.
    refetchInterval: (query) => {
      const status = query.state.data?.run.status;
      return status === "running" || status === "queued" || status === "stopping"
        ? 5000
        : false;
    },
  });
}
