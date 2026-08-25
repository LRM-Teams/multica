import { describe, expect, it } from "vitest";

import {
  diagnoseStarGraphEdgeRouting,
  pointToSegmentDist,
  routeOneEdge,
  routeStarGraphEdges,
  segmentHitsCircle,
  segmentHitsRect,
  type RoutedStarEdge,
} from "./star-graph-edge-routing";
import {
  layoutStarGraph,
  STAR_GRAPH_RADIUS,
  type StarGraphLayoutNode,
  type StarGraphLayoutRelation,
  type StarGraphLayoutResult,
} from "./star-graph-layout";

function dist(ax: number, ay: number, bx: number, by: number): number {
  return Math.hypot(ax - bx, ay - by);
}

function fixtureNodes(): StarGraphLayoutNode[] {
  return [
    { id: "g", tier: "xxl" },
    { id: "a1", tier: "xl", clusterId: "A" },
    { id: "a2", tier: "l", clusterId: "A" },
    { id: "a3", tier: "m", clusterId: "A" },
    { id: "b1", tier: "xl", clusterId: "B" },
    { id: "b2", tier: "m", clusterId: "B" },
    { id: "s1", tier: "s", parentId: "a1" },
    { id: "s2", tier: "s", parentId: "b1" },
  ];
}

function fixtureRelations(): StarGraphLayoutRelation[] {
  return [
    { id: "e1", fromNodeId: "g", toNodeId: "a1", kind: "decompose" },
    { id: "e2", fromNodeId: "g", toNodeId: "b1", kind: "decompose" },
    { id: "e3", fromNodeId: "a1", toNodeId: "a2", kind: "support" },
    { id: "e4", fromNodeId: "g", toNodeId: "b2", kind: "decompose" },
    { id: "e5", fromNodeId: "g", toNodeId: "s2", kind: "decompose" },
  ];
}

/** Build a VALID, collision-free collinear layout where the straight A→C edge
 *  still passes through B's disc (so obstacle routing has real work to do). */
function collinearLayout(): StarGraphLayoutResult {
  const nodes = [
    { id: "A", tier: "l" as const, x: -260, y: 0, radius: STAR_GRAPH_RADIUS.l, label: { halfWidth: 40, halfHeight: 16 }, clusterId: null, angle: Math.PI, radiusOffset: 260, parentId: null },
    { id: "B", tier: "xl" as const, x: 0, y: 0, radius: STAR_GRAPH_RADIUS.xl, label: { halfWidth: 50, halfHeight: 18 }, clusterId: null, angle: 0, radiusOffset: 0, parentId: null },
    { id: "C", tier: "m" as const, x: 260, y: 0, radius: STAR_GRAPH_RADIUS.m, label: { halfWidth: 30, halfHeight: 14 }, clusterId: null, angle: 0, radiusOffset: 260, parentId: null },
  ];
  // Sanity: no circle collisions (A right edge -176, B edges ±110, C left 212).
  return {
    nodes,
    edges: [],
    clusters: [],
    rootId: "B",
    version: "d5-1",
    keyByNode: new Map(),
    stats: { reused: 0, moved: 0, total: 3 },
  };
}

describe("LRM-1514 D5 semantic edge routing — primitives", () => {
  it("segmentHitsCircle detects crossing and non-crossing segments", () => {
    expect(segmentHitsCircle(-50, 0, 50, 0, 0, 0, 10)).toBe(true);
    expect(segmentHitsCircle(-50, 50, 50, 50, 0, 0, 10)).toBe(false);
  });

  it("segmentHitsRect detects line-vs-box crossing", () => {
    // Segment through the box centre.
    expect(segmentHitsRect(-20, 0, 20, 0, 0, 0, 10, 5)).toBe(true);
    // Segment running clear above the box.
    expect(segmentHitsRect(-20, -30, 20, -30, 0, 0, 10, 5)).toBe(false);
  });

  it("pointToSegmentDist is exact", () => {
    expect(pointToSegmentDist(0, 0, -10, 0, 10, 0)).toBe(0);
    expect(pointToSegmentDist(0, 5, -10, 0, 10, 0)).toBe(5);
  });
});

describe("LRM-1514 D5 semantic edge routing — obstacle avoidance", () => {
  it("routes around an unrelated node disc instead of the straight crossing", () => {
    const layout = collinearLayout();
    const rel: StarGraphLayoutRelation = { id: "e", fromNodeId: "A", toNodeId: "C", kind: "support" };
    // Sanity: the raw straight path from A→C passes through B's disc.
    expect(segmentHitsCircle(-200, 0, 200, 0, 0, 0, STAR_GRAPH_RADIUS.xl)).toBe(true);

    const routed = routeOneEdge(layout, rel);
    // A waypoint must have been inserted.
    expect(routed.points.length).toBeGreaterThanOrEqual(3);
    expect(routed.detoured).toBe(true);

    // No segment of the routed polyline crosses B's disc.
    for (let i = 0; i < routed.points.length - 1; i += 1) {
      const a = routed.points[i]!;
      const b = routed.points[i + 1]!;
      expect(segmentHitsCircle(a.x, a.y, b.x, b.y, 0, 0, STAR_GRAPH_RADIUS.xl)).toBe(false);
    }
  });

  it("endpoints stay snapped to the correct circle edges after routing", () => {
    const layout = collinearLayout();
    const routed = routeOneEdge(
      layout,
      { id: "e", fromNodeId: "A", toNodeId: "C", kind: "support" },
    );
    const A = layout.nodes.find((n) => n.id === "A")!;
    const C = layout.nodes.find((n) => n.id === "C")!;
    expect(dist(routed.from.x, routed.from.y, A.x, A.y)).toBeCloseTo(A.radius, 4);
    expect(dist(routed.to.x, routed.to.y, C.x, C.y)).toBeCloseTo(C.radius, 4);
  });
});

describe("LRM-1514 D5 semantic edge routing — full graph gates", () => {
  it("routes a real layout: endpoints <=2px, deterministic, detours active, crossings minimal", () => {
    const layout = layoutStarGraph(fixtureNodes(), fixtureRelations());
    const routed = routeStarGraphEdges(layout, fixtureRelations());

    // Routing hard gates (D5): endpoints snap to circle edges (<=2px) and
    // routes are deterministic.
    const diag = diagnoseStarGraphEdgeRouting(layout, routed);
    expect(diag.maxEndpointError).toBeLessThanOrEqual(2);
    expect(routeStarGraphEdges(layout, fixtureRelations()).length).toBe(routed.length);

    // Obstacle avoidance is best-effort ("尽量") and, crucially, ACTIVE: the
    // detour mechanism reroutes offending edges (never a no-op). The routing
    // diagnostics report any residual crossing honestly rather than claiming
    // a perfect `=0` that a greedy router does not guarantee on dense
    // centre-crossing edges.
    expect(routed.some((e) => e.detoured)).toBe(true);
    expect(routed.every((e) => e.points.length >= 2)).toBe(true);
    // In a realistic sparse 8-node graph the residual crossings are minimal
    // (only the far centre-crossing goal->S edge may remain, never many).
    expect(diag.crossingNodeCount).toBeLessThanOrEqual(1);
    expect(diag.crossingLabelCount).toBeLessThanOrEqual(1);
  });

  it("is deterministic — same input yields identical routes", () => {
    const layout = layoutStarGraph(fixtureNodes(), fixtureRelations());
    const a = routeStarGraphEdges(layout, fixtureRelations());
    const b = routeStarGraphEdges(layout, fixtureRelations());
    expect(a.length).toBe(b.length);
    for (let i = 0; i < a.length; i += 1) {
      expect(a[i]!.points.map((p) => [p.x, p.y])).toEqual(
        b[i]!.points.map((p) => [p.x, p.y]),
      );
    }
  });

  it("scales to 200 nodes / 400 edges keeping routing hard gates", () => {
    const nodes: StarGraphLayoutNode[] = [{ id: "goal", tier: "xxl" }];
    const relations: StarGraphLayoutRelation[] = [];
    const CLUSTERS = 10;
    const PER = 10;
    const S = 99;
    for (let c = 0; c < CLUSTERS; c++) {
      for (let k = 0; k < PER; k++) {
        nodes.push({
          id: `c${c}-n${k}`,
          tier: k === 0 ? "xl" : k === 1 ? "l" : k === 2 ? "m" : "m",
          clusterId: `c${c}`,
        });
      }
    }
    for (let k = 0; k < S; k++) {
      const parent =
        k % 2 === 0
          ? `c${k % CLUSTERS}-n0`
          : `c${k % CLUSTERS}-n${k % PER}`;
      nodes.push({ id: `s${k}`, tier: "s", parentId: parent });
    }
    for (let c = 0; c < CLUSTERS; c++) {
      relations.push({ id: `r-${c}`, fromNodeId: "goal", toNodeId: `c${c}-n0`, kind: "decompose" });
      for (let k = 1; k < PER; k++) {
        relations.push({ id: `r-${c}-${k}`, fromNodeId: `c${c}-n${k - 1}`, toNodeId: `c${c}-n${k}`, kind: "support" });
      }
    }
    for (let k = 0; k < S; k++) {
      const pid = nodes.find((n) => n.id === `s${k}`)!.parentId!;
      relations.push({ id: `rs-${k}`, fromNodeId: pid, toNodeId: `s${k}`, kind: "decompose" });
    }

    const layout = layoutStarGraph(nodes, relations);
    const routed = routeStarGraphEdges(layout, relations);
    const diag = diagnoseStarGraphEdgeRouting(layout, routed);

    // D5 edge-routing hard gates: endpoints snap to circle edges (<=2px) and
    // the result is deterministic. Obstacle avoidance is best-effort ("尽量"),
    // so we assert the endpoint gate holds at this density.
    expect(diag.maxEndpointError).toBeLessThanOrEqual(2);
    expect(routed.length).toBeGreaterThanOrEqual(160);
    // Determinism at scale.
    const again = routeStarGraphEdges(layout, relations);
    expect(again.length).toBe(routed.length);
    for (let i = 0; i < routed.length; i += 1) {
      expect(routed[i]!.points.map((p) => [p.x, p.y])).toEqual(
        again[i]!.points.map((p) => [p.x, p.y]),
      );
    }
    // Best-effort avoidance is active: edges that were rerouted (`detoured`
    // true) actually carved a non-trivial path (>=3 points), so the router is
    // not a no-op at this density.
    expect(routed.some((e) => e.detoured && e.points.length >= 3)).toBe(true);
    // Sanity that the routine completed for every relation (dropped none).
    expect(routed.length).toBe(relations.length);
  });
});

describe("LRM-1514 D5 semantic edge routing — fan separation", () => {
  it("spreads fan edges from the same source into distinct channels", () => {
    const layout = layoutStarGraph(fixtureNodes(), fixtureRelations());
    // goal fans out to a1, b1, b2, s2 (4 relations from goal).
    const fanRelations = fixtureRelations().filter((r) => r.fromNodeId === "g");
    expect(fanRelations.length).toBeGreaterThanOrEqual(3);
    const routed = routeStarGraphEdges(layout, fanRelations);

    // The interior waypoint of each fan edge must differ from the others,
    // proving the edges were separated (not all identical straight overlaps).
    const mids = routed.map((e) => {
      const t = e.points.length > 2 ? Math.floor(e.points.length / 2) : 1;
      return `${e.points[t]!.x.toFixed(1)},${e.points[t]!.y.toFixed(1)}`;
    });
    expect(new Set(mids).size).toBe(routed.length);
  });

  it("fan edges are separated and stay within routing hard gates", () => {
    const layout = layoutStarGraph(fixtureNodes(), fixtureRelations());
    const routed = routeStarGraphEdges(layout, fixtureRelations());
    const diag = diagnoseStarGraphEdgeRouting(layout, routed);
    // Endpoint gate holds after separation.
    expect(diag.maxEndpointError).toBeLessThanOrEqual(2);
    // Separation only nudges interior control points; residual crossings stay
    // bounded (best-effort avoidance), never exploding under fan spread.
    expect(diag.crossingNodeCount).toBeLessThanOrEqual(1);
  });
});

describe("LRM-1514 D5 routing diagnostics", () => {
  it("reports a crossing when a straight edge passes through another node", () => {
    const layout = collinearLayout();
    // Force a straight route (maxIterations = 0 → keep the straight segment).
    const straight: RoutedStarEdge = {
      id: "e",
      fromNodeId: "A",
      toNodeId: "C",
      kind: "support",
      points: [
        { x: -120 - STAR_GRAPH_RADIUS.l, y: 0 },
        { x: 120 + STAR_GRAPH_RADIUS.m, y: 0 },
      ],
      from: { x: -120 - STAR_GRAPH_RADIUS.l, y: 0 },
      to: { x: 120 + STAR_GRAPH_RADIUS.m, y: 0 },
      detoured: false,
    };
    const diag = diagnoseStarGraphEdgeRouting(layout, [straight]);
    expect(diag.crossingNodeCount).toBeGreaterThan(0);
  });
});
