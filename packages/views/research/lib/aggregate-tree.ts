import type { ResearchGraphNode } from "@multica/core/types";

export type AggregateTreeAssessment = "trusted" | "pending_review" | "detour";

export type AggregateTreeEntry = {
  id: string;
  node: ResearchGraphNode;
  parentId: string | null;
  childIds: readonly string[];
  childCount: number;
  descendantCount: number;
  themeKey: string;
  assessment: AggregateTreeAssessment;
  confidence?: number;
  reason?: string;
  evidenceSummary?: string;
};

export type AggregateTreeContractField =
  | "parent_id"
  | "child_ids"
  | "child_count"
  | "descendant_count"
  | "theme_key";

export type AggregateTreeContractGap = {
  nodeId: string;
  field: AggregateTreeContractField;
};

export type AggregateTreeSelection =
  | {
      status: "ready";
      byId: ReadonlyMap<string, AggregateTreeEntry>;
      roots: readonly AggregateTreeEntry[];
    }
  | {
      status: "incomplete";
      gaps: readonly AggregateTreeContractGap[];
    };

export type AggregateTreeColumnSelection = {
  rootId?: string;
  branchId?: string;
};

export type AggregateTreeColumns = {
  root: AggregateTreeEntry;
  branches: readonly AggregateTreeEntry[];
  branch: AggregateTreeEntry | null;
  leaves: readonly AggregateTreeEntry[];
};

function hasOwn(value: object, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(value, key);
}

function isParentId(value: unknown): value is string | null {
  return value === null || (typeof value === "string" && value.length > 0);
}

function isChildIds(value: unknown): value is string[] {
  return Array.isArray(value) && value.every((id) => typeof id === "string" && id.length > 0);
}

function isCount(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value) && value >= 0;
}

function isThemeKey(value: unknown): value is string {
  return typeof value === "string" && value.length > 0;
}

function normalizeAssessment(value: ResearchGraphNode["assessment"]): AggregateTreeAssessment {
  switch (value) {
    case "trusted":
    case "detour":
    case "pending_review":
      return value;
    default:
      return "pending_review";
  }
}

function contractGapsFor(node: ResearchGraphNode): AggregateTreeContractGap[] {
  const gaps: AggregateTreeContractGap[] = [];
  if (!hasOwn(node, "parent_id") || !isParentId(node.parent_id)) {
    gaps.push({ nodeId: node.id, field: "parent_id" });
  }
  if (!hasOwn(node, "child_ids") || !isChildIds(node.child_ids)) {
    gaps.push({ nodeId: node.id, field: "child_ids" });
  }
  if (!hasOwn(node, "child_count") || !isCount(node.child_count)) {
    gaps.push({ nodeId: node.id, field: "child_count" });
  }
  if (!hasOwn(node, "descendant_count") || !isCount(node.descendant_count)) {
    gaps.push({ nodeId: node.id, field: "descendant_count" });
  }
  if (!hasOwn(node, "theme_key") || !isThemeKey(node.theme_key)) {
    gaps.push({ nodeId: node.id, field: "theme_key" });
  }
  return gaps;
}

function entryFor(node: ResearchGraphNode): AggregateTreeEntry {
  return {
    id: node.id,
    node,
    parentId: node.parent_id!,
    childIds: [...node.child_ids!],
    childCount: node.child_count!,
    descendantCount: node.descendant_count!,
    themeKey: node.theme_key!,
    assessment: normalizeAssessment(node.assessment),
    ...(typeof node.confidence === "number" ? { confidence: node.confidence } : {}),
    ...(typeof node.reason === "string" && node.reason.length > 0 ? { reason: node.reason } : {}),
    ...(typeof node.evidence_summary === "string" && node.evidence_summary.length > 0
      ? { evidenceSummary: node.evidence_summary }
      : {}),
  };
}

/**
 * Adapts only the server-projected LRM-1278 tree fields. If a structural field
 * is absent or invalid, the caller receives the gaps instead of a fabricated tree.
 */
export function selectAggregateTree(nodes: readonly ResearchGraphNode[]): AggregateTreeSelection {
  const gaps = nodes.flatMap(contractGapsFor);
  if (gaps.length > 0) return { status: "incomplete", gaps };

  const byId = new Map<string, AggregateTreeEntry>();
  const roots: AggregateTreeEntry[] = [];
  for (const node of nodes) {
    const entry = entryFor(node);
    byId.set(entry.id, entry);
    if (entry.parentId === null) roots.push(entry);
  }

  return { status: "ready", byId, roots };
}

function listedChildren(
  selection: Extract<AggregateTreeSelection, { status: "ready" }>,
  parent: AggregateTreeEntry,
): AggregateTreeEntry[] {
  return parent.childIds.flatMap((childId) => {
    const child = selection.byId.get(childId);
    return child?.parentId === parent.id ? [child] : [];
  });
}

/**
 * Selects the UI's root → sibling branch → direct-child columns without
 * manufacturing relationships from parent_id or recalculating server counts.
 */
export function selectAggregateTreeColumns(
  selection: AggregateTreeSelection,
  requested: AggregateTreeColumnSelection = {},
): AggregateTreeColumns | null {
  if (selection.status !== "ready") return null;

  const root =
    selection.roots.find((entry) => entry.id === requested.rootId) ?? selection.roots[0];
  if (!root) return null;

  const branches = listedChildren(selection, root);
  const branch =
    branches.find((entry) => entry.id === requested.branchId) ?? branches[0] ?? null;
  const leaves = branch ? listedChildren(selection, branch) : [];

  return { root, branches, branch, leaves };
}
