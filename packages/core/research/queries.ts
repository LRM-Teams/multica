import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import type { InfiniteData } from "@tanstack/react-query";
import { api } from "../api";
import type { TypedGraphResponse } from "./graph-typed";

export type ResearchPresencePhase =
  | "idle"
  | "queued"
  | "running"
  | "done"
  | "failed"
  | "stale";

export type ResearchPresenceEntry = {
  activity: string;
  updatedAt: number;
  phase: ResearchPresencePhase;
  role: string;
  fleetMemberId: string | null;
  taskId: string | null;
  nodeId: string | null;
  branchId: string | null;
  stage: string | null;
  expiresAt: number | null;
  staleReason: string | null;
};

export type ResearchPresenceMap = Record<string, ResearchPresenceEntry>;

/** Wire shape from GET /presence before normalizeResearchPresenceMap. */
export type ResearchPresenceResponse = {
  session_id: string;
  presence: Record<
    string,
    {
      activity?: string;
      updated_at?: number;
      updatedAt?: number;
      phase?: string;
      role?: string;
      fleet_member_id?: string | null;
      task_id?: string | null;
      node_id?: string | null;
      branch_id?: string | null;
      stage?: string | null;
      expires_at?: number | null;
      stale_reason?: string | null;
    }
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
  graphTyped: (
    wsId: string,
    sessionId: string,
    pagination?: ResearchGraphTypedPagination,
  ) =>
    [
      "research",
      wsId,
      "graph-typed",
      sessionId,
      pagination?.limit ?? null,
      pagination?.offset ?? null,
    ] as const,
  graphTypedInfinite: (wsId: string, sessionId: string) =>
    ["research", wsId, "graph-typed-infinite", sessionId] as const,
};

/** Default first-screen page size for D5 constellation canvas (G8). */
export const RESEARCH_TYPED_GRAPH_PAGE_LIMIT = 500;

export type ResearchGraphTypedPagination = {
  limit?: number;
  offset?: number;
};

/**
 * Resolve the next offset without assuming every compatible server already
 * emits `total_node_count`. A full page means another page may exist; a short
 * page is the terminal signal for older servers.
 */
export function nextTypedGraphPageOffset(
  lastPage: TypedGraphResponse,
  allPages: readonly TypedGraphResponse[],
  pageLimit = RESEARCH_TYPED_GRAPH_PAGE_LIMIT,
): number | undefined {
  const loaded = allPages.reduce(
    (count, page) => count + (page.nodes?.length ?? 0),
    0,
  );
  const latestKnownTotal = [...allPages]
    .reverse()
    .find((page) => page.total_node_count != null)?.total_node_count;
  if (latestKnownTotal != null) {
    return loaded < latestKnownTotal ? loaded : undefined;
  }
  return (lastPage.nodes?.length ?? 0) >= pageLimit ? loaded : undefined;
}

/**
 * Offset pages are one logical snapshot. Mixing graph versions can invent a
 * topology that never existed, so fail the query and let the session retry all
 * pages through its existing projection-error recovery.
 */
export function requireConsistentTypedGraphPages(
  data: InfiniteData<TypedGraphResponse, number>,
): InfiniteData<TypedGraphResponse, number> {
  const versions = new Set(data.pages.map((page) => page.graph_version));
  if (versions.size > 1) {
    throw new Error(
      "GET /api/research/sessions/:id/graph/typed returned mixed graph versions",
    );
  }
  return data;
}

/** Normalize GET /presence wire map (snake updated_at) → ResearchPresenceMap. */
export function normalizeResearchPresenceMap(
  raw: ResearchPresenceResponse["presence"] | null | undefined,
): ResearchPresenceMap {
  const out: ResearchPresenceMap = {};
  if (!raw) return out;
  for (const [agentId, entry] of Object.entries(raw)) {
    const activity = typeof entry?.activity === "string" ? entry.activity.trim() : "";
    const updatedAt =
      typeof entry.updated_at === "number"
        ? entry.updated_at
        : typeof entry.updatedAt === "number"
          ? entry.updatedAt
          : Date.now();
    const phase: ResearchPresencePhase =
      entry.phase === "queued" || entry.phase === "running" ||
      entry.phase === "done" || entry.phase === "failed" || entry.phase === "stale"
        ? entry.phase : "idle";
    out[agentId] = {
      activity, updatedAt, phase,
      role: typeof entry.role === "string" ? entry.role : "",
      fleetMemberId: typeof entry.fleet_member_id === "string" ? entry.fleet_member_id : null,
      taskId: typeof entry.task_id === "string" ? entry.task_id : null,
      nodeId: typeof entry.node_id === "string" ? entry.node_id : null,
      branchId: typeof entry.branch_id === "string" ? entry.branch_id : null,
      stage: typeof entry.stage === "string" ? entry.stage : null,
      expiresAt: typeof entry.expires_at === "number" ? entry.expires_at : null,
      staleReason: typeof entry.stale_reason === "string" ? entry.stale_reason : null,
    };
  }
  return out;
}

export function researchFleetOptions(wsId: string) {
  return queryOptions({
    queryKey: researchKeys.fleet(wsId),
    queryFn: () => api.ensureResearchFleet(wsId),
    enabled: !!wsId,
  });
}

export function researchSessionListOptions(wsId: string) {
  return queryOptions({
    queryKey: researchKeys.sessions(wsId),
    queryFn: () => api.listResearchSessions(wsId),
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

/**
 * LRM-1497 · typed star graph (fed by LRM-1505). Server state stays in React
 * Query; the canvas renders FROM this canonical data — it never fabricates
 * topology. `graph_updated`/`research_session` WS invalidations refresh the
 * same key (see `ws-updaters`).
 */
export function researchGraphTypedOptions(
  wsId: string,
  sessionId: string,
  pagination?: ResearchGraphTypedPagination,
) {
  const page = pagination ?? {
    limit: RESEARCH_TYPED_GRAPH_PAGE_LIMIT,
    offset: 0,
  };
  return queryOptions({
    queryKey: researchKeys.graphTyped(wsId, sessionId, page),
    queryFn: () => api.getResearchGraphTyped(sessionId, page),
    enabled: !!wsId && !!sessionId,
  });
}

/**
 * Infinite typed-graph loader for large sessions (G8/G9). Pages merge via
 * `mergeTypedGraphPages`; the canvas never fabricates nodes beyond what loaded.
 */
export function researchGraphTypedInfiniteOptions(wsId: string, sessionId: string) {
  return infiniteQueryOptions({
    queryKey: researchKeys.graphTypedInfinite(wsId, sessionId),
    queryFn: ({ pageParam }) =>
      api.getResearchGraphTyped(sessionId, {
        limit: RESEARCH_TYPED_GRAPH_PAGE_LIMIT,
        offset: pageParam,
      }),
    initialPageParam: 0,
    getNextPageParam: (lastPage, allPages) =>
      nextTypedGraphPageOffset(lastPage, allPages),
    select: requireConsistentTypedGraphPages,
    enabled: !!wsId && !!sessionId,
  });
}
