"use client";

import { useCallback, useEffect, useMemo, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import type {
  ResearchV6Delta,
  ResearchV6ProjectionTransport,
} from "../../types/research-v6";
import { useResearchV6ProjectionStore } from "./store";
import { researchV6Keys } from "./queries";
import { getApi } from "../../api";
import { createResearchV6ProjectionTransport } from "../../api/research-v6";

export interface UseResearchV6ProjectionArgs {
  runId: string;
  wsId: string;
  /** Transport override (fixtures in tests/dev harness). Defaults to real API. */
  transport?: ResearchV6ProjectionTransport;
  /** Gap timeout for the underlying buffer (defaults to client's 8s). */
  gapTimeoutMs?: number;
}

export interface UseResearchV6ProjectionResult {
  nodes: ReadonlyMap<string, import("../../types/research-v6").ResearchV6ProjectionNode>;
  edges: ReadonlyMap<string, import("../../types/research-v6").ResearchV6ProjectionEdge>;
  lastConfirmedSequence: number;
  pendingDeltaCount: number;
  awaitedSequence: number | null;
  resyncRequestedCount: number;
  disconnected: boolean;
  /** Feed a WS delta into the projection cache. */
  applyDelta: (delta: ResearchV6Delta) => void;
  /** Reconnect from the last confirmed sequence. */
  reconnect: () => Promise<void>;
  /** Trigger an explicit resync (fresh snapshot). */
  refresh: () => Promise<void>;
}

/**
 * Research V6 Graph Projection hook.
 *
 * Seeds the client cache from the backend Snapshot (React Query / server state),
 * then applies WS deltas via the pure `ResearchV6ProjectionClient` (Zustand /
 * client display state). Reconnect carries the last confirmed sequence and the
 * server either continues contiguously or demands a resync; an unfilled gap or
 * a server-said-resync triggers exactly one observable resync (fresh snapshot).
 *
 * The WS delta is never written straight into a canonical Zustand store — it
 * flows through the projection client, which orders/idempotently applies it.
 */
export function useResearchV6Projection({
  runId,
  wsId,
  transport: transportOverride,
  gapTimeoutMs,
}: UseResearchV6ProjectionArgs): UseResearchV6ProjectionResult {
  const defaultTransport = useMemo<ResearchV6ProjectionTransport>(() => {
    if (transportOverride) return transportOverride;
    return createResearchV6ProjectionTransport(getApi());
  }, [transportOverride]);

  const transportRef = useRef(defaultTransport);
  transportRef.current = defaultTransport;

  // Server state: the raw snapshot (React Query). Errors/empty degrade.
  const { data: snapshot, isPending } = useQuery({
    queryKey: researchV6Keys.snapshot(wsId, runId),
    queryFn: () => transportRef.current.loadSnapshot(runId),
    enabled: !!runId && !!wsId,
  });

  const ensureClient = useResearchV6ProjectionStore((s) => s.ensureClient);
  const hydrate = useResearchV6ProjectionStore((s) => s.hydrate);
  const applyDeltaToStore = useResearchV6ProjectionStore((s) => s.applyDelta);

  // Seed the projection cache from the fresh snapshot (idempotent; a resync
  // later just replaces the graph).
  useEffect(() => {
    if (!snapshot || isPending) return;
    const client = ensureClient(runId, { gapTimeoutMs });
    client.applySnapshot(snapshot);
    hydrate(runId, client);
  }, [snapshot, isPending, runId, ensureClient, hydrate, gapTimeoutMs]);

  const applyDelta = useCallback(
    (delta: ResearchV6Delta) => applyDeltaToStore(runId, delta),
    [applyDeltaToStore, runId],
  );

  const refresh = useCallback(async () => {
    const client = ensureClient(runId, { gapTimeoutMs });
    const fresh = await transportRef.current.loadSnapshot(runId);
    client.applySnapshot(fresh);
    client.ackResyncCompleted();
    hydrate(runId, client);
  }, [ensureClient, hydrate, runId, gapTimeoutMs]);

  const reconnect = useCallback(async () => {
    const client = ensureClient(runId, { gapTimeoutMs });
    const verdict = await transportRef.current.resume(runId, client.getState().lastConfirmedSequence);
    if (!verdict.ok) {
      await refresh();
    }
    hydrate(runId, client);
  }, [ensureClient, hydrate, refresh, runId, gapTimeoutMs]);

  // Observe resync requests raised by the pure client (gap timeout / straddle),
  // driven by the store's resyncRequestedCount so we react to real changes, and
  // perform the fresh snapshot fetch once, then ack.
  const run = useResearchV6ProjectionStore((s) => s.runs[runId]);
  const resyncRequestedCount = run?.resyncRequestedCount ?? 0;
  const lastSyncedResyncRef = useRef(-1);

  useEffect(() => {
    if (resyncRequestedCount <= 0) return;
    if (resyncRequestedCount === lastSyncedResyncRef.current) return; // already handled
    lastSyncedResyncRef.current = resyncRequestedCount;
    const client = ensureClient(runId, { gapTimeoutMs });
    transportRef.current
      .loadSnapshot(runId)
      .then((fresh) => {
        client.applySnapshot(fresh);
        client.ackResyncCompleted();
        hydrate(runId, client);
      })
      .catch(() => {
        // Backend not ready — callers degrade; still clear the flag so the
        // next gap/request can be observed rather than stuck.
        client.ackResyncCompleted();
        hydrate(runId, client);
      });
  }, [resyncRequestedCount, ensureClient, hydrate, runId, gapTimeoutMs]);

  return {
    nodes: run?.nodes ?? EMPTY_MAP_NODES,
    edges: run?.edges ?? EMPTY_MAP_EDGES,
    lastConfirmedSequence: run?.lastConfirmedSequence ?? 0,
    pendingDeltaCount: run?.pendingDeltaCount ?? 0,
    awaitedSequence: run?.awaitingSequence ?? null,
    resyncRequestedCount,
    disconnected: run?.disconnected ?? true,
    applyDelta,
    reconnect,
    refresh,
  };
}

const EMPTY_MAP_NODES: ReadonlyMap<string, import("../../types/research-v6").ResearchV6ProjectionNode> =
  new Map();
const EMPTY_MAP_EDGES: ReadonlyMap<string, import("../../types/research-v6").ResearchV6ProjectionEdge> =
  new Map();
