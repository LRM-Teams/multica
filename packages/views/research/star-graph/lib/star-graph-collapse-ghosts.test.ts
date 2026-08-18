import { describe, expect, it } from "vitest";
import type { StarCanvasViewModel } from "./star-canvas-view-model";
import { selectStarGraphCollapseGhosts } from "./star-graph-collapse-ghosts";

function model(ids: readonly string[]): StarCanvasViewModel {
  return {
    entities: ids.map((id, index) => ({
      id,
      x: index * 100,
      y: index * 80,
      radius: 24,
      view: { tier: id === "root" ? "xl" : "s", state: "default", title: id },
    })),
    relations: [],
    clusters: [],
    frontiers: [],
    rootId: "root",
    version: ids.join("-"),
    stats: {},
    diagnostics: {},
  } as unknown as StarCanvasViewModel;
}

describe("selectStarGraphCollapseGhosts", () => {
  it("returns only declared nodes removed from the current projection", () => {
    const ghosts = selectStarGraphCollapseGhosts(
      model(["root", "child-a", "child-b", "unrelated"]),
      model(["root", "child-b", "unrelated"]),
      {
        sequence: 7,
        kind: "collapse",
        rootNodeId: "root",
        revealedNodeIds: ["child-a", "child-b", "unknown"],
      },
    );

    expect(ghosts.map(({ entity }) => entity.id)).toEqual(["child-a"]);
    expect(ghosts[0]).toMatchObject({ targetX: 0, targetY: 0, delayMs: 0 });
  });

  it("does not infer descendants without an explicit collapse transaction", () => {
    expect(
      selectStarGraphCollapseGhosts(
        model(["root", "child"]),
        model(["root"]),
        { sequence: 8, kind: "expand", rootNodeId: "root", revealedNodeIds: ["child"] },
      ),
    ).toEqual([]);
  });
});
