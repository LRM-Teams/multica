import { describe, expect, it } from "vitest";
import type { StarEntityView } from "./star-canvas-view-model";
import { selectSemanticLabelNodeIds } from "./star-graph-semantic-labels";

function entity(
  id: string,
  tier: StarEntityView["tier"],
  x: number,
  radius: number,
): StarEntityView {
  return {
    id,
    tier,
    x,
    y: 0,
    radius,
    label: { halfWidth: 45, halfHeight: 14 },
    clusterId: null,
    angle: 0,
    radiusOffset: 0,
    parentId: null,
    view: {
      id,
      tier,
      tierSource: "typed",
      state: "default",
      title: id,
    },
  };
}

describe("selectSemanticLabelNodeIds", () => {
  it("hides unreadable overview labels but always keeps the selected landmark", () => {
    const entities = [
      entity("goal", "xxl", 0, 124),
      entity("middle", "m", 300, 48),
      entity("selected", "l", 600, 84),
      entity("work", "s", 720, 29),
    ];

    expect(
      [...selectSemanticLabelNodeIds(entities, {
        zoom: 0.3,
        selectedNodeId: "selected",
      })],
    ).toEqual(["selected"]);
  });

  it("keeps the higher-tier label when two readable labels collide", () => {
    const entities = [
      entity("xl", "xl", 0, 110),
      entity("m", "m", 30, 48),
    ];

    expect([...selectSemanticLabelNodeIds(entities, { zoom: 1 })]).toEqual([
      "xl",
    ]);
  });

  it("preserves all legacy M+ labels when semantic selection is disabled", () => {
    const entities = [
      entity("xl", "xl", 0, 110),
      entity("m", "m", 10, 48),
      entity("work", "s", 20, 29),
    ];

    expect(
      [...selectSemanticLabelNodeIds(entities, { zoom: 0.25, enabled: false })],
    ).toEqual(["xl", "m"]);
  });
});
