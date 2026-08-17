import { describe, expect, it } from "vitest";
import type { StarEntityView } from "./star-canvas-view-model";
import { buildStarGraphFusionGhosts } from "./star-graph-fusion-transition";

function entity(id: string, x: number, tier: StarEntityView["tier"]): StarEntityView {
  return {
    id,
    tier,
    x,
    y: 100,
    radius: tier === "s" ? 29 : 48,
    label: { halfWidth: 20, halfHeight: 10 },
    clusterId: null,
    angle: 0,
    radiusOffset: 0,
    parentId: null,
    view: {
      id,
      tier,
      tierSource: "typed",
      state: "stable",
      title: id,
    },
  };
}

describe("buildStarGraphFusionGhosts", () => {
  it("moves only explicitly declared removed sources toward the successor", () => {
    const previous = [
      entity("source-a", 20, "s"),
      entity("source-b", 80, "s"),
      entity("unrelated", 140, "s"),
    ];
    const current = [entity("successor", 200, "m")];

    expect(
      buildStarGraphFusionGhosts(previous, current, {
        sequence: 7,
        successorNodeId: "successor",
        sourceNodeIds: ["source-a", "source-b", "source-a", "unknown"],
      }),
    ).toEqual([
      expect.objectContaining({
        id: "source-a",
        translateX: 180,
        translateY: 0,
      }),
      expect.objectContaining({
        id: "source-b",
        translateX: 120,
        translateY: 0,
      }),
    ]);
  });

  it("does not ghost a source that remains visible in the committed view", () => {
    const source = entity("source", 20, "s");
    expect(
      buildStarGraphFusionGhosts(
        [source],
        [source, entity("successor", 200, "m")],
        {
          sequence: 8,
          successorNodeId: "successor",
          sourceNodeIds: ["source"],
        },
      ),
    ).toEqual([]);
  });
});
