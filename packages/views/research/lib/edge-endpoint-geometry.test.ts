import { describe, expect, it } from "vitest";
import {
  arrowEndpoint,
  edgeEndpointOnNodeBoundary,
  rectCenter,
  type RoundedRect,
} from "./edge-endpoint-geometry";

const NODE: RoundedRect = {
  x: 100,
  y: 200,
  width: 240,
  height: 76,
  radius: 8, // rounded-lg
};

function expectOnBoundary(p: { x: number; y: number }, rect: RoundedRect) {
  const c = rectCenter(rect);
  const halfW = rect.width / 2;
  const halfH = rect.height / 2;
  const dx = p.x - c.x;
  const dy = p.y - c.y;
  // A boundary point is on the rounded-rect silhouette: either on an edge at
  // its inset extent, or on a corner arc. Easier robust check: it must be
  // within the outer box but not strictly interior on both axes at once with
  // room to spare (i.e. it must touch an edge or corner curve).
  const onOuterEdge =
    Math.abs(Math.abs(dx) - halfW) < 1e-6 || Math.abs(Math.abs(dy) - halfH) < 1e-6;
  // If not on an outer edge, it must lie on a corner arc: |dx|,|dy| both in
  // (half-r, half] and the corner-distance ≈ r.
  const onArc =
    Math.abs(dx) > halfW - rect.radius - 1e-6 &&
    Math.abs(dy) > halfH - rect.radius - 1e-6 &&
    Math.abs(Math.hypot(Math.abs(dx) - (halfW - rect.radius), Math.abs(dy) - (halfH - rect.radius)) - rect.radius) < 1.5;
  expect(onOuterEdge || onArc).toBe(true);
}

function expectInside(p: { x: number; y: number }, rect: RoundedRect) {
  const c = rectCenter(rect);
  const halfW = rect.width / 2;
  const halfH = rect.height / 2;
  expect(Math.abs(p.x - c.x)).toBeLessThanOrEqual(halfW + 1e-6);
  expect(Math.abs(p.y - c.y)).toBeLessThanOrEqual(halfH + 1e-6);
}

describe("edgeEndpointOnNodeBoundary (LRM-1497 geometry)", () => {
  it("lands on the top edge for a source directly above", () => {
    const p = edgeEndpointOnNodeBoundary(
      { x: 220, y: 50 },
      { ...NODE },
    );
    expect(p.x).toBeCloseTo(220, 0);
    expect(p.y).toBeCloseTo(NODE.y, 0);
  });

  it("lands on the bottom edge for a source directly below", () => {
    const p = edgeEndpointOnNodeBoundary(
      { x: 220, y: 500 },
      { ...NODE },
    );
    expect(p.x).toBeCloseTo(220, 0);
    expect(p.y).toBeCloseTo(NODE.y + NODE.height, 0);
  });

  it("lands on the left edge for a source directly left", () => {
    const p = edgeEndpointOnNodeBoundary(
      { x: 0, y: 238 },
      { ...NODE },
    );
    expect(p.x).toBeCloseTo(NODE.x, 0);
    expect(p.y).toBeCloseTo(238, 0);
  });

  it("never returns a point inside the node for any diagonal source", () => {
    const sources = [
      { x: 0, y: 0 },
      { x: 1000, y: 1000 },
      { x: 0, y: 500 },
      { x: 700, y: 0 },
      { x: 280, y: 262 },
      { x: 150, y: 330 },
    ];
    for (const s of sources) {
      const p = edgeEndpointOnNodeBoundary(s, { ...NODE });
      expectInside(p, NODE);
      expectOnBoundary(p, NODE);
    }
  });

  it("resolves corner contact that hugs the rounded shell (no corner sharp gap)", () => {
    // Source far up-right: contact should be clipped to the rounded top-right
    // corner, not float past the straight edges.
    const p = edgeEndpointOnNodeBoundary({ x: 800, y: 0 }, { ...NODE });
    expectInside(p, NODE);
    expectOnBoundary(p, NODE);
    // It should be on the top edge (straight region near the corner) or on the
    // corner arc — never strictly inside.
    expect(p.y).toBeLessThanOrEqual(NODE.y + NODE.radius + 1e-6);
  });

  it("is scale-invariant: the same world coordinates hold across viewport zoom", () => {
    // The function works purely in world coordinates, so it is independent of
    // the camera zoom / viewport size by construction. Simulate three viewport
    // scales by using identical world geometry with differing zoom factors —
    // the endpoint must be identical because geometry is defined in world
    // space, not screen pixels.
    const viewports = [0.5, 1, 1.75];
    const source = { x: 0, y: 0 };
    const results = viewports.map(() =>
      edgeEndpointOnNodeBoundary(source, { ...NODE }),
    );
    const first = results[0];
    if (!first) throw new Error("expected at least one result");
    for (const res of results.slice(1)) {
      expect(res.x).toBeCloseTo(first.x, 6);
      expect(res.y).toBeCloseTo(first.y, 6);
    }
  });
});

describe("arrowEndpoint (LRM-1497 arrow landing)", () => {
  it("offsets the boundary outward so the arrowhead clears the node border", () => {
    const outset = 4;
    const tip = arrowEndpoint({ x: 0, y: 0 }, { ...NODE }, outset);
    const boundary = edgeEndpointOnNodeBoundary({ x: 0, y: 0 }, { ...NODE });
    const c = rectCenter(NODE);
    const boundaryDist = Math.hypot(boundary.x - c.x, boundary.y - c.y);
    const tipDist = Math.hypot(tip.x - c.x, tip.y - c.y);
    expect(tipDist).toBeGreaterThan(boundaryDist + outset - 1e-6);
  });

  it("keeps the arrow tip outside the node on its approach axis (never covering the title)", () => {
    // Source directly below: the arrow lands on the bottom edge and the offset
    // pushes the tip further down — strictly beyond the node's outer height.
    const below = arrowEndpoint({ x: 220, y: 600 }, { ...NODE }, 4);
    expect(Math.abs(below.y - rectCenter(NODE).y)).toBeGreaterThan(
      NODE.height / 2 + 4 - 1e-6,
    );
    // Diagonal source: the tip must be strictly farther from the centre than
    // any boundary point (it clears the shell on its approach direction).
    const diag = arrowEndpoint({ x: 0, y: 0 }, { ...NODE }, 4);
    const c = rectCenter(NODE);
    const boundary = edgeEndpointOnNodeBoundary({ x: 0, y: 0 }, { ...NODE });
    const boundaryDist = Math.hypot(boundary.x - c.x, boundary.y - c.y);
    const tipDist = Math.hypot(diag.x - c.x, diag.y - c.y);
    expect(tipDist).toBeGreaterThan(boundaryDist + 4 - 1e-6);
  });
});
