"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import type { WSEventType } from "../../types";
import type { ResearchV6ProjectionTransport } from "../../types/research-v6";
import { getApi } from "../../api";
import { createResearchV6ProjectionTransport } from "../../api/research-v6";
import { useWS } from "../../realtime";
import { researchV6Keys } from "../research-v6/queries";
import { useResearchV6ProjectionStore } from "../research-v6/store";
import type { ResearchV6LiveProjectionControllerOptions } from "./types";
import { createRealtimeLiveSource } from "./realtime-source";
import type {
  ResearchV6LiveSource,
  LiveConnectionStatus,
  ProjectionSyncStatus,
} from "./types";
import { ResearchV6LiveProjectionController } from "./controller";

export interface UseResearchV6LiveProjectionArgs {
  runId: string;
  wsId: string;
  /** Transport override (fixtures in tests/dev harness). Defaults to real API. */
  transport?: ResearchV6ProjectionTransport;
  /** Live source override (deterministic tests / custom transport). Defaults to the WSProvider realtime bus. */
  live?: ResearchV6LiveSource;
  /** Gap timeout for the underlying buffer (defaults to client's 8s). */
  gapTimeoutMs?: number;
  /** Auto-connect the live link on mount. Default true. */
  autoConnect?: boolean;
}

export interface UseResearchV6LiveProjectionResult {
  /** Data state — the ordered, de-duplicated projection cache (display state). */
  nodes: ReadonlyMap<string, import("../../types/research-v6").ResearchV6ProjectionNode>;
  edges: ReadonlyMap<string, import("../../types/research-v6").ResearchV6ProjectionEdge>;
  lastConfirmedSequence: number;
  pendingDeltaCount: number;
  awaitedSequence: number | null;
  resyncRequestedCount: number;
  projectionSyncStatus: ProjectionSyncStatus;
  malformedFrameCount: number;
  /** Connection state — strictly apart from data state. */
  liveStatus: LiveConnectionStatus;
  reconnectAttempts: number;
  disconnected: boolean;
  /** Feed a delta into the projection cache (manual/out-of-band deltas). */
  applyDelta: (delta: import("../../types/research-v6").ResearchV6Delta) => void;
  /** Reconnect from the last confirmed sequence. */
  reconnect: () => void;
  /** Force a fresh snapshot resync. */
  refresh: () => Promise<void>;
  /** Tear down the live link (explicit cancel). */
  disconnect: () => void;
}

/**
 * Research V6 live Graph Projection hook (LRM-1483 / FE-07).
 *
 * Builds on FE-01's projection client + React Query snapshot and adds the
 * real-time layer:
 *   - auto-connects to the WSProvider realtime bus and streams
 *     `research_session:graph_updated` deltas into the ordered projection cache
 *     (idempotent / out-of-order tolerant via the client);
 *   - reconnect (source drop or explicit) carries the last confirmed sequence
 *     — the server either continues contiguously or demands a resync;
 *   - connection status (`liveStatus`) is tracked separately from data state
 *     so a socket drop never masquerades as graph staleness.
 *
 * React Query owns the server snapshot; the projection display cache stays in
 * the coordinating Zustand store (client display state only). Server state is
 * never written into Zustand.
 */
export function useResearchV6LiveProjection({
  runId,
  wsId,
  transport: transportOverride,
  live: liveOverride,
  gapTimeoutMs,
  autoConnect = true,
}: UseResearchV6LiveProjectionArgs): UseResearchV6LiveProjectionResult {
  const defaultTransport = useMemo<ResearchV6ProjectionTransport>(() => {
    if (transportOverride) return transportOverride;
    return createResearchV6ProjectionTransport(getApi());
  }, [transportOverride]);
  const transportRef = useRef(defaultTransport);
  transportRef.current = defaultTransport;

  const { subscribe, onReconnect, onConnectionStatus } = useWS();
  const realtimeBus = useMemo(
    () => ({
      subscribeEvent: (event: string, handler: (payload: unknown) => void) =>
        subscribe(event as WSEventType, handler),
      onBusReconnect: (cb: () => void) => onReconnect(cb),
      onBusConnectionStatus: onConnectionStatus,
    }),
    [subscribe, onReconnect, onConnectionStatus],
  );
  const resolvedLive = useMemo<ResearchV6LiveSource>(
    () => liveOverride ?? createRealtimeLiveSource(realtimeBus),
    [liveOverride, realtimeBus],
  );

  // Server state: the raw snapshot (React Query). Errors/empty degrade.
  const { data: snapshot, isPending } = useQuery({
    queryKey: researchV6Keys.snapshot(wsId, runId),
    queryFn: () => transportRef.current.loadSnapshot(runId),
    enabled: !!runId && !!wsId,
  });

  // Coordinating store (client display state).
  const ensureClient = useResearchV6ProjectionStore((s) => s.ensureClient);
  const hydrate = useResearchV6ProjectionStore((s) => s.hydrate);
  const teardownRun = useResearchV6ProjectionStore((s) => s.teardownRun);
  const runSlice = useResearchV6ProjectionStore((s) => (runId ? s.runs[runId] : undefined));

  // Controller owns the live lifecycle + its projection client.
  const [controller, setController] = useState<ResearchV6LiveProjectionController | null>(null);
  const [liveStatus, setLiveStatus] = useState<LiveConnectionStatus>("idle");
  const [reconnectAttempts, setReconnectAttempts] = useState(0);

  const controllerOptions = useMemo<ResearchV6LiveProjectionControllerOptions>(
    () => ({ gapTimeoutMs, autoConnect }),
    [gapTimeoutMs, autoConnect],
  );
  const optionsRef = useRef(controllerOptions);
  optionsRef.current = controllerOptions;

  // Create / re-create the controller with the run's projection client.
  useEffect(() => {
    if (!runId) {
      setController(null);
      return;
    }
    const client = ensureClient(runId, { gapTimeoutMs });
    const c = new ResearchV6LiveProjectionController(
      runId,
      transportRef.current,
      resolvedLive,
      optionsRef.current,
      client,
    );
    let active = true;
    const offStatus = c.onStatusChange((status) => {
      if (!active) return;
      setLiveStatus(status);
      setReconnectAttempts(c.getReconnectAttempts());
    });
    const offChange = c.onChange(() => {
      if (active) hydrate(runId, client);
    });
    setController(c);
    if (autoConnect) c.connect();
    return () => {
      active = false;
      offStatus();
      offChange();
      c.destroy();
      teardownRun(runId);
      setController(null);
    };
  }, [
    autoConnect,
    defaultTransport,
    ensureClient,
    gapTimeoutMs,
    hydrate,
    resolvedLive,
    runId,
    teardownRun,
  ]);

  // Seed the projection cache from the fresh server snapshot (idempotent).
  useEffect(() => {
    if (!snapshot || isPending || !controller) return;
    controller.seedSnapshot(snapshot);
  }, [snapshot, isPending, controller, runId]);

  const applyDelta = useCallback(
    (delta: import("../../types/research-v6").ResearchV6Delta) => {
      controller?.applyDelta(delta);
    },
    [controller],
  );

  const reconnect = useCallback(() => {
    controller?.reconnect();
  }, [controller]);

  const refresh = useCallback(async () => {
    await controller?.refresh();
  }, [controller]);

  const disconnect = useCallback(() => {
    controller?.disconnect();
  }, [controller]);

  return {
    nodes: runSlice?.nodes ?? EMPTY_MAP_NODES,
    edges: runSlice?.edges ?? EMPTY_MAP_EDGES,
    lastConfirmedSequence: runSlice?.lastConfirmedSequence ?? 0,
    pendingDeltaCount: runSlice?.pendingDeltaCount ?? 0,
    awaitedSequence: runSlice?.awaitingSequence ?? null,
    resyncRequestedCount: runSlice?.resyncRequestedCount ?? 0,
    projectionSyncStatus: controller?.getSyncStatus() ?? "idle",
    malformedFrameCount: controller?.getMalformedFrameCount() ?? 0,
    liveStatus,
    reconnectAttempts,
    disconnected: liveStatus === "disconnected" || liveStatus === "idle",
    applyDelta,
    reconnect,
    refresh,
    disconnect,
  };
}

const EMPTY_MAP_NODES: ReadonlyMap<string, import("../../types/research-v6").ResearchV6ProjectionNode> =
  new Map();
const EMPTY_MAP_EDGES: ReadonlyMap<string, import("../../types/research-v6").ResearchV6ProjectionEdge> =
  new Map();
