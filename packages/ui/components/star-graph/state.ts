/**
 * Star-graph node state matrix (LRM-1496 — D5 five-level node visual system).
 *
 * Pure design-token module, part of `@multica/ui/components/star-graph`.
 * It encodes the full D5 state surface for the circular tiered nodes —
 * default / hover / selected / focus / run / stable / pending-review /
 * conflict / abandoned / restart / failed.
 *
 * Every state pairs a semantic class with a TEXT label and an optional
 * non-color-only signal (glyph shape / border line style / animation), so the
 * state is never communicated by colour alone (D5 acceptance requirement).
 *
 * Like `tier.ts` this file must stay free of domain logic and MUST NOT import
 * `@multica/core`. The status→state mapping lives in
 * `packages/views/research/star-graph`.
 */

export const STAR_GRAPH_NODE_STATES = [
  "default",
  "hover",
  "selected",
  "focus",
  "run",
  "stable",
  "pending-review",
  "conflict",
  "abandoned",
  "restart",
  "failed",
] as const;

export type StarGraphNodeState = (typeof STAR_GRAPH_NODE_STATES)[number];

/** Non-colour codec — the shape/glyph that reinforces a state beyond hue. */
export type StarGraphStateGlyph =
  | "none"
  | "pulse" // run: animated pulse ring
  | "check" // stable/complete
  | "spinner" // pending-review / waiting
  | "exclaim" // conflict / attention
  | "ban" // failed / abandoned
  | "restart" // restart loop arrow
  | "dot"; // generic status dot

export interface StarGraphStateToken {
  state: StarGraphNodeState;
  /** Human-facing, readable aria label / badge text. */
  label: string;
  /** Aria label that reads the state in a sentence. */
  ariaLabel: string;
  /** Non-colour-only reinforcement glyph. */
  glyph: StarGraphStateGlyph;
  /** Semantic border line style: solid / dashed / dotted. */
  lineStyle: "solid" | "dashed" | "dotted";
  /** Extra animation class (pulse/restart) or "none". */
  animation: "pulse" | "restart" | "none";
  /** Whether the state renders on top of the node core. */
  overlays: boolean;
}

const STATE_TOKENS: Record<StarGraphNodeState, StarGraphStateToken> = {
  default: {
    state: "default",
    label: "就绪",
    ariaLabel: "就绪",
    glyph: "none",
    lineStyle: "solid",
    animation: "none",
    overlays: false,
  },
  hover: {
    state: "hover",
    label: "悬停",
    ariaLabel: "悬停，可点击打开",
    glyph: "none",
    lineStyle: "solid",
    animation: "none",
    overlays: false,
  },
  selected: {
    state: "selected",
    label: "已选中",
    ariaLabel: "已选中",
    glyph: "dot",
    lineStyle: "solid",
    animation: "none",
    overlays: true,
  },
  focus: {
    state: "focus",
    label: "已聚焦",
    ariaLabel: "已聚焦，按回车打开",
    glyph: "none",
    lineStyle: "solid",
    animation: "none",
    overlays: true,
  },
  run: {
    state: "run",
    label: "运行中",
    ariaLabel: "正在运行",
    glyph: "pulse",
    lineStyle: "dashed",
    animation: "pulse",
    overlays: true,
  },
  stable: {
    state: "stable",
    label: "稳定",
    ariaLabel: "已稳定",
    glyph: "check",
    lineStyle: "solid",
    animation: "none",
    overlays: false,
  },
  "pending-review": {
    state: "pending-review",
    label: "待复核",
    ariaLabel: "待复核",
    glyph: "spinner",
    lineStyle: "dotted",
    animation: "none",
    overlays: true,
  },
  conflict: {
    state: "conflict",
    label: "冲突",
    ariaLabel: "存在冲突",
    glyph: "exclaim",
    lineStyle: "dashed",
    animation: "none",
    overlays: true,
  },
  abandoned: {
    state: "abandoned",
    label: "已废弃",
    ariaLabel: "已废弃",
    glyph: "ban",
    lineStyle: "dotted",
    animation: "none",
    overlays: true,
  },
  restart: {
    state: "restart",
    label: "重启中",
    ariaLabel: "正在重启",
    glyph: "restart",
    lineStyle: "dashed",
    animation: "restart",
    overlays: true,
  },
  failed: {
    state: "failed",
    label: "失败",
    ariaLabel: "失败",
    glyph: "ban",
    lineStyle: "dashed",
    animation: "none",
    overlays: true,
  },
};

export function starGraphStateToken(state: StarGraphNodeState): StarGraphStateToken {
  return STATE_TOKENS[state];
}

/** Priority when multiple states compete — highest wins, never double-highlight. */
const STATE_PRIORITY: Record<StarGraphNodeState, number> = {
  failed: 11,
  conflict: 10,
  abandoned: 9,
  restart: 8,
  run: 7,
  "pending-review": 6,
  selected: 5,
  focus: 4,
  hover: 3,
  stable: 2,
  default: 0,
};

/**
 * Resolve competing state signals to a single winner by priority, mirroring
 * the established LRM-1469 conflict rule so the node never stacks competing
 * highlights. `focus` is downgraded to `selected` (focus is ephemeral, driven
 * by the component's own `:focus-visible`, not queued alongside business state).
 */
export function resolveStarGraphState(signals: StarGraphNodeState[]): StarGraphNodeState {
  const [first, ...rest] = signals;
  if (!first) return "default";
  let winner = first;
  for (const s of rest) {
    if (STATE_PRIORITY[s] > STATE_PRIORITY[winner]) winner = s;
  }
  return winner;
}

/** Whether a state needs an overlaid badge above the node core. */
export function starGraphStateOverlays(state: StarGraphNodeState): boolean {
  return STATE_TOKENS[state].overlays;
}
