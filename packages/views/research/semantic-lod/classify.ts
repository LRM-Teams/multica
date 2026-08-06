/**
 * Semantic LOD — classification (LRM-1488).
 *
 * `classifySemanticLOD` maps a node + context to one of the four discrete
 * semantic render forms; `classifyRouteOutcome` maps a node/edge status to a
 * route outcome through an explicit status registry (route-topology §3).
 *
 * Rules honoured here:
 *   - status is never inferred from title/summary prose;
 *   - unknown status strings fall into `neutral` (registry §3);
 *   - selected / ancestor-path / blocking-path nodes are never demoted below
 *     Landmark by an ordinary LOD decision (route-topology §6.2);
 *   - the 4th+ hop folds into Route Bundle / Spotlight (BUNDLE_FOLD_DEPTH).
 */
import {
  BUNDLE_FOLD_DEPTH,
  DEFAULT_VISIBLE_DEPTH,
  MAX_VISIBLE_DEPTH,
  type RouteOutcome,
  type SemanticContext,
  type SemanticLOD,
  zoomTier,
} from "./model";

/**
 * Explicit route-status registry (route-topology §3). Each canonical status
 * string maps to exactly one route outcome; anything not listed falls into
 * `neutral`. This is a display classification ONLY — it never rewrites a
 * canonical node status.
 */
const ROUTE_OUTCOME_BY_STATUS: Record<string, RouteOutcome> = {
  // exploring (active branch/query/task/attempt)
  active: "exploring",
  exploring: "exploring",
  running: "exploring",
  executing: "exploring",
  in_progress: "exploring",
  queued: "exploring",
  dispatching: "exploring",
  dispatched: "exploring",
  progress: "exploring",
  // accepted (accepted result/claim/insight/decision)
  accepted: "accepted",
  resolved: "accepted",
  done: "accepted",
  succeeded: "accepted",
  completed: "accepted",
  answered: "accepted",
  approved: "accepted",
  adopted: "accepted",
  // failed / lost (terminal execution failure)
  failed: "failed",
  lost: "failed",
  error: "failed",
  cancelled_failure: "failed",
  // cancelled / cancelling
  cancelled: "cancelled",
  canceling: "cancelled",
  cancelling: "cancelled",
  abandoned: "cancelled",
  stopped: "cancelled",
  // stale / invalidated
  stale: "stale",
  invalidated: "stale",
  invalid: "stale",
  superseded: "stale",
  expired: "stale",
  // disputed / challenged
  disputed: "disputed",
  challenged: "disputed",
  under_review: "disputed",
  // neutral / unknown
  unknown: "neutral",
  pending: "neutral",
  new: "neutral",
  created: "neutral",
  idle: "neutral",
};

/**
 * Classify a node/edge status into a route outcome using the explicit
 * registry. Unknown statuses always fall to `neutral` — never guessed.
 */
export function classifyRouteOutcome(status: string | null | undefined): RouteOutcome {
  if (!status) return "neutral";
  const key = status.trim().toLowerCase();
  return ROUTE_OUTCOME_BY_STATUS[key] ?? "neutral";
}

/** Canonical terminal / protected statuses a blocking path is built from. */
export const BLOCKING_OUTCOMES: ReadonlySet<RouteOutcome> = new Set<RouteOutcome>([
  "failed",
  "cancelled",
  "stale",
]);

/**
 * A blocking-path identifier is any status whose route outcome is failed /
 * cancelled / stale (viewport-performance §4 priority 2).
 */
export function isBlockingStatus(status: string | null | undefined): boolean {
  return BLOCKING_OUTCOMES.has(classifyRouteOutcome(status));
}

/**
 * True for "temporarily promote one beat" on recent transition roots
 * (route-topology §2.1 item 6).
 */
export function isTransitionRoot(
  context: SemanticContext,
  nodeId: string,
): boolean {
  return context.transitionRootIds.includes(nodeId);
}

export interface ClassifyLodInput {
  /** Node being classified. */
  id: string;
  /** Node kind (verbatim, may be unknown/generic). */
  kind: string;
  /** Node status (verbatim). */
  status: string;
  /** Node importance 0..1. */
  importance: number;
  context: SemanticContext;
}

export interface LodClassification {
  lod: SemanticLOD;
  /** Hop depth of this node (context.depthById, default 0 = root). */
  depth: number;
  /** Route outcome from the status registry. */
  outcome: RouteOutcome;
  /** True when this node must be preserved through any budget. */
  protected: boolean;
}

/** Nodes that must survive ordinary LOD decisions regardless of depth/zoom. */
function isProtectedCandidate(
  input: ClassifyLodInput,
  context: SemanticContext,
): boolean {
  const { id } = input;
  if (id === context.selectedId) return true;
  if (context.ancestorIds.includes(id)) return true;
  if (context.blockingIds.includes(id)) return true;
  return false;
}

/** Kinds promoted to a Landmark Card (route-topology §2.1). */
const LANDMARK_KINDS = new Set<string>([
  // root / active task
  "task",
  "episode",
  "episode_root",
  // canonical Insight / Decision / Dispute / Report Revision
  "insight",
  "decision",
  "dispute",
  "report_revision",
  // Integration (main narrative)
  "integration_round",
]);

/** Top-level lane promotion: qualifies as a Landmark under §2.1. */
function landmarkByKind(input: ClassifyLodInput): boolean {
  return LANDMARK_KINDS.has(input.kind);
}

/** True when this node is on the current narrative spine (root/active path). */
function isNarrativeSpine(
  input: ClassifyLodInput,
  context: SemanticContext,
): boolean {
  if (input.id === context.selectedId) return true;
  if (context.ancestorIds.includes(input.id)) return true;
  return context.runningIds.includes(input.id);
}

/** Kinds that render as a Waypoint pill (route-topology §2.2). */
const WAYPOINT_KINDS = new Set<string>([
  "question",
  "hypothesis",
  "task",
  "claim",
  "observation",
  "attempt",
  "branch",
  "search_plan",
  "integration_contribution",
]);

/** Whether the node is a candidate Waypoint by kind (route-topology §2.2). */
function waypointByKind(kind: string): boolean {
  return WAYPOINT_KINDS.has(kind);
}

/**
 * Classify a node into a discrete semantic render form. Protected nodes
 * (selected / ancestor / blocking path) are never demoted below Landmark by
 * an ordinary decision: only the explicit budget layer (enforceVisibleBudget)
 * decides when a hard cap physically forces a fold — and it may never fold a
 * protected node per viewport-performance §4.
 */
export function classifySemanticLOD(input: ClassifyLodInput): LodClassification {
  const { context } = input;
  const depth = context.depthById.get(input.id) ?? 0;
  const outcome = classifyRouteOutcome(input.status);
  const protectedNode = isProtectedCandidate(input, context);

  // 4th+ hop always folds into a Route Bundle / Spotlight unless protected.
  if (depth >= BUNDLE_FOLD_DEPTH && !protectedNode) {
    return { lod: "route-bundle", depth, outcome, protected: protectedNode };
  }

  // Depth beyond the visible range (2 by default, 3 only after explicit
  // expand) also folds — the graph may be deeper than the visible window.
  const maxVisible = context.explicitThirdLevel
    ? MAX_VISIBLE_DEPTH
    : DEFAULT_VISIBLE_DEPTH;
  if (depth > maxVisible && !protectedNode) {
    return { lod: "route-bundle", depth, outcome, protected: protectedNode };
  }

  const tier = zoomTier(context.zoomPct);

  // Protected nodes are always Landmarks (route-topology §6.2, §2.1).
  if (protectedNode) {
    return { lod: "landmark", depth, outcome, protected: true };
  }

  // Overview zoom (<35%): only root/selected/active-blocking/top Insight or
  // Decision cards; everything else collapses to trail dots / bundles. The
  // ≤12 landmark cap lives in the budget layer.
  if (tier === "overview") {
    if (
      landmarkByKind(input) ||
      isTransitionRoot(context, input.id) ||
      isNarrativeSpine(input, context)
    ) {
      return { lod: "landmark", depth, outcome, protected: false };
    }
    return { lod: "trail-dot", depth, outcome, protected: false };
  }

  // Route zoom (35–65%): compact landmark cards for narrative/Insight; the
  // rest become waypoints or dots.
  if (tier === "route") {
    if (
      landmarkByKind(input) ||
      isNarrativeSpine(input, context) ||
      input.importance >= 0.6
    ) {
      return { lod: "landmark", depth, outcome, protected: false };
    }
    if (waypointByKind(input.kind) || input.importance >= 0.3) {
      return { lod: "waypoint", depth, outcome, protected: false };
    }
    return { lod: "trail-dot", depth, outcome, protected: false };
  }

  // Work (66–119%) / Inspect (≥120%): standard cards; medium nodes waypoints.
  if (landmarkByKind(input) || input.importance >= 0.5) {
    return { lod: "landmark", depth, outcome, protected: false };
  }
  if (waypointByKind(input.kind) || input.importance >= 0.25) {
    return { lod: "waypoint", depth, outcome, protected: false };
  }
  return { lod: "trail-dot", depth, outcome, protected: false };
}
