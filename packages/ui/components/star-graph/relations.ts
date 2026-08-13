/**
 * StarGraph relation legend tokens (LRM-1496).
 *
 * Kept in a plain, non-component module (mirroring `tier.ts` / `state.ts`) so
 * `star-graph-map-key.tsx` stays Fast-Refresh-friendly
 * (react-doctor only-export-components) and the relation semantics can be
 * shared or tested without importing a React component.
 */

export type StarGraphRelationKey = "decompose" | "support" | "challenge" | "newdir";

export interface StarGraphRelationToken {
  /** Stable key. */
  key: StarGraphRelationKey;
  /** Short label shown in the Map Key. */
  label: string;
  /** Plain-language hover/focus explanation. */
  description: string;
  /** CSS class for the line demo (support/challenge/newdir). */
  demoClass: string;
}

export const STAR_GRAPH_RELATIONS: readonly StarGraphRelationToken[] = [
  {
    key: "decompose",
    label: "分解",
    description: "实线：一个目标/方向分解出多个子方向或子任务。",
    demoClass: "sg-line-demo",
  },
  {
    key: "support",
    label: "支持",
    description: "绿色实线：一个成果支持另一个成果或结论。",
    demoClass: "sg-line-demo sg-support",
  },
  {
    key: "challenge",
    label: "挑战",
    description: "橙色虚线：一个成果挑战或反驳另一个结论。",
    demoClass: "sg-line-demo sg-challenge",
  },
  {
    key: "newdir",
    label: "新方向",
    description: "紫色虚线：与既有成果无关的全新探索方向。",
    demoClass: "sg-line-demo sg-newdir",
  },
  {
    key: "neutral",
    label: "其他关系",
    description: "灰色点线：未归类或新版本关系；保留原始类型，不解释为支持或挑战。",
    demoClass: "sg-line-demo sg-neutral",
  },
];
