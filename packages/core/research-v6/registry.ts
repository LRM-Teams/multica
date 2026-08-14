/**
 * Research V6 — frontend node registry.
 *
 * Mirrors `docs/superpowers/plans/2026-08-05-autonomous-research-system.md`
 * §7.1 (Graph Projection Contract) and §7.2 (无限画布投影协议).
 *
 * This is the canonical frontend registry of the V6 node kinds, edge types
 * and transition kinds. It exists so that:
 *  - every kind the backend registers has a known display surface;
 *  - unknown future kinds degrade to a GenericNode with a recorded diagnostic
 *    instead of crashing the page;
 *  - display grouping lives here as frontend display state and is NEVER
 *    written back as a real Research Insight.
 *
 * Production paths never manufacture a V6 node as fake data: the backend
 * projection read model is the only source of canonical facts.
 */
import { z } from "zod";

import type {
  ResearchV6EdgeType,
  ResearchV6NodeKind,
  ResearchV6TransitionKind,
  ResearchV6UnknownKindDiagnostic,
} from "../types/research-v6";

/* -------------------------------------------------------------------------- *
 * Node kinds — §7.1 (30 kinds: 执行 / 检索语料 / 探究 / 整合 / 争议 /
 * 团队能力 / 发散监测 / 报告评测 / 运行摘要)
 * -------------------------------------------------------------------------- */

export const RESEARCH_V6_NODE_KINDS = [
  "goal",
  // Execution
  "task",
  "attempt",
  "result_artifact",
  // Corpus / Search
  "search_plan",
  "query_execution",
  "source_candidate",
  "screening_decision",
  "source_snapshot",
  // Evidence / conclusions
  "observation",
  "claim",
  // Inquiry
  "question",
  "hypothesis",
  "branch",
  "insight",
  "insight_derivation",
  // Integration
  "integration_round",
  "integration_contribution",
  // Dispute / deliberation
  "dispute",
  "dispute_position",
  "deliberation",
  "deliberation_turn",
  "decision",
  // Team / capability
  "team_formation",
  "team_membership",
  "capability_observation",
  // Divergence / monitoring
  "divergence_pass",
  "monitoring_cycle",
  // Report / evaluation
  "report_revision",
  "evaluation_defect",
  // Run summary
  "episode",
] as const satisfies readonly ResearchV6NodeKind[];

/** Frontend display grouping for node kinds (display-only, never written back). */
export type ResearchV6NodeGroup =
  | "execution"
  | "corpus"
  | "evidence"
  | "inquiry"
  | "integration"
  | "dispute"
  | "team"
  | "monitoring"
  | "report"
  | "run"
  | "generic";

export interface ResearchV6NodeKindMeta {
  kind: ResearchV6NodeKind;
  /** Human-facing label. */
  label: string;
  /** Display grouping (frontend-only). */
  group: ResearchV6NodeGroup;
}

const NODE_META: Record<ResearchV6NodeKind, Omit<ResearchV6NodeKindMeta, "kind">> = {
  goal: { label: "研究目标", group: "run" },
  task: { label: "任务", group: "execution" },
  attempt: { label: "尝试", group: "execution" },
  result_artifact: { label: "结果工件", group: "execution" },
  search_plan: { label: "检索计划", group: "corpus" },
  query_execution: { label: "查询执行", group: "corpus" },
  source_candidate: { label: "来源候选", group: "corpus" },
  screening_decision: { label: "筛选决定", group: "corpus" },
  source_snapshot: { label: "来源快照", group: "corpus" },
  observation: { label: "观测", group: "evidence" },
  claim: { label: "论断", group: "evidence" },
  question: { label: "问题", group: "inquiry" },
  hypothesis: { label: "假设", group: "inquiry" },
  branch: { label: "分支", group: "inquiry" },
  insight: { label: "洞察", group: "inquiry" },
  insight_derivation: { label: "洞察推导", group: "inquiry" },
  integration_round: { label: "整合轮次", group: "integration" },
  integration_contribution: { label: "整合贡献", group: "integration" },
  dispute: { label: "争议", group: "dispute" },
  dispute_position: { label: "争议立场", group: "dispute" },
  deliberation: { label: "审议", group: "dispute" },
  deliberation_turn: { label: "审议回合", group: "dispute" },
  decision: { label: "决策", group: "dispute" },
  team_formation: { label: "组队", group: "team" },
  team_membership: { label: "团队成员", group: "team" },
  capability_observation: { label: "能力观测", group: "team" },
  divergence_pass: { label: "发散扫描", group: "monitoring" },
  monitoring_cycle: { label: "监测周期", group: "monitoring" },
  report_revision: { label: "报告修订", group: "report" },
  evaluation_defect: { label: "评测缺陷", group: "report" },
  episode: { label: "运行摘要", group: "run" },
};

/** Registry lookup: known kind + display metadata. */
export const RESEARCH_V6_NODE_REGISTRY: ReadonlyMap<ResearchV6NodeKind, ResearchV6NodeKindMeta> =
  new Map(RESEARCH_V6_NODE_KINDS.map((k) => [k, { kind: k, ...NODE_META[k] }]));

/* -------------------------------------------------------------------------- *
 * Edge types — §7.1 (24 kinds, grouped by relation family)
 * -------------------------------------------------------------------------- */

export const RESEARCH_V6_EDGE_TYPES = [
  // structure / derivation
  "decomposes",
  "tests",
  "depends_on",
  "triggered",
  // production flow
  "produced",
  "consumed",
  "derived_from",
  "integrates",
  // evidence semantics
  "supports",
  "contradicts",
  "refines",
  "supersedes",
  "invalidates",
  // discussion / escalation
  "discussed_by",
  "challenged_by",
  "escalated_to",
  "resolved_by",
  // reporting / review
  "reported_in",
  "reviewed_by",
  "revised_by",
  // staffing / lifecycle
  "staffed_by",
  "created_for",
  "retired_after",
  "restart_of",
] as const satisfies readonly ResearchV6EdgeType[];

export type ResearchV6EdgeFamily =
  | "structure"
  | "production"
  | "evidence"
  | "discussion"
  | "reporting"
  | "lifecycle"
  | "generic";

const EDGE_META: Record<
  ResearchV6EdgeType,
  { type: ResearchV6EdgeType; label: string; family: ResearchV6EdgeFamily }
> = {
  decomposes: { type: "decomposes", label: "分解", family: "structure" },
  tests: { type: "tests", label: "检验", family: "structure" },
  depends_on: { type: "depends_on", label: "依赖", family: "structure" },
  triggered: { type: "triggered", label: "触发", family: "structure" },
  produced: { type: "produced", label: "产出", family: "production" },
  consumed: { type: "consumed", label: "消费", family: "production" },
  derived_from: { type: "derived_from", label: "派生自", family: "production" },
  integrates: { type: "integrates", label: "整合", family: "production" },
  supports: { type: "supports", label: "支持", family: "evidence" },
  contradicts: { type: "contradicts", label: "矛盾", family: "evidence" },
  refines: { type: "refines", label: "细化", family: "evidence" },
  supersedes: { type: "supersedes", label: "取代", family: "evidence" },
  invalidates: { type: "invalidates", label: "推翻", family: "evidence" },
  discussed_by: { type: "discussed_by", label: "由…讨论", family: "discussion" },
  challenged_by: { type: "challenged_by", label: "由…质疑", family: "discussion" },
  escalated_to: { type: "escalated_to", label: "升级至", family: "discussion" },
  resolved_by: { type: "resolved_by", label: "由…解决", family: "discussion" },
  reported_in: { type: "reported_in", label: "报告于", family: "reporting" },
  reviewed_by: { type: "reviewed_by", label: "由…评审", family: "reporting" },
  revised_by: { type: "revised_by", label: "由…修订", family: "reporting" },
  staffed_by: { type: "staffed_by", label: "由…配置", family: "lifecycle" },
  created_for: { type: "created_for", label: "为之创建", family: "lifecycle" },
  retired_after: { type: "retired_after", label: "之后退役", family: "lifecycle" },
  restart_of: { type: "restart_of", label: "重新开始自", family: "lifecycle" },
};

export const RESEARCH_V6_EDGE_REGISTRY: ReadonlyMap<
  ResearchV6EdgeType,
  { type: ResearchV6EdgeType; label: string; family: ResearchV6EdgeFamily }
> = new Map(
  RESEARCH_V6_EDGE_TYPES.map((t) => [t, EDGE_META[t]]),
);

/* -------------------------------------------------------------------------- *
 * Transition kinds — §7.2 (10 kinds)
 * -------------------------------------------------------------------------- */

export const RESEARCH_V6_TRANSITION_KINDS = [
  "branch_spawned",
  "task_dispatched",
  "result_accepted",
  "integration_formed",
  "insight_staled",
  "dispute_opened",
  "deliberation_progressed",
  "lead_escalated",
  "team_membership_changed",
  "report_revised",
] as const satisfies readonly ResearchV6TransitionKind[];

const TRANSITION_LABELS: Record<ResearchV6TransitionKind, string> = {
  branch_spawned: "分支生成",
  task_dispatched: "任务派发",
  result_accepted: "结果接受",
  integration_formed: "整合形成",
  insight_staled: "洞察失效",
  dispute_opened: "争议建立",
  deliberation_progressed: "审议推进",
  lead_escalated: "升级负责人",
  team_membership_changed: "成员变更",
  report_revised: "报告修订",
};

export const RESEARCH_V6_TRANSITION_REGISTRY: ReadonlyMap<
  ResearchV6TransitionKind,
  { kind: ResearchV6TransitionKind; label: string }
> = new Map(
  RESEARCH_V6_TRANSITION_KINDS.map((k) => [
    k,
    { kind: k, label: TRANSITION_LABELS[k] },
  ]),
);

/* -------------------------------------------------------------------------- *
 * Zod parse schemas — every kind/type has a parse test (AC #1).
 * -------------------------------------------------------------------------- */

/** strict enum: rejects unknown node kinds at the wire boundary. */
export const ResearchV6NodeKindSchema = z.enum(RESEARCH_V6_NODE_KINDS);
export const ResearchV6EdgeTypeSchema = z.enum(RESEARCH_V6_EDGE_TYPES);
export const ResearchV6TransitionKindSchema = z.enum(RESEARCH_V6_TRANSITION_KINDS);

/* -------------------------------------------------------------------------- *
 * Degradation: unknown kinds → GenericNode with diagnostics (AC #2).
 * -------------------------------------------------------------------------- */

export interface ResearchV6GenericNode {
  isGeneric: true;
  label: "未知节点" | "未知关系" | "未知转变";
  kind: string;
  group: "generic";
  /** Present only when the input kind was not recognised. */
  diagnostic: ResearchV6UnknownKindDiagnostic;
}

/** Known surface slot for a node kind. */
export interface ResearchV6KnownNodeSurface {
  isGeneric: false;
  kind: ResearchV6NodeKind;
  label: string;
  group: ResearchV6NodeGroup;
}

export type ResearchV6NodeSurface = ResearchV6KnownNodeSurface | ResearchV6GenericNode;

/**
 * Classify a raw node kind string against the registry. Unknown kinds always
 * degrade to a generic surface with a diagnostic — never throws, so an
 * unrecognised future node kind can never crash the page.
 */
export function classifyNodeKind(
  raw: string,
  ownerId: string,
  runId: string,
  diagnostics: ResearchV6UnknownKindDiagnostic[],
): ResearchV6NodeSurface {
  const parsed = ResearchV6NodeKindSchema.safeParse(raw);
  if (parsed.success) {
    const meta = RESEARCH_V6_NODE_REGISTRY.get(parsed.data);
    return {
      isGeneric: false,
      kind: parsed.data,
      label: meta?.label ?? raw,
      group: meta?.group ?? "generic",
    };
  }
  const next = diagnostics.length;
  const diagnostic: ResearchV6UnknownKindDiagnostic = {
    raw,
    field: "node_kind",
    owner_id: ownerId,
    run_id: runId,
    sequence: next + 1,
  };
  diagnostics.push(diagnostic);
  return {
    isGeneric: true,
    label: "未知节点",
    kind: raw,
    group: "generic",
    diagnostic,
  };
}

/** Classify a raw edge type; unknown types degrade to a generic relation. */
export function classifyEdgeType(
  raw: string,
  ownerId: string,
  runId: string,
  diagnostics: ResearchV6UnknownKindDiagnostic[],
): { isGeneric: boolean; type: string; label: string; diagnostic?: ResearchV6UnknownKindDiagnostic } {
  const parsed = ResearchV6EdgeTypeSchema.safeParse(raw);
  if (parsed.success) {
    const meta = RESEARCH_V6_EDGE_REGISTRY.get(parsed.data);
    return {
      isGeneric: false,
      type: parsed.data,
      label: meta?.label ?? raw,
    };
  }
  diagnostics.push({
    raw,
    field: "edge_type",
    owner_id: ownerId,
    run_id: runId,
    sequence: diagnostics.length + 1,
  });
  const diag = diagnostics[diagnostics.length - 1];
  return { isGeneric: true, type: raw, label: "未知关系", diagnostic: diag };
}

/** Classify a raw transition kind; unknown kinds degrade to null display. */
export function classifyTransitionKind(
  raw: string,
  runId: string,
  diagnostics: ResearchV6UnknownKindDiagnostic[],
): { isGeneric: boolean; kind: string; label: string | null; diagnostic?: ResearchV6UnknownKindDiagnostic } {
  const parsed = ResearchV6TransitionKindSchema.safeParse(raw);
  if (parsed.success) {
    const meta = RESEARCH_V6_TRANSITION_REGISTRY.get(parsed.data);
    return { isGeneric: false, kind: parsed.data, label: meta?.label ?? raw };
  }
  diagnostics.push({
    raw,
    field: "transition_kind",
    owner_id: runId,
    run_id: runId,
    sequence: diagnostics.length + 1,
  });
  const diag = diagnostics[diagnostics.length - 1];
  return { isGeneric: true, kind: raw, label: null, diagnostic: diag };
}
