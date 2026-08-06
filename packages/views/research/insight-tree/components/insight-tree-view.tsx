"use client";

/**
 * LRM-1476 — Insight 组合树视图（InsightTreeView）。
 *
 * 在 insight-tree 纯函数（planSummary / planExpanded / computeStalePaths /
 * preserveViewContext）之上实现可运行、可交互的组件：
 *
 *  - **摘要 / 展开** 两种模式（AC1）：摘要把顶层结论之外的子树折叠成
 *    DisplayGroupCard，显著降低 DOM 节点数；展开逐层铺开整棵 ≥3 层 DAG。
 *  - **逐层钻取 / 折叠**（AC1）：每个 Insight 卡可展开到直接输入、再逐层向下；
 *    折叠某棵子树时其 DOM 整体卸载（节点数显著下降）。
 *  - **上下文保持**（AC2）：摘要↔展开、展开/折叠只改可见集合；`selectedId`、
 *    `viewportCenter`、`zoom`、`pinnedIds` 原样保留，层级面包屑随选择更新。
 *  - **stale 路径 + 最小重新整合入口**（AC3）：direct/inherited 受影响路径沿
 *    卡描红 + 失效徽标，重新整合入口只出现在受影响分量上。
 *
 * 边界：本组件**不写回 canonical Graph**，不把显示分组冒充为 Insight；可见
 * 集合、层级、freshness 一律来自后端 Projection（或严格 fixture）。
 */

import { useMemo, useState } from "react";
import { GitMerge } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import type { InsightDerivationNode } from "../insight-derivation-contract";
import { planSummary, preserveViewContext, type CollapsedGroup } from "../insight-tree-layout";
import { computeStalePaths, type ReIntegrationTarget } from "../insight-tree-stale";
import { InsightCompoundCard } from "./insight-compound-card";
import { DisplayGroupCard } from "./display-group-card";

export type InsightViewMode = "summary" | "expanded";

export type ViewportContext = {
  viewportCenter: { x: number; y: number };
  zoom: number;
};

/** 计数：展开态下整棵可展开 DAG 的可见节点行数。 */
export function countExpandedVisible(
  nodes: readonly InsightDerivationNode[],
  expandedIds: ReadonlySet<string>,
): number {
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const consumed = new Set<string>();
  for (const n of nodes) for (const p of n.inputIds) consumed.add(p);
  const roots = nodes.filter((n) => !consumed.has(n.id)).map((n) => n.id);
  let count = 0;
  const walk = (id: string) => {
    count += 1;
    const n = byId.get(id);
    if (n && expandedIds.has(id)) for (const c of n.inputIds) walk(c);
  };
  for (const r of roots) walk(r);
  return count;
}

export function InsightTreeView({
  nodes,
  initialMode = "summary",
  initialExpanded = new Set<string>(),
  initialSelectedId = null,
  initialViewport = { viewportCenter: { x: 0, y: 0 }, zoom: 1 },
  onSelect,
  onReintegrate,
  cardLabels,
  labels,
}: {
  nodes: InsightDerivationNode[];
  initialMode?: InsightViewMode;
  initialExpanded?: ReadonlySet<string>;
  initialSelectedId?: string | null;
  initialViewport?: ViewportContext;
  onSelect?: (id: string) => void;
  onReintegrate?: (target: ReIntegrationTarget) => void;
  cardLabels?: InsightCompoundCardLabels;
  labels?: Partial<InsightTreeViewLabels>;
}) {
  const L = labels ?? {};
  const summaryToggleLabel = L.summaryToggleLabel ?? "摘要";
  const expandedToggleLabel = L.expandedToggleLabel ?? "展开";
  const expandAllLabel = L.expandAllLabel ?? "展开全部";
  const collapseAllLabel = L.collapseAllLabel ?? "折叠全部";
  const selectedLabel = L.selectedLabel ?? "已选";
  const noneLabel = L.noneLabel ?? "无";
  const viewportLabel = L.viewportLabel ?? "相机";
  const reIntegrationTitle = L.reIntegrationTitle ?? "重新整合";
  const reIntegrationAction = L.reIntegrationAction ?? "重新整合";
  const stalePathSummary = L.stalePathSummary ?? "受影响路径";
  const collapsedLabel = L.collapsedLabel ?? "折叠节点";
  const breadcrumbLabel = L.breadcrumbLabel ?? "层级";

  const [mode, setMode] = useState<InsightViewMode>(initialMode);
  const [expandedIds, setExpandedIds] = useState<ReadonlySet<string>>(initialExpanded);
  const [groupsExpanded, setGroupsExpanded] = useState<ReadonlySet<string>>(
    () => new Set(),
  );
  const [selectedId, setSelectedId] = useState<string | null>(initialSelectedId);
  const [viewport, setViewport] = useState<ViewportContext>(initialViewport);

  const byId = useMemo(() => new Map(nodes.map((n) => [n.id, n])), [nodes]);

  // 顶层锚点 = DAG 汇点（对外无消费者的最高层 Insight）。
  const roots = useMemo(() => {
    const consumed = new Set<string>();
    for (const n of nodes) for (const p of n.inputIds) consumed.add(p);
    return nodes.filter((n) => !consumed.has(n.id)).map((n) => n.id);
  }, [nodes]);

  const stalePaths = useMemo(() => computeStalePaths(nodes), [nodes]);

  const affectByNodeId: ReadonlyMap<string, "direct" | "inherited"> = useMemo(() => {
    const m = new Map<string, "direct" | "inherited">();
    for (const e of stalePaths.affected) {
      if (e.affect !== "fresh") m.set(e.nodeId, e.affect);
    }
    return m;
  }, [stalePaths]);

  // pinned = 选中节点及其输入祖先 + stale 受影响路径（保证失效传播可辨）。
  const pinnedIds = useMemo(() => {
    const pinned = new Set<string>(affectByNodeId.keys());
    if (selectedId) {
      const stack = [selectedId];
      const seen = new Set<string>();
      while (stack.length > 0) {
        const id = stack.pop()!;
        if (seen.has(id) || pinned.has(id)) continue;
        seen.add(id);
        pinned.add(id);
        const node = byId.get(id);
        if (node) for (const p of node.inputIds) stack.push(p);
      }
    }
    return pinned;
  }, [selectedId, affectByNodeId, byId]);

  const summary = useMemo(() => planSummary(nodes, pinnedIds), [nodes, pinnedIds]);

  const select = (id: string) => {
    setSelectedId(id);
    onSelect?.(id);
  };

  const toggleExpanded = (id: string) => {
    setExpandedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const toggleGroup = (groupId: string) => {
    setGroupsExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(groupId)) next.delete(groupId);
      else next.add(groupId);
      return next;
    });
  };

  const expandedGroupMemberCount = useMemo(() => {
    let count = 0;
    for (const g of summary.collapsed) {
      if (groupsExpanded.has(g.groupId)) count += g.memberIds.length;
    }
    return count;
  }, [summary.collapsed, groupsExpanded]);

  const breadcrumb = useMemo(() => {
    if (!selectedId) return [];
    const chain: string[] = [selectedId];
    const seen = new Set<string>([selectedId]);
    let cursor = selectedId;
    for (let guard = 0; guard < nodes.length; guard++) {
      const node = byId.get(cursor);
      const parentId = node?.inputIds.find((p) => byId.has(p) && !seen.has(p));
      if (!parentId) break;
      chain.unshift(parentId);
      seen.add(parentId);
      cursor = parentId;
    }
    return chain;
  }, [selectedId, byId, nodes.length]);

  const renderNodeCard = (nodeId: string, depth: number) => {
    const node = byId.get(nodeId);
    if (!node) return null;
    const affect = affectByNodeId.get(nodeId);
    const expanded = expandedIds.has(nodeId);
    const expandable = node.level >= 1 && node.inputIds.length > 0;

    return (
      <div key={nodeId} className="flex flex-col gap-1.5" data-view-row>
        <InsightCompoundCard
          node={node}
          stale={affect ? { stale: true, affect, reason: node.staleReason } : null}
          selected={selectedId === nodeId}
          expanded={expanded}
          expandable={expandable}
          onToggleExpand={() => toggleExpanded(nodeId)}
          onSelect={() => select(nodeId)}
          labels={cardLabels}
        />
        {expanded && node.inputIds.length > 0 && (
          <div
            className="flex flex-col gap-1.5 border-l border-border/40 pl-3"
            data-children
          >
            {node.inputIds.map((childId) => (
              <div key={childId}>
                {renderNodeCard(childId, depth + 1)}
              </div>
            ))}
          </div>
        )}
      </div>
    );
  };

  const renderFlatCard = (nodeId: string) => {
    const node = byId.get(nodeId);
    if (!node) return null;
    const affect = affectByNodeId.get(nodeId);
    return (
      <div key={nodeId} data-view-row>
        <InsightCompoundCard
          node={node}
          stale={affect ? { stale: true, affect, reason: node.staleReason } : null}
          selected={selectedId === nodeId}
          onSelect={() => select(nodeId)}
          labels={cardLabels}
        />
      </div>
    );
  };

  const renderGroup = (group: CollapsedGroup) => {
    const expanded = groupsExpanded.has(group.groupId);
    return (
      <div key={group.groupId} className="flex flex-col gap-1.5" data-view-row>
        <DisplayGroupCard
          group={group}
          expanded={expanded}
          onToggle={() => toggleGroup(group.groupId)}
          labels={cardLabels}
        />
        {expanded && (
          <div
            className="flex flex-col gap-1.5 border-l border-border/40 pl-3"
            data-children
          >
            {group.memberIds.map((memberId) => renderFlatCard(memberId))}
          </div>
        )}
      </div>
    );
  };

  const visibleCount =
    mode === "expanded"
      ? countExpandedVisible(nodes, expandedIds)
      : summary.visibleNodeCount + expandedGroupMemberCount;

  const summaryAction = (
    <>
      <button
        type="button"
        onClick={() => {
          // AC2：模式切换只改可见集合；选择/相机/缩放/pinned 经 preserveViewContext 原样保留。
          const kept = preserveViewContext(
            { viewportCenter: viewport.viewportCenter, zoom: viewport.zoom, selectedId, pinnedIds },
            {},
          );
          setViewport({ viewportCenter: kept.viewportCenter, zoom: kept.zoom });
          setMode((m) => (m === "summary" ? "expanded" : "summary"));
        }}
        aria-pressed={mode === "expanded"}
        className="rounded-md border border-border px-2 py-1 text-xs font-medium text-foreground hover:bg-muted"
        data-testid="mode-toggle"
      >
        {mode === "summary" ? expandedToggleLabel : summaryToggleLabel}
      </button>
      <button
        type="button"
        onClick={() => setExpandedIds(new Set(nodes.map((n) => n.id)))}
        className="rounded-md border border-border px-2 py-1 text-xs font-medium text-muted-foreground hover:bg-muted"
        data-testid="expand-all"
      >
        {expandAllLabel}
      </button>
      <button
        type="button"
        onClick={() => setExpandedIds(new Set())}
        className="rounded-md border border-border px-2 py-1 text-xs font-medium text-muted-foreground hover:bg-muted"
        data-testid="collapse-all"
      >
        {collapseAllLabel}
      </button>
    </>
  );

  return (
    <div
      className="flex min-w-0 flex-col gap-3 rounded-xl border border-border bg-background p-3"
      data-testid="insight-tree-view"
    >
      {/* 顶部 chrome：模式切换 + 上下文保持状态 + 面包屑 */}
      <div className="flex flex-wrap items-center gap-2">
        {summaryAction}
        <span className="text-xs text-muted-foreground" data-testid="visible-count">
          {visibleCount} {collapsedLabel}
        </span>
        <span className="text-xs text-muted-foreground" data-testid="viewport-context">
          {viewportLabel} z{viewport.zoom.toFixed(2)} ({Math.round(viewport.viewportCenter.x)},
          {Math.round(viewport.viewportCenter.y)})
        </span>
      </div>

      <div
        className="flex flex-wrap items-center gap-1 text-xs text-muted-foreground"
        data-testid="breadcrumb"
        aria-label={breadcrumbLabel}
      >
        {breadcrumb.length === 0 ? (
          <span>{selectedLabel} {noneLabel}</span>
        ) : (
          breadcrumb.map((id, i) => (
            <span key={id} className="flex items-center gap-1">
              {i > 0 ? <span aria-hidden>›</span> : null}
              <span
                data-breadcrumb-id={id}
                className={cn(
                  "rounded px-1",
                  i === breadcrumb.length - 1 ? "bg-primary/15 text-foreground" : "",
                )}
              >
                {byId.get(id)?.conclusion ?? id}
              </span>
            </span>
          ))
        )}
      </div>

      {/* 受影响路径摘要 */}
      {stalePaths.affectedCount > 0 && (
        <div
          className="inline-flex items-center gap-2 rounded-md bg-destructive/10 px-2 py-1 text-xs font-medium text-destructive"
          data-testid="stale-path-summary"
        >
          {stalePathSummary} {stalePaths.affectedCount}
        </div>
      )}

      {/* 树主体 */}
      <div className="flex flex-col gap-2" data-testid="tree-body">
        {mode === "expanded"
          ? roots.map((r) => renderNodeCard(r, 0))
          : (
              <>
                {summary.viewNodes.map((v) =>
                  v.kind === "node" ? renderFlatCard(v.node.id) : null,
                )}
                {summary.collapsed.map((g) => renderGroup(g))}
              </>
            )}
      </div>

      {/* 最小重新整合入口 */}
      {stalePaths.reIntegrationTargets.length > 0 && (
        <div className="flex flex-col gap-1.5 border-t border-border pt-2" data-testid="reintegration">
          <span className="text-xs font-medium text-foreground">{reIntegrationTitle}</span>
          {stalePaths.reIntegrationTargets.map((target) => (
            <div
              key={target.insightId}
              className="flex items-center justify-between gap-2 rounded-md bg-muted/50 px-2 py-1.5"
              data-testid="reintegration-target"
              data-insight-id={target.insightId}
            >
              <span className="min-w-0 truncate text-xs text-foreground">
                {byId.get(target.insightId)?.conclusion ?? target.insightId}
              </span>
              <button
                type="button"
                onClick={() => onReintegrate?.(target)}
                className="inline-flex shrink-0 items-center gap-1 rounded-md bg-destructive/15 px-2 py-1 text-xs font-semibold text-destructive hover:bg-destructive/25"
                data-testid="reintegrate-button"
              >
                <GitMerge className="size-3.5" aria-hidden />
                {reIntegrationAction}
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

export type InsightCompoundCardLabels = NonNullable<
  Parameters<typeof InsightCompoundCard>[0]["labels"]
>;

export type InsightTreeViewLabels = {
  summaryToggleLabel: string;
  expandedToggleLabel: string;
  expandAllLabel: string;
  collapseAllLabel: string;
  selectedLabel: string;
  noneLabel: string;
  viewportLabel: string;
  reIntegrationTitle: string;
  reIntegrationAction: string;
  stalePathSummary: string;
  collapsedLabel: string;
  breadcrumbLabel: string;
};
