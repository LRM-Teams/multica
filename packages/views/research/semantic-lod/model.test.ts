// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  VIEWPORT_BUDGETS,
  zoomTier,
  DEFAULT_VISIBLE_DEPTH,
  MAX_VISIBLE_DEPTH,
  BUNDLE_FOLD_DEPTH,
} from "./model";

describe("zoomTier", () => {
  it("maps percentages to the four working bands", () => {
    expect(zoomTier(10)).toBe("overview");
    expect(zoomTier(34)).toBe("overview");
    expect(zoomTier(35)).toBe("route");
    expect(zoomTier(65)).toBe("route");
    expect(zoomTier(66)).toBe("work");
    expect(zoomTier(119)).toBe("work");
    expect(zoomTier(120)).toBe("inspect");
    expect(zoomTier(200)).toBe("inspect");
  });
});

describe("depth constants (viewport-performance §2)", () => {
  it("defaults to 2 visible hops and admits at most 3 after explicit expand", () => {
    expect(DEFAULT_VISIBLE_DEPTH).toBe(2);
    expect(MAX_VISIBLE_DEPTH).toBe(3);
    expect(BUNDLE_FOLD_DEPTH).toBe(4);
  });
});

describe("VIEWPORT_BUDGETS (route-topology §6.3 / viewport §3)", () => {
  it("desktop: landmark 48, semantic 120/180, graphic DOM 220", () => {
    const d = VIEWPORT_BUDGETS.desktop;
    expect(d.semanticNodeSoft).toBe(120);
    expect(d.semanticNodeHard).toBe(180);
    expect(d.graphicDomHard).toBe(220);
    expect(d.landmarkHard).toBe(48);
  });

  it("mobile: landmark hard 12 overrides overview 25% zoom", () => {
    // 25% zoom overview keeps ≤12 landmark cards — spec route-topology §6.1.
    expect(VIEWPORT_BUDGETS.mobile.landmarkHard).toBe(12);
    // Semantic-node hard never exceeds spec.
    expect(VIEWPORT_BUDGETS.mobile.semanticNodeHard).toBe(48);
  });

  it("type caps are upper bounds included in the total", () => {
    for (const tier of ["desktop", "tablet", "mobile"] as const) {
      const b = VIEWPORT_BUDGETS[tier];
      const capSum =
        b.landmarkHard + b.waypointHard + b.trailDotHard + b.bundleHard;
      expect(capSum).toBeGreaterThanOrEqual(b.semanticNodeHard);
    }
  });
});
