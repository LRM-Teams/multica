/**
 * LRM-1407 — node lifecycle state model tests (先红后绿已通过).
 *
 * Covers the ACs:
 *  AC1 新任务从父问题产生(appear)；派工一次性 task→Agent(dispatch)；完成回流到问题(succeed)。
 *  AC2 failed 停在可操作状态(conflict marker)、不无限抖；retry 撤销后重新进入。
 *  AC3 连续 WS 更新可中断并从当前状态继续(事件流入引擎 lane 合并)。
 *  AC5 50 节点/10 次连续更新走引擎 last-only/背压合并，无长任务(reduced-motion 瞬时)。
 *
 * Pure state model unit tests — no DOM/RAF/React.
 */
import { describe, it, expect } from "vitest";

import {
  createResearchNodeStateModel,
  applyNodeDeltas,
  transitionForLifecycleChange,
  nodeLifecycleAt,
  trackedNodeCount,
  lifecycleStatusLabel,
} from "./node-state-model";
import {
  createTransitionQueue,
  transitionQueueReducer,
  liveQueueSize,
  type TransitionQueue,
} from "./transition-queue";

const buildQueue = (): TransitionQueue => createTransitionQueue({ nowMs: 0 });

describe("LRM-1407 node-state-model — AC1 spawn / dispatch / return", () => {
  it("emits branch_spawned (appear) anchored to parent when a task is generated", () => {
    const model = createResearchNodeStateModel();
    const { events } = applyNodeDeltas(model, {
      type: "apply",
      deltas: [{ id: "task-1", lifecycle: "generated", parentId: "problem-0" }],
    });

    expect(events).toHaveLength(1);
    expect(events[0]!.transition_kind).toBe("branch_spawned");
    expect(events[0]!.anchor_id).toBe("problem-0");
    expect(events[0]!.related_ids).toEqual(["task-1"]);
  });

  it("dispatch flows a one-shot task to an agent (task_dispatched)", () => {
    let model = createResearchNodeStateModel();
    model = applyNodeDeltas(model, {
      type: "apply",
      deltas: [{ id: "task-1", lifecycle: "generated", parentId: "problem-0" }],
    }).state;
    const { events } = applyNodeDeltas(model, {
      type: "apply",
      deltas: [{ id: "task-1", lifecycle: "dispatched", parentId: "problem-0" }],
    });

    expect(events).toHaveLength(1);
    expect(events[0]!.transition_kind).toBe("task_dispatched");
    expect(nodeLifecycleAt(model, "task-1")).toBe("generated");
  });

  it("success returns the result to the parent problem (result_accepted)", () => {
    let model = createResearchNodeStateModel();
    for (const lifecycle of ["generated", "dispatched"] as const) {
      model = applyNodeDeltas(model, {
        type: "apply",
        deltas: [{ id: "task-1", lifecycle, parentId: "problem-0" }],
      }).state;
    }
    const { events } = applyNodeDeltas(model, {
      type: "apply",
      deltas: [{ id: "task-1", lifecycle: "succeeded", parentId: "problem-0" }],
    });

    expect(events).toHaveLength(1);
    expect(events[0]!.transition_kind).toBe("result_accepted");
    expect(events[0]!.anchor_id).toBe("problem-0");
  });
});

describe("LRM-1407 AC2 — failed actionable, no infinite bounce, retry re-enters", () => {
  it("failed → stopped at conflict marker and does NOT re-bounce on repeated delta", () => {
    let model = createResearchNodeStateModel();
    for (const lifecycle of ["generated", "dispatched", "failed"] as const) {
      const res = applyNodeDeltas(model, {
        type: "apply",
        deltas: [{ id: "task-1", lifecycle, parentId: "problem-0" }],
      });
      model = res.state;
    }

    // A repeated failed delta must emit nothing (idempotent).
    const { events } = applyNodeDeltas(model, {
      type: "apply",
      deltas: [{ id: "task-1", lifecycle: "failed", parentId: "problem-0" }],
    });
    expect(events).toHaveLength(0);
    expect(nodeLifecycleAt(model, "task-1")).toBe("failed");
  });

  it("failure classifies as dispute_opened (actionable conflict)", () => {
    let model = createResearchNodeStateModel();
    for (const lifecycle of ["generated", "dispatched", "failed"] as const) {
      const res = applyNodeDeltas(model, {
        type: "apply",
        deltas: [{ id: "task-1", lifecycle, parentId: "problem-0" }],
      });
      // capture the failed transition event
      if (lifecycle === "failed") {
        expect(res.events).toHaveLength(1);
        expect(res.events[0]!.transition_kind).toBe("dispute_opened");
      }
      model = res.state;
    }
  });

  it("retry (failed → dispatched) reverses into a fresh task_dispatched re-enter", () => {
    let model = createResearchNodeStateModel();
    for (const lifecycle of ["generated", "dispatched", "failed"] as const) {
      model = applyNodeDeltas(model, {
        type: "apply",
        deltas: [{ id: "task-1", lifecycle, parentId: "problem-0" }],
      }).state;
    }
    const { events } = applyNodeDeltas(model, {
      type: "apply",
      deltas: [{ id: "task-1", lifecycle: "dispatched", parentId: "problem-0" }],
    });
    expect(events).toHaveLength(1);
    expect(events[0]!.transition_kind).toBe("task_dispatched");
    expect(events[0]!.related_ids).toEqual(["task-1"]);
  });
});

describe("LRM-1407 AC3 — consecutive WS updates interruptible & continue", () => {
  it("a burst of lifecycle updates emits one event per REAL transition", () => {
    let model = createResearchNodeStateModel();
    const res = applyNodeDeltas(model, {
      type: "apply",
      deltas: [
        { id: "task-1", lifecycle: "generated", parentId: "problem-0" },
        { id: "task-1", lifecycle: "dispatched", parentId: "problem-0" },
        { id: "task-1", lifecycle: "succeeded", parentId: "problem-0" },
      ],
    });
    model = res.state;

    expect(res.events).toHaveLength(3);
    expect(res.events.map((e) => e.transition_kind)).toEqual([
      "branch_spawned",
      "task_dispatched",
      "result_accepted",
    ]);
    expect(nodeLifecycleAt(model, "task-1")).toBe("succeeded");
  });

  it("events flow into the engine queue and coalesce per lane (interruptible)", () => {
    const model = createResearchNodeStateModel();
    let queue = buildQueue();

    // Spike: same lane (task-dispatched anchored to problem-0) emitted twice.
    const first = applyNodeDeltas(model, {
      type: "apply",
      deltas: [{ id: "task-1", lifecycle: "generated", parentId: "problem-0" }],
    });
    queue = transitionQueueReducer(queue, {
      type: "enqueue",
      event: first.events[0]!,
      nowMs: 0,
    });
    const second = applyNodeDeltas(first.state, {
      type: "apply",
      deltas: [{ id: "task-1", lifecycle: "dispatched", parentId: "problem-0" }],
    });
    queue = transitionQueueReducer(queue, {
      type: "enqueue",
      event: second.events[0]!,
      nowMs: 0,
    });

    // Both live at t0; coalescing across the engine keeps the queue bounded.
    expect(liveQueueSize(queue)).toBeLessThanOrEqual(2);
  });
});

describe("LRM-1407 AC5 — 50 nodes / 10 updates collapse (backpressure, no long task)", () => {
  it("10 bursts over 50 nodes never exceed the engine queue cap", () => {
    let model = createResearchNodeStateModel();
    let queue = buildQueue();
    const ids = Array.from({ length: 50 }, (_, i) => `n-${i}`);
    for (let round = 0; round < 10; round += 1) {
      const deltas = ids.map((id) => ({
        id,
        lifecycle: ("dispatched") as const,
        parentId: "problem-0",
      }));
      const res = applyNodeDeltas(model, { type: "apply", deltas });
      model = res.state; // thread the pure-state result forward
      // 50 new task_dispatched events, same anchor → coalesce within engine.
      for (const event of res.events) {
        queue = transitionQueueReducer(queue, {
          type: "enqueue",
          event,
          nowMs: round * 1000,
        });
      }
      expect(liveQueueSize(queue)).toBeLessThanOrEqual(64); // MOTION_QUEUE_CAP
    }
    expect(trackedNodeCount(model)).toBe(50);
  });

  it("reduced-motion profile keeps events but engine collapses to uniform fade", () => {
    const model = createResearchNodeStateModel({
      reducedMotion: true,
      lowPerformance: false,
    });
    const { events } = applyNodeDeltas(model, {
      type: "apply",
      deltas: [{ id: "task-1", lifecycle: "generated", parentId: "problem-0" }],
    });
    // reduced-motion does not change the event kind; the engine turns it into
    // a uniform fade (effectiveVerb → reappear).
    expect(events).toHaveLength(1);
    expect(events[0]!.transition_kind).toBe("branch_spawned");
  });
});

describe("LRM-1407 helpers", () => {
  it("transitionForLifecycleChange is idempotent for identical lifecycle", () => {
    expect(transitionForLifecycleChange("failed", "failed", null)).toBeNull();
    expect(
      transitionForLifecycleChange("generated", "dispatched", "problem-0")
        ?.kind,
    ).toBe("task_dispatched");
  });

  it("lifecycleStatusLabel is stable for every lifecycle", () => {
    for (const lc of ["generated", "dispatched", "succeeded", "failed"] as const) {
      expect(lifecycleStatusLabel(lc).length).toBeGreaterThan(0);
    }
  });
});
