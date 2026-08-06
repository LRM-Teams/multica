/**
 * LRM-1446 — trajectory motion animator: intent → scoped CSS transform/opacity.
 *
 * Parent LRM-1393. Slice 2 on top of the LRM-1400 intent/budget layer
 * (`trajectory-motion-intents.ts`, already on dev).
 *
 * Scope: turn a set of active motion intents plus the previous/next lane
 * layouts into concrete, per-target animation directives (transform, opacity,
 * transition, degradation flags) that a component can apply as scoped classes
 * / inline styles. This file is deliberately side-effect-free and UI-free: no
 * DOM, no RAF, no React. The consumer maps directives → CSS.
 *
 * Hard rules mapped from LRM-1393 AC / LRM-1400 AC:
 *  - Animation only affects transform/opacity; it never mutates layout values.
 *  - Same-lane budget-coalesced intents emit ONE directive (no re-trigger).
 *  - Cancelled intents emit nothing.
 *  - reduced-motion → all transforms become "none" (zero path displacement),
 *    static highlight/status kept.
 *  - low-performance → directive carries lowPerformance (caller drops to
 *    30fps / disables glow); visual semantics preserved.
 *  - Single-element duration capped at 320ms.
 */

import type { TrajectoryLaneLayout } from "@multica/core/research";
import type { TrajectoryMotionIntent } from "./trajectory-motion-intents";
import { DEFAULT_TRAJECTORY_MOTION_BUDGET_MS } from "./trajectory-motion-intents";

/** A resolved, ready-to-apply animation directive for a single target. */
export interface TrajectoryAnimationDirective {
  /** Commit or segment id the directive applies to. */
  targetId: string;
  kind: TrajectoryMotionIntent["kind"];
  /** CSS transform for the "start" frame; reduced-motion ⇒ "none". */
  transform: string;
  /** CSS transform for the "settled" frame. */
  targetTransform: string;
  /** CSS opacity for the "start" frame. */
  opacity: number;
  /** CSS opacity for the "settled" frame. */
  targetOpacity: number;
  /**
   * `transition: <transform|opacity> <dur>ms <ease> <delay>ms`. Under
   * reduced-motion this is emptied so nothing animates.
   */
  transition: string;
  /**
   * Low-performance degradation: caller should drop to 30fps and disable glow.
   * Visual semantics (position/highlight/status text) remain intact.
   */
  lowPerformance: boolean;
  /**
   * Optional static highlight/status marker kept even under reduced-motion.
   * The consumer renders these as persistent classes/labels.
   */
  highlight?: string;
}

/** Animate within a 320ms cap and a light ease-out curve. */
export const TRAJECTORY_MOTION_MAX_SINGLE_DURATION_MS = 320;
export const TRAJECTORY_MOTION_EASING = "cubic-bezier(0.22, 1, 0.36, 1)";

function fmt(v: number): string {
  return `${Math.round(v)}px`;
}

/**
 * Resolve the movement direction for a target by diffing the old vs new
 * layout positions. Lane differences map to X, row differences to Y. Commits
 * that only exist in the new layout (newly grown/appended) originate from
 * their parent in the new layout, else from their own new position.
 * Returns { dx, dy } normalized sign/magnitude springboard — callers combine
 * with the intent's displacement magnitude.
 */
function resolveDirection(
  next: TrajectoryLaneLayout,
  prev: TrajectoryLaneLayout,
  targetId: string,
  kind: TrajectoryMotionIntent["kind"],
): { px: number; py: number } {
  const nextCommit = next.commits.find((c) => c.id === targetId);
  if (!nextCommit) {
    // Segment targets (merge-flow) carry their own geometry via segments.
    const seg = next.segments.find((s) => s.id === targetId);
    if (!seg) return { px: 0, py: 0 };
    const dx = seg.to.lane - seg.from.lane;
    const dy = seg.to.row - seg.from.row;
    return { px: Math.sign(dx), py: Math.sign(dy) };
  }
  const prevCommit = prev.commits.find((c) => c.id === targetId);
  if (prevCommit) {
    // Existing commit moved (e.g. re-anchored by a new grow): diff real motion.
    const dx = nextCommit.lane - prevCommit.lane;
    const dy = nextCommit.row - prevCommit.row;
    return { px: Math.sign(dx), py: Math.sign(dy) };
  }
  // New commit: originate from its parent's position in the new layout.
  const parent = next.commits.find((c) => next.segments.some((s) => s.toCommitId === targetId && s.fromCommitId === c.id));
  if (parent) {
    return { px: Math.sign(nextCommit.lane - parent.lane), py: Math.sign(nextCommit.row - parent.row) };
  }
  // Default: grow downward along the lane (append).
  return { px: 0, py: kind === "merge-flow" ? -1 : 1 };
}

function transformFor(pxDelta: number, pyDelta: number, displacementPx: number): string {
  return `translate(${fmt(pxDelta * displacementPx)}, ${fmt(pyDelta * displacementPx)})`;
}

/**
 * Convert a list of active (non-cancelled) trajectory intents plus the
 * previous/next lane layouts into animation directives.
 *
 * @param intents  Active intents (see `activeTrajectoryIntents`).
 * @param prev     Previous committed lane layout (before the WS/local update).
 * @param next     Current lane layout (after the update).
 * @param nowMs    Current wall-clock for delay derivation.
 *
 * Rules applied inside:
 *  - Each coalesced intent (per lane+kind, within the budget window the intent
 *    layer already produced) emits exactly one directive per target id.
 *  - check-out-focus emits a static highlight with zero movement.
 *  - reduced-motion resolution happens through the intent's own normalized
 *    `displacementPx` (LRM-1400 already forces it to 0), and we additionally
 *    suppress transition so nothing moves visually.
 *  - low-performance flows through.
 */
export function resolveTrajectoryMotionDirectives(
  intents: readonly TrajectoryMotionIntent[],
  prev: TrajectoryLaneLayout,
  next: TrajectoryLaneLayout,
  nowMs: number,
): TrajectoryAnimationDirective[] {
  const directives: TrajectoryAnimationDirective[] = [];
  for (const intent of intents) {
    if (intent.cancelled) continue;

    const reduced = intent.displacementPx === 0;

    for (const targetId of intent.targetIds) {
      // check-out-focus: zero-motion static highlight, opacity pulse only.
      if (intent.kind === "checkout-focus") {
        directives.push({
          targetId,
          kind: intent.kind,
          transform: "none",
          targetTransform: "none",
          opacity: 0.72,
          targetOpacity: 1,
          transition: "",
          lowPerformance: intent.lowPerformance,
          highlight: "trajectory-focus",
        });
        continue;
      }

      const { px, py } = resolveDirection(next, prev, targetId, intent.kind);
      const startTransform = reduced ? "none" : transformFor(px, py, intent.displacementPx);
      const delay = Math.max(0, Math.min(DEFAULT_TRAJECTORY_MOTION_BUDGET_MS, nowMs - intent.createdAtMs));

      directives.push({
        targetId,
        kind: intent.kind,
        transform: startTransform,
        targetTransform: "none",
        opacity: 0.4,
        targetOpacity: 1,
        transition: reduced
          ? ""
          : `transform ${TRAJECTORY_MOTION_MAX_SINGLE_DURATION_MS}ms ${TRAJECTORY_MOTION_EASING} ${delay}ms, opacity ${TRAJECTORY_MOTION_MAX_SINGLE_DURATION_MS}ms ${TRAJECTORY_MOTION_EASING} ${delay}ms`,
        lowPerformance: intent.lowPerformance,
        // Reduced-motion keeps a static status label so semantics survive.
        highlight: reduced && intent.status ? "trajectory-status" : undefined,
      });
    }
  }
  return directives;
}

/** Per-lane budget summary for tests / observability (not for rendering). */
export function trajectoryBudgetSummary(intents: readonly TrajectoryMotionIntent[]): {
  lane: string;
  kinds: TrajectoryMotionIntent["kind"][];
  counts: number;
}[] {
  const byLane = new Map<string, { kinds: Set<TrajectoryMotionIntent["kind"]>; counts: number }>();
  for (const i of intents) {
    if (i.cancelled) continue;
    const entry = byLane.get(i.lane) ?? { kinds: new Set(), counts: 0 };
    entry.kinds.add(i.kind);
    entry.counts += 1;
    byLane.set(i.lane, entry);
  }
  return Array.from(byLane.entries()).map(([lane, v]) => ({ lane, kinds: Array.from(v.kinds), counts: v.counts }));
}

/** Convenience guard: is this directive one that moves the element at all? */
export function directiveHasMotion(d: TrajectoryAnimationDirective): boolean {
  return d.transition !== "" && d.transform !== "none";
}
