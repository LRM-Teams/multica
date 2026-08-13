import { describe, expect, it } from "vitest";
import type { StarEntityView } from "./star-canvas-view-model";
import {
  LOW_ZOOM_CLUSTER_COLLAPSE,
  STAR_GRAPH_DOM_BUDGET,
  OVERVIEW_LANDMARK_BUDGET,
  computeClusterHiddenCounts,
  effectiveEntityBudget,
  filterEntitiesForCanvasDisplay,
  filterRelationsToVisibleEntities,
  selectVisibleEntityIds,
} from "./star-graph-visible-budget";
import { emptyCanvasFilter } from "@multica/core/research";

function entity(
  partial: Pick<StarEntityView, "id" | "tier"> &
    Partial<Pick<StarEntityView, "view" | "clusterId">> &
    { state?: StarEntityView["view"]["state"] },
): StarEntityView {
  return {
    id: partial.id,
    tier: partial.tier,
    x: 0,
    y: 0,
    radius: 40,
    label: { halfWidth: 20, halfHeight: 10 },
    clusterId: partial.clusterId ?? null,
    angle: 0,
    radiusOffset: 0,
    parentId: null,
    view: {
      id: partial.id,
      tier: partial.tier,
      tierSource: "typed",
      state: partial.state ?? "default",
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

  it("caps visible entities at the desktop DOM budget", () => {
    const entities = [
      entity({ id: "goal", tier: "xxl" }),
      ...Array.from({ length: 250 }, (_, index) =>
        entity({ id: `node-${index}`, tier: index % 3 === 0 ? "l" : "s" }),
      ),
    ];
    const visible = selectVisibleEntityIds(entities, { rootId: "goal" });
    expect(visible.size).toBeLessThanOrEqual(STAR_GRAPH_DOM_BUDGET);
    expect(visible.has("goal")).toBe(true);
  });

  it("reduces budget at low zoom", () => {
    expect(effectiveEntityBudget(220, 0.4)).toBeLessThan(220);
    expect(effectiveEntityBudget(220, 1)).toBe(220);
  });

  it("caps production Landmark-style cards at 12 in 25% overview", () => {
    const entities = [
      entity({ id: "goal", tier: "xxl" }),
      ...Array.from({ length: 40 }, (_, index) =>
        entity({
          id: `cluster-${index}`,
          tier: "xl",
          clusterId: `c-${index}`,
        }),
      ),
    ];
    const visible = selectVisibleEntityIds(entities, {
      rootId: "goal",
      zoom: 0.25,
    });

    expect(visible.size).toBe(OVERVIEW_LANDMARK_BUDGET);
    expect(visible.has("goal")).toBe(true);
  });

  it("never lets protected nodes exceed the hard DOM budget", () => {
    const entities = [
      entity({ id: "goal", tier: "xxl" }),
      ...Array.from({ length: 30 }, (_, index) =>
        entity({ id: `running-${index}`, tier: "s", state: "run" }),
      ),
    ];
    const visible = selectVisibleEntityIds(entities, {
      rootId: "goal",
      relatedNodeIds: new Set(entities.map((item) => item.id)),
      budget: 8,
    });

    expect(visible.size).toBe(8);
    expect(visible.has("goal")).toBe(true);
  });

  it("collapses to one node per cluster at very low zoom", () => {
    const entities = [
      entity({ id: "goal", tier: "xxl" }),
      entity({ id: "a1", tier: "s", clusterId: "c1" }),
      entity({ id: "a2", tier: "s", clusterId: "c1" }),
      entity({ id: "b1", tier: "s", clusterId: "c2" }),
      entity({ id: "b2", tier: "s", clusterId: "c2" }),
    ];

    const visible = selectVisibleEntityIds(entities, {
      rootId: "goal",
      zoom: LOW_ZOOM_CLUSTER_COLLAPSE - 0.1,
    });

    expect(visible.has("goal")).toBe(true);
    expect(["a1", "a2"].filter((id) => visible.has(id)).length).toBe(1);
    expect(["b1", "b2"].filter((id) => visible.has(id)).length).toBe(1);
  });
});

describe("computeClusterHiddenCounts", () => {
  it("counts hidden nodes per cluster", () => {
    const entities = [
      entity({ id: "a1", tier: "s", clusterId: "c1" }),
      entity({ id: "a2", tier: "s", clusterId: "c1" }),
    ];
    const hidden = computeClusterHiddenCounts(entities, new Set(["a1"]));
    expect(hidden.get("c1")).toBe(1);
  });
});

describe("filterEntitiesForCanvasDisplay", () => {
  it("keeps root and selection visible under an active filter", () => {
    const entities = [
      entity({ id: "goal", tier: "xxl" }),
      entity({ id: "selected", tier: "m" }),
      entity({ id: "hidden", tier: "s" }),
    ];
    const filtered = filterEntitiesForCanvasDisplay(entities, {
      filter: { status: "running" },
      nodeById: new Map([
        ["goal", { id: "goal", status: "active", level: "xxl" }],
        ["selected", { id: "selected", status: "running", level: "m" }],
        ["hidden", { id: "hidden", status: "completed", level: "s" }],
      ]),
      rootId: "goal",
      selectedNodeId: "selected",
    });
    expect(filtered.map((entry) => entry.id)).toEqual(["goal", "selected"]);
  });

  it("passes through all entities when filter is blank", () => {
    const entities = [entity({ id: "a", tier: "l" })];
    expect(
      filterEntitiesForCanvasDisplay(entities, {
        filter: emptyCanvasFilter(),
        nodeById: new Map(),
        rootId: null,
      }).length,
    ).toBe(1);
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
