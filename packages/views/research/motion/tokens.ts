/**
 * LRM-1477 — Research V6 unified motion token source.
 *
 * Spec: docs/superpowers/specs/2026-08-06-research-live-canvas-motion-direction-spec.md §1
 *   (§1 common params authoritative per rewritten contract PR #2415 / 2582aca79)
 * Design owner: UI 设计·聊天体验 (LRM-1471 / UI-03).
 *
 * This module is the SINGLE token source for research motion. The legacy
 * per-module constants (node-enter-motion, canvas-reorg-motion,
 * semantic-aggregation-motion) keep their values but re-export from here so a
 * future migration has one place to edit. Values follow the documented spec so
 * visual behavior is unchanged during the transition.
 */

// ─── Core budget (spec §3) ──────────────────────────────────────────────────

/** Hard upper bound for any single-element animation (ms). */
export const MOTION_SINGLE_MAX_MS = 320;

/** Total budget for one batch of animations; anything past this truncates to 0 displacement (ms). */
export const MOTION_TOTAL_BUDGET_MS = 900;

/** Default enter/merge/escalate easing (ease-out-cubic). */
export const MOTION_EASING = "cubic-bezier(0.22, 1, 0.36, 1)";

/** Delay between adjacent nodes in one batch (ms). */
export const MOTION_STAGGER_MS = 40;

/** Max stagger steps before a large batch stops trailing (prevents runaway tail). */
export const MOTION_STAGGER_CAP = 6;

/** Batch start delay (ms). */
export const MOTION_START_MS = 80;

// ─── Per-verb durations (spec §1) ───────────────────────────────────────────
// Aligned to rewritten motion-direction-spec §1 (PR #2415):
//   node-enter 180ms · edge-draw 220ms · reorg-element ≤320ms ·
//   choreography ≤900ms · camera-focus 260ms easeOutCubic · queue-peak ≤64.

export const MOTION_NODE_ENTER_MS = 180;
export const MOTION_EDGE_DRAW_MS = 220;
export const MOTION_APPEAR_MS = 180; // node-enter
// Single-element reorg upper bound (reorg-element spec token).
export const MOTION_MERGE_MS = 320;
export const MOTION_CONFLICT_MS = 320;
export const MOTION_ESCALATE_MS = 320;
export const MOTION_STALE_MS = 300;
export const MOTION_REVISE_MS = 300;
export const MOTION_REAPPEAR_MS = 180;
export const MOTION_CAMERA_MS = 260; // camera-focus

// ─── Per-verb displacement / static-marker constants (spec §3, §2.2) ────────

/** Conflict relative separation (px). */
export const MOTION_CONFLICT_GAP_PX = 12;

/** Escalate up-rise (px). */
export const MOTION_ESCALATE_RISE_PX = 8;

/** Appear up-rise (px). */
export const MOTION_APPEAR_RISE_PX = 8;

/** Stale final opacity (kept, never fades to invisible). */
export const MOTION_STALE_OPACITY = 0.55;

// ─── Reduced Motion / low-perf (spec §4) ────────────────────────────────────

/** Uniform fade-in duration when reduced-motion or budget-truncated (ms). */
export const MOTION_REDUCED_FADE_MS = 200;

/** Low-performance target frame budget — one visual frame every N ms (~30fps). */
export const MOTION_LOW_PERF_FRAME_MS = 33;

/** Low-performance: budget shortened to half the full batch budget. */
export const MOTION_LOW_PERF_BUDGET_MS = Math.floor(MOTION_TOTAL_BUDGET_MS / 2);

/** Queue hard cap (spec §5.3). */
export const MOTION_QUEUE_CAP = 64;

// ─── Legacy aliases (migration path, spec §7) ───────────────────────────────
// Values identical to the existing modules they replace; keep the old names
// re-exported so existing callers keep compiling during the transition window.

export const NODE_ENTER_DURATION_MS = 280;
export const NODE_ENTER_STAGGER_MS = MOTION_STAGGER_MS;
export const NODE_ENTER_STAGGER_CAP = 10;

export const REORG_SINGLE_ELEMENT_MAX_MS = MOTION_SINGLE_MAX_MS;
export const REORG_TOTAL_BUDGET_MS = MOTION_TOTAL_BUDGET_MS;
export const REORG_EASING = MOTION_EASING;
export const REORG_LANE_STAGGER_CAP = MOTION_STAGGER_CAP;
export const REORG_LANE_STAGGER_MS = MOTION_STAGGER_MS;

export const SEMANTIC_MOTION_NODE_DURATION_MS = MOTION_MERGE_MS;
export const SEMANTIC_MOTION_TOTAL_BUDGET_MS = MOTION_TOTAL_BUDGET_MS;
export const SEMANTIC_MOTION_START_MS = MOTION_START_MS;
export const SEMANTIC_MOTION_STAGGER_MS = 36;
export const SEMANTIC_MOTION_STAGGER_CAP = MOTION_STAGGER_CAP;

// ─── D5 lifecycle verbs (LRM-1537 §3.1) ─────────────────────────────────────
// Three D5 lifecycle events were missing from the 10-kind map. Their durations
// inherit the closest existing families so every component stays on the shared
// token source and never hardcodes its own timing curve.

/** ⑤ 废弃 retire — grey-out + strikethrough + clickable history (same family as stale). */
export const MOTION_RETIRE_MS = MOTION_STALE_MS; // 300ms
/** ⑥ 重启 restart — short relation emphasis → weaken (edge-draw family). */
export const MOTION_RESTART_MS = 240; // 240ms (edge-draw family upper bound)
/** ⑦ 目标修改 regoal — impact-ordered playback, ≤ merge budget (180–320ms). */
export const MOTION_REGOAL_MS = MOTION_MERGE_MS; // 320ms
/** Restart relation emphasis retention (ms) before it weakens to rest. */
export const MOTION_RESTART_EMPHASIS_MS = 600;
/** Goal-modification highlight ceiling (ms) for the per-version highlight set. */
export const MOTION_REGOAL_HIGHLIGHT_MS = 600;
/** Stagger cap for a batch of D5 lifecycle events (LRM-1537 §3.4). */
export const MOTION_D5_STAGGER_CAP = MOTION_STAGGER_CAP; // ≤6

// ─── D5 motion layer tokens (LRM-1537 §3.2) ─────────────────────────────────
// An explicit presentation-only layering so concurrent priorities do not fight
// for the same glow/ring. NEVER written back to canonical (LRM-1471 boundary).

export type MotionLayer =
  | "ambient" // low-frequency progress tick / ambient pulse (dot token `pulse`)
  | "canonical" // lifecycle transition (dispatch / outcome / merge / retire…)
  | "focus" // user direct operation (selection / expand / chat-card link)
  | "blocking"; // error / obstruction / restart emphasis

/** Layer precedence, highest priority last; low-priority is cancelled by higher. */
export const MOTION_LAYER_PRIORITY: readonly MotionLayer[] = [
  "ambient",
  "canonical",
  "blocking",
  "focus",
];

// ─── D5 glow concurrency limits (LRM-1537 §3.3) ─────────────────────────────
// Simultaneous active visual emphasis is bounded so a burst never floods the
// whole screen with rings/glows at once.

export const MOTION_GLOW_MAX_ACTIVE = 1; // one active transition glow
export const MOTION_GLOW_MAX_SELECTED = 1; // one selected ring
export const MOTION_GLOW_MAX_BLOCKING = 1; // one blocking mark
export const MOTION_GLOW_MAX_ROUTE_PROBE = 2; // two route probes
