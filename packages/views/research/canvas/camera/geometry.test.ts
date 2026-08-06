// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  boundsCenter,
  isWithinRegion,
  safeCenterPoint,
  safeViewportRegion,
  viewportCenterOnBounds,
  visibleWorldRegion,
  type Insets,
  type NodeBounds,
} from "./geometry";

const insets: Insets = { top: 56, right: 184, bottom: 84, left: 16 };

describe("safeViewportRegion", () => {
  it("shrinks the viewport by the overlay insets on every side", () => {
    const region = safeViewportRegion({ width: 1280, height: 800 }, insets);
    expect(region).toEqual({ x: 16, y: 56, width: 1280 - 16 - 184, height: 800 - 56 - 84 });
  });

  it("clamps to at least 1px so no degenerate region exists", () => {
    const region = safeViewportRegion({ width: 40, height: 40 }, {
      top: 50,
      right: 50,
      bottom: 50,
      left: 50,
    });
    expect(region.width).toBeGreaterThan(0);
    expect(region.height).toBeGreaterThan(0);
  });
});

describe("safeCenterPoint", () => {
  it("is the middle of the un-covered rectangle", () => {
    const region = safeViewportRegion({ width: 1280, height: 800 }, insets);
    const center = safeCenterPoint({ width: 1280, height: 800 }, insets);
    expect(center.x).toBeCloseTo(region.x + region.width / 2);
    expect(center.y).toBeCloseTo(region.y + region.height / 2);
  });

  it("sits above/left of the bottom dock and MiniMap", () => {
    const center = safeCenterPoint({ width: 1280, height: 800 }, insets);
    expect(center.x).toBeLessThan(1280 / 2); // pushed left clear of MiniMap
    expect(center.y).toBeLessThan(800 / 2); // pushed up clear of dock
    expect(center.y).toBeGreaterThan(56); // below the top panel
  });
});

describe("viewportCenterOnBounds", () => {
  const bounds: NodeBounds = { x: 400, y: 300, width: 240, height: 76 };

  it("keeps the same zoom while moving to the safe centre", () => {
    const out = viewportCenterOnBounds(bounds, 0.8, { width: 1280, height: 800 }, insets);
    expect(out.zoom).toBe(0.8);
    // Safe-centre of bounds' centre lands at the safe region centre.
    const safe = safeCenterPoint({ width: 1280, height: 800 }, insets);
    const center = boundsCenter(bounds);
    expect(out.x).toBeCloseTo(center.x - safe.x / 0.8);
    expect(out.y).toBeCloseTo(center.y - safe.y / 0.8);
  });

  it("never hides the node under the bottom dock or MiniMap", () => {
    // Bring the node's centre to the safe-centre; the world point must project
    // to the safe-centre screen pixel.
    const vp = viewportCenterOnBounds(bounds, 1, { width: 1280, height: 800 }, insets);
    const screenX = (centerOf(bounds).x - vp.x) * vp.zoom;
    const screenY = (centerOf(bounds).y - vp.y) * vp.zoom;
    expect(screenX).toBeCloseTo(safeCenterPoint({ width: 1280, height: 800 }, insets).x);
    expect(screenY).toBeCloseTo(safeCenterPoint({ width: 1280, height: 800 }, insets).y);
    expect(screenY).toBeLessThan(800 - 84); // above dock
  });

  it("works at different zoom levels without drifting the world target", () => {
    for (const zoom of [0.25, 0.6, 1.25, 1.75]) {
      const vp = viewportCenterOnBounds(bounds, zoom, { width: 1280, height: 800 }, insets);
      const safe = safeCenterPoint({ width: 1280, height: 800 }, insets);
      const center = centerOf(bounds);
      expect(vp.x).toBeCloseTo(center.x - safe.x / zoom);
      expect(vp.y).toBeCloseTo(center.y - safe.y / zoom);
    }
  });
});

describe("visibleWorldRegion / isWithinRegion", () => {
  it("computes the visible world rect for a viewport", () => {
    const region = visibleWorldRegion({ x: 100, y: 50, zoom: 0.5 }, { width: 1000, height: 500 });
    expect(region.width).toBeCloseTo(2000);
    expect(region.height).toBeCloseTo(1000);
  });

  it("reports whether bounds sit inside a region with padding", () => {
    const bounds: NodeBounds = { x: 500, y: 300, width: 100, height: 50 };
    const region = { x: 0, y: 0, width: 1000, height: 800 };
    expect(isWithinRegion(bounds, region, 20)).toBe(true);
    expect(isWithinRegion({ ...bounds, x: 10 }, region, 20)).toBe(false);
    expect(isWithinRegion({ ...bounds, x: 940 }, region, 20)).toBe(false);
  });
});

function centerOf(bounds: NodeBounds): { x: number; y: number } {
  return boundsCenter(bounds);
}
