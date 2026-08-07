/**
 * Semantic LOD — visible budget enforcement (LRM-1488).
 *
 * `enforceVisibleBudget` takes the per-node LOD classifications and squeezes
 * them into the viewport budget caps (route-topology §6.3 / viewport §3). It
 * is a pure, deterministic function of (nodes, context, budget): the same
 * input always yields the same fold decision, and it never reads the DOM.
 *
 * Retention priority follows viewport-performance §4:
 *   1. selected + ancestors + directly-related
 *   2. blocking / failed / cancelling / stale path
 *   3. running task/attempt
 *   4. recent transition affected roots
 *   5. pinned
 *   6. importance-high fresh nodes
 *   7. everything else → fold into a Route Bundle / Display Group
 *
 * Selected / ancestor / blocking nodes are NEVER cut by a budget
 * (viewport-performance §4 footer).
 */
import {
  BUNDLE_FOLD_DEPTH,
  MAX_VISIBLE_DEPTH,
  VIEWPORT_BUDGETS,
  zoomTier,
  type SemanticContext,
  type SemanticLOD,
  type ViewportTier,
  type VisibleBudget,
} from "./model";
import type { LodClassification } from "./classify";

export interface LODEntry {
  id: string;
  kind: string;
  status: string;
  importance: number;
  classification: LodClassification;
}

export interface BudgetResult {
  /** Final per-node semantic render form after budget folding. */
  byNode: ReadonlyMap<string, SemanticLOD>;
  /** Nodes that were folded into a Route Bundle / Display Group. */
  foldedIntoBundle: readonly string[];
  /**
   * True when expanding here would exceed the hard semantic-node cap, so the
   * renderer shows a Route Bundle / Spotlight instead of spreading nodes.
   */
  refuseAdvance: boolean;
  /** Live counts after enforcement (diagnostics / legend). */
  counts: {
    landmark: number;
    waypoint: number;
    trailDot: number;
    bundle: number;
    displayGroup: number;
    totalSemanticNodes: number;
    graphicDomEstimate: number;
  };
}

/** Route Bundle / Display Group virtual node id (honest aggregate). */
export function foldTarget(kind: string): string {
  if (
    kind === "query_execution" ||
    kind === "source_candidate" ||
    kind === "screening_decision" ||
    kind === "observation"
  ) {
    return "route-bundle:trail";
  }
  return "route-bundle:default";
}

/** Retention rank for ordering fold candidates (lower = keep first). */
function retentionRank(
  entry: LODEntry,
  context: SemanticContext,
): number {
  const id = entry.id;
  if (id === context.selectedId) return 0;
  if (context.ancestorIds.includes(id)) return 1;
  if (context.blockingIds.includes(id)) return 2;
  if (context.runningIds.includes(id)) return 3;
  if (context.transitionRootIds.includes(id)) return 4;
  if (context.pinnedIds.includes(id)) return 5;
  // Higher importance = higher priority (kept before lower-importance nodes).
  return 100 - Math.round(entry.importance * 99);
}

/**
 * Enforce the visible budget. Node depth survives inside each entry's
 * classification; folds happen only for non-protected candidates, and the
 * hard semantic-node cap can force a collapse into bundles instead of a
 * full spread.
 */
export function enforceVisibleBudget(
  args: {
    entries: readonly LODEntry[];
    context: SemanticContext;
    tier: ViewportTier;
    budget?: VisibleBudget;
  },
): BudgetResult {
  const budget = args.budget ?? VIEWPORT_BUDGETS[args.tier];
  const { context } = args;

  const byRank = (a: LODEntry, b: LODEntry) =>
    retentionRank(a, context) - retentionRank(b, context);

  const foldedIds = new Set<string>();
  const finalLod = new Map<string, SemanticLOD>();

  // 1) Bucket by current classification.
  const protectedNodes: LODEntry[] = [];
  const landmarks: LODEntry[] = [];
  const waypoints: LODEntry[] = [];
  const trailDots: LODEntry[] = [];

  for (const entry of args.entries) {
    if (entry.classification.protected) {
      protectedNodes.push(entry);
      continue;
    }
    // Nodes already classified as a Route Bundle aggregate are folded members
    // (route-topology §6.2 4th layer) — they are never individually rendered.
    if (entry.classification.lod === "route-bundle") {
      foldedIds.add(entry.id);
      continue;
    }
    switch (entry.classification.lod) {
      case "landmark":
        landmarks.push(entry);
        break;
      case "waypoint":
        waypoints.push(entry);
        break;
      default:
        trailDots.push(entry);
        break;
    }
  }

  for (const p of protectedNodes) {
    finalLod.set(p.id, "landmark");
  }

  // 2) Landmark hard cap → demote lowest-priority non-protected landmarks.
  //    Overview zoom (<35%) caps the TOTAL landmark count at 12 regardless of
  //    viewport tier (route-topology §6.1), so the non-protected allowance is
  //    12 minus the protected (selected/root/blocking) cards already kept.
  const overview = zoomTier(context.zoomPct) === "overview";
  const effectiveLandmarkHard = overview
    ? Math.max(0, 12 - protectedNodes.length)
    : budget.landmarkHard;
  const sortedLandmarks = [...landmarks].sort(byRank);
  const keptLandmarks: LODEntry[] = [];
  const demotedLandmarks: LODEntry[] = [];
  for (const lm of sortedLandmarks) {
    if (keptLandmarks.length < effectiveLandmarkHard) {
      keptLandmarks.push(lm);
    } else {
      demotedLandmarks.push(lm);
    }
  }
  for (const lm of keptLandmarks) finalLod.set(lm.id, "landmark");

  // 3) Waypoint hard cap → demote excess waypoints to trail dots.
  const waypointPool = [...demotedLandmarks, ...waypoints].sort(byRank);
  const keptWaypoints: LODEntry[] = [];
  const excessWaypoints: LODEntry[] = [];
  for (const wp of waypointPool) {
    if (keptWaypoints.length < budget.waypointHard) {
      keptWaypoints.push(wp);
    } else {
      excessWaypoints.push(wp);
    }
  }
  for (const wp of keptWaypoints) finalLod.set(wp.id, "waypoint");

  // 4) Trail-dot hard cap → the overflow folds into Route Bundle.
  const trailPool = [...excessWaypoints, ...trailDots].sort(byRank);
  const keptTrail: LODEntry[] = [];
  for (const dot of trailPool) {
    if (keptTrail.length < budget.trailDotHard) {
      keptTrail.push(dot);
    } else {
      foldedIds.add(dot.id);
    }
  }
  for (const dot of keptTrail) finalLod.set(dot.id, "trail-dot");

  // 5) Total semantic-node soft/hard cap. The Route Bundle/Display Group is
  //    one virtual semantic node counting toward the total (§6.3).
  let totalVisible =
    protectedNodes.length +
    keptLandmarks.length +
    keptWaypoints.length +
    keptTrail.length;
  const bundleCount = foldedIds.size > 0 ? 1 : 0;
  let refuseAdvance = false;

  if (totalVisible + bundleCount > budget.semanticNodeSoft) {
    // Fold lowest-priority kept visible nodes (never protected).
    const foldable = [
      ...keptTrail.map((e) => e),
      ...keptWaypoints.map((e) => e),
      ...keptLandmarks.map((e) => e),
    ].sort(byRank);
    for (const item of foldable) {
      if (totalVisible + bundleCount <= budget.semanticNodeSoft) break;
      foldedIds.add(item.id);
      finalLod.delete(item.id);
      totalVisible -= 1;
    }
  }

  // Hard semantic-node cap can never be exceeded; refuse advance → Spotlight.
  if (totalVisible + bundleCount > budget.semanticNodeHard) {
    refuseAdvance = true;
  }

  // 6) Every remaining non-protected node has a final LOD or is folded.
  for (const entry of args.entries) {
    if (entry.classification.protected) continue;
    if (!finalLod.has(entry.id) && !foldedIds.has(entry.id)) {
      foldedIds.add(entry.id);
    }
  }

  const foldedArray = args.entries
    .filter((e) => foldedIds.has(e.id))
    .map((e) => e.id);

  const nonProtectedVisible = args.entries.filter(
    (e) => !e.classification.protected && finalLod.has(e.id),
  );
  const landmarkCount = args.entries.filter(
    (e) => finalLod.get(e.id) === "landmark",
  ).length;
  const waypointCount = nonProtectedVisible.filter(
    (e) => finalLod.get(e.id) === "waypoint",
  ).length;
  const trailDotCount = nonProtectedVisible.filter(
    (e) => finalLod.get(e.id) === "trail-dot",
  ).length;

  const counts = {
    landmark: landmarkCount,
    waypoint: waypointCount,
    trailDot: trailDotCount,
    bundle: bundleCount,
    displayGroup: 0,
    totalSemanticNodes: totalVisible + bundleCount,
    graphicDomEstimate:
      landmarkCount + waypointCount + trailDotCount + bundleCount,
  };

  return {
    byNode: finalLod,
    foldedIntoBundle: foldedArray,
    refuseAdvance,
    counts,
  };
}

export { BUNDLE_FOLD_DEPTH, MAX_VISIBLE_DEPTH };
