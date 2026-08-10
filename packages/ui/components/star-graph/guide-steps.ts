/**
 * StarGraphGuide steps — the three-phase D5 on-boarding content (LRM-1496).
 *
 * Kept in a plain, non-component module (mirroring `tier.ts` / `state.ts`) so
 * `star-graph-guide.tsx` stays Fast-Refresh-friendly
 * (react-doctor only-export-components) and the copy can be shared or tested
 * without importing a React component.
 */

export interface StarGraphGuideStep {
  /** Stable step key. */
  key: string;
  /** Short heading. */
  title: string;
  /** Plain-language body. */
  body: string;
}

export const STAR_GRAPH_GUIDE_STEPS: readonly StarGraphGuideStep[] = [
  {
    key: "levels",
    title: "成果等级",
    body: "大圆是已融合的成果，小圆点是在工作的 Agent。",
  },
  {
    key: "agent",
    title: "S 级 Agent",
    body: "小点是正在运行的 Agent：会显示工作、等待、受阻或已完成状态。",
  },
  {
    key: "lines",
    title: "连线与聚类",
    body: "实线表示支持或分解，虚线表示挑战或新方向；虚线圈代表真实成员的聚类边界。",
  },
];
