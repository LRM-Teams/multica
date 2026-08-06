import { describe, expect, it } from "vitest";
import {
  SEMANTIC_MOTION_NODE_DURATION_MS,
  SEMANTIC_MOTION_TOTAL_BUDGET_MS,
  interruptSemanticAggregationMotion,
  planSemanticAggregationMotion,
  type SemanticMotionNodeSnapshot,
} from "./semantic-aggregation-motion";

function node(
  id: string,
  parentId: string | null,
  depth: number,
): SemanticMotionNodeSnapshot {
  return { id, parentId, depth };
}

describe("planSemanticAggregationMotion", () => {
  it("plans split operations from a stable aggregate to newly visible children", () => {
    const previous = [node("A", null, 0)];
    const next = [node("A", null, 0), node("a", "A", 1), node("b", "A", 1)];

    const plan = planSemanticAggregationMotion(previous, next);

    expect(plan.kind).toBe("split");
    expect(plan.stableIds).toEqual(["A"]);
    expect(plan.operations).toEqual([
      {
        phase: "split",
        nodeId: "a",
        anchorId: "A",
        delayMs: 80,
        durationMs: SEMANTIC_MOTION_NODE_DURATION_MS,
      },
      {
        phase: "split",
        nodeId: "b",
        anchorId: "A",
        delayMs: 116,
        durationMs: SEMANTIC_MOTION_NODE_DURATION_MS,
      },
    ]);
  });

  it("plans merge operations from disappearing children back into their aggregate", () => {
    const previous = [node("A", null, 0), node("a", "A", 1), node("b", "A", 1)];
    const next = [node("A", null, 0)];

    const plan = planSemanticAggregationMotion(previous, next);

    expect(plan.kind).toBe("merge");
    expect(plan.stableIds).toEqual(["A"]);
    expect(plan.operations.map(({ phase, nodeId, anchorId }) => ({
      phase,
      nodeId,
      anchorId,
    }))).toEqual([
      { phase: "merge", nodeId: "a", anchorId: "A" },
      { phase: "merge", nodeId: "b", anchorId: "A" },
    ]);
  });

  it("returns no animation when the visible semantic tree is unchanged", () => {
    const frame = [node("A", null, 0), node("a", "A", 1)];

    const plan = planSemanticAggregationMotion(frame, frame);

    expect(plan).toEqual({
      kind: "stable",
      stableIds: ["A", "a"],
      operations: [],
      enterIds: [],
      exitIds: [],
      totalDurationMs: 0,
      interrupted: false,
    });
  });

  it("caps large-group stagger and total duration within the canvas budget", () => {
    const previous = [node("A", null, 0)];
    const next = [
      node("A", null, 0),
      ...Array.from({ length: 100 }, (_, index) => node(`child-${index}`, "A", 1)),
    ];

    const plan = planSemanticAggregationMotion(previous, next);
    const delays = plan.operations.map((operation) => operation.delayMs);

    expect(Math.max(...delays)).toBeLessThanOrEqual(296);
    expect(plan.totalDurationMs).toBeLessThanOrEqual(SEMANTIC_MOTION_TOTAL_BUDGET_MS);
    expect(plan.operations.every((operation) => operation.durationMs <= 320)).toBe(true);
  });
});

describe("semantic aggregation motion boundaries", () => {
  it("stages a multi-level split from parent aggregate to child aggregate to leaf", () => {
    const previous = [node("A+", null, 0)];
    const next = [
      node("A+", null, 0),
      node("A", "A+", 1),
      node("a", "A", 2),
    ];

    const plan = planSemanticAggregationMotion(previous, next);

    expect(plan.operations.map(({ nodeId, anchorId, delayMs }) => ({
      nodeId,
      anchorId,
      delayMs,
    }))).toEqual([
      { nodeId: "A", anchorId: "A+", delayMs: 80 },
      { nodeId: "a", anchorId: "A", delayMs: 116 },
    ]);
  });

  it("merges a multi-level branch from deepest leaf toward the surviving root", () => {
    const previous = [
      node("A+", null, 0),
      node("A", "A+", 1),
      node("a", "A", 2),
    ];
    const next = [node("A+", null, 0)];

    const plan = planSemanticAggregationMotion(previous, next);

    expect(plan.operations.map(({ nodeId, anchorId, delayMs }) => ({
      nodeId,
      anchorId,
      delayMs,
    }))).toEqual([
      { nodeId: "a", anchorId: "A", delayMs: 80 },
      { nodeId: "A", anchorId: "A+", delayMs: 116 },
    ]);
  });

  it("plans a regroup when a visible node moves between semantic aggregates", () => {
    const previous = [
      node("root", null, 0),
      node("A", "root", 1),
      node("B", "root", 1),
      node("a", "A", 2),
    ];
    const next = [
      node("root", null, 0),
      node("A", "root", 1),
      node("B", "root", 1),
      node("a", "B", 2),
    ];

    const plan = planSemanticAggregationMotion(previous, next);

    expect(plan.kind).toBe("regroup");
    expect(plan.stableIds).toEqual(["root", "A", "B"]);
    expect(plan.operations).toContainEqual({
      phase: "regroup",
      nodeId: "a",
      anchorId: "B",
      fromAnchorId: "A",
      delayMs: 80,
      durationMs: SEMANTIC_MOTION_NODE_DURATION_MS,
    });
  });

  it("classifies simultaneous folding and unfolding as mixed", () => {
    const previous = [
      node("A", null, 0),
      node("a", "A", 1),
      node("B", null, 0),
    ];
    const next = [
      node("A", null, 0),
      node("B", null, 0),
      node("b", "B", 1),
    ];

    const plan = planSemanticAggregationMotion(previous, next);

    expect(plan.kind).toBe("mixed");
    expect(plan.operations.map((operation) => operation.phase)).toEqual(["merge", "split"]);
  });

  it("uses instant operations when reduced motion is requested", () => {
    const plan = planSemanticAggregationMotion(
      [node("A", null, 0)],
      [node("A", null, 0), node("a", "A", 1)],
      { reducedMotion: true },
    );

    expect(plan.kind).toBe("split");
    expect(plan.operations).toHaveLength(1);
    expect(plan.operations[0]).toMatchObject({ delayMs: 0, durationMs: 0 });
    expect(plan.totalDurationMs).toBe(0);
  });

  it("settles every operation immediately when the user interrupts", () => {
    const running = planSemanticAggregationMotion(
      [node("A", null, 0)],
      [node("A", null, 0), node("a", "A", 1), node("b", "A", 1)],
    );

    const interrupted = interruptSemanticAggregationMotion(running);

    expect(interrupted.interrupted).toBe(true);
    expect(interrupted.totalDurationMs).toBe(0);
    expect(interrupted.operations.every((operation) => (
      operation.delayMs === 0 && operation.durationMs === 0
    ))).toBe(true);
  });

  it("routes unrelated root replacement through ordinary enter and exit paths", () => {
    const plan = planSemanticAggregationMotion(
      [node("old-root", null, 0)],
      [node("new-root", null, 0)],
    );

    expect(plan.kind).toBe("replace");
    expect(plan.operations).toEqual([]);
    expect(plan.enterIds).toEqual(["new-root"]);
    expect(plan.exitIds).toEqual(["old-root"]);
  });
});


describe("semantic aggregation motion review regressions", () => {
  it("preserves a null regroup destination when a child becomes a root", () => {
    const plan = planSemanticAggregationMotion(
      [node("A", null, 0), node("a", "A", 1)],
      [node("A", null, 0), node("a", null, 0)],
    );

    expect(plan.operations).toContainEqual({
      phase: "regroup",
      nodeId: "a",
      anchorId: null,
      fromAnchorId: "A",
      delayMs: 80,
      durationMs: SEMANTIC_MOTION_NODE_DURATION_MS,
    });
  });

  it("preserves a null regroup origin when a root joins an aggregate", () => {
    const plan = planSemanticAggregationMotion(
      [node("B", null, 0), node("a", null, 0)],
      [node("B", null, 0), node("a", "B", 1)],
    );

    expect(plan.operations).toContainEqual({
      phase: "regroup",
      nodeId: "a",
      anchorId: "B",
      fromAnchorId: null,
      delayMs: 80,
      durationMs: SEMANTIC_MOTION_NODE_DURATION_MS,
    });
  });

  it("leaves depth-only geometry changes to canvas reorg motion", () => {
    const plan = planSemanticAggregationMotion(
      [node("A", null, 0), node("a", "A", 1)],
      [node("A", null, 0), node("a", "A", 2)],
    );

    expect(plan.kind).toBe("stable");
    expect(plan.stableIds).toEqual(["A", "a"]);
    expect(plan.operations).toEqual([]);
  });

  it("deduplicates repeated snapshot IDs before planning", () => {
    const plan = planSemanticAggregationMotion(
      [node("A", null, 0), node("A", "ignored", 99)],
      [node("A", null, 0), node("A", "ignored", 99)],
    );

    expect(plan.kind).toBe("stable");
    expect(plan.stableIds).toEqual(["A"]);
    expect(plan.operations).toEqual([]);
  });

  it("classifies semantic motion plus root replacement as mixed", () => {
    const plan = planSemanticAggregationMotion(
      [node("A", null, 0), node("old-root", null, 0)],
      [node("A", null, 0), node("a", "A", 1), node("new-root", null, 0)],
    );

    expect(plan.kind).toBe("mixed");
    expect(plan.operations.map((operation) => operation.phase)).toEqual(["split"]);
    expect(plan.enterIds).toEqual(["new-root"]);
    expect(plan.exitIds).toEqual(["old-root"]);
  });
});
