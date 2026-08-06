/**
 * LRM-1470 — Insight Derivation DAG 合约 fixture（≥3 层）。
 *
 * 生产路径不保留此 fixture；它是测试与交互原型的数据源，严格按
 * 2026-08-05-autonomous-research-system.md §9.1 Recursive Integration 构造：
 *  - Claim 视为 level 0；Insight level = 1 + max(input level)。
 *  - 两个以上来自不同 Task/Branch 的输入可形成一级 Insight；
 *    多个一级 Insight 形成跨主题二级 Insight，后续层级同规则、不设固定层数。
 *  - freshness 由后端给定；本 fixture 以一个后续被 superseded 的输入演示
 *    stale 的 `input_superseded` 传播（对应 §9.1 失效规则）。
 */

import type { InsightDerivationNode } from "./insight-derivation-contract";

/** 简便构造一层仅 freshness 不同的节点。 */
function node(
  partial: Omit<InsightDerivationNode, "level"> & { level: number },
): InsightDerivationNode {
  return { ...partial };
}

/**
 * 种子语义（来自计划 §9.1 例子）：
 *  - C1「官方价格低」、C2「迁移成本高」、C3「条款禁止目标业务」为叶子 Claim (L0)
 *  - 一级 Insight I1「标价优势不能代表目标地区总成本」(L1) 由 C1,C2,C3 归纳
 *  - C4「替代供应商条款宽松」加入后，一级 Insight I2「替代方案约束更少」(L1)
 *  - 二级 Insight R1「选型需以目标地区全成本为准，替代方案可行」(L2) 由 I1,I2 归纳
 *  - 三级 Insight M1「跨市场选型决策主结论」(L3) 由 R1 与另一支 I3 归纳
 *
 * 演示 stale：C2 随后被 C5「迁移成本实际低于迁移期损失」superseded，
 * 使 I1 → R1 → M1 整条祖先路径进入 stale（input_superseded）。
 */
export const insightDerivationFixture: InsightDerivationNode[] = [
  // ── level 0 / Claims（叶子）───────────────────────────────
  node({ id: "c1", level: 0, inputIds: [], freshness: "fresh",
    conclusion: "官方价格低" , evidenceCoverage: "3 来源" }),
  node({ id: "c2", level: 0, inputIds: [], freshness: "stale",
    staleReason: "input_superseded",
    conclusion: "迁移成本高", evidenceCoverage: "2 来源" }),
  node({ id: "c3", level: 0, inputIds: [], freshness: "fresh",
    conclusion: "目标地区条款禁止目标业务", evidenceCoverage: "1 来源" }),
  node({ id: "c4", level: 0, inputIds: [], freshness: "fresh",
    conclusion: "替代供应商条款宽松", evidenceCoverage: "2 来源" }),
  node({ id: "c5", level: 0, inputIds: [], freshness: "fresh",
    conclusion: "迁移成本实际低于迁移期损失", evidenceCoverage: "2 来源" }),
  node({ id: "c6", level: 0, inputIds: [], freshness: "fresh",
    conclusion: "区域市场合规门槛一致", evidenceCoverage: "1 来源" }),

  // ── level 1 / 一级 Insight ────────────────────────────────
  node({ id: "i1", level: 1, inputIds: ["c1", "c2", "c3"], freshness: "stale",
    staleReason: "input_superseded",
    conclusion: "标价优势不能代表目标地区总成本", evidenceCoverage: "6 输入",
    contradictionCount: 1, integrationRoundId: "round-1" }),
  node({ id: "i2", level: 1, inputIds: ["c4"], freshness: "fresh",
    conclusion: "替代方案约束更少", evidenceCoverage: "2 输入",
    integrationRoundId: "round-2" }),
  node({ id: "i3", level: 1, inputIds: ["c6"], freshness: "fresh",
    conclusion: "区域合规门槛一致", evidenceCoverage: "1 输入",
    integrationRoundId: "round-2" }),

  // ── level 2 / 二级 Insight ────────────────────────────────
  node({ id: "r1", level: 2, inputIds: ["i1", "i2"], freshness: "stale",
    staleReason: "input_superseded",
    conclusion: "选型需以目标地区全成本为准，替代方案可行",
    evidenceCoverage: "8 输入", contradictionCount: 1, integrationRoundId: "round-3" }),
  node({ id: "r2", level: 2, inputIds: ["i3"], freshness: "fresh",
    conclusion: "合规可作为统一前置门槛", evidenceCoverage: "1 输入",
    integrationRoundId: "round-3" }),

  // ── level 3 / 三级 Insight ────────────────────────────────
  node({ id: "m1", level: 3, inputIds: ["r1", "r2"], freshness: "stale",
    staleReason: "input_superseded",
    conclusion: "跨市场选型决策主结论：以目标地区全成本与合规门槛为准",
    evidenceCoverage: "10 输入", contradictionCount: 1, integrationRoundId: "round-4" }),
];
