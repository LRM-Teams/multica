import { describe, expect, it } from "vitest";
import type { StarCanvasViewModel } from "./star-canvas-view-model";
import { selectStarGraphCollapseRelations } from "./star-graph-collapse-relations";

const previous = {
  entities: [
    { id: "root" },
    { id: "child" },
    { id: "sibling" },
  ],
  relations: [
    { id: "declared", fromNodeId: "root", toNodeId: "child" },
    { id: "unrelated", fromNodeId: "root", toNodeId: "sibling" },
    { id: "lateral", fromNodeId: "child", toNodeId: "sibling" },
  ],
} as unknown as StarCanvasViewModel;

const current = {
  entities: [{ id: "root" }, { id: "sibling" }],
  relations: [{ id: "unrelated", fromNodeId: "root", toNodeId: "sibling" }],
} as unknown as StarCanvasViewModel;

describe("selectStarGraphCollapseRelations", () => {
  it("retains only removed root relations named by the collapse transaction", () => {
    const result = selectStarGraphCollapseRelations(previous, current, {
      sequence: 3,
      kind: "collapse",
      rootNodeId: "root",
      revealedNodeIds: ["child"],
    });

    expect(result.map((relation) => relation.id)).toEqual(["declared"]);
    expect(result[0]).toMatchObject({ fromNodeId: "child", toNodeId: "root" });
  });

  it("returns nothing without an explicit collapse", () => {
    expect(
      selectStarGraphCollapseRelations(previous, current, {
        sequence: 4,
        kind: "expand",
        rootNodeId: "root",
        revealedNodeIds: ["child"],
      }),
    ).toEqual([]);
  });
});
