import type { ResearchGraphEdge, ResearchGraphNode } from "@multica/core/types";

/**
 * LRM-1091 AC fixture: 30 nodes / 3 branches / 2 merges.
 * Matches LRM-1116 prototype topology (laneSeq + fork/merge points).
 */
const TITLES = [
  "界定网页游戏范围",
  "对标传奇网页版玩法",
  "盘点人员角色",
  "服务器与环境基线",
  "技术栈候选 A",
  "技术栈候选 B",
  "客户端渲染路径",
  "实时同步方案",
  "战斗循环草案",
  "经济系统风险",
  "美术管线估算",
  "运维与发布",
  "安全与反外挂",
  "分叉：轻量原型",
  "分叉：完整模拟",
  "轻量：UI 壳",
  "轻量：战斗 tick",
  "轻量：联网最小集",
  "完整：场景流",
  "完整：数值表",
  "完整：匹配服",
  "汇合：选型决策",
  "验证：压测计划",
  "验证：成本模型",
  "验证：里程碑",
  "汇合：交付大纲",
  "交付：人员清单",
  "交付：环境清单",
  "交付：风险与 dig",
  "最终交付卡",
] as const;

/** Explicit expected lanes for the fixture (product prototype). */
export const FIXTURE_LANE_SEQ = [
  0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 0–12 main
  1, 2, // 13–14 fork
  1, 1, 1, 2, 2, 2, // 15–20
  0, // 21 merge1
  2, 2, 2, // 22–24
  0, // 25 merge2
  0, 0, 0, 0, // 26–29
] as const;

function ts(i: number): string {
  return `2026-08-03T00:${String(i).padStart(2, "0")}:00Z`;
}

function nodeTypeForIndex(i: number): ResearchGraphNode["node_type"] {
  if (i === 0) return "goal";
  if (i === 21 || i === 25) return "stage_gate";
  if (i === 19) return "dead_end";
  if (i >= 22 && i <= 24) return "conflict";
  if (i >= 13 && i <= 20) return "probe";
  if (i >= 26) return "finding";
  return "subquestion";
}

function statusForIndex(i: number): string {
  if (i === 16) return "running";
  if (i === 19) return "failed";
  if (i > 25) return "pending";
  return "done";
}

/**
 * Edges encode the prototype forks/merges:
 * linear main 0→12, fork 12→13 and 12→14, explore 13→15→16→17,
 * verify 14→18→19→20, merge1 17→21 & 20→21, verify 21→22→23→24,
 * merge2 24→25, main 21→25→26→…→29.
 */
export function buildPlanarFixture30(): {
  nodes: ResearchGraphNode[];
  edges: ResearchGraphEdge[];
} {
  const sessionId = "fixture-planar-30";
  const nodes: ResearchGraphNode[] = TITLES.map((title, i) => ({
    id: `n${i}`,
    session_id: sessionId,
    node_type: nodeTypeForIndex(i),
    title,
    summary: `fixture row ${i}`,
    status: statusForIndex(i),
    actor_agent_id: null,
    payload: {
      logic_lane:
        FIXTURE_LANE_SEQ[i] === 0
          ? "orchestrate"
          : FIXTURE_LANE_SEQ[i] === 1
            ? "source"
            : "validate",
      owner: ["Ava", "Ben", "Chen", "Dia", "Eden"][i % 5],
      phase: `S${Math.min(4, Math.floor(i / 8) + 1)}`,
    },
    created_at: ts(i),
    updated_at: ts(i),
  }));

  const edge = (
    id: string,
    from: number,
    to: number,
  ): ResearchGraphEdge => ({
    id,
    session_id: sessionId,
    from_node_id: `n${from}`,
    to_node_id: `n${to}`,
    edge_type: "leads_to",
    created_at: ts(Math.max(from, to)),
  });

  const edges: ResearchGraphEdge[] = [];
  // Main spine 0→12
  for (let i = 0; i < 12; i++) edges.push(edge(`e-${i}-${i + 1}`, i, i + 1));
  // Fork from 12
  edges.push(edge("e-12-13", 12, 13));
  edges.push(edge("e-12-14", 12, 14));
  // Explore branch 13→15→16→17
  edges.push(edge("e-13-15", 13, 15));
  edges.push(edge("e-15-16", 15, 16));
  edges.push(edge("e-16-17", 16, 17));
  // Verify branch 14→18→19→20
  edges.push(edge("e-14-18", 14, 18));
  edges.push(edge("e-18-19", 18, 19));
  edges.push(edge("e-19-20", 19, 20));
  // Merge1 → 21
  edges.push(edge("e-17-21", 17, 21));
  edges.push(edge("e-20-21", 20, 21));
  // Verify continuation 21→22→23→24
  edges.push(edge("e-21-22", 21, 22));
  edges.push(edge("e-22-23", 22, 23));
  edges.push(edge("e-23-24", 23, 24));
  // Also keep main continuity into merge2 via 21→25 and 24→25
  edges.push(edge("e-21-25", 21, 25));
  edges.push(edge("e-24-25", 24, 25));
  // Tail 25→29
  for (let i = 25; i < 29; i++) edges.push(edge(`e-${i}-${i + 1}`, i, i + 1));

  return { nodes, edges };
}
