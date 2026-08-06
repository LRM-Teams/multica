/**
 * LRM-1470 — stale 失效传播的可辨认显示逻辑。
 *
 * 传播方向事实来自后端（freshness 已在 canonical 上标好）；本模块只做：
 *  - 沿 `derived_from / integrates` 的反向引用，把「stale 输入」的祖先路径
 *    明确标出来，并区分直接受影响与传递受影响；
 *  - 对每条受影响路径给出可行动的「重新整合」入口映射（input ids → 需要
 *    重新整合的高层 Insight ids），作为前端按钮数据源。
 *
 * 边界：本模块不推断 canonical freshness（那是后端职责），只在后端已把
 * 至少一个输入标 stale 的前提下计算受影响的显示路径与按钮入口。
 */

import type { InsightDerivationNode, InsightFreshness } from "./insight-derivation-contract";

/** 路径上每个节点的受影响程度。 */
export type StaleAffectKind =
  | "direct"      // 该节点至少有一个直接输入已 stale
  | "inherited"   // 该节点是 stale 祖先的传递后代（自身输入未直接变 stale）
  | "fresh";      // 不在受影响的 stale 路径上

export type StalePathEntry = {
  nodeId: string;
  level: number;
  affect: StaleAffectKind;
  /** 触发本节点受影响的最底层 stale 输入 id（可多个）。 */
  triggerInputIds: readonly string[];
};

export type StalePathResult = {
  /** 受影响节点（stale 或其传递祖先），按 level 升序。 */
  affected: StalePathEntry[];
  /** 受影响节点的 count（用于 UI「受影响路径」摘要角标）。 */
  affectedCount: number;
  /** 独立受影响根（最底层受影响节点）—— 重新整合的最小入口集合。 */
  affectedRoots: string[];
  /** 重新整合入口：最底层 stale 的每个连通祖先各自需一次「重新整合」。 */
  reIntegrationTargets: ReIntegrationTarget[];
};

export type ReIntegrationTarget = {
  /** 需要重新整合的高层 Insight id（affected 祖先中 level 最高的）。 */
  insightId: string;
  /** 该 Insight 之下受影响的输入集合（重新整合的范围提示）。 */
  staleInputIds: readonly string[];
  /** 后端 freshness 原因之一。 */
  staleReason: InsightDerivationNode["staleReason"];
};

/**
 * 从若干 stale 叶输入出发，沿反向引用把受影响祖先路径标出来。
 * `rootCandidates` 可为全部节点；内部按需只用 stale 相关连通分量。
 */
export function computeStalePaths(
  nodes: readonly InsightDerivationNode[],
): StalePathResult {
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const freshnessById = new Map<string, InsightFreshness>(
    nodes.map((n) => [n.id, n.freshness]),
  );

  // 1) 收集最底层 stale 节点（没有输入的 stale 事实源头）。
  //    注意：真正 canonical stale 由后端给定；这里只按给定 freshness 递归
  //    向上，绝不自我判定「该不该 stale」。
  const allStaleInputs = new Set<string>();
  for (const n of nodes) if (n.freshness === "stale") allStaleInputs.add(n.id);

  // 2) 沿 inputIds 正向做传递闭包，找出所有受这些 stale 输入影响的祖先。
  //    同一祖先可能被多个 seed 触发，因此一个节点要 visit 多次向上延伸。
  const affected = new Map<
    string,
    { trigger: Set<string>; kind: StaleAffectKind }
  >();
  // 已入队的节点集合，避免同一 (node) 无限入队；种子去重后保证收敛。
  const queued = new Set<string>();
  const ensure = (nodeId: string, seedId: string) => {
    const node = byId.get(nodeId);
    if (!node) return;
    const current = affected.get(nodeId);
    if (!current) {
      const direct = node.inputIds.some(
        (id) => affected.has(id) || allStaleInputs.has(id),
      );
      affected.set(nodeId, {
        trigger: new Set(nodeId === seedId ? [seedId] : [seedId]),
        kind: direct || nodeId === seedId ? "direct" : "inherited",
      });
    } else {
      current.trigger.add(seedId);
      // 若此前被标为 inherited，但后来发现某输入更早受直接影响则升级。
      if (node.inputIds.some((id) => affected.has(id))) current.kind = "direct";
    }
  };
  for (const seedId of allStaleInputs) {
    if (queued.has(seedId)) continue;

    // BFS 自 stale 种子向上沿 child.inputIds 传播。
    const stack = [seedId];
    while (stack.length > 0) {
      const id = stack.pop()!;
      if (queued.has(id)) continue;
      queued.add(id);
      ensure(id, seedId);
      for (const child of nodes) {
        if (child.inputIds.includes(id) && !queued.has(child.id)) {
          stack.push(child.id);
        }
      }
    }
  }

  // 整理受影响节点的 trigger 集合与最终 kind（direct 优先于 inherited）。
  const entries: StalePathEntry[] = [];
  for (const [nodeId, { trigger, kind }] of affected) {
    const node = byId.get(nodeId);
    if (!node) continue;
    entries.push({
      nodeId,
      level: node.level,
      affect: kind,
      triggerInputIds: [...trigger],
    });
  }
  entries.sort((a, b) => a.level - b.level);

  // 3) affected roots：受影响节点中 level 最低的那些（无受影响祖先或祖先不受影响）。
  const affectedIds = new Set(affected.keys());
  const roots = new Set<string>();
  for (const e of entries) {
    const node = byId.get(e.nodeId)!;
    const hasAffectedAncestor = node.inputIds.some((id) => affectedIds.has(id));
    if (!hasAffectedAncestor) roots.add(e.nodeId);
  }

  // 4) 重新整合入口：每个连通受影响分量取 level 最高的祖先为该分量重整合目标。
  const targets: ReIntegrationTarget[] = [];
  const visited = new Set<string>();
  for (const rootId of roots) {
    // 沿 inputIds 递归收集分量内所有受影响节点。
    const component: string[] = [];
    const walk = (id: string) => {
      if (visited.has(id) || !affectedIds.has(id)) return;
      visited.add(id);
      component.push(id);
      for (const c of nodes) {
        if (c.inputIds.includes(id) && affectedIds.has(c.id)) walk(c.id);
      }
    };
    walk(rootId);
    if (component.length === 0) continue;
    const top =
      component
        .map((id) => byId.get(id)!)
        .sort((a, b) => b.level - a.level)[0] ?? undefined;
    if (!top) continue;
    const staleIds = component.filter((id) => freshnessById.get(id) === "stale");
    targets.push({
      insightId: top.id,
      staleInputIds: staleIds,
      staleReason: top.staleReason ?? component
        .map((id) => byId.get(id)!.staleReason)
        .find(Boolean),
    });
  }

  return {
    affected: entries,
    affectedCount: entries.length,
    affectedRoots: [...roots],
    reIntegrationTargets: targets,
  };
}
