"use client";

import { useEffect, useRef } from "react";
import {
  computeSliceNeeds,
  useResearchSlice,
  type ProjectionSliceGateway,
  type SliceNeed,
} from "@multica/core/research-v6-slice";

export interface UseResearchSliceViewportOptions {
  gateway: ProjectionSliceGateway;
  /** Canonical run root — loaded first on open. */
  seedRoot: string | null;
  /** Roots newly revealed by viewport panning. */
  visibleRoots?: readonly string[];
  /** Composite nodes the user expanded this interaction. */
  expandedRoots?: readonly string[];
  /** An explicitly expanded composite node to deep-load now. */
  compositeExpandRoot?: string | null;
  /** Retained-node cache budget. */
  nodeBudget?: number;
  /** Merged render-state node cap. */
  renderNodeBudget?: number;
  /** Merged render-state root cap. */
  maxRoots?: number;
}

export interface UseResearchSliceViewportReturn {
  /** Called once per interaction to open/expand/pan-trigger slice loading. */
  request: (roots: {
    visibleRoots?: readonly string[];
    expandedRoots?: readonly string[];
    compositeExpandRoot?: string | null;
  }) => void;
  /** Root slices currently loaded by the slice engine. */
  roots: ReturnType<typeof useResearchSlice>["roots"];
  /** Live unique node count (bounded by the cache budget). */
  uniqueNodeCount: number;
  /** Wire requests issued on the underlying gateway. */
  wireRequests: number;
  /** Roots already requested (dedupe source). */
  requestedRoots: readonly string[];
}

/**
 * Viewport-driven Projection Slice loading (LRM-1465 AC1/AC2).
 *
 * First open requests only the seed slice; composite-node expand and viewport
 * pan each request only the adjacent roots they newly need. Already-requested
 * roots are never re-fired (no duplicate pagination), and all pages are merged
 * through the bounded slice cache so the browser never holds the whole graph.
 */
export function useResearchSliceViewport(
  options: UseResearchSliceViewportOptions,
): UseResearchSliceViewportReturn {
  const { gateway, seedRoot, nodeBudget, renderNodeBudget, maxRoots } = options;
  const slice = useResearchSlice({ gateway, nodeBudget, renderNodeBudget, maxRoots });

  const requestedRef = useRef<Set<string>>(new Set());
  const optionsRef = useRef(options);
  optionsRef.current = options;

  // Prime the seed root once on open (first slice request only).
  useEffect(() => {
    if (!seedRoot || requestedRef.current.has(seedRoot)) return;
    requestedRef.current.add(seedRoot);
    slice.ensureRoot(seedRoot);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [seedRoot]);

  const request = (roots: {
    visibleRoots?: readonly string[];
    expandedRoots?: readonly string[];
    compositeExpandRoot?: string | null;
  }) => {
    const needs: SliceNeed[] = computeSliceNeeds({
      seedRoot: null,
      loadedRoots: requestedRef.current,
      visibleRoots: roots.visibleRoots,
      expandedRoots: roots.expandedRoots,
      compositeExpandRoot: roots.compositeExpandRoot ?? optionsRef.current.compositeExpandRoot ?? null,
    });
    for (const need of needs) {
      if (requestedRef.current.has(need.root)) continue;
      requestedRef.current.add(need.root);
      slice.ensureRoot(need.root, {
        direction: need.direction,
        maxDepth: need.maxDepth,
        limit: need.limit,
      });
    }
  };

  return {
    request,
    roots: slice.roots,
    uniqueNodeCount: slice.uniqueNodeCount,
    wireRequests: slice.wireRequests,
    requestedRoots: Array.from(requestedRef.current),
  };
}
