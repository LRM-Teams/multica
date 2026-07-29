import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export type ResearchPresenceMap = Record<
  string,
  { activity: string; updatedAt: number }
>;

export const researchKeys = {
  all: (wsId: string) => ["research", wsId] as const,
  fleet: (wsId: string) => ["research", wsId, "fleet"] as const,
  sessions: (wsId: string) => ["research", wsId, "sessions"] as const,
  snapshot: (wsId: string, sessionId: string) =>
    ["research", wsId, "snapshot", sessionId] as const,
  presence: (wsId: string, sessionId: string) =>
    ["research", wsId, "presence", sessionId] as const,
};

export function researchFleetOptions(wsId: string) {
  return queryOptions({
    queryKey: researchKeys.fleet(wsId),
    queryFn: () => api.ensureResearchFleet(),
    enabled: !!wsId,
  });
}

export function researchSessionListOptions(wsId: string) {
  return queryOptions({
    queryKey: researchKeys.sessions(wsId),
    queryFn: () => api.listResearchSessions(),
    enabled: !!wsId,
  });
}

export function researchSessionSnapshotOptions(wsId: string, sessionId: string) {
  return queryOptions({
    queryKey: researchKeys.snapshot(wsId, sessionId),
    queryFn: () => api.getResearchSessionSnapshot(sessionId),
    enabled: !!wsId && !!sessionId,
    refetchInterval: (query) => {
      const status = query.state.data?.session?.status;
      return status === "running" ? 4000 : false;
    },
  });
}
