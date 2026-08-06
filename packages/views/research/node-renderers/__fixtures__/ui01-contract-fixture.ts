/**
 * Research V6 — node renderers contract fixture (dev/test only, never prod).
 *
 * Strictly follows the canonical V6 projection contract
 * (`@multica/core/types/research-v6`) and the LRM-1469 bridge. It is named and
 * exported explicitly and is NEVER imported from production paths — it feeds
 * tests, the storybook-ish demo and screenshot review.
 */

import type { ResearchV6ProjectionNode } from "@multica/core/types/research-v6";
import { KNOWN_NODE_KINDS } from "../node-kind-registry";

const RUN_ID = "run-ui01";

/** One projection node per kind (30 nodes + generic unknown). */
export const UI01_FIXTURE_NODES: ResearchV6ProjectionNode[] = [
  ...KNOWN_NODE_KINDS.map(
    (kind, i): ResearchV6ProjectionNode => nodeOf(kind, `e${i}`, {
      title: `示例 · ${kind}`,
      summary: `${kind} 的简要摘要，用于验证卡片布局与缩放。`,
      status: stageFor(i),
      importance: (i % 3) + 1,
      actor_agent_id: `agent:${(i % 5) + 1}`,
      attempt_id: `attempt:${(i % 4) + 1}`,
    }),
  ),
  // unknown future kind → generic degradation (must never crash)
  nodeOf("some_future_kind", "unknown-1", {
    title: "未来的未知节点类型",
    summary: "这个 kind 尚未注册，必须降级为 generic 卡片且页面不崩溃。",
    status: "pending",
  }),
];

/** Explicit 8-state sample nodes (AC2) for the matrix tests / demo. */
export const UI01_STATE_NODES: ResearchV6ProjectionNode[] = [
  nodeOf("task", "st-default", {
    title: "默认态",
    summary: "就绪",
    status: "idle",
    actor_agent_id: "agent:lindberg",
    detail: { objective: "核验市场可行性", current_action: "待启动", resolved_count: 0, progress_count: 0, risk_count: 0 },
  }),
  nodeOf("attempt", "st-selected", {
    title: "选中态",
    summary: "选中高亮",
    status: "idle",
    actor_agent_id: "agent:morgan",
    detail: { objective: "分析竞品梯度", current_action: "整理结论", resolved_count: 1, progress_count: 1, risk_count: 0 },
  }),
  nodeOf("query_execution", "st-loading", {
    title: "加载态",
    summary: "占位",
    status: "pending",
    actor_agent_id: "agent:beckham",
    detail: { objective: "检索语料库", current_action: "等待运行时接收", resolved_count: 0, progress_count: 0, risk_count: 0 },
  }),
  nodeOf("attempt", "st-running", {
    title: "运行态",
    summary: "脉冲",
    status: "running",
    actor_agent_id: "agent:rino",
    detail: { objective: "核验 3 个来源", current_action: "正在执行 · 检索", resolved_count: 2, progress_count: 1, risk_count: 1 },
  }),
  nodeOf("task", "st-failed", {
    title: "失败态",
    summary: "错误",
    status: "failed",
    actor_agent_id: "agent:caozs2",
    detail: { objective: "建立基线", current_action: "来源超时", resolved_count: 0, progress_count: 0, risk_count: 2 },
  }),
  nodeOf("insight", "st-stale", {
    title: "过期态",
    summary: "虚线",
    status: "stale",
    actor_agent_id: "agent:wendy",
    detail: { objective: "解释结论关系", current_action: "被取代", resolved_count: 3, progress_count: 0, risk_count: 0 },
  }),
  nodeOf("episode", "st-terminal", {
    title: "终态",
    summary: "完成",
    status: "done",
    actor_agent_id: "agent:lindberg",
    detail: { objective: "汇总运行报告", current_action: "最近完成", resolved_count: 5, progress_count: 1, risk_count: 1 },
  }),
  nodeOf("source_candidate", "st-unknown", {
    title: "未知状态",
    summary: "降级",
    status: "weird__kind",
    detail: { objective: "候选来源", current_action: "状态未知", resolved_count: 0, progress_count: 0, risk_count: 0 },
  }),
];

/** Render-node view model (kind + label + state), used by the demo grid. */
export interface UI01DemoRow {
  node: ResearchV6ProjectionNode;
  surfaceLabel: string;
}

function nodeOf(
  kind: string,
  entityId: string,
  overrides: Partial<ResearchV6ProjectionNode> = {},
): ResearchV6ProjectionNode {
  const id = `${RUN_ID}:${kind}:${entityId}`;
  return {
    id,
    run_id: RUN_ID,
    entity_kind: kind,
    entity_id: entityId,
    node_kind: kind,
    node_subtype: "",
    schema_version: 1,
    title: `${kind} ${entityId}`,
    summary: `fixture summary for ${kind}`,
    status: "idle",
    importance: 1,
    freshness: null,
    contract_version: "1",
    plan_version: "1",
    strategy_version: "1",
    actor_agent_id: null,
    task_id: null,
    attempt_id: null,
    created_at: null,
    updated_at: null,
    cost: null,
    detail: { entityId },
    created_sequence: 1,
    updated_sequence: 1,
    terminal_sequence: null,
    ...overrides,
  };
}

/** Cycle statuses so the 30-kind grid exercises several states. */
function stageFor(i: number): string {
  const phases = ["idle", "running", "done", "failed", "stale"] as const;
  return phases[i % phases.length] ?? "idle";
}
