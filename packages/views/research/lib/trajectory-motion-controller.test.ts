/**
 * LRM-1447 — trajectory-motion-controller pure tests (parent LRM-1393).
 *
 * AC coverage:
 *  #1 per-target start/settled two-frame state from a directive window;
 *     targets outside the visible/window produce nothing.
 *  #2 same-target multiple directives merge to one final transition; a
 *     cancelled/interrupt (static) directive snaps to settled without flashing
 *     back to start.
 *  #3 life-cycle: after the window expires the directive leaves the active
 *     set and the target is released; removed layout targets are cleaned.
 *  #4 reduced-motion resolves straight to settled with zero displacement;
 *     low-performance flag passes through.
 *  #5 pure unit tests only: no layout mutation, no RAF, no React components.
 */

import { describe, expect, it } from "vitest";
import { buildTrajectoryLaneLayout } from "@multica/core/research";
import type { TrajectoryMotionEvent, TrajectoryMotionState } from "./trajectory-motion-intents";
import {
  activeTrajectoryIntents,
  applyTrajectoryEvent,
  createTrajectoryMotionState,
} from "./trajectory-motion-intents";
import { resolveTrajectoryMotionDirectives } from "./trajectory-motion-animator";
import {
  advanceTrajectoryMotion,
  createTrajectoryMotionController,
  directiveIsStatic,
  trajectoryActiveTargets,
  trajectoryFrameAt,
  transitionDurationMs,
  type TrajectoryMotionControllerState,
} from "./trajectory-motion-controller";

const NORMAL = { reducedMotion: false, lowPerformance: false };
const REDUCED = { reducedMotion: true, lowPerformance: false };
const LOW_PERF = { reducedMotion: false, lowPerformance: true };

function grow(lane: string, targetIds: string[]): TrajectoryMotionEvent {
  return { kind: "branch-grow", lane, targetIds };
}
function focus(lane: string, targetIds: string[], status = "running"): TrajectoryMotionEvent {
  return { kind: "checkout-focus", lane, targetIds, status };
}

function stateWith(events: TrajectoryMotionEvent[], profile = NORMAL): TrajectoryMotionState {
  let s = createTrajectoryMotionState();
  events.forEach((e, idx) => {
    s = applyTrajectoryEvent(s, e, profile, idx, 60_000);
  });
  return s;
}

function mainLayout() {
  return buildTrajectoryLaneLayout([
    { id: "m0", branchKey: "main", parentIds: [], status: "done" },
    { id: "m1", branchKey: "main", parentIds: ["m0"], status: "done" },
  ]);
}
function grownLayout() {
  return buildTrajectoryLaneLayout([
    { id: "m0", branchKey: "main", parentIds: [], status: "done" },
    { id: "m1", branchKey: "main", parentIds: ["m0"], status: "done" },
    { id: "b0", branchKey: "feature", parentIds: ["m1"], status: "running" },
  ]);
}

/** intent → animator → controller, keeping the source of truth canonical. */
function advanceThrough(
  prev: ReturnType<typeof mainLayout>,
  next: ReturnType<typeof mainLayout>,
  events: TrajectoryMotionEvent[],
  profile = NORMAL,
  nowMs = 5,
): TrajectoryMotionControllerState {
  const intentState = stateWith(events, profile);
  const directives = resolveTrajectoryMotionDirectives(activeTrajectoryIntents(intentState), prev, next, nowMs);
  let ctl = createTrajectoryMotionController();
  ctl = advanceTrajectoryMotion(ctl, directives, next.commits.map((c) => c.id), nowMs);
  return ctl;
}

describe("two-frame states (AC 1)", () => {
  it("a moving directive yields an enter state exposing start + settled frames", () => {
    const ctl = advanceThrough(mainLayout(), grownLayout(), [grow("feature", ["b0"])], NORMAL, 5);
    const got = trajectoryFrameAt(ctl, "b0");
    expect(got).not.toBeNull();
    expect(got!.entry.phase).toBe("enter");
    expect(got!.frame.transform).toMatch(/^translate\(/);
    // start opacity < settled opacity (0.4 → 1)
    expect(got!.entry.start.opacity).toBeLessThan(got!.entry.settled.opacity);
    expect(transitionDurationMs(got!.entry.settled.transition)).toBeGreaterThan(0);
    expect(trajectoryActiveTargets(ctl)).toEqual(["b0"]);
  });

  it("a target not in the layout/window produces nothing", () => {
    const ctl = advanceThrough(mainLayout(), mainLayout(), [], NORMAL, 5);
    expect(trajectoryFrameAt(ctl, "nope")).toBeNull();
    expect(trajectoryActiveTargets(ctl)).toEqual([]);
  });
});

describe("merge / cancel (AC 2)", () => {
  it("a downstream static directive snaps a settled target to settled (no start flash)", () => {
    // Seed a moving grow on b0, then on a later tick feed the static
    // checkout-focus directive for the same target (reduced-motion / focus).
    let s = createTrajectoryMotionState();
    s = applyTrajectoryEvent(s, grow("feature", ["b0"]), NORMAL, 0, 60_000);
    s = applyTrajectoryEvent(s, focus("feature", ["b0"], "done"), NORMAL, 10, 60_000);
    const dirs = resolveTrajectoryMotionDirectives(activeTrajectoryIntents(s), grownLayout(), grownLayout(), 5);
    // Focus replaces grow; the focus directive is static (transform none / no transition).
    const focusDir = dirs.find((d) => d.highlight === "trajectory-focus")!;
    expect(directiveIsStatic(focusDir)).toBe(true);

    let ctl = createTrajectoryMotionController();
    ctl = advanceTrajectoryMotion(ctl, [focusDir], ["b0"], 5);
    const got = trajectoryFrameAt(ctl, "b0")!;
    expect(got.entry.phase).toBe("settled"); // snapped, no enter
    expect(got.frame.transform).toBe("none");
    expect(transitionDurationMs(got.frame.transition)).toBe(0);
  });

  it("an already-settled same-kind directive does not re-trigger from start", () => {
    let ctl = createTrajectoryMotionController();
    // Tick 1: b0 animates (enter).
    ctl = advanceThrough(mainLayout(), grownLayout(), [grow("feature", ["b0"])], NORMAL, 0);
    expect(trajectoryFrameAt(ctl, "b0")!.entry.phase).toBe("enter");
    // Tick 2 well after the window: same grow directive lands again → stays settled.
    const again = stateWith([grow("feature", ["b0"])], NORMAL);
    const dirs = resolveTrajectoryMotionDirectives(activeTrajectoryIntents(again), mainLayout(), grownLayout(), 500);
    ctl = advanceTrajectoryMotion(ctl, dirs, ["b0"], 1000);
    const got = trajectoryFrameAt(ctl, "b0")!;
    expect(got.entry.phase).toBe("settled");
    expect(got.frame.transform).toBe("none");
    expect(trajectoryActiveTargets(ctl)).toEqual([]);
  });

  it("an interrupting moving directive rebases start onto current settle, no jump to old start", () => {
    let ctl = createTrajectoryMotionController();
    ctl = advanceThrough(mainLayout(), grownLayout(), [grow("feature", ["b0"])], NORMAL, 0);
    const firstSettle = trajectoryFrameAt(ctl, "b0")!.entry.settled;
    // A different grow lands mid-flight (same lane+kind). Controller keeps the
    // existing settle as the new start so motion continues, no flash.
    const interrupt = stateWith([grow("feature", ["b0"])], NORMAL);
    const dirs = resolveTrajectoryMotionDirectives(activeTrajectoryIntents(interrupt), mainLayout(), grownLayout(), 10);
    ctl = advanceTrajectoryMotion(ctl, dirs, ["b0"], 10);
    const got = trajectoryFrameAt(ctl, "b0")!;
    expect(got.entry.phase).toBe("enter");
    expect(got.entry.start).toEqual(firstSettle);
  });
});

describe("lifecycle / cleanup (AC 3)", () => {
  it("an expired enter entry releases to settled and leaves the active set", () => {
    let ctl = advanceThrough(mainLayout(), grownLayout(), [grow("feature", ["b0"])], NORMAL, 0);
    expect(trajectoryActiveTargets(ctl)).toEqual(["b0"]);
    // Re-apply with no new directives at nowMs far beyond the window.
    ctl = advanceTrajectoryMotion(ctl, [], ["b0"], 10_000);
    const got = trajectoryFrameAt(ctl, "b0")!;
    expect(got.entry.phase).toBe("settled");
    expect(trajectoryActiveTargets(ctl)).toEqual([]);
  });

  it("a target removed from the layout is cleaned up (no leak)", () => {
    let ctl = advanceThrough(mainLayout(), grownLayout(), [grow("feature", ["b0"])], NORMAL, 0);
    expect(trajectoryFrameAt(ctl, "b0")).not.toBeNull();
    // b0 disappears from the visible layout now.
    ctl = advanceTrajectoryMotion(ctl, [], ["m0", "m1"], 10);
    expect(trajectoryFrameAt(ctl, "b0")).toBeNull();
    expect(trajectoryActiveTargets(ctl)).toEqual([]);
  });
});

describe("degradation (AC 4)", () => {
  it("reduced-motion resolves straight to settled with zero displacement", () => {
    const ctl = advanceThrough(mainLayout(), grownLayout(), [
      grow("feature", ["b0"]),
      focus("feature", ["b0"], "done"),
    ], REDUCED, 5);
    const got = trajectoryFrameAt(ctl, "b0")!;
    // Under reduced-motion the animator emits transform none + empty transition →
    // controller settles immediately.
    expect(got.entry.phase).toBe("settled");
    expect(got.frame.transform).toBe("none");
    expect(transitionDurationMs(got.frame.transition)).toBe(0);
    expect(trajectoryActiveTargets(ctl)).toEqual([]);
  });

  it("low-performance flag passes through the controller", () => {
    const ctl = advanceThrough(mainLayout(), grownLayout(), [grow("feature", ["b0"])], LOW_PERF, 5);
    const got = trajectoryFrameAt(ctl, "b0")!;
    expect(got.entry.lowPerformance).toBe(true);
  });
});

describe("pure (AC 5)", () => {
  it("advance does not mutate the source directive list", () => {
    const prev = mainLayout();
    const next = grownLayout();
    const s = stateWith([grow("feature", ["b0"])], NORMAL);
    const dirs = resolveTrajectoryMotionDirectives(activeTrajectoryIntents(s), prev, next, 5);
    const snapshot = JSON.stringify(dirs);
    const ctl = advanceTrajectoryMotion(createTrajectoryMotionController(), dirs, ["b0"], 5);
    expect(JSON.stringify(dirs)).toBe(snapshot);
    expect(trajectoryActiveTargets(ctl)).toEqual(["b0"]);
  });
});
