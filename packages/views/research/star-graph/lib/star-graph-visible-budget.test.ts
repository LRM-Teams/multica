import { describe, expect, it } from "vitest";
import type { StarEntityView } from "./star-canvas-view-model";
import {
  STAR_GRAPH_DOM_BUDGET,
  filterRelationsToVisibleEntities,
  selectVisibleEntityIds,
} from "./star-graph-visible-budget";

function entity(
  partial: Pick<StarEntityView, "id" | "tier"> &
    Partial<Pick<StarEntityView, "view">>,
): StarEntityView {
  return {
    id: partial.id,
    tier: partial.tier,
    x: 0,
    y: 0,
    radius: 40,
    label: { halfWidth: 20, halfHeight: 10 },
    clusterId: null,
    angle: 0,
    radiusOffset: 0,
    parentId: null,
    view: {
      id: partial.id,
      tier: partial.tier,
      tierSource: "typed",
      state: "default",
      title: partial.id,
      ...(partial.view ?? {}),
    },
  };
}

describe("selectVisibleEntityIds", () => {
  it("returns all ids when under budget", () => {
    const entities = [
      entity({ id: "goal", tier: "xxl" }),
      entity({ id: "a", tier: "l" }),
    ];
    expect(selectVisibleEntityIds(entities, { rootId: "goal" })).toEqual(
      new Set(["goal", "a"]),
    );
  });

  it("keeps root, selection and related nodes before lower tiers", () => {
    const entities = [
      entity({ id: "goal", tier: "xxl" }),
      entity({ id: "selected", tier: "m" }),
      entity({ id: "related", tier: "s" }),
      ...Array.from({ length: 8 }, (_, index) =>
        entity({ id: `filler-${index}`, tier: "s" }),
      ),
    ];

    const visible = selectVisibleEntityIds(entities, {
      rootId: "goal",
      selectedNodeId: "selected",
      relatedNodeIds: new Set(["related"]),
      budget: 5,
    });

    expect(visible.has("goal")).toBe(true);
    expect(visible.has("selected")).toBe(true);
    expect(visible.has("related")).toBe(true);
    expect(visible.size).toBe(5);
  });

  it("uses the D5 default budget constant", () => {
    expect(STAR_GRAPH_DOM_BUDGET).toBe(220);
  });
});

describe("filterRelationsToVisibleEntities", () => {
  it("drops edges when either endpoint is hidden", () => {
    const visible = new Set(["a", "b"]);
    const relations = [
      { id: "e1", fromNodeId: "a", toNodeId: "b" },
      { id: "e2", fromNodeId: "a", toNodeId: "c" },
    ];
    expect(filterRelationsToVisibleEntities(relations, visible)).toEqual([
      relations[0],
    ]);
  });
});
