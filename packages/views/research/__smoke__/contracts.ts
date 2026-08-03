/**
 * Parallel FE smoke contracts for LRM-1117.
 * Test-only helpers — no production imports from this file.
 * Implementation PRs (1104 / 1109 / 1100 / 1105) should flip matching
 * `it.fails` cases in `parallel-regression.matrix.test.tsx` to `it` once green.
 */

export const SMOKE_ISSUES = {
  listDuplicate: "LRM-1104",
  breakpoints: "LRM-1109",
  overlayA11y: "LRM-1100",
  canvasKeyboard: "LRM-1105",
} as const;

/** useIsMobile / Tailwind md boundary (packages/ui/hooks/use-mobile.ts). */
export const MOBILE_BREAKPOINT_PX = 768;

/** Acceptance viewports for LRM-1109 (dead-zone mid = 700; switch edges = 767/768). */
export const BREAKPOINT_SMOKE_WIDTHS = [360, 700, 767, 768] as const;

export function isMobileViewport(widthPx: number, breakpoint = MOBILE_BREAKPOINT_PX): boolean {
  return widthPx < breakpoint;
}

/**
 * LRM-1104: goal chip must not restate the row title.
 * Equal or either side a prefix of the other (after truncate/whitespace collapse)
 * counts as redundant.
 */
export function isGoalChipRedundant(titleText: string, goalSummary: string): boolean {
  const title = titleText.trim().replace(/\s+/g, " ");
  const goal = goalSummary.trim().replace(/\s+/g, " ");
  if (!title || !goal) return false;
  const a = title.replace(/…$/u, "");
  const b = goal.replace(/…$/u, "");
  if (!a || !b) return false;
  return a === b || a.startsWith(b) || b.startsWith(a);
}

/** LRM-1100 desktop overlay a11y checklist (aux + chat drawers). */
export const OVERLAY_A11Y_CONTRACT = {
  escapeCloses: true,
  focusMovesInOnOpen: true,
  focusRestoresOnClose: true,
  auxHasAriaLabelledby: true,
  chatHasAriaLabel: true,
} as const;

/**
 * LRM-1105 keyboard map freeze (parent LRM-1102). Smoke only — implementers
 * wire these in research-canvas / graph nodes.
 */
export const CANVAS_KEYBOARD_CONTRACT = {
  ArrowLeft: "main-chain-prev",
  ArrowRight: "main-chain-next",
  ArrowUp: "fork-cross-lane",
  ArrowDown: "fork-cross-lane",
  Enter: "open-detail-drawer",
  " ": "open-detail-drawer",
  ".": "open-action-ring",
  Escape: "dismiss-layer",
  "+": "zoom-in",
  "-": "zoom-out",
  "0": "zoom-reset",
  Home: "jump-first",
  End: "jump-last",
} as const;

export function failHint(issue: string, detail: string): string {
  return `[${issue}] ${detail}`;
}
