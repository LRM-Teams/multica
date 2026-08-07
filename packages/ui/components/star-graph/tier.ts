/**
 * Star-graph tier token module (LRM-1496 — D5 five-level node visual system).
 *
 * This is a *pure design-token* module inside `@multica/ui`. It only encodes
 * the visual contract of the five circular tiers — size, concentric rings,
 * glow strength, accent tone and the human-facing level label. It contains NO
 * domain logic: it never decides which research entity maps to which tier,
 * never reads a node id / kind / status key, and never imports `@multica/core`.
 * The tier assignment lives in `packages/views/research/star-graph`.
 *
 * Geometries mirror the D5 reference (`multica-research-constellation-v2.html`)
 * but expressed as Tailwind/utility-friendly semantic tokens, never as raw hex
 * in component logic.
 */

export const STAR_GRAPH_TIERS = ["xxl", "xl", "l", "m", "s"] as const;

export type StarGraphTier = (typeof STAR_GRAPH_TIERS)[number];

export interface StarGraphTierToken {
  /** Stable tier key. */
  tier: StarGraphTier;
  /** Human-facing label (short; used in Map Key). */
  label: string;
  /** Long explanation used for Map Key hover/focus and guide. */
  description: string;
  /** Outer circle diameter, in CSS px. */
  sizePx: number;
  /** Number of concentric decorative rings inside the node. */
  ringCount: 0 | 1 | 2 | 3;
  /** Glow intensity 0..3 (0 = none, 3 = strongest soft halo). */
  glow: 0 | 1 | 2 | 3;
  /** Whether this tier renders a `role="group"` header band. */
  hasHeader: boolean;
  /** Semantic accent family used for the node. */
  accent: "synthesis" | "stable" | "result" | "agent";
}

const TIER_TOKENS: Record<StarGraphTier, StarGraphTierToken> = {
  xxl: {
    tier: "xxl",
    label: "XXL",
    description: "最终综合成果：最大节点，多重同心圆与柔和光晕，展示跨轮融合的总结论。",
    sizePx: 248,
    ringCount: 3,
    glow: 3,
    hasHeader: true,
    accent: "synthesis",
  },
  xl: {
    tier: "xl",
    label: "XL",
    description: "稳定主结论：大圆双环、绿色强调，展示文档数、置信度与结论数。",
    sizePx: 220,
    ringCount: 2,
    glow: 2,
    hasHeader: true,
    accent: "stable",
  },
  l: {
    tier: "l",
    label: "L",
    description: "较大中间成果：展示标题、轮次与关键指标。",
    sizePx: 168,
    ringCount: 1,
    glow: 1,
    hasHeader: true,
    accent: "result",
  },
  m: {
    tier: "m",
    label: "M",
    description: "中等中间成果：尺寸更小的圆形，展示标题与轮次/文档数。",
    sizePx: 96,
    ringCount: 0,
    glow: 1,
    hasHeader: true,
    accent: "result",
  },
  s: {
    tier: "s",
    label: "S",
    description: "正在运行的 Agent：小圆点/小圆，展示任务短名与工作/等待/受阻/完成状态。",
    sizePx: 58,
    ringCount: 0,
    glow: 1,
    hasHeader: false,
    accent: "agent",
  },
};

export function starGraphTierToken(tier: StarGraphTier): StarGraphTierToken {
  return TIER_TOKENS[tier];
}

/** Iterate tiers from largest to smallest (for Map Key layout). */
export const STAR_GRAPH_TIERS_LARGE_TO_SMALL: readonly StarGraphTier[] = [
  ...STAR_GRAPH_TIERS,
];

/** Guard: a raw tier string is one of the five known tiers. */
export function isStarGraphTier(value: string): value is StarGraphTier {
  return (STAR_GRAPH_TIERS as readonly string[]).includes(value);
}
