/**
 * Research V6 · Slice needs driver (LRM-1465 / FE-03).
 *
 * Pure, framework-agnostic decision of WHICH slice requests a viewport needs,
 * so "first open", "composite-node expand", and "viewport pan" each emit only
 * the slices they need — never a re-request of an already-loaded root, and
 * never the whole graph. The viewport hook and the canvas can call this from a
 * React render without side effects; the returned needs are de-duplicated and
 * ordered by priority.
 */

import type { SliceDirection } from "./types";

export type SliceNeedReason = "seed" | "expand" | "pan" | "composite-expand";

export interface SliceNeed {
  root: string;
  reason: SliceNeedReason;
  direction: SliceDirection;
  maxDepth: number;
  limit: number;
  /** Stable priority: lower numbers load first. */
  priority: number;
}

export interface SliceNeedsInput {
  /** The run's canonical root — loaded first on open. */
  seedRoot: string | null;
  /** Roots already loaded or in-flight (dedupe target). */
  loadedRoots: ReadonlySet<string> | readonly string[];
  /** Composite nodes the user expanded this interaction. */
  expandedRoots?: readonly string[];
  /** Additional roots revealed by viewport panning. */
  visibleRoots?: readonly string[];
  /** A node was explicitly expanded (composite) — root to deep-load. */
  compositeExpandRoot?: string | null;
  /** Page size for adjacent slices. */
  limit?: number;
}

const FIRST_OPEN_PRIORITY = 0;
const EXPAND_PRIORITY = 1;
const PAN_PRIORITY = 2;

export function computeSliceNeeds(input: SliceNeedsInput): SliceNeed[] {
  const loaded = toSet(input.loadedRoots);
  const limit = Math.max(1, input.limit ?? 500);
  const needs: SliceNeed[] = [];
  const seen = new Set<string>();

  const push = (need: Omit<SliceNeed, "limit">) => {
    if (seen.has(need.root)) return;
    if (loaded.has(need.root)) return;
    seen.add(need.root);
    needs.push({ ...need, limit });
  };

  // First open: only the seed root.
  if (input.seedRoot) {
    push({
      root: input.seedRoot,
      reason: "seed",
      direction: "out",
      maxDepth: 8,
      priority: FIRST_OPEN_PRIORITY,
    });
  }

  // Explicit composite-node expansion: deep-load that one root on demand.
  if (input.compositeExpandRoot) {
    push({
      root: input.compositeExpandRoot,
      reason: "composite-expand",
      direction: "both",
      maxDepth: 12,
      priority: EXPAND_PRIORITY,
    });
  }

  for (const root of input.expandedRoots ?? []) {
    push({
      root,
      reason: "expand",
      direction: "out",
      maxDepth: 8,
      priority: EXPAND_PRIORITY,
    });
  }

  // Viewport pan: load adjacent roots that just became visible.
  for (const root of input.visibleRoots ?? []) {
    push({
      root,
      reason: "pan",
      direction: "out",
      maxDepth: 8,
      priority: PAN_PRIORITY,
    });
  }

  return needs.sort((a, b) => a.priority - b.priority);
}

function toSet(roots: ReadonlySet<string> | readonly string[] | undefined): Set<string> {
  if (!roots) return new Set();
  return roots instanceof Set ? new Set(roots) : new Set(roots);
}

/** Count of distinct roots that still need a slice (i.e. not yet loaded). */
export function pendingNeedCount(input: SliceNeedsInput): number {
  return computeSliceNeeds(input).length;
}
