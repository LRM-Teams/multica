/**
 * Deterministic per-agent identity color.
 *
 * Multi-agent group chats need every agent to be distinguishable at a glance.
 * A hash of the agent id picks a stable entry from a fixed palette, so the
 * same agent always renders the same color across sessions, devices and
 * surfaces — without the backend having to store a color.
 *
 * Palette values mirror the group-chat design (Figma "协作通讯"): a saturated
 * foreground for the monogram / bot icon, over a low-alpha tint of the same
 * hue for the avatar fill. These are deliberate identity colors (like label
 * colors), not theme styling — hence raw values applied via inline style
 * rather than semantic design tokens.
 *
 * Scope: avatars / monograms only. Body @mention tokens use the shared Slack
 * soft-bg token (`mentionTokenClassName`) — do not reuse this palette there.
 */
export interface AgentColor {
  /** Saturated color for the monogram text / bot icon. */
  fg: string;
  /** Low-alpha tint of the same hue for the avatar background. */
  bg: string;
}

export const AGENT_COLOR_PALETTE: readonly AgentColor[] = [
  { fg: "#50009B", bg: "rgba(80, 0, 155, 0.10)" }, // purple
  { fg: "#189B92", bg: "rgba(24, 155, 146, 0.10)" }, // teal
  { fg: "#F38E00", bg: "rgba(243, 142, 0, 0.18)" }, // orange
  { fg: "#155CFC", bg: "rgba(21, 92, 252, 0.10)" }, // blue
  { fg: "#C2255C", bg: "rgba(194, 37, 92, 0.10)" }, // magenta
  { fg: "#2F9E44", bg: "rgba(47, 158, 68, 0.12)" }, // green
];

/** djb2-style string hash → stable, well-distributed unsigned int. */
function hashId(id: string): number {
  let hash = 5381;
  for (let i = 0; i < id.length; i++) {
    hash = (hash * 33 + id.charCodeAt(i)) >>> 0;
  }
  return hash;
}

/**
 * Stable identity color for an agent id. Accepts null/undefined so callers
 * that hold optional ids (activity stacks, unfinished fixtures) never throw
 * mid-render — they land on the same slot as an empty string.
 */
export function agentColor(id: string | null | undefined): AgentColor {
  // The modulo always lands in range; the assertion satisfies
  // noUncheckedIndexedAccess without a meaningless runtime fallback.
  return AGENT_COLOR_PALETTE[hashId(id ?? "") % AGENT_COLOR_PALETTE.length]!;
}
