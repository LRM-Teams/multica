import { describe, expect, it } from "vitest";

import {
  layoutStarGraph,
  type StarGraphLayoutNode,
} from "./star-graph-layout";
import {
  nodeOcclusionCheck,
  safeLayoutBox,
  translateLayoutInto,
} from "./star-graph-viewport";

function fixtureNodes(): StarGraphLayoutNode[] {
  return [
    { id: "goal", tier: "xxl" },
    { id: "a1", tier: "xl", clusterId: "A" },
    { id: "a2", tier: "l", clusterId: "A" },
    { id: "a3", tier: "m", clusterId: "A" },
    { id: "b1", tier: "xl", clusterId: "B" },
    { id: "b2", tier: "m", clusterId: "B" },
    { id: "s1", tier: "s", parentId: "a1" },
    { id: "s2", tier: "s", parentId: "b1" },
  ];
}

describe("LRM-1514 D5 viewport occlusion (AC: core nodes never occluded)", () => {
  it("safeLayoutBox leaves clear room for the right panel", () => {
    const box = safeLayoutBox({ viewport: { width: 1440, height: 900 }, rightPanelWidth: 360 });
    // available width excludes the panel + padding.
    expect(box.width).toBeCloseTo(1440 - 360 - 24 * 2, 6);
    expect(box.height).toBeCloseTo(900 - 48, 6);
  });

  it("no core node is occluded once fitted into the safe band", () => {
    const layout = layoutStarGraph(fixtureNodes());
    // Fit into a 1440x900 canvas with chat bar open (360px right panel).
    const fitted = translateLayoutInto(layout, { width: 1440, height: 900 }, { rightPanelWidth: 360 });
    const report = nodeOcclusionCheck(fitted, { width: 1440, height: 900 }, { rightPanelWidth: 360 });
    expect(report.occludedIds).toEqual([]);
    expect(report.rootOccluded).toBe(false);
  });

  it("all viewport sizes + chat on/off keep the goal free of occlusion", () => {
    const layout = layoutStarGraph(fixtureNodes());
    const cases: Array<[number, number, number, string]> = [
      [1440, 900, 360, "1440x900 chat-open"],
      [1440, 900, 0, "1440x900 chat-closed"],
      [1920, 1080, 360, "1920x1080 chat-open"],
      [1920, 1080, 0, "1920x1080 chat-closed"],
      [1280, 720, 300, "narrow 1280x720 chat-open"],
      [1024, 768, 260, "narrow 1024x768"],
      [448, 900, 0, "tablet 768x900 with 320 sibling rail already excluded"],
      [360, 800, 0, "mobile 360x800 sheet-closed"],
      [720, 450, 0, "1440x900 at 200% browser zoom"],
    ];
    for (const [w, h, panel, label] of cases) {
      const fitted = translateLayoutInto(layout, { width: w, height: h }, { rightPanelWidth: panel });
      const report = nodeOcclusionCheck(fitted, { width: w, height: h }, { rightPanelWidth: panel });
      expect(report.rootOccluded).toBe(false);
      expect(report.occludedIds).toEqual([]);
      void label;
    }
  });

  it("discloses occlusion before fitting (raw layout may exceed the band)", () => {
    const layout = layoutStarGraph(fixtureNodes());
    // A raw unscaled layout on a narrow canvas with a panel should report that
    // some node would be clipped — proving the check is not a vacuous pass.
    const raw = nodeOcclusionCheck(layout, { width: 640, height: 480 }, { rightPanelWidth: 220 });
    expect(raw.occludedIds.length).toBeGreaterThan(0);
  });

  it("translate/scale preserves relative geometry (no visible reshuffle)", () => {
    const layout = layoutStarGraph(fixtureNodes());
    const fittedL = translateLayoutInto(layout, { width: 1440, height: 900 }, { rightPanelWidth: 360 });
    const fittedR = translateLayoutInto(layout, { width: 1440, height: 900 }, { rightPanelWidth: 0 });
    // For each pair of nodes, the signed dx/dy ordering is preserved.
    const a1a = fittedL.nodes.find((n) => n.id === "a1")!;
    const a1b = fittedR.nodes.find((n) => n.id === "a1")!;
    const goalA = fittedL.nodes.find((n) => n.id === "goal")!;
    const goalB = fittedR.nodes.find((n) => n.id === "goal")!;
    // The relative offset between goal and a1 must be identical across the two
    // views (only a uniform translate differs), so clusters never reshuffle.
    expect(a1a.x - goalA.x).toBeCloseTo(a1b.x - goalB.x, 3);
    expect(a1a.y - goalA.y).toBeCloseTo(a1b.y - goalB.y, 3);
  });
});
