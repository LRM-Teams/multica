import type { ResearchSemanticNode, SemanticAggregationProjectionInput } from "./semantic-aggregation";

export type SemanticAggregationWindowColumnKind = "ancestors" | "current" | "children";
export type SemanticAggregationWindowStats = {
  childCount: number;
  descendantCount: number;
  taskStatus: { active: number; inactive: number };
  aggregateStatus: { forming: number; stable: number };
};
export type SemanticAggregationWindowNode = {
  id: string;
  kind: ResearchSemanticNode["kind"];
  parentId: string | null;
  stats: SemanticAggregationWindowStats;
};
export type SemanticAggregationWindowRemainder = {
  id: string;
  kind: "remainder";
  /** Actual backend node IDs hidden by this same-column window. */
  sourceNodeIds: readonly string[];
  hiddenCount: number;
  nextOffset: number;
  stats: SemanticAggregationWindowStats;
};
export type SemanticAggregationWindowItem = SemanticAggregationWindowNode | SemanticAggregationWindowRemainder;
export type SemanticAggregationWindowColumn = {
  kind: SemanticAggregationWindowColumnKind;
  ownerId: string | null;
  items: readonly SemanticAggregationWindowItem[];
};
export type SemanticAggregationWindowDiagnostic = {
  code: "duplicate_id" | "missing_member" | "multiple_parents" | "cycle" | "unknown_focus";
  nodeId: string;
};
export type SemanticAggregationVisibleWindow = {
  columns: readonly [SemanticAggregationWindowColumn, SemanticAggregationWindowColumn, SemanticAggregationWindowColumn];
  focusId: string | null;
  visibleNodeCount: number;
  indexedNodeCount: number;
  diagnostics: readonly SemanticAggregationWindowDiagnostic[];
  /** Deterministic counters used by scale tests instead of timing alone. */
  work: { nodeVisits: number; edgeVisits: number };
};
export type SemanticAggregationVisibleWindowOptions = {
  perColumnBudget?: number;
  totalNodeBudget?: number;
  columnOffsets?: Partial<Record<SemanticAggregationWindowColumnKind, number>>;
};

const DEFAULT_COLUMN_BUDGET = 8;
const DEFAULT_TOTAL_BUDGET = DEFAULT_COLUMN_BUDGET * 3;

function emptyStats(): SemanticAggregationWindowStats {
  return {
    childCount: 0,
    descendantCount: 0,
    taskStatus: { active: 0, inactive: 0 },
    aggregateStatus: { forming: 0, stable: 0 },
  };
}

function addStats(target: SemanticAggregationWindowStats, source: SemanticAggregationWindowStats): void {
  target.childCount += source.childCount;
  target.descendantCount += source.descendantCount;
  target.taskStatus.active += source.taskStatus.active;
  target.taskStatus.inactive += source.taskStatus.inactive;
  target.aggregateStatus.forming += source.aggregateStatus.forming;
  target.aggregateStatus.stable += source.aggregateStatus.stable;
}

function finiteBudget(value: number | undefined, fallback: number): number {
  if (value === undefined || !Number.isFinite(value)) return fallback;
  return Math.max(0, Math.floor(value));
}

export function buildSemanticAggregationVisibleWindow(
  input: SemanticAggregationProjectionInput,
  focusPath: readonly string[] = [],
  options: SemanticAggregationVisibleWindowOptions = {},
): SemanticAggregationVisibleWindow {
  const diagnostics: SemanticAggregationWindowDiagnostic[] = [];
  const byId = new Map<string, ResearchSemanticNode>();
  let nodeVisits = 0;
  let edgeVisits = 0;

  for (const node of [...input.aggregates, ...input.tasks]) {
    nodeVisits += 1;
    if (byId.has(node.id)) {
      diagnostics.push({ code: "duplicate_id", nodeId: node.id });
      continue;
    }
    byId.set(node.id, node);
  }

  // Build the parent lookup once. Rendering consumers never need edges.filter.
  const parentById = new Map<string, string>();
  for (const aggregate of input.aggregates) {
    if (byId.get(aggregate.id) !== aggregate) continue;
    for (const memberId of aggregate.memberIds) {
      edgeVisits += 1;
      if (!byId.has(memberId)) {
        diagnostics.push({ code: "missing_member", nodeId: memberId });
        continue;
      }
      const parentId = parentById.get(memberId);
      if (parentId && parentId !== aggregate.id) {
        diagnostics.push({ code: "multiple_parents", nodeId: memberId });
        continue;
      }
      if (!parentId) parentById.set(memberId, aggregate.id);
    }
  }

  // Cut one parent edge per cycle so all later walks are finite and deterministic.
  const completed = new Set<string>();
  for (const startId of byId.keys()) {
    if (completed.has(startId)) continue;
    const path: string[] = [];
    const pathIndex = new Map<string, number>();
    let currentId: string | undefined = startId;
    while (currentId && !completed.has(currentId)) {
      nodeVisits += 1;
      if (pathIndex.has(currentId)) {
        diagnostics.push({ code: "cycle", nodeId: currentId });
        parentById.delete(currentId);
        break;
      }
      pathIndex.set(currentId, path.length);
      path.push(currentId);
      currentId = parentById.get(currentId);
    }
    for (const id of path) completed.add(id);
  }

  const childrenById = new Map<string, string[]>();
  for (const aggregate of input.aggregates) {
    if (byId.get(aggregate.id) !== aggregate) continue;
    const children: string[] = [];
    const seen = new Set<string>();
    for (const memberId of aggregate.memberIds) {
      edgeVisits += 1;
      if (seen.has(memberId) || parentById.get(memberId) !== aggregate.id) continue;
      seen.add(memberId);
      children.push(memberId);
    }
    childrenById.set(aggregate.id, children);
  }

  const roots = [...byId.keys()].filter((id) => !parentById.has(id));
  const statsById = new Map<string, SemanticAggregationWindowStats>();
  const stack = roots.map((id) => ({ id, visited: false })).reverse();
  while (stack.length > 0) {
    const current = stack.pop()!;
    nodeVisits += 1;
    if (!current.visited) {
      stack.push({ id: current.id, visited: true });
      const children = childrenById.get(current.id) ?? [];
      for (let index = children.length - 1; index >= 0; index -= 1) {
        edgeVisits += 1;
        stack.push({ id: children[index]!, visited: false });
      }
      continue;
    }
    const node = byId.get(current.id)!;
    const children = childrenById.get(current.id) ?? [];
    const stats = emptyStats();
    stats.childCount = children.length;
    if (node.kind === "task") stats.taskStatus[node.active ? "active" : "inactive"] += 1;
    else stats.aggregateStatus[node.stability] += 1;
    for (const childId of children) {
      edgeVisits += 1;
      const childStats = statsById.get(childId);
      if (!childStats) continue;
      stats.descendantCount += 1 + childStats.descendantCount;
      stats.taskStatus.active += childStats.taskStatus.active;
      stats.taskStatus.inactive += childStats.taskStatus.inactive;
      stats.aggregateStatus.forming += childStats.aggregateStatus.forming;
      stats.aggregateStatus.stable += childStats.aggregateStatus.stable;
    }
    statsById.set(current.id, stats);
  }

  let focusId: string | null = null;
  for (const id of focusPath) {
    if (!byId.has(id)) diagnostics.push({ code: "unknown_focus", nodeId: id });
    else focusId = id;
  }
  const ancestorIds: string[] = [];
  if (focusId) {
    let parentId = parentById.get(focusId);
    while (parentId) {
      nodeVisits += 1;
      ancestorIds.push(parentId);
      parentId = parentById.get(parentId);
    }
    ancestorIds.reverse();
  }
  const currentIds = focusId ? (childrenById.get(parentById.get(focusId) ?? "") ?? roots) : roots;
  const childIds = focusId ? (childrenById.get(focusId) ?? []) : [];
  const perColumnBudget = finiteBudget(options.perColumnBudget, DEFAULT_COLUMN_BUDGET);
  let remainingBudget = finiteBudget(options.totalNodeBudget, DEFAULT_TOTAL_BUDGET);

  const makeColumn = (
    kind: SemanticAggregationWindowColumnKind,
    ownerId: string | null,
    sourceIds: readonly string[],
    pinnedId?: string | null,
  ): SemanticAggregationWindowColumn => {
    const limit = Math.min(perColumnBudget, remainingBudget);
    if (limit === 0 || sourceIds.length === 0) return { kind, ownerId, items: [] };
    const offset = Math.min(finiteBudget(options.columnOffsets?.[kind], 0), Math.max(0, sourceIds.length - 1));
    let visibleIds = sourceIds.slice(offset, offset + limit);
    const hasOverflow = offset > 0 || offset + visibleIds.length < sourceIds.length;
    if (hasOverflow) {
      const realNodeLimit = Math.max(0, limit - 1);
      visibleIds = sourceIds.slice(offset, offset + realNodeLimit);
      if (pinnedId && sourceIds.includes(pinnedId) && !visibleIds.includes(pinnedId) && realNodeLimit > 0) {
        visibleIds = [...visibleIds.slice(0, realNodeLimit - 1), pinnedId];
      }
    }
    const visibleSet = new Set(visibleIds);
    const hiddenIds = sourceIds.filter((id) => !visibleSet.has(id));
    const items: SemanticAggregationWindowItem[] = visibleIds.map((id) => ({
      id,
      kind: byId.get(id)!.kind,
      parentId: parentById.get(id) ?? null,
      stats: statsById.get(id) ?? emptyStats(),
    }));
    if (hiddenIds.length > 0) {
      const stats = emptyStats();
      for (const id of hiddenIds) addStats(stats, statsById.get(id) ?? emptyStats());
      items.push({
        id: `visible-window:${kind}:${ownerId ?? "root"}:overflow:${offset + visibleIds.length}`,
        kind: "remainder",
        sourceNodeIds: hiddenIds,
        hiddenCount: hiddenIds.length,
        nextOffset: Math.min(sourceIds.length - 1, offset + Math.max(1, visibleIds.length)),
        stats,
      });
    }
    remainingBudget -= items.length;
    return { kind, ownerId, items };
  };

  const columns: SemanticAggregationVisibleWindow["columns"] = [
    makeColumn("ancestors", focusId, ancestorIds),
    makeColumn("current", parentById.get(focusId ?? "") ?? null, currentIds, focusId),
    makeColumn("children", focusId, childIds),
  ];
  const visibleNodeCount = columns.reduce((sum, column) => sum + column.items.length, 0);
  return { columns, focusId, visibleNodeCount, indexedNodeCount: byId.size, diagnostics, work: { nodeVisits, edgeVisits } };
}
