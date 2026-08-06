"use client";

/**
 * LRM-1476 — Insight 组合树可见节点计数。
 *
 * 纯函数：展开态下整棵可展开 DAG 的可见节点行数。独立成模块（非组件文件）
 * 是为了满足 react-doctor/only-export-components —— 组件文件只导出组件，
 * 纯工具函数放入自己的模块，避免 Fast Refresh 状态保持被打断。
 */

import type { InsightDerivationNode } from "../insight-derivation-contract";

/** 计数：展开态下整棵可展开 DAG 的可见节点行数。 */
export function countExpandedVisible(
  nodes: readonly InsightDerivationNode[],
  expandedIds: ReadonlySet<string>,
): number {
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const consumed = new Set<string>();
  for (const n of nodes) for (const p of n.inputIds) consumed.add(p);
  const roots: string[] = [];
  for (const n of nodes) if (!consumed.has(n.id)) roots.push(n.id);
  let count = 0;
  const walk = (id: string) => {
    count += 1;
    const n = byId.get(id);
    if (n && expandedIds.has(id)) for (const c of n.inputIds) walk(c);
  };
  for (const r of roots) walk(r);
  return count;
}
