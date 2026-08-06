import type { ResearchGraphEdge, ResearchGraphNode } from "@multica/core/types";

/**
 * LRM-1472 / UI-04 §9 — Dispute · Deliberation · Escalation contract fixture.
 * DEV/APP ONLY — never referenced by production paths. It feeds canvas + detail
 * rendering, unit tests, and screenshot review. Shape follows the V6 registered
 * `node_kind`/typed edges; no fake data is ever presented as canonical state.
 *
 * Topology:
 *   claim X ←supports/contradicts/refines→ 3 positions (P1/P2/P3)
 *   positions ← challenged_by → evidence fan
 *   deliberation spine (4 turns) below the fan
 *   escalation: deadlock → escalated_to → Research Director (lead_adjudication)
 *   decision node; later reopen via invalidates/supersedes → investigation
 */
const SESSION = "fixture-session-dispute-1478";

function n(
  id: string,
  node_type: ResearchGraphNode["node_type"],
  title: string,
  status: string,
  payload: Record<string, unknown>,
  extra: Partial<ResearchGraphNode> = {},
): ResearchGraphNode {
  return {
    id,
    session_id: SESSION,
    node_type,
    title,
    summary: "",
    status,
    actor_agent_id: null,
    payload,
    created_at: "2026-08-06T00:00:00Z",
    updated_at: "2026-08-06T00:00:00Z",
    ...extra,
  };
}

function e(
  from_node_id: string,
  to_node_id: string,
  edge_type: ResearchGraphEdge["edge_type"],
): ResearchGraphEdge {
  return {
    id: `${from_node_id}->${to_node_id}:${edge_type}`,
    session_id: SESSION,
    from_node_id,
    to_node_id,
    edge_type,
    created_at: "2026-08-06T00:00:00Z",
  };
}

export const DISPUTE_SESSION_ID = SESSION;

export const DISPUTE_NODES: ResearchGraphNode[] = [
  // Dispute root — open (bucket A), delivery-blocking.
  n("dispute-1", "dispute", "统计口径：月活的算法分歧", "investigating", {
    conflict_type: "measurement",
    severity: "high",
    impact_scope: "delivery_metrics",
    blocking: true,
  }),
  // 3 positions: P1 supports, P2 contradicts, P3 conditional/refines.
  n(
    "pos-1",
    "dispute_position",
    "P1：月活=去重设备数",
    "proposed",
    { stance: "supports", claim_ids: ["claim-x"], evidence_ids: ["ev-1"], author: "agent-01" },
    { title: "P1" },
  ),
  n(
    "pos-2",
    "dispute_position",
    "P2：月活=活跃会话数",
    "proposed",
    { stance: "contradicts", claim_ids: ["claim-x"], evidence_ids: ["ev-2", "ev-3"], author: "agent-02" },
    { title: "P2" },
  ),
  n(
    "pos-3",
    "dispute_position",
    "P3：按登录频次分层口径",
    "proposed",
    { stance: "conditional", claim_ids: ["claim-x"], evidence_ids: ["ev-4"], author: "agent-03" },
    { title: "P3" },
  ),
  // Evidence fan.
  n("ev-1", "finding", "设备去重日志采样", "done", {}, {}),
  n("ev-2", "finding", "会话级埋点快照", "done", {}, {}),
  n("ev-3", "finding", "多端同人重复计数", "done", {}, {}),
  n("ev-4", "finding", "登录频次分布表", "done", {}, {}),
  // Deliberation spine with 4 turns (≥3) carrying progress markers.
  n("delib-1", "deliberation", "月活口径讨论", "deadlocked", {
    participant_ids: ["agent-01", "agent-02", "agent-03"],
    progress_level: 0.4,
    turn_count: 4,
    budget: 6,
    deadlock_reason: "双方证据互斥",
    escalation_reason: "30 分钟内无法收敛",
  }),
  n("turn-1", "deliberation_turn", "T1 · 定义澄清", "done", {
    marker: "evidence_added",
    position: "月活=去重设备数",
    evidence_ids: ["ev-1"],
  }),
  n("turn-2", "deliberation_turn", "T2 · 会话口径反例", "done", {
    marker: "position_changed",
    position: "月活=活跃会话数",
    evidence_ids: ["ev-2", "ev-3"],
  }),
  n("turn-3", "deliberation_turn", "T3 · 分层口径提出", "done", {
    marker: "scope_refined",
    position: "按登录频次分层",
    evidence_ids: ["ev-4"],
  }),
  n("turn-4", "deliberation_turn", "T4 · 僵局", "done", {
    marker: "no_change",
    challenge: "设备去重无法解释多端重复",
  }),
  // Escalation to Research Director (罗纳尔多) via lead_adjudication task.
  n("director-1", "agent_activity", "研究总监（罗纳尔多）裁决", "running", {
    task_kind: "lead_adjudication",
  }),
  // Decision node (bucket C) + reopen (bucket D).
  n(
    "decision-1",
    "decision",
    "裁决：以分层口径为准，含条件",
    "current",
    {
      verdict: "conditionally_resolved",
      conditions: ["月度留存≥60% 时采用", "低频层按 30 天活跃"],
      residual_impact: "分频层计数依赖登录日志完整性",
      decided_by: "director-1",
    },
    { title: "D1" },
  ),
  n(
    "decision-2",
    "decision",
    "历史裁决：以去重设备数为主",
    "superseded",
    {
      verdict: "obsolete",
      conditions: [],
      residual_impact: "",
      decided_by: "agent-01",
    },
    { title: "D0" },
  ),
];

export const DISPUTE_EDGES: ResearchGraphEdge[] = [
  // claims / positions typed edges.
  e("pos-1", "dispute-1", "supports"),
  e("pos-2", "dispute-1", "contradicts"),
  e("pos-3", "dispute-1", "refines"),
  // evidence fan → positions.
  e("ev-1", "pos-1", "supports"),
  e("ev-2", "pos-2", "supports"),
  e("ev-3", "pos-2", "supports"),
  e("ev-3", "pos-1", "contradicts"),
  e("ev-4", "pos-3", "supports"),
  // challenge.
  e("turn-4", "pos-2", "challenged_by"),
  // deliberation attachment.
  e("dispute-1", "delib-1", "discussed_by"),
  e("turn-1", "delib-1", "discussed_by"),
  e("turn-2", "delib-1", "discussed_by"),
  e("turn-3", "delib-1", "discussed_by"),
  e("turn-4", "delib-1", "discussed_by"),
  // escalation → director.
  e("delib-1", "director-1", "escalated_to"),
  // decision + reopen history.
  e("decision-1", "dispute-1", "resolved_by"),
  e("decision-2", "dispute-1", "supersedes"),
  // reopen: new contradicting evidence returns dispute to investigating.
  e("ev-3", "decision-1", "invalidates"),
];

/** All nodes contained in the dispute subgraph (for canvas/detail render tests). */
export const DISPUTE_FIXTURE = {
  session_id: SESSION,
  nodes: DISPUTE_NODES,
  edges: DISPUTE_EDGES,
};
