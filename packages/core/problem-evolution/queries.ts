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
  });
}
