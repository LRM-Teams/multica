import { describe, expect, it } from "vitest";
import type { StarCanvasViewModel } from "./star-canvas-view-model";
import { buildStarGraphExpansionMotion } from "./star-graph-expansion-motion";

const model = {
  entities: [
    { id: "root", x: 300, y: 240, radius: 80, view: {} },
    { id: "child", x: 120, y: 90, radius: 40, view: {} },
  ],
  relations: [],
  clusters: [],
  frontiers: [],
  rootId: "root",
  version: "1",
  stats: {},
  diagnostics: {},
} as unknown as StarCanvasViewModel;

describe("buildStarGraphExpansionMotion", () => {
  it("reveals only server-declared children from the expanded root position", () => {
    const directives = buildStarGraphExpansionMotion(model, {
      sequence: 4,
      kind: "expand",
      rootNodeId: "root",
      revealedNodeIds: ["child", "not-loaded"],
    });

    expect([...directives.keys()]).toEqual(["child"]);
    expect(directives.get("child")?.style).toMatchObject({
      "--expansion-origin-x": "180px",
      "--expansion-origin-y": "150px",
      "--expansion-blur": "5px",
    });
  });

  it("crystallizes the retained root when a disclosed layer collapses", () => {
    const directives = buildStarGraphExpansionMotion(model, {
      sequence: "collapse-2",
      kind: "collapse",
      rootNodeId: "root",
      revealedNodeIds: ["child"],
    });

    expect([...directives.keys()]).toEqual(["root"]);
    expect(directives.get("root")?.style.animationName).toBe(
      "research-motion-expansion-collapse",
    );
  });

  it("removes reveal blur in low-performance mode", () => {
    const directives = buildStarGraphExpansionMotion(
      model,
      {
        sequence: 5,
        kind: "expand",
        rootNodeId: "root",
        revealedNodeIds: ["child"],
      },
      true,
    );
    expect(directives.get("child")?.style["--expansion-blur"]).toBe("0px");
  });
});
