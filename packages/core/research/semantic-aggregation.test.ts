import { describe, expect, it } from "vitest";
import {
  buildSemanticAggregationProjection,
  type ResearchSemanticAggregate,
  type ResearchSemanticTask,
  type SemanticAggregationProjection,
} from "./semantic-aggregation";

function task(id: string, active = false): ResearchSemanticTask {
  return { id, kind: "task", active };
}

function aggregate(
  id: string,
  level: number,
  memberIds: string[],
  stability: ResearchSemanticAggregate["stability"] = "stable",
): ResearchSemanticAggregate {
  return { id, kind: "aggregate", level, memberIds, stability };
}

function ready(
  projection: SemanticAggregationProjection,
): Extract<SemanticAggregationProjection, { status: "ready" }> {
  expect(projection.status).toBe("ready");
  if (projection.status !== "ready") throw new Error("expected ready projection");
  return projection;
}

describe("buildSemanticAggregationProjection", () => {
  it("reports missing aggregate members instead of fabricating a tree", () => {
    const projection = buildSemanticAggregationProjection({
      tasks: [task("a")],
      aggregates: [aggregate("A", 1, ["a", "missing"])],
    });

    expect(projection).toEqual({
      status: "invalid",
      issues: [
        {
          code: "missing_member",
          aggregateId: "A",
          memberId: "missing",
        },
      ],
    });
  });

  it("folds stable branches but expands every path containing active work", () => {
    const projection = ready(
      buildSemanticAggregationProjection({
        tasks: [task("a"), task("b"), task("c", true), task("d")],
        aggregates: [
          aggregate("A", 1, ["a", "b"]),
          aggregate("B", 1, ["c", "d"]),
          aggregate("A+", 2, ["A", "B"]),
        ],
      }),
    );

    expect(projection.roots).toEqual(["A+"]);
    expect(projection.autoExpandedIds).toEqual(["A+", "B"]);
    expect(
      projection.visibleNodes.map(({ id, depth, collapsed }) => ({
        id,
        depth,
        collapsed,
      })),
    ).toEqual([
      { id: "A+", depth: 0, collapsed: false },
      { id: "A", depth: 1, collapsed: true },
      { id: "B", depth: 1, collapsed: false },
      { id: "c", depth: 2, collapsed: false },
      { id: "d", depth: 2, collapsed: false },
    ]);
  });

  it("expands one completed branch when the user explicitly opens it", () => {
    const projection = ready(
      buildSemanticAggregationProjection(
        {
          tasks: [task("a"), task("b"), task("c", true), task("d")],
          aggregates: [
            aggregate("A", 1, ["a", "b"]),
            aggregate("B", 1, ["c", "d"]),
            aggregate("A+", 2, ["A", "B"]),
          ],
        },
        { expandedIds: new Set(["A"]) },
      ),
    );

    expect(projection.visibleNodes.map((node) => node.id)).toEqual([
      "A+",
      "A",
      "a",
      "b",
      "B",
      "c",
      "d",
    ]);
    expect(projection.expandedIds).toEqual(["A+", "A", "B"]);
  });
});


describe("semantic aggregation contract boundaries", () => {
  it("rejects cyclic aggregate membership instead of recursing forever", () => {
    const projection = buildSemanticAggregationProjection({
      tasks: [],
      aggregates: [
        aggregate("A", 1, ["B"]),
        aggregate("B", 2, ["A"]),
      ],
    });

    expect(projection.status).toBe("invalid");
    if (projection.status !== "invalid") throw new Error("expected invalid projection");
    expect(projection.issues.some((issue) => issue.code === "cycle")).toBe(true);
  });

  it("rejects a member assigned to more than one aggregate", () => {
    const projection = buildSemanticAggregationProjection({
      tasks: [task("a")],
      aggregates: [
        aggregate("A", 1, ["a"]),
        aggregate("B", 1, ["a"]),
      ],
    });

    expect(projection.status).toBe("invalid");
    if (projection.status !== "invalid") throw new Error("expected invalid projection");
    expect(projection.issues).toContainEqual({
      code: "multiple_parents",
      memberId: "a",
      parentIds: ["A", "B"],
    });
  });

  it("requires every aggregate to be exactly one level above its members", () => {
    const projection = buildSemanticAggregationProjection({
      tasks: [task("a")],
      aggregates: [aggregate("A+", 2, ["a"])],
    });

    expect(projection.status).toBe("invalid");
    if (projection.status !== "invalid") throw new Error("expected invalid projection");
    expect(projection.issues).toContainEqual({
      code: "invalid_level",
      aggregateId: "A+",
      memberId: "a",
      expectedLevel: 1,
      actualLevel: 2,
    });
  });

  it("keeps forming aggregates and their ancestors expanded", () => {
    const projection = ready(
      buildSemanticAggregationProjection({
        tasks: [task("a"), task("b")],
        aggregates: [
          aggregate("A", 1, ["a", "b"], "forming"),
          aggregate("A+", 2, ["A"]),
        ],
      }),
    );

    expect(projection.autoExpandedIds).toEqual(["A+", "A"]);
    expect(projection.visibleNodes.map((node) => node.id)).toEqual(["A+", "A", "a", "b"]);
  });

  it("keeps all ancestors of a selected completed leaf expanded", () => {
    const projection = ready(
      buildSemanticAggregationProjection(
        {
          tasks: [task("a"), task("b"), task("c")],
          aggregates: [
            aggregate("A", 1, ["a", "b"]),
            aggregate("B", 1, ["c"]),
            aggregate("A+", 2, ["A", "B"]),
          ],
        },
        { selectedId: "a" },
      ),
    );

    expect(projection.autoExpandedIds).toEqual(["A+", "A"]);
    expect(projection.visibleNodes.map((node) => node.id)).toEqual([
      "A+",
      "A",
      "a",
      "b",
      "B",
    ]);
  });

  it("keeps ungrouped active tasks visible as roots", () => {
    const projection = ready(
      buildSemanticAggregationProjection({
        tasks: [task("ungrouped", true)],
        aggregates: [],
      }),
    );

    expect(projection.roots).toEqual(["ungrouped"]);
    expect(projection.visibleNodes).toEqual([
      {
        id: "ungrouped",
        kind: "task",
        depth: 0,
        parentId: null,
        collapsed: false,
        active: true,
      },
    ]);
  });
});


describe("semantic aggregation scale and duplicate defenses", () => {
  it("rejects a member repeated within the same aggregate", () => {
    const projection = buildSemanticAggregationProjection({
      tasks: [task("a")],
      aggregates: [aggregate("A", 1, ["a", "a"])],
    });

    expect(projection.status).toBe("invalid");
    if (projection.status !== "invalid") throw new Error("expected invalid projection");
    expect(projection.issues).toContainEqual({
      code: "duplicate_member",
      aggregateId: "A",
      memberId: "a",
    });
  });

  it("projects a 12,000-level forming tree without recursive stack growth", () => {
    const depth = 12_000;
    const aggregates: ResearchSemanticAggregate[] = [];
    for (let level = 1; level <= depth; level += 1) {
      aggregates.push(
        aggregate(
          `A${level}`,
          level,
          [level === 1 ? "leaf" : `A${level - 1}`],
          "forming",
        ),
      );
    }

    const projection = ready(
      buildSemanticAggregationProjection({
        tasks: [task("leaf", true)],
        aggregates,
      }),
    );

    expect(projection.roots).toEqual([`A${depth}`]);
    expect(projection.autoExpandedIds).toHaveLength(depth);
    expect(projection.visibleNodes).toHaveLength(depth + 1);
    expect(projection.visibleNodes.at(-1)?.id).toBe("leaf");
  });
});


it("rejects a 12,000-aggregate rootless cycle without recursive stack growth", () => {
  const depth = 12_000;
  const aggregates: ResearchSemanticAggregate[] = [];
  for (let level = 1; level <= depth; level += 1) {
    aggregates.push(
      aggregate(
        `cycle-${level}`,
        level,
        [level === 1 ? `cycle-${depth}` : `cycle-${level - 1}`],
      ),
    );
  }

  const projection = buildSemanticAggregationProjection({ tasks: [], aggregates });

  expect(projection.status).toBe("invalid");
  if (projection.status !== "invalid") throw new Error("expected invalid projection");
  expect(projection.issues.some((issue) => issue.code === "cycle")).toBe(true);
});
