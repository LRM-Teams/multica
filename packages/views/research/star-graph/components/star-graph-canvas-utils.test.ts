import { describe, expect, it } from "vitest";

import {
  centerCameraOnPoint,
  computeEntityBounds,
  fitCameraToBounds,
  focusCameraOnEntity,
  isEdgeLabelClear,
  planExpansionTransactionCamera,
  quadraticEdgePath,
  relationEdgeClass,
  zoomCamera,
  zoomPercent,
} from "./star-graph-canvas-utils";

describe("star-graph-canvas-utils", () => {
  it("computes bounds from entity circles", () => {
    const bounds = computeEntityBounds([
      {
        id: "a",
        tier: "l",
        x: 0,
        y: 0,
        radius: 84,
        label: { halfWidth: 40, halfHeight: 20 },
        clusterId: null,
        angle: 0,
        radiusOffset: 0,
        parentId: null,
        view: {
          id: "a",
          tier: "l",
          tierSource: "typed",
          state: "default",
          title: "A",
        },
      },
      {
        id: "b",
        tier: "s",
        x: 200,
        y: 100,
        radius: 29,
        label: { halfWidth: 20, halfHeight: 10 },
        clusterId: null,
        angle: 0,
        radiusOffset: 0,
        parentId: "a",
        view: {
          id: "b",
          tier: "s",
          tierSource: "typed",
          state: "run",
          title: "Probe",
        },
      },
    ]);

    expect(bounds).toEqual({
      minX: -84,
      minY: -84,
      maxX: 229,
      maxY: 129,
      width: 313,
      height: 213,
      centerX: 72.5,
      centerY: 22.5,
    });
  });

  it("fits camera to bounds inside viewport", () => {
    const bounds = computeEntityBounds([
      {
        id: "a",
        tier: "m",
        x: 0,
        y: 0,
        radius: 48,
        label: { halfWidth: 24, halfHeight: 12 },
        clusterId: null,
        angle: 0,
        radiusOffset: 0,
        parentId: null,
        view: {
          id: "a",
          tier: "m",
          tierSource: "typed",
          state: "default",
          title: "A",
        },
      },
    ])!;
    const camera = fitCameraToBounds(bounds, { width: 800, height: 600 });
    expect(camera.zoom).toBeGreaterThan(0);
    expect(zoomPercent(camera)).toBeGreaterThan(0);
  });

  it("does not enlarge sparse graphs above their authored node scale", () => {
    const camera = fitCameraToBounds(
      {
        minX: -48,
        minY: -210,
        maxX: 592,
        maxY: 220,
        width: 640,
        height: 430,
        centerX: 272,
        centerY: 5,
      },
      { width: 1296, height: 900 },
    );

    expect(camera.zoom).toBeLessThanOrEqual(1);
  });

  it("maps relation kinds to edge classes", () => {
    expect(relationEdgeClass("decompose", "leads_to")).toBe("sg-edge-decompose");
    expect(relationEdgeClass("support", "supports")).toBe("sg-edge-support");
    expect(relationEdgeClass("challenge", "contradicts")).toBe("sg-edge-challenge");
    expect(relationEdgeClass("newdir", "restart_of")).toBe("sg-edge-newdir");
    expect(relationEdgeClass("support", "merged_from")).toBe("sg-edge-merge");
    expect(relationEdgeClass("support", "integrates")).toBe("sg-edge-merge");
    expect(relationEdgeClass("decompose", "decomposes")).toBe(
      "sg-edge-decompose",
    );
    expect(relationEdgeClass("decompose", "depends_on")).toBe(
      "sg-edge-decompose",
    );
    expect(relationEdgeClass("decompose", "tests")).toBe("sg-edge-decompose");
    expect(relationEdgeClass("decompose", "produced")).toBe(
      "sg-edge-decompose",
    );
    expect(relationEdgeClass("support", "future_relation")).toBe("sg-edge-neutral");
    expect(relationEdgeClass("support", "discussed_by")).toBe("sg-edge-neutral");
    expect(relationEdgeClass("support", "staffed_by")).toBe("sg-edge-neutral");
    expect(relationEdgeClass("support", "reported_in")).toBe("sg-edge-neutral");
  });

  it("builds a curved edge path", () => {
    expect(
      quadraticEdgePath({ x: 0, y: 0 }, { x: 100, y: 50 }),
    ).toMatch(/^M 0\.0 0\.0 Q .+ .+ 100\.0 50\.0$/);
  });

  it("suppresses an edge label when another node occupies its midpoint", () => {
    const relation = {
      fromNodeId: "from",
      toNodeId: "to",
      from: { x: 0, y: 0 },
      to: { x: 200, y: 0 },
    };

    expect(
      isEdgeLabelClear(relation, [
        { id: "agent", x: 100, y: 9, radius: 29 },
      ]),
    ).toBe(false);
    expect(
      isEdgeLabelClear(relation, [
        { id: "agent", x: 100, y: 100, radius: 29 },
      ]),
    ).toBe(true);
  });

  it("zooms around an anchor point", () => {
    const next = zoomCamera({ x: 10, y: 20, zoom: 1 }, 2, { x: 100, y: 100 });
    expect(next.zoom).toBe(2);
    expect(next.x).not.toBe(10);
    expect(next.y).not.toBe(20);
  });

  it("centers camera on a world point inside the safe band", () => {
    const camera = centerCameraOnPoint(
      { x: 400, y: 300 },
      { width: 1200, height: 800 },
      { x: 0, y: 0, zoom: 1 },
      { rightPanelWidth: 360, padding: 56 },
    );
    expect(camera.zoom).toBe(1);
    const safeCenterX = 56 + (1200 - 360 - 112) / 2;
    expect(camera.x).toBeCloseTo(safeCenterX - 400, 0);
    expect(camera.y).toBeCloseTo(400 - 300, 0);
  });

  it("zooms an overview camera until an M+ landmark is readable", () => {
    const camera = focusCameraOnEntity(
      { x: 400, y: 300, radius: 48, tier: "m" },
      { width: 1200, height: 800 },
      { x: 0, y: 0, zoom: 0.25 },
      { rightPanelWidth: 360 },
    );

    expect(camera.zoom).toBeCloseTo(1.375, 3);
    expect(camera.x).toBeCloseTo(420 - 400 * camera.zoom, 0);
    expect(camera.y).toBeCloseTo(400 - 300 * camera.zoom, 0);
  });

  it("preserves a closer user camera and does not enlarge S points", () => {
    const landmark = focusCameraOnEntity(
      { x: 0, y: 0, radius: 110, tier: "xl" },
      { width: 1000, height: 700 },
      { x: 10, y: 20, zoom: 1.6 },
    );
    const workPoint = focusCameraOnEntity(
      { x: 0, y: 0, radius: 29, tier: "s" },
      { width: 1000, height: 700 },
      { x: 10, y: 20, zoom: 0.4 },
    );

    expect(landmark.zoom).toBe(1.6);
    expect(workPoint.zoom).toBe(0.4);
  });

  it("frames only the root and server-declared expanded layer", () => {
    const entity = (id: string, x: number) => ({
      id,
      x,
      y: 200,
      radius: 48,
      tier: "m",
      view: { tier: "m", state: "default", title: id },
    });
    const camera = planExpansionTransactionCamera(
      {
        entities: [entity("root", 200), entity("child", 900), entity("unrelated", 4000)],
      } as unknown as import("../lib/star-canvas-view-model").StarCanvasViewModel,
      {
        sequence: 12,
        kind: "expand",
        rootNodeId: "root",
        revealedNodeIds: ["child"],
      },
      { width: 1200, height: 800 },
      { x: 0, y: 0, zoom: 1 },
      { rightPanelWidth: 320 },
    );

    expect(camera).not.toBeNull();
    expect(camera!.zoom).toBeGreaterThan(0.25);
    expect(camera!.x).toBeGreaterThan(-1000);
  });

  it("waits until an explicitly revealed node is present", () => {
    const camera = planExpansionTransactionCamera(
      {
        entities: [{ id: "root", x: 200, y: 200, radius: 80, tier: "xl" }],
      } as unknown as import("../lib/star-canvas-view-model").StarCanvasViewModel,
      {
        sequence: 13,
        kind: "expand",
        rootNodeId: "root",
        revealedNodeIds: ["not-loaded"],
      },
      { width: 1200, height: 800 },
      { x: 0, y: 0, zoom: 1 },
    );

    expect(camera).toBeNull();
  });
});
