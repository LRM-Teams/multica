import {
  REORG_SINGLE_ELEMENT_MAX_MS,
  REORG_TOTAL_BUDGET_MS,
} from "./canvas-reorg-motion";

export const SEMANTIC_MOTION_NODE_DURATION_MS = REORG_SINGLE_ELEMENT_MAX_MS;
export const SEMANTIC_MOTION_TOTAL_BUDGET_MS = REORG_TOTAL_BUDGET_MS;
export const SEMANTIC_MOTION_START_MS = 80;
export const SEMANTIC_MOTION_STAGGER_MS = 36;
export const SEMANTIC_MOTION_STAGGER_CAP = 6;

export type SemanticMotionNodeSnapshot = {
  id: string;
  parentId: string | null;
  depth: number;
};

export type SemanticMotionOperation = {
  phase: "split" | "merge" | "regroup";
  nodeId: string;
  /** Split origin, merge destination, or regroup destination. */
  anchorId: string | null;
  /** Present on regroup, including null when the node came from a root. */
  fromAnchorId?: string | null;
  delayMs: number;
  durationMs: number;
};

export type SemanticAggregationMotionPlan = {
  kind: "stable" | "split" | "merge" | "regroup" | "mixed" | "replace";
  stableIds: string[];
  operations: SemanticMotionOperation[];
  /** Added roots with no visible aggregate anchor use the ordinary enter path. */
  enterIds: string[];
  /** Removed roots with no visible aggregate anchor use the ordinary exit path. */
  exitIds: string[];
  totalDurationMs: number;
  interrupted: boolean;
};

function operationDelay(index: number): number {
  return (
    SEMANTIC_MOTION_START_MS +
    Math.min(index, SEMANTIC_MOTION_STAGGER_CAP) * SEMANTIC_MOTION_STAGGER_MS
  );
}

function operationsFor(
  phase: "split" | "merge",
  nodes: SemanticMotionNodeSnapshot[],
  knownIds: ReadonlySet<string>,
): { anchored: SemanticMotionOperation[]; unanchored: string[] } {
  const ordered = nodes
    .map((node, inputIndex) => ({ node, inputIndex }))
    .toSorted((left, right) => {
      const depthDelta =
        phase === "split"
          ? left.node.depth - right.node.depth
          : right.node.depth - left.node.depth;
      return depthDelta || left.inputIndex - right.inputIndex;
    });
  const anchored: SemanticMotionOperation[] = [];
  const unanchored: string[] = [];
  for (const { node } of ordered) {
    if (!node.parentId || !knownIds.has(node.parentId)) {
      unanchored.push(node.id);
      continue;
    }
    anchored.push({
      phase,
      nodeId: node.id,
      anchorId: node.parentId,
      delayMs: operationDelay(anchored.length),
      durationMs: SEMANTIC_MOTION_NODE_DURATION_MS,
    });
  }
  return { anchored, unanchored };
}

export function planSemanticAggregationMotion(
  previous: readonly SemanticMotionNodeSnapshot[],
  next: readonly SemanticMotionNodeSnapshot[],
  options: { reducedMotion?: boolean } = {},
): SemanticAggregationMotionPlan {
  const unique = (nodes: readonly SemanticMotionNodeSnapshot[]) => {
    const seen = new Set<string>();
    return nodes.filter((node) => {
      if (seen.has(node.id)) return false;
      seen.add(node.id);
      return true;
    });
  };
  const previousNodes = unique(previous);
  const nextNodes = unique(next);
  const previousById = new Map(previousNodes.map((node) => [node.id, node]));
  const nextById = new Map(nextNodes.map((node) => [node.id, node]));
  const previousIds = new Set(previousById.keys());
  const nextIds = new Set(nextById.keys());
  const knownIds = new Set([...previousIds, ...nextIds]);
  const regrouped = nextNodes.filter((node) => {
    const prior = previousById.get(node.id);
    return !!prior && prior.parentId !== node.parentId;
  });
  const regroupedIds = new Set(regrouped.map((node) => node.id));
  const stableIds = nextNodes
    .filter((node) => previousIds.has(node.id) && !regroupedIds.has(node.id))
    .map((node) => node.id);
  const added = nextNodes.filter((node) => !previousIds.has(node.id));
  const removed = previousNodes.filter((node) => !nextIds.has(node.id));

  const split = operationsFor("split", added, knownIds);
  const merge = operationsFor("merge", removed, knownIds);
  const regroupOperations: SemanticMotionOperation[] = regrouped.map((node, index) => {
    const prior = previousById.get(node.id)!;
    return {
      phase: "regroup",
      nodeId: node.id,
      anchorId: node.parentId,
      fromAnchorId: prior.parentId,
      delayMs: operationDelay(index),
      durationMs: SEMANTIC_MOTION_NODE_DURATION_MS,
    };
  });
  let operations = [...merge.anchored, ...regroupOperations, ...split.anchored];
  if (options.reducedMotion) {
    operations = operations.map((operation) => ({
      ...operation,
      delayMs: 0,
      durationMs: 0,
    }));
  }

  const phases = new Set(operations.map((operation) => operation.phase));
  const hasReplacement = split.unanchored.length > 0 || merge.unanchored.length > 0;
  const kind = phases.size + (hasReplacement ? 1 : 0) > 1
    ? "mixed"
    : phases.has("split")
      ? "split"
      : phases.has("merge")
        ? "merge"
        : phases.has("regroup")
          ? "regroup"
          : hasReplacement
            ? "replace"
            : "stable";
  let totalDurationMs = 0;
  for (const operation of operations) {
    totalDurationMs = Math.max(
      totalDurationMs,
      operation.delayMs + operation.durationMs,
    );
  }
  totalDurationMs = Math.min(SEMANTIC_MOTION_TOTAL_BUDGET_MS, totalDurationMs);

  return {
    kind,
    stableIds,
    operations,
    enterIds: split.unanchored,
    exitIds: merge.unanchored,
    totalDurationMs,
    interrupted: false,
  };
}

export function interruptSemanticAggregationMotion(
  plan: SemanticAggregationMotionPlan,
): SemanticAggregationMotionPlan {
  return {
    ...plan,
    operations: plan.operations.map((operation) => ({
      ...operation,
      delayMs: 0,
      durationMs: 0,
    })),
    totalDurationMs: 0,
    interrupted: true,
  };
}
