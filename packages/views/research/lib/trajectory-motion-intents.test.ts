/**
 * LRM-1400 — trajectory-motion-intents pure state tests (parent LRM-1393).
 *
 * AC coverage:
 *  #1 four kinds → independent, cancellable intents; checkout-focus replaces
 *     a prior focus intent.
 *  #2 same-lane consecutive branch-grow / commit-append coalesce inside the
 *     budget window; merge-flow is never merged across lanes.
 *  #3 document hidden → no queuing; recover drops history and only new events
 *     are processed.
 *  #4 reduced-motion → zero path displacement but static status intent kept;
 *     low-performance → lowPerformance flag surfaced.
 *  #5 pure unit tests only: no layout segments, no RAF, no React components.
 */

import { describe, expect, it } from "vitest";
import {
  type TrajectoryMotionEvent,
  type TrajectoryMotionState,
  TRAJECTORY_MOTION_MAX_DISPLACEMENT_PX,
  activeTrajectoryIntents,
  applyTrajectoryEvent,
  cancelTrajectoryIntent,
  createTrajectoryMotionState,
  setTrajectoryMotionVisibility,
} from "./trajectory-motion-intents";

const NORMAL = { reducedMotion: false, lowPerformance: false };
const REDUCED = { reducedMotion: true, lowPerformance: false };
const LOW_PERF = { reducedMotion: false, lowPerformance: true };

function grow(lane = "main", targetIds = ["a"]): TrajectoryMotionEvent {
  return { kind: "branch-grow", lane, targetIds };
}
function append(lane = "main", targetIds = ["a"]): TrajectoryMotionEvent {
  return { kind: "commit-append", lane, targetIds };
}
function merge(lane = "main", targetIds = ["a", "b"]): TrajectoryMotionEvent {
  return { kind: "merge-flow", lane, targetIds };
}
function focus(lane = "main", targetIds = ["a"]): TrajectoryMotionEvent {
  return { kind: "checkout-focus", lane, targetIds, status: "running" };
}

function activeCount(s: TrajectoryMotionState): number {
  return activeTrajectoryIntents(s).length;
}

describe("kind independence and cancellability (AC 1)", () => {
  it("four distinct kinds produce four independent intents", () => {
    let s = createTrajectoryMotionState();
    s = applyTrajectoryEvent(s, grow(), NORMAL, 0, 10_000);
    s = applyTrajectoryEvent(s, append(), NORMAL, 1, 10_000);
    s = applyTrajectoryEvent(s, merge(), NORMAL, 2, 10_000);
    s = applyTrajectoryEvent(s, focus(), NORMAL, 3, 10_000);
    expect(activeCount(s)).toBe(4);
    const kinds = activeTrajectoryIntents(s).map((i) => i.kind).sort();
    expect(kinds).toEqual(["branch-grow", "checkout-focus", "commit-append", "merge-flow"]);
  });

  it("each event gets a cancellable id, and cancelling removes it from active set", () => {
    let s = createTrajectoryMotionState();
    s = applyTrajectoryEvent(s, grow(), NORMAL, 0, 10_000);
    s = applyTrajectoryEvent(s, append(), NORMAL, 1, 10_000);
    const growId = activeTrajectoryIntents(s)[0]!.id;
    s = cancelTrajectoryIntent(s, growId);
    expect(activeCount(s)).toBe(1);
    expect(s.intents[0]!.cancelled).toBe(true);
  });

  it("checkout-focus replaces a prior focus intent outright", () => {
    let s = createTrajectoryMotionState();
    s = applyTrajectoryEvent(s, focus("main", ["a"]), NORMAL, 0, 10_000);
    s = applyTrajectoryEvent(s, focus("other", ["b"]), NORMAL, 1, 10_000);
    expect(activeCount(s)).toBe(1);
    const only = activeTrajectoryIntents(s)[0]!;
    expect(only.kind).toBe("checkout-focus");
    expect(only.lane).toBe("other");
    expect(only.targetIds).toEqual(["b"]);
  });
});

describe("budget-window coalescing (AC 2)", () => {
  it("same-lane consecutive branch-grow coalesces inside the budget window", () => {
    let s = createTrajectoryMotionState();
    s = applyTrajectoryEvent(s, grow("main", ["a"]), NORMAL, 0, 1000);
    s = applyTrajectoryEvent(s, grow("main", ["b"]), NORMAL, 100, 1000);
    expect(activeCount(s)).toBe(1);
    const only = activeTrajectoryIntents(s)[0]!;
    expect(only.kind).toBe("branch-grow");
    expect(only.targetIds).toEqual(["a", "b"]);
  });

  it("same-lane consecutive commit-append coalesces inside the budget window", () => {
    let s = createTrajectoryMotionState();
    s = applyTrajectoryEvent(s, append("main", ["a"]), NORMAL, 0, 1000);
    s = applyTrajectoryEvent(s, append("main", ["b"]), NORMAL, 50, 1000);
    s = applyTrajectoryEvent(s, append("main", ["c"]), NORMAL, 120, 1000);
    expect(activeCount(s)).toBe(1);
    expect(activeTrajectoryIntents(s)[0]!.targetIds).toEqual(["a", "b", "c"]);
  });

  it("events outside the budget window do not coalesce", () => {
    let s = createTrajectoryMotionState();
    s = applyTrajectoryEvent(s, grow("main", ["a"]), NORMAL, 0, 100);
    s = applyTrajectoryEvent(s, grow("main", ["b"]), NORMAL, 500, 100);
    expect(activeCount(s)).toBe(2);
  });

  it("merge-flow never merges across lanes even within the budget window", () => {
    let s = createTrajectoryMotionState();
    s = applyTrajectoryEvent(s, merge("lane-1", ["a", "b"]), NORMAL, 0, 1000);
    s = applyTrajectoryEvent(s, merge("lane-2", ["c"]), NORMAL, 50, 1000);
    expect(activeCount(s)).toBe(2);
  });

  it("same-lane merge-flow can coalesce within its window", () => {
    let s = createTrajectoryMotionState();
    s = applyTrajectoryEvent(s, merge("main", ["a"]), NORMAL, 0, 1000);
    s = applyTrajectoryEvent(s, merge("main", ["b"]), NORMAL, 50, 1000);
    expect(activeCount(s)).toBe(1);
  });

  it("different kinds on the same lane never coalesce", () => {
    let s = createTrajectoryMotionState();
    s = applyTrajectoryEvent(s, grow("main", ["a"]), NORMAL, 0, 1000);
    s = applyTrajectoryEvent(s, append("main", ["b"]), NORMAL, 50, 1000);
    expect(activeCount(s)).toBe(2);
  });
});

describe("document-hidden behavior (AC 3)", () => {
  it("events while hidden are not queued and are dropped", () => {
    let s = createTrajectoryMotionState();
    s = setTrajectoryMotionVisibility(s, false, 0);
    s = applyTrajectoryEvent(s, grow(), NORMAL, 10, 1000);
    s = applyTrajectoryEvent(s, append(), NORMAL, 20, 1000);
    expect(activeCount(s)).toBe(0);
  });

  it("recover does not replay dropped history, only new events processed", () => {
    let s = createTrajectoryMotionState();
    s = applyTrajectoryEvent(s, grow("lane-a", ["a"]), NORMAL, 0, 1000);
    s = setTrajectoryMotionVisibility(s, false, 100);
    s = applyTrajectoryEvent(s, grow("lane-b", ["b"]), NORMAL, 200, 1000); // dropped
    s = setTrajectoryMotionVisibility(s, true, 300);
    s = applyTrajectoryEvent(s, append("lane-c", ["c"]), NORMAL, 400, 1000); // processed
    const ids = activeTrajectoryIntents(s)
      .flatMap((i) => i.targetIds)
      .sort();
    expect(ids).toEqual(["a", "c"]); // "b" was dropped while hidden and never replayed
    expect(ids).not.toContain("b");
  });
});

describe("degradation (AC 4)", () => {
  it("reduced-motion forces zero displacement but keeps the status intent", () => {
    let s = createTrajectoryMotionState();
    // a commit-append carries a status label that must survive reduced-motion
    s = applyTrajectoryEvent(
      s,
      { kind: "commit-append", lane: "main", targetIds: ["a"], status: "success" },
      REDUCED,
      0,
      1000,
    );
    const only = activeTrajectoryIntents(s)[0]!;
    expect(only.displacementPx).toBe(0);
    expect(only.status).toBe("success");
  });

  it("normal profile keeps a non-zero displacement bounded by the max", () => {
    let s = createTrajectoryMotionState();
    s = applyTrajectoryEvent(s, merge("main", ["a", "b"]), NORMAL, 0, 1000);
    const only = activeTrajectoryIntents(s)[0]!;
    expect(only.displacementPx).toBeGreaterThan(0);
    expect(only.displacementPx).toBeLessThanOrEqual(TRAJECTORY_MOTION_MAX_DISPLACEMENT_PX);
  });

  it("low-performance surfaces the lowPerformance flag on the intent", () => {
    let s = createTrajectoryMotionState();
    s = applyTrajectoryEvent(s, grow("main", ["a"]), LOW_PERF, 0, 1000);
    expect(activeTrajectoryIntents(s)[0]!.lowPerformance).toBe(true);
    expect(activeTrajectoryIntents(s)[0]!.lowPerformance).toBe(true);
  });
});
