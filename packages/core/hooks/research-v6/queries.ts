export const researchV6Keys = {
  all: (wsId: string, runId: string) => ["research-v6", wsId, runId] as const,
  snapshot: (wsId: string, runId: string) =>
    ["research-v6", wsId, runId, "snapshot"] as const,
  /** Last confirmed sequence cursor for a run's projection cache. */
  cursor: (wsId: string, runId: string) =>
    ["research-v6", wsId, runId, "cursor"] as const,
};
