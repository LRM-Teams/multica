import { create } from "zustand";
import type {
  ResearchV6Delta,
  ResearchV6ProjectionNode,
  ResearchV6ProjectionEdge,
  ResearchV6Snapshot,
} from "../../types/research-v6";
import { ResearchV6ProjectionClient } from "./projection-client";
import {
  isResearchV6DeltaForRun,
  isResearchV6SnapshotForRun,
} from "./projection-identity";

/**
 * Client-side display cache for the Research V6 Graph Projection.
 *
 * This is client state (display), managed with Zustand. It is deliberately
 * separate from any canonical Zustand store and from React Query's server
 * state: React Query owns the raw backend snapshot/delta pages; this store owns
 * the ordered projection cache built from them (buffers, cursor, applied graph).
 *
 * It never writes back to canonical state and never treats display grouping as
 * a real Insight.
 */
export interface ResearchV6RunSlice {
  nodes: ReadonlyMap<string, ResearchV6ProjectionNode>;
  edges: ReadonlyMap<string, ResearchV6ProjectionEdge>;
  lastConfirmedSequence: number;
  pendingDeltaCount: number;
  awaitingSequence: number | null;
  resyncRequestedCount: number;
  disconnected: boolean;
}

interface ResearchV6ProjectionStoreShape {
  /** runId → projection slice. */
  runs: Record<string, ResearchV6RunSlice>;
  /** runId → internal client (holds buffered deltas, not exposed over the wire). */
  clients: Record<string, ResearchV6ProjectionClient>;
  /** Sync a run's client into its public slice. */
  hydrate: (runId: string, client: ResearchV6ProjectionClient) => void;
  /** Ensure a client exists for a run; returns the (possibly new) client. */
  ensureClient: (
    runId: string,
    options?: { gapTimeoutMs?: number },
  ) => ResearchV6ProjectionClient;
  /** Remove all state for a run (disconnect / cleanup). */
  teardownRun: (runId: string) => void;
  /** Mark a run disconnected without dropping its projection cache. */
  markDisconnected: (runId: string, disconnected: boolean) => void;
  /** Apply a WS delta to a run's projection cache. */
  applyDelta: (runId: string, delta: ResearchV6Delta) => void;
  /** Load a fresh snapshot into a run's projection cache (resync). */
  applySnapshot: (runId: string, snapshot: ResearchV6Snapshot) => void;
}

function sliceOf(client: ResearchV6ProjectionClient, disconnected = false): ResearchV6RunSlice {
  const s = client.getState();
  return {
    nodes: s.nodes,
    edges: s.edges,
    lastConfirmedSequence: s.lastConfirmedSequence,
    pendingDeltaCount: s.pendingDeltas.size,
    awaitingSequence: s.awaitingSequence,
    resyncRequestedCount: s.resyncRequestedCount,
    disconnected,
  };
}

export const useResearchV6ProjectionStore = create<ResearchV6ProjectionStoreShape>()((set, get) => ({
  runs: {},
  clients: {},

  ensureClient(runId, options) {
    let client = get().clients[runId];
    if (!client) {
      client = new ResearchV6ProjectionClient(options);
      set((state) => ({
        clients: { ...state.clients, [runId]: client! },
        runs: {
          ...state.runs,
          [runId]: sliceOf(client!, true),
        },
      }));
    }
    return client;
  },

  hydrate(runId, client) {
    set((state) => ({
      clients: { ...state.clients, [runId]: client },
      runs: { ...state.runs, [runId]: sliceOf(client, state.runs[runId]?.disconnected ?? false) },
    }));
  },

  applyDelta(runId, delta) {
    const client = get().clients[runId];
    if (!client) return;
    if (!isResearchV6DeltaForRun(delta, runId)) {
      client.requestResync();
      get().hydrate(runId, client);
      return;
    }
    client.applyDelta(delta);
    get().hydrate(runId, client);
  },

  applySnapshot(runId, snapshot) {
    if (!isResearchV6SnapshotForRun(snapshot, runId)) return;
    const client = get().clients[runId];
    if (!client) {
      const fresh = new ResearchV6ProjectionClient();
      fresh.applySnapshot(snapshot);
      get().hydrate(runId, fresh);
      return;
    }
    client.applySnapshot(snapshot);
    get().hydrate(runId, client);
  },

  markDisconnected(runId, disconnected) {
    set((state) => {
      const run = state.runs[runId];
      if (!run) return state;
      return {
        runs: { ...state.runs, [runId]: { ...run, disconnected } },
      };
    });
  },

  teardownRun(runId) {
    set((state) => {
      const runs = { ...state.runs };
      const clients = { ...state.clients };
      delete runs[runId];
      delete clients[runId];
      return { runs, clients };
    });
  },
}));
