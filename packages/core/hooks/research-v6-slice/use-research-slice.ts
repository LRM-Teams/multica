"use client";

import { useCallback, useEffect, useMemo, useReducer, useRef } from "react";
import { SlicePageCache } from "./cache";
import type { ProjectionSliceGateway } from "./fixture";
import { SliceLoader } from "./loader";
import type {
  ProjectionSliceRequest,
  ProjectionSliceResponse,
  SliceLoadPhase,
  SliceNode,
} from "./types";

/** Merged client-side view of one root's loaded slices. */
export interface RootSliceView {
  root: string;
  nodes: Record<string, SliceNode>;
  edgesByKey: Map<string, { from: string; to: string; type: string }>;
  hasMore: boolean;
  nextCursor: string | null;
  phase: SliceLoadPhase;
  error: string | null;
  /** Pages successfully loaded for this root (dedup by cursor). */
  pagesLoaded: number;
}

export interface SliceBundleState {
  roots: Record<string, RootSliceView>;
  /** Aggregate unique nodes across the merged render state. */
  uniqueNodeCount: number;
  /** Wire requests observed since the bundle was created. */
  wireRequests: number;
  /** LRU recency clock per root. */
  recency: Record<string, number>;
  lruClock: number;
  /** Hard cap on merged nodes held in render state. */
  renderNodeBudget: number;
  /** Hard cap on loaded roots held in render state. */
  maxRoots: number;
}

type SliceAction =
  | { type: "root-loading"; root: string }
  | { type: "root-success"; root: string; page: ProjectionSliceResponse }
  | { type: "root-error"; root: string; error: string }
  | { type: "root-clear"; root: string }
  | { type: "bump-wire" };

export interface UseResearchSliceOptions {
  gateway: ProjectionSliceGateway;
  /** Cache retained-node budget (default 1500). */
  nodeBudget?: number;
  /** Merged render-state node cap (default 1500). */
  renderNodeBudget?: number;
  /** Merged render-state root cap (default 40). */
  maxRoots?: number;
  /** Enable cancelling superseded in-flight requests automatically. */
  autoCancelStale?: boolean;
}

function emptyRoot(root: string): RootSliceView {
  return {
    root,
    nodes: {},
    edgesByKey: new Map(),
    hasMore: false,
    nextCursor: null,
    phase: "idle",
    error: null,
    pagesLoaded: 0,
  };
}

function countNodes(roots: Record<string, RootSliceView>): number {
  let total = 0;
  for (const r of Object.keys(roots)) total += Object.keys(roots[r]!.nodes).length;
  return total;
}

function evictOverBudget(
  roots: Record<string, RootSliceView>,
  recency: Record<string, number>,
  keep: string,
  renderNodeBudget: number,
  maxRoots: number,
): { roots: Record<string, RootSliceView>; recency: Record<string, number> } {
  const nextRoots = { ...roots };
  const nextRecency = { ...recency };
  let total = countNodes(nextRoots);
  const rootIds = Object.keys(nextRoots).sort((a, b) => nextRecency[a]! - nextRecency[b]!); // oldest first
  for (const r of rootIds) {
    if (r === keep) continue;
    if (total <= renderNodeBudget && Object.keys(nextRoots).length <= maxRoots) break;
    delete nextRoots[r];
    delete nextRecency[r];
    total = countNodes(nextRoots);
  }
  return { roots: nextRoots, recency: nextRecency };
}

function reducer(state: SliceBundleState, action: SliceAction): SliceBundleState {
  switch (action.type) {
    case "root-loading": {
      const prev = state.roots[action.root];
      const view = prev ?? emptyRoot(action.root);
      return {
        ...state,
        roots: { ...state.roots, [action.root]: { ...view, phase: "loading", error: null } },
        recency: { ...state.recency, [action.root]: ++state.lruClock },
        lruClock: state.lruClock + 1,
      };
    }
    case "root-success": {
      const { root, page } = action;
      const prev = state.roots[root] ?? emptyRoot(root);
      const nodes = { ...prev.nodes };
      const edgesByKey = new Map(prev.edgesByKey);
      for (const n of page.nodes) nodes[n.node.id] = n;
      for (const e of page.edges) {
        edgesByKey.set(e.edge.id, {
          from: e.edge.from_node_id,
          to: e.edge.to_node_id,
          type: e.edge.edge_type,
        });
      }
      const nextRoots = {
        ...state.roots,
        [root]: {
          root,
          nodes,
          edgesByKey,
          hasMore: page.hasMore,
          nextCursor: page.nextCursor,
          phase: "success" as SliceLoadPhase,
          error: null,
          pagesLoaded: prev.pagesLoaded + 1,
        },
      };
      const recency = { ...state.recency, [root]: ++state.lruClock };
      const lruClock = state.lruClock + 1;
      const bounded = evictOverBudget(nextRoots, recency, root, state.renderNodeBudget, state.maxRoots);
      return {
        ...state,
        roots: bounded.roots,
        recency: bounded.recency,
        lruClock,
        uniqueNodeCount: countNodes(bounded.roots),
      };
    }
    case "root-error": {
      const prev = state.roots[action.root] ?? emptyRoot(action.root);
      return {
        ...state,
        roots: { ...state.roots, [action.root]: { ...prev, phase: "error", error: action.error } },
      };
    }
    case "root-clear": {
      const nextRoots = { ...state.roots };
      const nextRecency = { ...state.recency };
      delete nextRoots[action.root];
      delete nextRecency[action.root];
      return {
        ...state,
        roots: nextRoots,
        recency: nextRecency,
        uniqueNodeCount: countNodes(nextRoots),
      };
    }
    case "bump-wire":
      return { ...state, wireRequests: state.wireRequests + 1 };
    default:
      return state;
  }
}

/**
 * React state hook for bounded Projection Slice loading. Owns a `SliceLoader`
 * and a `SlicePageCache`, and merges slice pages into a bounded, deduplicated
 * render view. It never asks the gateway for the whole graph — only
 * `limit`-bounded pages on demand, coalesced and cached — and it bounds both
 * the cache and the merged render state (LRU root eviction) so a 10k-node run
 * is never fully held in the browser.
 */
export function useResearchSlice(options: UseResearchSliceOptions) {
  const {
    gateway,
    nodeBudget = 1500,
    renderNodeBudget = 1500,
    maxRoots = 40,
    autoCancelStale = true,
  } = options;
  const [state, dispatch] = useReducer(reducer, {
    roots: {},
    uniqueNodeCount: 0,
    wireRequests: 0,
    recency: {},
    lruClock: 0,
    renderNodeBudget,
    maxRoots,
  });

  const optionsKey = `${renderNodeBudget}|${maxRoots}`;
  const loaderRef = useRef<SliceLoader | null>(null);
  const loader = useMemo(() => {
    const cache = new SlicePageCache({ nodeBudget, maxEntries: 300 });
    return new SliceLoader({ cache, gateway });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [gateway, nodeBudget, optionsKey]);

  loaderRef.current = loader;

  // Observe wire requests for diagnostics / Network-verification in tests.
  useEffect(() => {
    const off = gateway.observe(() => dispatch({ type: "bump-wire" }));
    return off;
  }, [gateway]);

  const ensureRoot = useCallback(
    (root: string, overrides?: Partial<Pick<ProjectionSliceRequest, "direction" | "maxDepth" | "limit" | "status" | "importanceFloor" | "relationTypes">>) => {
      const req: ProjectionSliceRequest = {
        root,
        direction: overrides?.direction ?? "out",
        maxDepth: overrides?.maxDepth ?? 8,
        limit: overrides?.limit ?? 500,
        status: overrides?.status ?? null,
        importanceFloor: overrides?.importanceFloor ?? 0,
        relationTypes: overrides?.relationTypes ?? null,
      };
      dispatch({ type: "root-loading", root });
      void loader.load(req).then(
        (res) => {
          if (!loader.isLatest(root, res.token)) return;
          dispatch({ type: "root-success", root, page: res.page });
        },
        (err: unknown) => {
          if (err instanceof Error && err.name === "AbortError") return;
          dispatch({ type: "root-error", root, error: err instanceof Error ? err.message : "slice error" });
        },
      );
      if (autoCancelStale) loader.cancelStale();
    },
    [autoCancelStale, loader],
  );

  const loadMore = useCallback(
    (root: string) => {
      const view = state.roots[root];
      if (!view || !view.hasMore || !view.nextCursor) return;
      const req: ProjectionSliceRequest = {
        root,
        direction: "out",
        maxDepth: 8,
        limit: 500,
        status: null,
        importanceFloor: 0,
        relationTypes: null,
        cursor: view.nextCursor,
      };
      dispatch({ type: "root-loading", root });
      void loader.load(req).then(
        (res) => {
          if (!loader.isLatest(root, res.token)) return;
          dispatch({ type: "root-success", root, page: res.page });
        },
        (err: unknown) => {
          if (err instanceof Error && err.name === "AbortError") return;
          dispatch({ type: "root-error", root, error: err instanceof Error ? err.message : "slice error" });
        },
      );
    },
    [state.roots, loader],
  );

  const reloadRoot = useCallback(
    (root: string) => {
      loader.cancelRoot(root);
      dispatch({ type: "root-clear", root });
      ensureRoot(root);
    },
    [ensureRoot, loader],
  );

  const clearRoot = useCallback(
    (root: string) => {
      loader.cancelRoot(root);
      dispatch({ type: "root-clear", root });
    },
    [loader],
  );

  // On unmount, cancel everything.
  useEffect(() => {
    return () => {
      loaderRef.current?.cancelAll();
    };
  }, []);

  const result = useMemo(
    () => ({
      ...state,
      root: (id: string): RootSliceView | undefined => state.roots[id],
      stats: loader.getStats(),
      inflight: loader.inflightCount(),
    }),
    [state, loader],
  );

  return { ...result, ensureRoot, loadMore, reloadRoot, clearRoot };
}

export type UseResearchSliceReturn = ReturnType<typeof useResearchSlice>;

export function nodeCountOf(state: SliceBundleState): number {
  return state.uniqueNodeCount;
}
