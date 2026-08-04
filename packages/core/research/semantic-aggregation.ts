export type ResearchSemanticAggregationStability = "forming" | "stable";

export type ResearchSemanticTask = {
  id: string;
  kind: "task";
  /** Active work must remain visible through every aggregate ancestor. */
  active: boolean;
};

export type ResearchSemanticAggregate = {
  id: string;
  kind: "aggregate";
  /** Tasks are level 0; each aggregate is exactly one level above its members. */
  level: number;
  memberIds: readonly string[];
  stability: ResearchSemanticAggregationStability;
};

export type ResearchSemanticNode = ResearchSemanticTask | ResearchSemanticAggregate;

export type SemanticAggregationIssue =
  | {
      code: "duplicate_id";
      nodeId: string;
    }
  | {
      code: "missing_member";
      aggregateId: string;
      memberId: string;
    }
  | {
      code: "duplicate_member";
      aggregateId: string;
      memberId: string;
    }
  | {
      code: "multiple_parents";
      memberId: string;
      parentIds: [string, string];
    }
  | {
      code: "invalid_level";
      aggregateId: string;
      memberId: string;
      expectedLevel: number;
      actualLevel: number;
    }
  | {
      code: "cycle";
      nodeIds: string[];
    };

export type VisibleSemanticAggregationNode = {
  id: string;
  kind: ResearchSemanticNode["kind"];
  depth: number;
  parentId: string | null;
  collapsed: boolean;
  active: boolean;
};

export type SemanticAggregationProjection =
  | {
      status: "invalid";
      issues: SemanticAggregationIssue[];
    }
  | {
      status: "ready";
      roots: string[];
      visibleNodes: VisibleSemanticAggregationNode[];
      /** Expansion caused by active work, forming aggregates, or selection. */
      autoExpandedIds: string[];
      /** Effective expansion after combining automatic and explicit state. */
      expandedIds: string[];
    };

export type SemanticAggregationProjectionInput = {
  tasks: readonly ResearchSemanticTask[];
  aggregates: readonly ResearchSemanticAggregate[];
};

export type SemanticAggregationViewState = {
  expandedIds?: ReadonlySet<string>;
  selectedId?: string | null;
};

function validateAggregation(
  input: SemanticAggregationProjectionInput,
): {
  byId: Map<string, ResearchSemanticNode>;
  parentById: Map<string, string>;
  issues: SemanticAggregationIssue[];
} {
  const byId = new Map<string, ResearchSemanticNode>();
  const issues: SemanticAggregationIssue[] = [];

  for (const node of [...input.tasks, ...input.aggregates]) {
    if (byId.has(node.id)) {
      issues.push({ code: "duplicate_id", nodeId: node.id });
      continue;
    }
    byId.set(node.id, node);
  }

  const parentById = new Map<string, string>();
  for (const aggregate of input.aggregates) {
    const seenMemberIds = new Set<string>();
    for (const memberId of aggregate.memberIds) {
      if (seenMemberIds.has(memberId)) {
        issues.push({ code: "duplicate_member", aggregateId: aggregate.id, memberId });
        continue;
      }
      seenMemberIds.add(memberId);

      const member = byId.get(memberId);
      if (!member) {
        issues.push({ code: "missing_member", aggregateId: aggregate.id, memberId });
        continue;
      }

      const previousParent = parentById.get(memberId);
      if (previousParent && previousParent !== aggregate.id) {
        issues.push({
          code: "multiple_parents",
          memberId,
          parentIds: [previousParent, aggregate.id],
        });
        continue;
      }
      parentById.set(memberId, aggregate.id);

      const memberLevel = member.kind === "task" ? 0 : member.level;
      const expectedLevel = memberLevel + 1;
      if (aggregate.level !== expectedLevel) {
        issues.push({
          code: "invalid_level",
          aggregateId: aggregate.id,
          memberId,
          expectedLevel,
          actualLevel: aggregate.level,
        });
      }
    }
  }

  // Every member has at most one parent, so parent-chain walking detects all
  // cycles in linear time without growing the JavaScript call stack.
  const completed = new Set<string>();
  for (const aggregate of input.aggregates) {
    if (completed.has(aggregate.id)) continue;
    const path: string[] = [];
    const pathIndex = new Map<string, number>();
    let current: string | undefined = aggregate.id;
    while (current && byId.get(current)?.kind === "aggregate" && !completed.has(current)) {
      const cycleStart = pathIndex.get(current);
      if (cycleStart !== undefined) {
        issues.push({ code: "cycle", nodeIds: [...path.slice(cycleStart), current] });
        break;
      }
      pathIndex.set(current, path.length);
      path.push(current);
      current = parentById.get(current);
    }
    for (const id of path) completed.add(id);
  }

  return { byId, parentById, issues };
}

function collectAutoExpanded(
  input: SemanticAggregationProjectionInput,
  byId: ReadonlyMap<string, ResearchSemanticNode>,
  parentById: ReadonlyMap<string, string>,
  selectedId: string | null | undefined,
): Set<string> {
  const autoExpanded = new Set<string>();
  const queue: string[] = [];
  const queued = new Set<string>();
  const enqueue = (id: string): void => {
    if (queued.has(id)) return;
    queued.add(id);
    queue.push(id);
  };

  for (const task of input.tasks) {
    if (task.active) enqueue(task.id);
  }
  for (const aggregate of input.aggregates) {
    if (aggregate.stability !== "forming") continue;
    autoExpanded.add(aggregate.id);
    enqueue(aggregate.id);
  }
  if (selectedId && byId.has(selectedId)) enqueue(selectedId);

  for (let index = 0; index < queue.length; index += 1) {
    const parent = parentById.get(queue[index]!);
    if (!parent) continue;
    autoExpanded.add(parent);
    enqueue(parent);
  }
  return autoExpanded;
}

export function buildSemanticAggregationProjection(
  input: SemanticAggregationProjectionInput,
  view: SemanticAggregationViewState = {},
): SemanticAggregationProjection {
  const { byId, parentById, issues } = validateAggregation(input);
  if (issues.length > 0) return { status: "invalid", issues };

  const roots = [
    ...input.aggregates.filter((node) => !parentById.has(node.id)).map((node) => node.id),
    ...input.tasks.filter((node) => !parentById.has(node.id)).map((node) => node.id),
  ];

  const autoExpanded = collectAutoExpanded(
    input,
    byId,
    parentById,
    view.selectedId,
  );

  const expanded = new Set(autoExpanded);
  for (const id of view.expandedIds ?? []) {
    if (byId.get(id)?.kind === "aggregate") expanded.add(id);
  }

  const visibleNodes: VisibleSemanticAggregationNode[] = [];
  const stack: { id: string; depth: number; parentId: string | null }[] = [];
  for (let index = roots.length - 1; index >= 0; index -= 1) {
    stack.push({ id: roots[index]!, depth: 0, parentId: null });
  }

  while (stack.length > 0) {
    const current = stack.pop()!;
    const node = byId.get(current.id);
    if (!node) continue;
    const isExpanded = node.kind === "aggregate" && expanded.has(node.id);
    visibleNodes.push({
      id: node.id,
      kind: node.kind,
      depth: current.depth,
      parentId: current.parentId,
      collapsed: node.kind === "aggregate" && !isExpanded,
      active: node.kind === "task" && node.active,
    });
    if (node.kind !== "aggregate" || !isExpanded) continue;
    for (let index = node.memberIds.length - 1; index >= 0; index -= 1) {
      stack.push({
        id: node.memberIds[index]!,
        depth: current.depth + 1,
        parentId: node.id,
      });
    }
  }

  const visibleAggregateIds = visibleNodes
    .filter((node) => node.kind === "aggregate")
    .map((node) => node.id);

  return {
    status: "ready",
    roots,
    visibleNodes,
    autoExpandedIds: visibleAggregateIds.filter((id) => autoExpanded.has(id)),
    expandedIds: visibleAggregateIds.filter((id) => expanded.has(id)),
  };
}
