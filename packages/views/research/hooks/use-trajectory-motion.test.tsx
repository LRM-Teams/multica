// @vitest-environment jsdom
/**
 * LRM-1448 — trajectory motion UI consumption layer tests (parent LRM-1393).
 *
 * AC coverage:
 *  #1 hook/reducer inputs intents + prev/next layout + clock, runs
 *     intents→animator→controller, outputs per-target renderable frame and
 *     enter/settled phase; targets outside the visible window produce nothing.
 *  #2 start→settled swap guard: enter uses the start frame; after activate the
 *     caller receives the settled frame; cancelled/interrupt resolves to
 *     settled without flashing back to start.
 *  #3 lifecycle: animation ends and leaves the active set; removing a target
 *     cleans its pending frame; reduced-motion keeps zero displacement while
 *     the static highlight survives; low-performance passes through.
 *  #5 React hook uses an injected scheduler (no real RAF/fake timers) so the
 *     settle swap is deterministic; no layout/business-code changes.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { buildTrajectoryLaneLayout } from "@multica/core/research";
import type { TrajectoryMotionEvent, TrajectoryMotionProfile } from "../lib/trajectory-motion-intents";
import { createTrajectoryMotionState } from "../lib/trajectory-motion-intents";
import { createTrajectoryMotionController } from "../lib/trajectory-motion-controller";
import {
  reduceTrajectoryMotionFrame,
  trajectoryRenderFrameAt,
  useTrajectoryMotion,
} from "./use-trajectory-motion";

const NORMAL: TrajectoryMotionProfile = { reducedMotion: false, lowPerformance: false };
const REDUCED: TrajectoryMotionProfile = { reducedMotion: true, lowPerformance: false };
const LOW_PERF: TrajectoryMotionProfile = { reducedMotion: false, lowPerformance: true };

function grow(lane: string, targetIds: string[], status?: string | null): TrajectoryMotionEvent {
  return { kind: "branch-grow", lane, targetIds, ...(status !== undefined ? { status } : {}) };
}
function append(lane: string, targetIds: string[], status?: string | null): TrajectoryMotionEvent {
  return { kind: "commit-append", lane, targetIds, ...(status !== undefined ? { status } : {}) };
}
function focus(lane: string, targetIds: string[], status = "running"): TrajectoryMotionEvent {
  return { kind: "checkout-focus", lane, targetIds, status };
}

function baseLayout() {
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
function appendedLayout() {
  return buildTrajectoryLaneLayout([
    { id: "m0", branchKey: "main", parentIds: [], status: "done" },
    { id: "m1", branchKey: "main", parentIds: ["m0"], status: "done" },
    { id: "b0", branchKey: "feature", parentIds: ["m1"], status: "done" },
    { id: "b1", branchKey: "feature", parentIds: ["b0"], status: "running" },
  ]);
}

function frameReducer(
  events: TrajectoryMotionEvent[],
  prev: ReturnType<typeof grownLayout>,
  next: ReturnType<typeof grownLayout>,
  profile: TrajectoryMotionProfile = NORMAL,
  nowMs = 5,
  visible = next.commits.map((c) => c.id),
  state?: { motion: ReturnType<typeof createTrajectoryMotionState>; controller: ReturnType<typeof createTrajectoryMotionController> },
) {
  const motion = state?.motion ?? createTrajectoryMotionState();
  const controller = state?.controller ?? createTrajectoryMotionController();
  const out = reduceTrajectoryMotionFrame(motion, controller, events, prev, next, visible, nowMs, profile);
  return { ...out, motion, controller };
}

describe("reduceTrajectoryMotionFrame (pure pipeline)", () => {
  it("AC #1: entering target exposes the start frame; settled phase swaps to settled", () => {
    const prev = baseLayout();
    const next = grownLayout(); // b0 grew
    // First pass at nowMs=5 creates the entering entry (activatesAtMs = 5+320).
    const first = frameReducer([grow("feature", ["b0"])], prev, next, NORMAL, 5);

    const enterFrame = trajectoryRenderFrameAt(first.frames, "b0");
    expect(enterFrame).not.toBeNull();
    expect(enterFrame!.phase).toBe("enter");
    // Animate: start frame has a translate, settle frame is none.
    expect(enterFrame!.style.transform).toMatch(/translate/);
    expect(enterFrame!.style.opacity).toBeLessThan(1);

    // Re-advance the SAME controller past the activate window: it swaps to
    // settled (no flash back to start).
    const later = frameReducer([], prev, next, NORMAL, 5 + 400, next.commits.map((c) => c.id), first);
    const settled = trajectoryRenderFrameAt(later.frames, "b0");
    expect(settled).not.toBeNull();
    expect(settled!.phase).toBe("settled");
    expect(settled!.style.transform).toBe("none");
  });

  it("AC #1: targets outside the visible window produce no frame", () => {
    const prev = baseLayout();
    const next = grownLayout();
    // Window only contains m0/m1 — b0 grew but is not yet visible.
    const { frames } = frameReducer([grow("feature", ["b0"])], prev, next, NORMAL, 5, ["m0", "m1"]);
    expect(trajectoryRenderFrameAt(frames, "b0")).toBeNull();
    expect(frames.size).toBe(0);
  });

  it("AC #3: removed target is cleaned up (no leak)", () => {
    const prev = baseLayout();
    const next = grownLayout();
    const first = frameReducer([grow("feature", ["b0"])], prev, next);
    expect(trajectoryRenderFrameAt(first.frames, "b0")).not.toBeNull();

    // Next reduce on the SAME controller: b0 no longer visible → cleaned.
    const second = frameReducer([], prev, next, NORMAL, 5 + 400, ["m0", "m1"], first);
    expect(trajectoryRenderFrameAt(second.frames, "b0")).toBeNull();
    // b0 is no longer tracked by the controller at all.
    expect(second.controller.entries.some((e) => e.targetId === "b0")).toBe(false);
  });

  it("AC #2/#4 (reduced-motion): settles immediately with zero displacement but keeps static highlight/status", () => {
    const prev = baseLayout();
    const next = grownLayout();
    // Status-carrying grow: under reduced-motion the static status survives.
    const { frames } = frameReducer([grow("feature", ["b0"], "running")], prev, next, REDUCED);

    const f = trajectoryRenderFrameAt(frames, "b0");
    expect(f).not.toBeNull();
    expect(f!.phase).toBe("settled"); // no animation to schedule
    expect(f!.style.transform).toBe("none"); // zero path displacement
    // Static status label survives reduced-motion.
    expect(f!.highlight).toBeTruthy();
  });

  it("AC #4: low-performance flag passes through", () => {
    const prev = baseLayout();
    const next = grownLayout();
    const { frames } = frameReducer([grow("feature", ["b0"])], prev, next, LOW_PERF);
    expect(trajectoryRenderFrameAt(frames, "b0")!.lowPerformance).toBe(true);
  });

  it("AC #2: checkout-focus is a static highlight with no movement", () => {
    const prev = baseLayout();
    const next = grownLayout();
    const { frames } = frameReducer([focus("main", ["m1"], "running")], prev, next);

    const f = trajectoryRenderFrameAt(frames, "m1");
    expect(f).not.toBeNull();
    expect(f!.kind).toBe("checkout-focus");
    expect(f!.style.transform).toBe("none"); // focus intent is highlight, not a move
    expect(f!.phase).toBe("settled");
    expect(f!.highlight).toBe("trajectory-focus");
  });

  it("AC #2/#3: same-target append coalesces (enter→settled, no re-flash)", () => {
    const prev = appendedLayout();
    const next = appendedLayout();
    // Two append events on the same lane inside a budget window coalesce.
    const { frames } = frameReducer(
      [append("feature", ["b1"]), append("feature", ["b1"])],
      prev,
      next,
    );
    const f = trajectoryRenderFrameAt(frames, "b1");
    expect(f).not.toBeNull();
    // Coalesced into a single transition (enter) — never two re-flashes.
    expect(f!.phase).toBe("enter");
  });
});

describe("useTrajectoryMotion (React hook)", () => {
  function makeScheduler() {
    let handle = 0;
    const timers = new Map<number, () => void>();
    const api = {
      setTimeout: (fn: () => void, _ms: number) => {
        const id = ++handle;
        timers.set(id, fn);
        return id;
      },
      clearTimeout: (h: unknown) => {
        timers.delete(Number(h));
      },
      /** Fire the currently scheduled settle callback (virtual clock advance). */
      fire: () => {
        const fns = Array.from(timers.values());
        timers.clear();
        for (const fn of fns) fn();
      },
      pending: () => timers.size,
    };
    return api;
  }

  it("applies events and exposes per-target frames", () => {
    const scheduler = makeScheduler();
    const prev = baseLayout();
    const next = grownLayout();
    const { result } = renderHook(() =>
      useTrajectoryMotion({
        layout: next,
        prevLayout: prev,
        visibleTargetIds: next.commits.map((c) => c.id),
        events: [grow("feature", ["b0"])],
        profile: NORMAL,
        scheduler,
      }),
    );

    const f = result.current.get("b0");
    expect(f).toBeDefined();
    expect(f!.phase).toBe("enter");
    expect(f!.style.transform).toMatch(/translate/);
  });

  it("schedules a settle swap and flips enter→settled without flashing back to start", () => {
    const scheduler = makeScheduler();
    const prev = baseLayout();
    const next = grownLayout();
    const { result } = renderHook(() =>
      useTrajectoryMotion({
        layout: next,
        prevLayout: prev,
        visibleTargetIds: next.commits.map((c) => c.id),
        events: [grow("feature", ["b0"])],
        profile: NORMAL,
        scheduler,
      }),
    );

    expect(result.current.get("b0")!.phase).toBe("enter");
    expect(scheduler.pending()).toBeGreaterThan(0);

    act(() => scheduler.fire());

    const f = result.current.get("b0");
    expect(f).toBeDefined();
    expect(f!.phase).toBe("settled");
    expect(f!.style.transform).toBe("none");
  });

  it("reduced-motion renders settled immediately with no pending settle", () => {
    const scheduler = makeScheduler();
    const prev = baseLayout();
    const next = grownLayout();
    const { result } = renderHook(() =>
      useTrajectoryMotion({
        layout: next,
        prevLayout: prev,
        visibleTargetIds: next.commits.map((c) => c.id),
        events: [grow("feature", ["b0"], "running")],
        profile: REDUCED,
        scheduler,
      }),
    );

    const f = result.current.get("b0");
    expect(f!.phase).toBe("settled");
    expect(f!.style.transform).toBe("none");
    expect(f!.highlight).toBeTruthy();
    expect(scheduler.pending()).toBe(0);
  });
});
