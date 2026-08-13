"use client";

/**
 * useResearchSessionCanvas (LRM-1484 / FE-08).
 *
 * React Query server-state hook that:
 *   1. probes V6 capability for the current run (404/501 → V5; 200-schema-error
 *      → interface error; unknown version → diagnostic);
 *   2. builds a unified `ResearchSessionCanvas` from the selected source through
 *      the pure adapter above (never reads V5/V6 wire shapes in renderers);
 *   3. drives V6 on-demand slice loading through the committed slice gateway so
 *      the browser never downloads the full graph (data-contract §2 / §5.1);
 *   4. scopes every query to `(sessionId, source)` so switching session or
 *      version invalidates old keys — in-flight requests are aborted and a
 *      stale response can never overwrite the new session.
 *
 * All transports are injected for determinism in tests; the production wiring
 * provides real API-backed loaders (one per source).
 */
import { useCallback, useMemo } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import type {
  ProjectionSliceGateway,
  UseResearchSliceReturn,
} from "@multica/core/research-v6-slice";
import { useResearchSlice } from "@multica/core/research-v6-slice";
import type { CanvasSnapshot } from "@multica/core/adapters";
import type { ResearchGraphEdge, ResearchGraphNode } from "@multica/core/types";
import type { ResearchV6Snapshot } from "@multica/core/types/research-v6";
import {
  emptyCanvasSnapshot,
  capabilityFromThrownError,
  sourceOfVerdict,
  type CapabilityVerdict,
  type ResearchSource,
} from "./capability";
import {
  adaptV5Session,
  adaptV6Session,
  type ResearchSessionCanvas,
  type V5SessionGraphInput,
} from "./session-adapter";

export interface SessionCanvasTransports {
  /** V6 projection snapshot loader; absent → capability probe is skipped and V5 is used. */
  loadV6Snapshot?: (
    runId: string,
    signal?: AbortSignal,
  ) => Promise<ResearchV6Snapshot>;
  /** V5 session graph loader. */
  loadV5Session?: (
    sessionId: string,
    signal?: AbortSignal,
  ) => Promise<V5SessionGraphInput>;
  /** V6 slice gateway for on-demand expansion (never full-graph). */
  v6SliceGateway?: ProjectionSliceGateway;
}

/** Capability probe outcome + the canonical V6 snapshot when the probe succeeds. */
export interface ProbeResult {
  verdict: CapabilityVerdict;
  /** Present only when the V6 snapshot route answered with valid data. */
  snapshot?: ResearchV6Snapshot;
}

export interface UseResearchSessionCanvasArgs {
  wsId: string;
  /** V5 session id. */
  sessionId: string;
  /** V6 run id; when absent the V6 probe is skipped and V5 is used. */
  runId?: string;
  transports: SessionCanvasTransports;
  /** Slice default depth (viewport spec default 2; drill-up allowed to 3). */
  sliceMaxDepth?: number;
}

export type SessionCanvasStatus =
  | "probing" // V6 probe in flight
  | "v5" // using V5 adapter
  | "v6" // using V6 adapter (slice-driven)
  | "error"; // interface error / unknown version

export interface UseResearchSessionCanvasResult {
  /** Canonical unified snapshot (V5 or V6). */
  snapshot: CanvasSnapshot;
  /** Which source produced the snapshot. */
  source: ResearchSource | null;
  status: SessionCanvasStatus;
  verdict: CapabilityVerdict | null;
  /** Adapter sanitised view (source, diagnostics). */
  canvas: ResearchSessionCanvas | null;
  /** Non-null only on interface/unknown-version errors. */
  error: { kind: "interface-error" | "unknown-version"; reason: string } | null;
  /** True while the snapshot query is loading. */
  isLoading: boolean;
  /** True for initial loads and background capability/V5 retries. */
  isFetching: boolean;
  /** V6 slice engine result (v6 mode only). */
  slice: UseResearchSliceReturn | null;
  refetch: () => void;
}

export const researchSessionCanvasKeys = {
  all: (wsId: string, sessionId: string) =>
    ["research-canvas", wsId, sessionId] as const,
  capability: (wsId: string, sessionId: string, runId: string) =>
    ["research-canvas", wsId, sessionId, "capability", runId] as const,
  snapshot: (wsId: string, sessionId: string, source: ResearchSource) =>
    ["research-canvas", wsId, sessionId, "snapshot", source] as const,
};

export function useResearchSessionCanvas({
  wsId,
  sessionId,
  runId,
  transports,
  sliceMaxDepth = 2,
}: UseResearchSessionCanvasArgs): UseResearchSessionCanvasResult {
  const qc = useQueryClient();
  const canProbeV6 = !!runId && !!transports.loadV6Snapshot;

  // 1) Capability probe — React Query server state, scoped to (sessionId, runId).
  // The probe IS the V6 snapshot-route attempt (data-contract §2). A successful
  // V6 load seeds the canonical snapshot directly — we never issue a second,
  // parallel full-snapshot fetch for the render seed.
  const probe = useQuery<ProbeResult>({
    queryKey: researchSessionCanvasKeys.capability(wsId, sessionId, runId ?? ""),
    queryFn: async ({ signal }): Promise<ProbeResult> => {
      const loader = transports.loadV6Snapshot;
      if (!runId || !loader) {
        return { verdict: { kind: "fallback-v5", source: "v5" } };
      }
      try {
        const snapshot = await loader(runId, signal);
        return { verdict: { kind: "v6", source: "v6" }, snapshot };
      } catch (error) {
        // AbortError (session/version switched): rethrow so React Query marks
        // the query cancelled and discards it — a stale probe must never write
        // a synthetic fallback into the cache for a newer session.
        if (error instanceof Error && error.name === "AbortError") {
          throw error;
        }
        const verdict = capabilityFromThrownError(error);
        return {
          verdict: verdict ?? {
            kind: "interface-error",
            reason: "V6 probe was cancelled without a capability verdict",
          },
        };
      }
    },
    enabled: canProbeV6,
  });

  // 2) Resolution: build the unified canvas from the selected source.
  // When V6 is not probeable (no runId / no V6 loader) the session is served by
  // the verified V5 adapter deterministically — a synthetic fallback verdict.
  const resolvedVerdict: CapabilityVerdict | null =
    probe.data?.verdict ??
    (canProbeV6 ? null : { kind: "fallback-v5", source: "v5" as const });
  const source: ResearchSource | null = sourceOfVerdict(resolvedVerdict);

  const v5Query = useQuery({
    queryKey: researchSessionCanvasKeys.snapshot(wsId, sessionId, "v5"),
    queryFn: ({ signal }) =>
      transports.loadV5Session ? transports.loadV5Session(sessionId, signal) : Promise.resolve(undefined),
    enabled: !canProbeV6 || (resolvedVerdict?.kind ?? undefined) === "fallback-v5",
  });

  // 3) Derive the sanitised ResearchSessionCanvas + unified snapshot.
  const canvas = useMemo<ResearchSessionCanvas | null>(() => {
    const verdict = resolvedVerdict;
    if (!verdict) return null;

    if (verdict.kind === "v6") {
      const v6Snapshot = probe.data?.snapshot;
      if (!v6Snapshot) return null;
      return adaptV6Session(v6Snapshot);
    }
    if (verdict.kind === "fallback-v5") {
      if (!v5Query.data) return null;
      return adaptV5Session({
        sessionId,
        nodes: v5Query.data.nodes as readonly ResearchGraphNode[],
        edges: v5Query.data.edges as readonly ResearchGraphEdge[],
      });
    }
    return null;
  }, [resolvedVerdict, probe.data, v5Query.data, sessionId]);

  // 4) V6 slice engine — on-demand expansion, never a full-graph download.
  //
  // The slice hook must always run (no conditional hooks), but it is inert in
  // V5 mode or when no V6 slice gateway is supplied: the inert gateway is never
  // exercised because no root is ever requested unless `ensureSliceRoot` is
  // called from V6 mode. It exists only to keep the router/hook contract stable.
  const slice = useResearchSlice({
    gateway: transports.v6SliceGateway ?? inertSliceGateway,
    renderNodeBudget: 220 * 2, // viewport spec DOM ≤220 desktop; margin for grouping cards
    nodeBudget: 1500,
    maxRoots: 40,
    autoCancelStale: true,
  });
  const sliceEnabled = source === "v6" && !!transports.v6SliceGateway;
  const effectiveSlice = sliceEnabled ? slice : null;

  const ensureSliceRoot = useCallback(
    (root: string) => {
      if (!effectiveSlice) return;
      effectiveSlice.ensureRoot(root, { maxDepth: sliceMaxDepth, limit: 200 });
    },
    [effectiveSlice, sliceMaxDepth],
  );

  const isLoading =
    (canProbeV6 && probe.isLoading) ||
    (source === "v5" && v5Query.isLoading);
  const isFetching =
    (canProbeV6 && probe.isFetching) ||
    (source === "v5" && v5Query.isFetching);

  const error =
    resolvedVerdict &&
    (resolvedVerdict.kind === "interface-error" ||
      resolvedVerdict.kind === "unknown-version")
      ? { kind: resolvedVerdict.kind, reason: resolvedVerdict.reason }
      : null;

  const refetch = useCallback(() => {
    void qc.invalidateQueries({ queryKey: researchSessionCanvasKeys.all(wsId, sessionId) });
  }, [qc, wsId, sessionId]);

  // Wrap the slice result so callers can trigger a root slice load after the
  // probe resolves (kept outside the slice object to avoid identity churn).
  const sliceWithEnsure = useMemo(() => {
    if (!effectiveSlice) return null;
    return { ...effectiveSlice, ensureSliceRoot };
  }, [effectiveSlice, ensureSliceRoot]);

  const status: SessionCanvasStatus =
    error ? "error"
    : source === "v6" ? "v6"
    : source === "v5" ? "v5"
    : canProbeV6 && probe.isLoading ? "probing"
    : canProbeV6 ? "error"
    : "v5";

  return {
    snapshot: canvas?.snapshot ?? emptyCanvasSnapshot(),
    source,
    status,
    verdict: resolvedVerdict,
    canvas,
    error,
    isLoading,
    isFetching,
    slice: sliceWithEnsure,
    refetch,
  };
}

/**
 * Inert slice gateway used when no V6 slice gateway is supplied (V5 mode, or
 * V6 without a gateway configured). It is never exercised: `ensureSliceRoot` is
 * only reachable from V6 mode and only when a real gateway exists. If it is
 * ever called it rejects loudly so a silent full-graph download can never
 * happen by accident.
 */
const inertSliceGateway: ProjectionSliceGateway = {
  request: () =>
    Promise.reject(new Error("inert slice gateway: no V6 slice source configured")),
  observe: () => () => {},
};
