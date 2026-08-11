/**
 * LRM-1470 — Research V6 Insight 组合树（packages/views/research/insight-tree）。
 *
 * 本包是「递归 Insight 组合树」的布局规格、交互原型与后端合约适配。
 * 只含纯函数与 fixture，无 DOM、无副作用；前端将其接入 D5 star-graph 或 git list，
 * 作为摘要/展开切换、失效传播路径与重新整合入口的数据来源。
 *
 * 边界：本包绝不写回 canonical Graph，绝不把显示分组伪造为真实 Insight；
 * level / freshness / 输入关系一律以后端 Projection 为准。
 */

export * from "./insight-derivation-contract";
export * from "./insight-derivation-fixture";
export * from "./insight-tree-layout";
export * from "./insight-tree-stale";
