import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export type ResearchPresenceMap = Record<
  string,
  { activity: string; updatedAt: number }
>;

/** Wire shape from GET /presence before normalizeResearchPresenceMap. */
export type ResearchPresenceResponse = {
  session_id: string;
  presence: Record<
    string,
    { activity?: string; updated_at?: number; updatedAt?: number }
  >;
};

export const researchKeys = {
  all: (wsId: string) => ["research", wsId] as const,
  fleet: (wsId: string) => ["research", wsId, "fleet"] as const,
  sessions: (wsId: string) => ["research", wsId, "sessions"] as const,
  snapshot: (wsId: string, sessionId: string) =>
    ["research", wsId, "snapshot", sessionId] as const,
  presence: (wsId: string, sessionId: string) =>
    ["research", wsId, "presence", sessionId] as const,
  productRounds: (wsId: string, sessionId: string) =>
    ["research", wsId, "product-rounds", sessionId] as const,
};

/** Normalize GET /presence wire map (snake updated_at) → ResearchPresenceMap. */
export function normalizeResearchPresenceMap(
  raw: Record<string, { activity?: string; updated_at?: number; updatedAt?: number }> | null | undefined,
): ResearchPresenceMap {
  const out: ResearchPresenceMap = {};
  if (!raw) return out;
  for (const [agentId, entry] of Object.entries(raw)) {
    const activity = typeof entry?.activity === "string" ? entry.activity.trim() : "";
    if (!activity) continue;
    const updatedAt =
      typeof entry.updated_at === "number"
        ? entry.updated_at
        : typeof entry.updatedAt === "number"
          ? entry.updatedAt
          : Date.now();
    out[agentId] = { activity, updatedAt };
  }
  return out;
}

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

export function researchPresenceOptions(wsId: string, sessionId: string) {
  return queryOptions({
    queryKey: researchKeys.presence(wsId, sessionId),
    queryFn: async (): Promise<ResearchPresenceMap> => {
      const res = await api.getResearchPresence(sessionId);
      return normalizeResearchPresenceMap(res.presence);
    },
    enabled: !!wsId && !!sessionId,
    // WS `research_session:presence` patches the cache; poll as a backstop when
    // the socket is quiet (LRM-775 — never Infinity stub).
    staleTime: 8_000,
    refetchInterval: 8_000,
  });
}

export function researchProductRoundsOptions(wsId: string, sessionId: string) {
  return queryOptions({
    queryKey: researchKeys.productRounds(wsId, sessionId),
    queryFn: () => api.listResearchProductRoundCards(sessionId),
    enabled: !!wsId && !!sessionId,
    // Endpoint lands with LRM-911 migration 255; empty is fine until then.
    retry: false,
  });
}