/**
 * LRM-1470 — Insight 组合树的摘要/展开布局与上下文保持。
 *
 * 纯函数、无 DOM，覆盖 AC1/AC2：
 *  - AC1：至少 3 层 Insight DAG 可在摘要和展开态间切换，折叠后画布节点数显著下降。
 *  - AC2：展开和合并保持选择、相机与上下文，动画结束后无重叠/跳位。
 *
 * 订阅后端 Projection Slice / fresh 状态；本模块只决定「显示哪些节点、
 * 如何归组、哪些子树折叠成显示分组」，不写回 canonical Graph。
 */

import type { InsightDerivationNode } from "./insight-derivation-contract";

/** 画布上可见节点的两种身份：真实投影节点 vs 纯前端显示分组。 */
export type InsightViewNode =
  | { kind: "node"; node: InsightDerivationNode }
  | { kind: "display_group"; groupId: string; memberIds: readonly string[]; label: string };

export type InsightTreeNode = {
  id: string;
  level: number;
  /** 直接输入 id 的投影。 */
  inputIds: readonly string[];
  freshness: "fresh" | "stale";
};

/** 用户/前端持有的视图上下文，展开/合并期间原样保留。 */
export type InsightViewContext = {
  /** 画布视口中心（world 坐标），动画前后不变。 */
  viewportCenter: { x: number; y: number };
  zoom: number;
  /** 当前选中节点 id（可为空）。 */
  selectedId: string | null;
  /** 为保持「上下文」而始终展开的节点集合（选中节点路径 + stale 受影响路径）。 */
  pinnedIds: ReadonlySet<string>;
};

/** 摘要模式下被折叠成单个显示分组的子树信息。 */
export type CollapsedGroup = {
  groupId: string;
  memberIds: readonly string[];
  /** 分组内节点总数（用于「显示分组 n 个节点」的诚实标注）。 */
  memberCount: number;
  /** 是否位于 stale 受影响路径上（决定分组是否带失效标记）。 */
  onStalePath: boolean;
};

export type SummaryPlan = {
  viewNodes: readonly InsightViewNode[];
  /** 被折叠的子树 → 分组。 */
  collapsed: readonly CollapsedGroup[];
  /** 摘要态可见节点数。 */
  visibleNodeCount: number;
  /** 折叠节省的节点数（= 原见节点数 - 摘要可见数）。 */
  collapsedCount: number;
};

export const MAX_GENERIC_DEPTH = 3;

/** 归一化成布局可用的最小节点视图。 */
function toTreeNode(n: InsightDerivationNode): InsightTreeNode {
  return { id: n.id, level: n.level, inputIds: n.inputIds, freshness: n.freshness };
}

/**
 * 摘要模式的锚点：对外没有消费者的「顶层层级 Insight」（DAG 汇点）。
 * 摘要只展示这些顶层结论卡；必要时沿选中/stale 路径向下钻取保持一致上下文。
 */
function sinkNodes(nodes: readonly InsightTreeNode[]): InsightTreeNode[] {
  const consumed = new Set<string>();
  for (const n of nodes) for (const p of n.inputIds) consumed.add(p);
  return nodes.filter((n) => !consumed.has(n.id));
}

/**
 * 摘要模式：只展开「顶层 + 选中/stale 上下文」路径，其余子树折叠成
 * 显示分组。折叠后画布节点数应显著低于展开态（AC1）。
 *
 * @param nodes   后端投影的全部 Insight 节点。
 * @param pinned  为保留上下文而必须展开的节点 id（选中节点祖先 + stale 路径）。
 * @returns 摘要可见节点 + 折叠分组 + 节点统计。
 */
export function planSummary(
  nodes: readonly InsightDerivationNode[],
  pinned: ReadonlySet<string> = new Set(),
): SummaryPlan {
  const tree = nodes.map(toTreeNode);
  const byId = new Map(tree.map((n) => [n.id, n]));
  const nodeById = new Map(nodes.map((n) => [n.id, n]));

  // 1) 摘要锚点 = DAG 汇点（顶层层级 Insight），摘要默认只展示这些结论。
  const anchors = sinkNodes(tree);
  const visible = new Set(anchors.map((n) => n.id));

  // 2) 沿选中/stale 上下文路径向下钻取：pinned 节点及其全部输入祖先必须可见。
  //    这样 stale 输入到祖先的传播路径始终可辨认，选中节点不因折叠丢失。
  const drill = (id: string) => {
    if (visible.has(id)) return;
    visible.add(id);
    const n = byId.get(id);
    if (!n) return;
    for (const p of n.inputIds) drill(p);
  };
  for (const pid of pinned) drill(pid);

  // 3) 其余隐藏节点按「最接近的可见祖先」聚类成显示分组。
  const collapsedGroups = new Map<string, string[]>(); // parentId -> hidden ids
  for (const n of tree) {
    if (visible.has(n.id)) continue;
    let cursor = n;
    let anchor: string | null = null;
    while (cursor.inputIds.length > 0) {
      const pid = cursor.inputIds[0];
      const parent = pid ? byId.get(pid) : undefined;
      if (!parent) break;
      if (visible.has(parent.id)) {
        anchor = parent.id;
        break;
      }
      cursor = parent;
    }
    const key = anchor ?? "orphan";
    const arr = collapsedGroups.get(key) ?? [];
    arr.push(n.id);
    collapsedGroups.set(key, arr);
  }

  const onStalePath = new Set<string>();
  for (const n of tree) if (n.freshness === "stale") onStalePath.add(n.id);

  const collapsed: CollapsedGroup[] = [];
  for (const [anchor, ids] of collapsedGroups) {
    collapsed.push({
      groupId: `group:${anchor}`,
      memberIds: ids,
      memberCount: ids.length,
      onStalePath: ids.some((id) => onStalePath.has(id)),
    });
  }

  const viewNodes: InsightViewNode[] = [];
  for (const id of visible) {
    const n = nodeById.get(id);
    if (n) viewNodes.push({ kind: "node", node: n });
  }

  const visibleNodeCount = viewNodes.length;
  const collapsedCount = tree.length - visibleNodeCount;

  return { viewNodes, collapsed, visibleNodeCount, collapsedCount };
}

/**
 * 展开态：全部节点可见，不再有任何显示分组。
 */
export function planExpanded(nodes: readonly InsightDerivationNode[]): InsightViewNode[] {
  return nodes.map((n) => ({ kind: "node" as const, node: n }));
}

/**
 * 在摘要↔展开切换时保持选择、相机与上下文（AC2）。
 * 展开/合并只改变可见节点集合与布局；相机中心、zoom、选中节点原样传回。
 */
export function preserveViewContext<TContext extends InsightViewContext>(
  previous: TContext,
  nextSelection?: { viewportCenter?: { x: number; y: number }; zoom?: number; selectedId?: string | null },
): TContext {
  return {
    ...previous,
    ...(nextSelection?.viewportCenter ? { viewportCenter: nextSelection.viewportCenter } : {}),
    ...(nextSelection?.zoom !== undefined ? { zoom: nextSelection.zoom } : {}),
    ...(nextSelection?.selectedId !== undefined ? { selectedId: nextSelection.selectedId } : {}),
  };
}
