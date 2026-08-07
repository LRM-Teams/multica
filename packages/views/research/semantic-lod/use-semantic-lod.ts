"use client";

/**
 * Semantic LOD — live selection hook (LRM-1488).
 *
 * Convenience hook that recomputes the semantic classification for a graph
 * whenever its context (selection / blocking / running / zoom / explicit
 * expand) changes. Pure data in → classified render decisions out; no DOM or
 * geometry reads live here.
 */
import { useMemo } from "react";
import {
  selectSemanticNodes,
  type SemanticSelectContext,
  type SemanticSelectResult,
  type SemanticEdgeInput,
  type SemanticNodeInput,
} from "./selector";
import type { ViewportTier } from "./model";

export interface UseSemanticLodArgs {
  nodes: readonly SemanticNodeInput[];
  edges: readonly SemanticEdgeInput[];
  context: SemanticSelectContext;
  tier: ViewportTier;
}

export function useSemanticLod(
  args: UseSemanticLodArgs,
): SemanticSelectResult {
  return useMemo(
    () => selectSemanticNodes(args),
    // Recompute exactly when classification inputs change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [args.nodes, args.edges, args.context, args.tier],
  );
}
