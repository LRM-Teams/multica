import { describe, expect, it } from "vitest";
import type {
  ResearchSemanticAggregate,
  ResearchSemanticTask,
  SemanticAggregationProjectionInput,
} from "./semantic-aggregation";
import {
  buildSemanticAggregationVisibleWindow,
  type SemanticAggregationVisibleWindow,
} from "./semantic-aggregation-visible-window";

function task(id: string, active = false): ResearchSemanticTask {
  return { id, kind: "task", active };
}

function aggregate(
  id: string,
  level: number,
  memberIds: readonly string[],
  stability: ResearchSemanticAggregate["stability"] = "stable",
): ResearchSemanticAggregate {
  return { id, kind: "aggregate", level, memberIds, stability };
}

function nodeIds(window: SemanticAggregationVisibleWindow, column: number): string[] {
  return window.columns[column]!.items.map((item) => item.id);
}

function makeBalancedFixture(taskCount: number): SemanticAggregationProjectionInput {
  const tasks = Array.from({ length: taskCount }, (_, index) =>
    task(`task-${String(index).padStart(4, "0")}`, index % 11 === 0),
  );
  let memberIds = tasks.map(({ id }) => id);
  const aggregates: ResearchSemanticAggregate[] = [];
  let level = 1;
  while (memberIds.length > 1) {
    const next: string[] = [];
    for (let index = 0; index < memberIds.length; index += 8) {
      const id = `aggregate-${level}-${String(index / 8).padStart(4, "0")}`;
      aggregates.push(aggregate(id, level, memberIds.slice(index, index + 8)));
      next.push(id);
    }
    memberIds = next;
    level += 1;
  }
  return { tasks, aggregates };
}

describe("buildSemanticAggregationVisibleWindow", () => {
  it("returns only ancestor, current, and direct-child columns within both budgets", () => {
    const input = makeBalancedFixture(96);
    const focusId = "aggregate-1-0003";
    const window = buildSemanticAggregationVisibleWindow(input, [focusId]);

    expect(window.columns.map((column) => column.kind)).toEqual([
      "ancestors",
      "current",
      "children",
    ]);
    expect(window.columns).toHaveLength(3);
    expect(window.columns.every((column) => column.items.length <= 8)).toBe(true);
    expect(window.visibleNodeCount).toBeLessThanOrEqual(24);
    expect(nodeIds(window, 1)).toContain(focusId);
    expect(nodeIds(window, 2)).toEqual(
      input.aggregates.find(({ id }) => id === focusId)!.memberIds,
    );
  });

  it("uses a stable remainder entry with real hidden peers and complete statistics", () => {
    const tasks = Array.from({ length: 12 }, (_, index) => task(`task-${index}`, index < 3));
    const input = {
      tasks,
      aggregates: [aggregate("root", 1, tasks.map(({ id }) => id), "forming")],
    };

    const first = buildSemanticAggregationVisibleWindow(input, ["root"], {
      perColumnBudget: 5,
    });
    const second = buildSemanticAggregationVisibleWindow(input, ["root"], {
      perColumnBudget: 5,
    });
    const remainder = first.columns[2].items.at(-1);

    expect(first).toEqual(second);
    expect(first.columns[2].items).toHaveLength(5);
    expect(remainder).toMatchObject({
      id: "visible-window:children:root:overflow:4",
      kind: "remainder",
      hiddenCount: 8,
      sourceNodeIds: tasks.slice(4).map(({ id }) => id),
      stats: {
        childCount: 0,
        descendantCount: 0,
        taskStatus: { active: 0, inactive: 8 },
        aggregateStatus: { forming: 0, stable: 0 },
      },
    });
    expect(remainder?.kind === "remainder" && remainder.sourceNodeIds).not.toContain(
      remainder?.id,
    );
  });

  it("keeps the focused peer visible without changing unrelated item IDs or order", () => {
    const tasks = Array.from({ length: 20 }, (_, index) => task(`task-${index}`));
    const input = {
      tasks,
      aggregates: [aggregate("root", 1, tasks.map(({ id }) => id))],
    };
    const a = buildSemanticAggregationVisibleWindow(input, ["root", "task-12"]);
    const b = buildSemanticAggregationVisibleWindow(input, ["root", "task-13"]);

    expect(nodeIds(a, 1)).toContain("task-12");
    expect(nodeIds(b, 1)).toContain("task-13");
    expect(nodeIds(a, 1).slice(0, 6)).toEqual(nodeIds(b, 1).slice(0, 6));
  });

  it("honors a tight total budget while preserving three safe columns", () => {
    const window = buildSemanticAggregationVisibleWindow(
      makeBalancedFixture(500),
      ["aggregate-1-0010"],
      { perColumnBudget: 8, totalNodeBudget: 10 },
    );

    expect(window.columns).toHaveLength(3);
    expect(window.visibleNodeCount).toBeLessThanOrEqual(10);
    expect(window.columns.reduce((sum, column) => sum + column.items.length, 0)).toBe(
      window.visibleNodeCount,
    );
  });

  it("degrades safely for missing focus nodes, missing members, multiple parents, and cycles", () => {
    const input = {
      tasks: [task("leaf"), task("orphan")],
      aggregates: [
        aggregate("A", 1, ["leaf", "missing", "B"]),
        aggregate("B", 2, ["A", "leaf"]),
      ],
    };

    const window = buildSemanticAggregationVisibleWindow(input, ["gone", "B", "leaf"]);

    expect(window.columns).toHaveLength(3);
    expect(window.diagnostics).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ code: "missing_member", nodeId: "missing" }),
        expect.objectContaining({ code: "multiple_parents", nodeId: "leaf" }),
        expect.objectContaining({ code: "cycle", nodeId: expect.any(String) }),
        expect.objectContaining({ code: "unknown_focus", nodeId: "gone" }),
      ]),
    );
    expect(window.visibleNodeCount).toBeLessThanOrEqual(24);
  });
});

describe("semantic aggregation visible-window scale", () => {
  for (const size of [96, 500, 2_000]) {
    it(`indexes and windows a ${size}-task fixture with bounded allocation`, () => {
      const input = makeBalancedFixture(size);
      const startedAt = performance.now();
      const window = buildSemanticAggregationVisibleWindow(input, [input.tasks.at(-1)!.id]);
      const elapsedMs = performance.now() - startedAt;

      expect(window.indexedNodeCount).toBe(input.tasks.length + input.aggregates.length);
      expect(window.visibleNodeCount).toBeLessThanOrEqual(24);
      expect(window.columns.every((column) => column.items.length <= 8)).toBe(true);
      expect(window.work.nodeVisits).toBeLessThanOrEqual(window.indexedNodeCount * 8);
      expect(window.work.edgeVisits).toBeLessThanOrEqual(
        input.aggregates.reduce((sum, node) => sum + node.memberIds.length, 0) * 4,
      );
      expect(elapsedMs).toBeLessThan(100);
    });
  }
});
