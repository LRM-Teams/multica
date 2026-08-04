/**
 * LRM-1208 gate-shot harness — mounts the REAL `ResearchGitList` so the lane
 * colors resolve through live CSS custom properties. jsdom cannot resolve
 * `var(--research-lane-N)` on an SVG `stroke` attribute or on inline
 * `style.borderColor`, which is exactly the surface this slice fixes.
 *
 * `?theme=dark` puts `.dark` on <html>; default is light.
 *
 * Temporary tooling: delete after the shots are attached to LRM-1208.
 */
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { I18nextProvider } from "react-i18next";
import type {
  ResearchGraphEdge,
  ResearchGraphNode,
} from "../../packages/core/types";
import { createI18n } from "../../packages/core/i18n/create-i18n";
import { ResearchGitList } from "../../packages/views/research/components/research-git-list";
import zhResearch from "../../packages/views/locales/zh-Hans/research.json";
import zhCommon from "../../packages/views/locales/zh-Hans/common.json";
import "./harness.css";

const i18n = createI18n("zh-Hans", {
  "zh-Hans": { research: zhResearch, common: zhCommon },
});

const SESSION = "lrm1208-lanes";
const ts = (i: number) =>
  new Date(Date.UTC(2026, 7, 1, 0, i)).toISOString();

/**
 * Spine of 3, then a 5-way fork so every lane token (1..5) renders, then a
 * merge back. Mirrors the real product topology shape (LRM-1116) rather than a
 * synthetic star.
 */
const TITLES = [
  "界定调研范围",
  "锁定可信来源池",
  "分叉点：五条并行取证路线",
  "路线 A：官方披露与年报",
  "路线 B：行业协会统计",
  "路线 C：一线渠道访谈",
  "路线 D：论文与专利检索",
  "路线 E：竞品公开定价抓取",
  "A：交叉校验份额口径",
  "B：口径差异归因",
  "C：渠道价格带回填",
  "D：技术路线可行性",
  "E：定价弹性初估",
  "汇合：证据一致性裁定",
  "交付大纲",
] as const;

const LANE_OF = [0, 0, 0, 0, 1, 2, 3, 4, 0, 1, 2, 3, 4, 0, 0] as const;
const STATUS_OF = [
  "completed",
  "completed",
  "completed",
  "completed",
  "running",
  "completed",
  "failed",
  "pending",
  "completed",
  "running",
  "completed",
  "failed",
  "pending",
  "running",
  "pending",
] as const;

const nodes: ResearchGraphNode[] = TITLES.map((title, i) => ({
  id: `n${i}`,
  session_id: SESSION,
  node_type: i === TITLES.length - 1 ? "deliverable" : "step",
  title,
  summary: `lane ${LANE_OF[i]} · row ${i}`,
  status: STATUS_OF[i]!,
  actor_agent_id: null,
  payload: { logic_lane: LANE_OF[i] === 0 ? "orchestrate" : "source" },
  created_at: ts(i),
  updated_at: ts(i),
}));

const edge = (from: number, to: number): ResearchGraphEdge => ({
  id: `e-${from}-${to}`,
  session_id: SESSION,
  from_node_id: `n${from}`,
  to_node_id: `n${to}`,
  edge_type: "leads_to",
  created_at: ts(Math.max(from, to)),
});

const edges: ResearchGraphEdge[] = [
  edge(0, 1),
  edge(1, 2),
  // 5-way fork
  edge(2, 3),
  edge(2, 4),
  edge(2, 5),
  edge(2, 6),
  edge(2, 7),
  // each route advances once
  edge(3, 8),
  edge(4, 9),
  edge(5, 10),
  edge(6, 11),
  edge(7, 12),
  // merge back to the spine, then deliverable
  edge(8, 13),
  edge(9, 13),
  edge(10, 13),
  edge(11, 13),
  edge(12, 13),
  edge(13, 14),
];

const params = new URLSearchParams(window.location.search);
if (params.get("theme") === "dark") {
  document.documentElement.classList.add("dark");
} else {
  document.documentElement.classList.add("light");
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <I18nextProvider i18n={i18n}>
      <div className="h-screen w-full" data-case="git-list">
        <ResearchGitList nodes={nodes} edges={edges} selectedId="n9" />
      </div>
    </I18nextProvider>
  </StrictMode>,
);
