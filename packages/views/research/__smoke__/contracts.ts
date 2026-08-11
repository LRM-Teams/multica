/**
 * Parallel FE smoke contracts for LRM-1117.
 * Test-only helpers — no production imports from this file.
 * Implementation PRs (1104 / 1109 / 1100 / 1105 / 1091) should flip matching
 * `it.fails` cases in `parallel-regression.matrix.test.tsx` to `it` once green.
 */

export const SMOKE_ISSUES = {
  listDuplicate: "LRM-1104",
  breakpoints: "LRM-1109",
  overlayA11y: "LRM-1100",
  canvasKeyboard: "LRM-1105",
  /** Planar topology + action visibility — flip after LRM-1091 lands. */
  canvasPlanar: "LRM-1091",
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
 * LRM-1105 keyboard map freeze (parent LRM-1102).
 * Slice 1 (#1952): pure helpers — hard-gated.
 * Slice 2 (#1968): canvas-keyboard-nav pure module — hard-gated via helpers.
 * Slice 3 (#2010 / LRM-1190): Home/End via resolveCanvasKeyEvent — hard-gated.
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

/**
 * LRM-1091 planar keyboard AC (product increment on top of LRM-1105 freeze).
 * #1956 shipped layout + arrow/Enter/Esc/Shift+F10 — hard-gated.
 * Legacy planar graph-node chrome removed with D5 migration.
 */
export const PLANAR_KEYBOARD_CONTRACT = {
  ArrowUp: "topology-prev",
  ArrowDown: "topology-next",
  ArrowLeft: "branch-prev",
  ArrowRight: "branch-next",
  Enter: "open-detail-drawer",
  Escape: "dismiss-layer",
  "Shift+F10": "open-context-menu",
} as const;

/** Card AABB used by the 30-node no-overlap / no-pierce smoke. */
export type SmokeRect = { id: string; x: number; y: number; w: number; h: number };

export function rectsOverlap(a: SmokeRect, b: SmokeRect, epsilon = 0.5): boolean {
  return (
    a.x < b.x + b.w - epsilon &&
    a.x + a.w - epsilon > b.x &&
    a.y < b.y + b.h - epsilon &&
    a.y + a.h - epsilon > b.y
  );
}

export function findOverlappingPairs(rects: SmokeRect[]): Array<[string, string]> {
  const pairs: Array<[string, string]> = [];
  for (let i = 0; i < rects.length; i++) {
    for (let j = i + 1; j < rects.length; j++) {
      if (rectsOverlap(rects[i]!, rects[j]!)) {
        pairs.push([rects[i]!.id, rects[j]!.id]);
      }
    }
  }
  return pairs;
}

/**
 * Branch accent (fork/lane chrome) must not reuse status token colors.
 * Smoke asserts string inequality after 1091 exposes stable CSS vars / classes.
 */
export const BRANCH_VS_STATUS_COLOR_CONTRACT = {
  branchTokens: ["--branch-fork", "--branch-lane"] as const,
  statusTokens: ["--success", "--warning", "--destructive", "--muted-foreground"] as const,
} as const;

/** Action visibility + destructive safety hooks (status/permission gated). */
export const ACTION_VISIBILITY_CONTRACT = {
  gatedByStatusOrPermission: true,
  destructiveNeedsConfirmOrUndo: true,
  destructiveActionIds: ["delete", "abandon", "force_stop"] as const,
} as const;

export function failHint(issue: string, detail: string): string {
  return `[${issue}] ${detail}`;
}
