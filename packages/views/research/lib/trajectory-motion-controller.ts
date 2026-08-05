/**
 * LRM-1447 — trajectory motion controller: directive → per-element frame /
 * lifecycle pure state layer.
 *
 * Parent LRM-1393. Slice 3 on top of the already-merged LRM-1400 intent layer
 * (`trajectory-motion-intents.ts`) and LRM-1446 animator layer
 * (`trajectory-motion-animator.ts`).
 *
 * Scope: consume the animator's `TrajectoryAnimationDirective[]` and produce
 * renderable per-element frame/lifecycle state. This layer resolves the three
 * things the animator intentionally leaves open for a component:
 *
 *   1. **start → settle two frames**: one directive yields a *start* frame and
 *      a *settled* frame plus an active window; a consumer applies the start
 *      frame and swaps to the settled frame once the animation settles.
 *   2. **same-element merge / cancel**: multiple same-direction directives on
 *      one target coalesce into a single final transition; an interrupted
 *      directive snaps to its settled frame instead of flashing back to start.
 *   3. **lifecycle & cleanup**: once an animation expires the directive leaves
 *      the active set and the target is released; targets removed from the
 *      layout are cleaned up so nothing leaks.
 *
 * Like the intent/animator layers this file is deliberately side-effect-free
 * and UI-free: no DOM, no RAF, no React. reduced-motion / low-performance are
 * already normalized by the animator; this layer only passes the flags through
 * and never re-decides displacement.
 */

import type { TrajectoryMotionKind } from "./trajectory-motion-intents";
import type { TrajectoryAnimationDirective } from "./trajectory-motion-animator";

/** A single renderable CSS frame for a target. */
export interface TrajectoryFrame {
  transform: string;
  opacity: number;
  transition: string;
}

/** Phase of a target within the controller lifecycle. */
export type TrajectoryControllerPhase = "enter" | "settled";

/**
 * One live controller entry per target id.
 *
 * - `phase === "enter"`: the start frame applies now and the transition is in
 *   flight; the consumer swaps to `settled` at `activatesAtMs` (or on the
 *   transition end). Only produces a start frame while inside the window.
 * - `phase === "settled"`: no animation; only the settled frame is exposed.
 *   Also the representation of reduced-motion / cancelled-snap (zero motion).
 */
export interface TrajectoryControllerEntry {
  targetId: string;
  kind: TrajectoryMotionKind;
  phase: TrajectoryControllerPhase;
  /** Enter frame (first render). Meaningless when settled. */
  start: TrajectoryFrame;
  /** Stable frame (last render). */
  settled: TrajectoryFrame;
  /**
   * Wall-clock when this entry finishes animating and becomes settled
   * (`activatesAtMs = nowMs + durationMs`). 0 = settles immediately.
   */
  activatesAtMs: number;
  /** Duration of the active transition in ms; 0 = no animation. */
  durationMs: number;
  lowPerformance: boolean;
  /** Static highlight/status kept even under reduced-motion. */
  highlight?: string;
}

/** The immutable-ish pure controller state. Mutated in place by the reducer. */
export interface TrajectoryMotionControllerState {
  /** Live entries keyed by target id. */
  entries: TrajectoryControllerEntry[];
  seq: number;
}

export function createTrajectoryMotionController(): TrajectoryMotionControllerState {
  return { entries: [], seq: 0 };
}

/**
 * True when the directive carries zero movement and no transition — the
 * animator produces this for reduced-motion, checkout-focus, and interrupted
 * (snapped) intents. Such a directive must always resolve straight to settled.
 */
export function directiveIsStatic(d: { transform: string; transition: string }): boolean {
  return d.transform === "none" && d.transition === "";
}

/**
 * Extract the millisecond duration from a CSS transition string. The animator
 * emits `transform <dur>ms <ease> <delay>ms, opacity <dur>ms ...` so the first
 * `\d+ms` is the shared duration. Returns 0 when there is no active transition.
 */
export function transitionDurationMs(transition: string): number {
  if (!transition) return 0;
  const m = /(\d+)ms/.exec(transition);
  return m ? Number(m[1]) : 0;
}

function frameFromDirective(
  d: TrajectoryAnimationDirective,
  opts: { start: boolean },
): TrajectoryFrame {
  return {
    transform: opts.start ? d.transform : d.targetTransform,
    opacity: opts.start ? d.opacity : d.targetOpacity,
    transition: d.transition,
  };
}

function makeSettleEntry(
  d: TrajectoryAnimationDirective,
  nowMs: number,
): TrajectoryControllerEntry {
  return {
    targetId: d.targetId,
    kind: d.kind,
    phase: "settled",
    start: frameFromDirective(d, { start: true }),
    settled: frameFromDirective(d, { start: false }),
    activatesAtMs: nowMs,
    durationMs: 0,
    lowPerformance: d.lowPerformance,
    highlight: d.highlight,
  };
}

/**
 * Reduce a batch of animator directives plus the current visible target ids
 * into the next controller state, mutating and returning `state`.
 *
 * Rules (LRM-1447 AC):
 *   1. Per target id, a non-static directive within its active window produces
 *      an `enter` (start frame) entry; a static / expired directive produces a
 *      `settled` entry. Targets absent from `visibleTargetIds` produce nothing
 *      and are cleaned up (no leak).
 *   2. Same-element coalesce / cancel: a static directive (transform "none" +
 *      empty transition) on an already-active target snaps it to settled
 *      WITHOUT flashing back to start; a settled-again directive keeps the
 *      settled frame (no re-trigger). Multiple same-direction entries never
 *      restart once they have settled.
 *   3. Lifecycle: entries whose `activatesAtMs` has passed are released from
 *      the active set (phase → settled). Expired + removed targets are dropped.
 *   4. reduced-motion / low-performance come pre-normalized from the animator;
 *      this layer passes `lowPerformance` through and treats `transform ===
 *      "none" && transition === ""` as settled-only (zero displacement).
 */
export function advanceTrajectoryMotion(
  state: TrajectoryMotionControllerState,
  directives: readonly TrajectoryAnimationDirective[],
  visibleTargetIds: readonly string[],
  nowMs: number,
): TrajectoryMotionControllerState {
  state.seq += 1;

  const visible = new Set(visibleTargetIds);
  const byTarget = new Map<string, TrajectoryControllerEntry>();

  // Keep existing entries for targets still visible; settle those whose window
  // has passed, and drop everything for removed targets (cleanup).
  for (const entry of state.entries) {
    if (!visible.has(entry.targetId)) continue;
    const settled = entry.phase === "settled" || nowMs >= entry.activatesAtMs;
    byTarget.set(entry.targetId, settled ? { ...entry, phase: "settled", activatesAtMs: nowMs } : entry);
  }

  for (const d of directives) {
    if (!visible.has(d.targetId)) continue; // not in the animation/layout window
    const existing = byTarget.get(d.targetId);
    const next = makeSettleEntry(d, nowMs);

    if (directiveIsStatic(d)) {
      // AC4 / AC2-cancel: static directive resolves straight to settled, no
      // flash back to start, no active timer.
      byTarget.set(d.targetId, next);
      continue;
    }

    const durationMs = transitionDurationMs(d.transition);
    const enter: TrajectoryControllerEntry = {
      ...next,
      phase: "enter",
      activatesAtMs: nowMs + durationMs,
      durationMs,
    };

    if (existing && existing.phase === "settled" && existing.kind === d.kind) {
      // AC2-coalesce: already settled for the same kind — do not re-trigger
      // from start; keep the settled frame as the final state.
      byTarget.set(d.targetId, { ...next, phase: "settled", activatesAtMs: nowMs });
      continue;
    }

    if (existing && existing.phase === "enter") {
      // AC2-interrupt/merge: an in-flight entry is retargeted to the new
      // settle values but must NOT jump back to the original start. Rebase the
      // new start onto the current settle so the motion continues forward.
      byTarget.set(d.targetId, {
        ...enter,
        start: existing.settled,
      });
      continue;
    }

    byTarget.set(d.targetId, enter);
  }

  state.entries = Array.from(byTarget.values());
  return state;
}

/**
 * The renderable frame state for a target id, or `null` when the target is not
 * tracked (not visible / not in the window).
 *
 * - enter phase → returns the **start** frame (consumer swaps to settled on
 *   settle); the caller may also read the settled frame to prepare the swap.
 * - settled phase → returns the settled frame.
 */
export function trajectoryFrameAt(
  state: TrajectoryMotionControllerState,
  targetId: string,
): { entry: TrajectoryControllerEntry; frame: TrajectoryFrame } | null {
  const entry = state.entries.find((e) => e.targetId === targetId);
  if (!entry) return null;
  return { entry, frame: entry.phase === "enter" ? entry.start : entry.settled };
}

/** Target ids currently animating (enter phase, in-flight). */
export function trajectoryActiveTargets(state: TrajectoryMotionControllerState): string[] {
  return state.entries.filter((e) => e.phase === "enter").map((e) => e.targetId);
}
