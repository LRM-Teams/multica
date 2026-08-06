/**
 * LRM-1446 — trajectory-motion-animator pure tests (parent LRM-1393).
 *
 * AC coverage:
 *  #1 four kinds → concrete transform/opacity/transition directives.
 *  #2 same-lane budget-coalesced intent emits ONE directive per target;
 *     merge-flow direction derives from segment geometry.
 *  #3 cancelled intents emit nothing.
 *  #4 reduced-motion → start transform "none" + static status highlight, no
 *     transition; low-performance → lowPerformance flag.
 *  #5 pure unit tests only: no layout mutation, no RAF, no React components.
 */

import { describe, expect, it } from "vitest";
import { buildTrajectoryLaneLayout } from "@multica/core/research";
import type { TrajectoryMotionEvent, TrajectoryMotionState } from "./trajectory-motion-intents";
import {
  activeTrajectoryIntents,
  applyTrajectoryEvent,
  cancelTrajectoryIntent,
  createTrajectoryMotionState,
} from "./trajectory-motion-intents";
import {
  TRAJECTORY_MOTION_MAX_SINGLE_DURATION_MS,
  directiveHasMotion,
  resolveTrajectoryMotionDirectives,
  trajectoryBudgetSummary,
} from "./trajectory-motion-animator";

const NORMAL = { reducedMotion: false, lowPerformance: false };
const REDUCED = { reducedMotion: true, lowPerformance: false };
const LOW_PERF = { reducedMotion: false, lowPerformance: true };

function grow(lane: string, targetIds: string[]): TrajectoryMotionEvent {
  return { kind: "branch-grow", lane, targetIds };
}
function appendC(lane: string, targetIds: string[]): TrajectoryMotionEvent {
  return { kind: "commit-append", lane, targetIds };
}
function merge(lane: string, targetIds: string[]): TrajectoryMotionEvent {
  return { kind: "merge-flow", lane, targetIds };
}
function focus(lane: string, targetIds: string[], status = "running"): TrajectoryMotionEvent {
  return { kind: "checkout-focus", lane, targetIds, status };
}

function stateWith(events: TrajectoryMotionEvent[], profile = NORMAL): TrajectoryMotionState {
  let s = createTrajectoryMotionState();
  events.forEach((e, idx) => {
    s = applyTrajectoryEvent(s, e, profile, idx, 60_000); // huge budget → no coalescing
  });
  return s;
}

/** main: m0 -> m1 ; branch: m1 -> b0 (grow), merge b0 into m2. */
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
function mergedLayout() {
  return buildTrajectoryLaneLayout([
    { id: "m0", branchKey: "main", parentIds: [], status: "done" },
    { id: "m1", branchKey: "main", parentIds: ["m0"], status: "done" },
    { id: "b0", branchKey: "feature", parentIds: ["m1"], status: "done" },
    { id: "m2", branchKey: "main", parentIds: ["m1", "b0"], status: "running" },
  ]);
}

describe("directive semantics (AC 1)", () => {
  it("branch-grow emits a moving directive that settles to none", () => {
    const prev = mainLayout();
    const next = grownLayout();
    const s = stateWith([grow("feature", ["b0"])]);
    const dirs = resolveTrajectoryMotionDirectives(activeTrajectoryIntents(s), prev, next, 5);
    expect(dirs).toHaveLength(1);
    const d = dirs[0]!;
    expect(d.targetId).toBe("b0");
    expect(d.kind).toBe("branch-grow");
    expect(d.transform).toMatch(/^translate\(/);
    expect(d.targetTransform).toBe("none");
    expect(directiveHasMotion(d)).toBe(true);
    expect(d.opacity).toBeLessThan(d.targetOpacity);
    expect(d.transition).toContain(`${TRAJECTORY_MOTION_MAX_SINGLE_DURATION_MS}ms`);
  });

  it("commit-append resolves motion out of its parent (x from lane, y from row)", () => {
    const prev = mainLayout();
    const next = grownLayout();
    const s = stateWith([appendC("feature", ["b0"])]);
    const d = resolveTrajectoryMotionDirectives(activeTrajectoryIntents(s), prev, next, 5)[0]!;
    // b0 is new; its parent is m1 (main lane 0 / row 1), b0 is feature lane 1 / row 2,
    // so the grow is a translate with both x and y displacement (8px each).
    expect(d.transform).toBe("translate(8px, 8px)");
    expect(directiveHasMotion(d)).toBe(true);
  });

  it("checkout-focus is zero-motion static highlight", () => {
    const prev = mainLayout();
    const next = mainLayout();
    const s = stateWith([focus("main", ["m1"], "running")]);
    const d = resolveTrajectoryMotionDirectives(activeTrajectoryIntents(s), prev, next, 5)[0]!;
    expect(d.kind).toBe("checkout-focus");
    expect(d.transform).toBe("none");
    expect(d.targetTransform).toBe("none");
    expect(d.transition).toBe("");
    expect(d.highlight).toBe("trajectory-focus");
    expect(directiveHasMotion(d)).toBe(false);
  });

  it("merge-flow direction derives from segment geometry (parent→child lane delta)", () => {
    const prev = grownLayout();
    const next = mergedLayout();
    const s = stateWith([merge("main", ["m2"])]);
    const d = resolveTrajectoryMotionDirectives(activeTrajectoryIntents(s), prev, next, 5)[0]!;
    // m2 is new in next; its merge segment(s) carry lane-from/to where parent m1/b0
    // differ from m2's lane, so direction is non-zero along x and/or y.
    expect(d.kind).toBe("merge-flow");
    expect(d.transform).toMatch(/^translate\(/);
  });
});

describe("budget / coalescing (AC 2)", () => {
  it("coalesced same-lane branch-grow emits one directive per target id", () => {
    // Two events on the same lane+kind inside the budget window coalesce in the
    // intent layer into a single intent (LRM-1400) → animator emits one directive.
    let s = createTrajectoryMotionState();
    s = applyTrajectoryEvent(s, grow("feature", ["b0"]), NORMAL, 0, 320);
    s = applyTrajectoryEvent(s, grow("feature", ["b1"]), NORMAL, 50, 320);
    const intents = activeTrajectoryIntents(s);
    expect(intents).toHaveLength(1);
    const prev = mainLayout();
    const next = buildTrajectoryLaneLayout([
      { id: "m0", branchKey: "main", parentIds: [], status: "done" },
      { id: "m1", branchKey: "main", parentIds: ["m0"], status: "done" },
      { id: "b0", branchKey: "feature", parentIds: ["m1"], status: "running" },
      { id: "b1", branchKey: "feature", parentIds: ["b0"], status: "running" },
    ]);
    const dirs = resolveTrajectoryMotionDirectives(intents, prev, next, 200);
    expect(dirs.map((d) => d.targetId).sort()).toEqual(["b0", "b1"]);
  });

  it("budget summary groups by lane and counts coalesced intents", () => {
    let s = createTrajectoryMotionState();
    s = applyTrajectoryEvent(s, grow("feature", ["b0"]), NORMAL, 0, 320);
    s = applyTrajectoryEvent(s, grow("feature", ["b1"]), NORMAL, 50, 320);
    const summary = trajectoryBudgetSummary(activeTrajectoryIntents(s));
    expect(summary).toEqual([{ lane: "feature", kinds: ["branch-grow"], counts: 1 }]);
  });
});

describe("cancellation (AC 3)", () => {
  it("cancelled intents emit zero directives", () => {
    let s = createTrajectoryMotionState();
    s = applyTrajectoryEvent(s, grow("feature", ["b0"]), NORMAL, 0, 60_000);
    const growId = activeTrajectoryIntents(s)[0]!.id;
    s = cancelTrajectoryIntent(s, growId);
    const prev = mainLayout();
    const next = grownLayout();
    const dirs = resolveTrajectoryMotionDirectives(activeTrajectoryIntents(s), prev, next, 5);
    expect(dirs).toHaveLength(0);
  });
});

describe("degradation (AC 4)", () => {
  it("reduced-motion forces start transform none and keeps a static status highlight", () => {
    const prev = mainLayout();
    const next = grownLayout();
    // Grow with an explicit status so reduced-motion surfaces it as static highlight.
    const growStatus: TrajectoryMotionEvent = { kind: "branch-grow", lane: "feature", targetIds: ["b0"], status: "running" };
    const s = stateWith([growStatus, focus("feature", ["b0"], "done")], REDUCED);
    const dirs = resolveTrajectoryMotionDirectives(activeTrajectoryIntents(s), prev, next, 5);
    for (const d of dirs) {
      expect(d.transform).toBe("none");
      expect(d.targetTransform).toBe("none");
      expect(d.transition).toBe("");
      expect(directiveHasMotion(d)).toBe(false);
    }
    // The growing intent with status surfaces a static status highlight.
    expect(dirs.some((d) => d.highlight === "trajectory-status")).toBe(true);
  });

  it("low-performance flags directives without dropping visual semantics", () => {
    const prev = mainLayout();
    const next = grownLayout();
    const s = stateWith([grow("feature", ["b0"])], LOW_PERF);
    const d = resolveTrajectoryMotionDirectives(activeTrajectoryIntents(s), prev, next, 5)[0]!;
    expect(d.lowPerformance).toBe(true);
    expect(d.transform).toMatch(/^translate\(/); // still positioned
    expect(d.targetOpacity).toBe(1);
  });
});

describe("pure (AC 5)", () => {
  it("does not mutate the given layouts or intensity", () => {
    const prev = mainLayout();
    const next = grownLayout();
    const prevRows = prev.rowCount;
    const nextRows = next.rowCount;
    let s = createTrajectoryMotionState();
    s = applyTrajectoryEvent(s, grow("feature", ["b0"]), NORMAL, 0, 60_000);
    resolveTrajectoryMotionDirectives(activeTrajectoryIntents(s), prev, next, 5);
    expect(prev.rowCount).toBe(prevRows);
    expect(next.rowCount).toBe(nextRows);
    expect(activeTrajectoryIntents(s)).toHaveLength(1);
  });
});
