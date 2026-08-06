/**
 * LRM-1470 — Insight Derivation 合成树 · 后端合约适配层。
 *
 * 本模块只消费后端 Projection 提供的 Insight Derivation 事实，并把
 * canonical 字段与前端显示状态严格分开：
 *
 *  - canonical（后端为准，前端不推断）：
 *    level、输入引用 derived_from/integrates、freshness、价值变化、整合轮次。
 *  - 前端显示状态（只能本地，不能写回 canonical Graph）：
 *    摘要/展开分组、选择、相机、受影响路径高亮、重新整合入口。
 *
 * 边界（对应 LRM-1444 / 2026-08-05-autonomous-research-system.md §9.1、§7.2）：
 *  - 真正的节点融合只能由已验收 Insight Derivation 创建新 Insight；
 *    前端显示分组不是 Research Insight，不能写回 canonical Graph。
 *  - 层级由 `1 + max(input insight level)` 计算（Claim 视为 level 0），
 *    不能由 Agent 自报，前端也不得从摘要/聊天/动画状态推导。
 *  - 任一输入被 refuted / superseded / 范围改变 / 访问权限撤销时，
 *    依赖它的所有祖先 Insight 进入 stale；前端只沿后端事实标记路径。
 */

export type InsightFreshness = "fresh" | "stale";

/** 失效原因，与 2026-08-05 计划 §9.1 一致。 */
export type InsightStaleReason =
  | "input_refuted"
  | "input_superseded"
  | "scope_changed"
  | "access_revoked";

/** canonical Insight 节点的投影字段（后端提供，前端只读）。 */
export interface InsightDerivationNode {
  /** 稳定投影节点 id。 */
  id: string;
  /** Claim 视为 level 0；Insight level = 1 + max(input level)。 */
  level: number;
  /** 直接输入节点 id 的投影（derived_from / integrates 的去重合并）。 */
  inputIds: readonly string[];
  /** canonical freshness，由后端根据输入失效传播计算。 */
  freshness: InsightFreshness;
  /** 失效原因之一（仅 stale 时有值）。 */
  staleReason?: InsightStaleReason;
  /** 展示结论文案（后端生成的简短 Insight 结论）。 */
  conclusion: string;
  /** 证据覆盖摘要（后端提供，可能是数量或短文案）。 */
  evidenceCoverage?: string;
  /** 输入中仍存在 contradiction 的数量（为展示矛盾徽标）。 */
  contradictionCount?: number;
  /** 贡献 Agent / 整合轮次标识（用于详情，可空）。 */
  integrationRoundId?: string;
  contributingAgentIds?: readonly string[];
  /** 后端提供的可观察价值说明（可选，用于详情）。 */
  valueNote?: string;
}

/**
 * 对从后端快照读到的节点做最小校验：拒绝缺 canonical 字段的节点，
 * 而不是编造 level / freshness / 输入关系。
 *
 * @returns 返回 { ok, nodes, invalidIds }；invalidIds 非空时调用方必须
 *   按合约缺口处理（例如标记「后端未就绪」空态），不能假装树真实存在。
 */
export function selectInsightDerivationNodes(
  nodes: readonly InsightDerivationNode[],
): { ok: true; nodes: InsightDerivationNode[]; invalidIds: string[] } | {
  ok: false;
  nodes: InsightDerivationNode[];
  invalidIds: string[];
} {
  const invalidIds: string[] = [];
  const valid: InsightDerivationNode[] = [];
  for (const n of nodes) {
    const bad =
      typeof n.id !== "string" || n.id.length === 0 ||
      !Number.isInteger(n.level) || n.level < 0 ||
      !Array.isArray(n.inputIds) ||
      (n.freshness !== "fresh" && n.freshness !== "stale");
    if (bad) {
      invalidIds.push(n.id ?? "<missing-id>");
      continue;
    }
    valid.push(n);
  }
  return invalidIds.length > 0
    ? { ok: false, nodes: valid, invalidIds }
    : { ok: true, nodes: valid, invalidIds };
}

/** 输入引用必须指向真实存在且 level 更低的节点。 */
export function validateInputEdges(
  nodes: readonly InsightDerivationNode[],
): { invalid: string[] } {
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const invalid: string[] = [];
  for (const n of nodes) {
    for (const inputId of n.inputIds) {
      const input = byId.get(inputId);
      if (!input) {
        invalid.push(`${n.id}:missing-input:${inputId}`);
      } else if (input.level >= n.level) {
        invalid.push(`${n.id}:non-monotonic-level:${inputId}`);
      }
    }
  }
  return { invalid };
}
