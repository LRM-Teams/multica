import { describe, expect, it } from "vitest";

import {
  circleEdgeEndpoints,
  defaultLabelBox,
  diagnoseStarGraphLayout,
  labelBoxesOverlap,
  layoutStarGraph,
  STAR_GRAPH_RADIUS,
  type StarGraphLayoutNode,
  type StarGraphLayoutRelation,
} from "./star-graph-layout";

/* ------------------------------------------------------------------ *
 * Shared fixtures — no fixed example coordinates anywhere. The engine must
 * derive every position from the algorithm, not from a hand-coded snapshot.
 * ------------------------------------------------------------------ */

function fixtureNodes(): StarGraphLayoutNode[] {
  return [
    { id: "g", tier: "xxl", clusterId: "synth" },
    // cluster A — 4 members
    { id: "a1", tier: "xl", clusterId: "A" },
    { id: "a2", tier: "l", clusterId: "A" },
    { id: "a3", tier: "m", clusterId: "A" },
    { id: "a4", tier: "m", clusterId: "A" },
    // cluster B — 3 members
    { id: "b1", tier: "xl", clusterId: "B" },
    { id: "b2", tier: "l", clusterId: "B" },
    { id: "b3", tier: "m", clusterId: "B" },
    // free (unclustered) new direction
    { id: "f1", tier: "m" },
    // S agents orbiting parent results
    { id: "s1", tier: "s", parentId: "a1" },
    { id: "s2", tier: "s", parentId: "a1" },
    { id: "s3", tier: "s", parentId: "b1" },
  ];
}

function fixtureRelations(): StarGraphLayoutRelation[] {
  return [
    { id: "e1", fromNodeId: "g", toNodeId: "a1", kind: "decompose" },
    { id: "e2", fromNodeId: "g", toNodeId: "b1", kind: "decompose" },
    { id: "e3", fromNodeId: "a1", toNodeId: "a2", kind: "support" },
    { id: "e4", fromNodeId: "a1", toNodeId: "s1", kind: "decompose" },
    { id: "e5", fromNodeId: "g", toNodeId: "f1", kind: "newdir" },
  ];
}

describe("LRM-1514 star-graph layout — D5 baseline from algorithm", () => {
  it("generates the five-tier structure deterministically, root at origin", () => {
    const layout = layoutStarGraph(fixtureNodes(), fixtureRelations());
    expect(layout.rootId).toBe("g");

    const root = layout.nodes.find((n) => n.id === "g");
    expect(root).toBeDefined();
    expect(root!.x).toBe(0);
    expect(root!.y).toBe(0);

    // All input tiers are represented in the output.
    for (const tier of ["xxl", "xl", "l", "m", "s"] as const) {
      expect(layout.nodes.some((n) => n.tier === tier)).toBe(true);
    }
    // Both clusters are real output clusters.
    expect(layout.clusters.map((c) => c.clusterId).sort()).toEqual(["A", "B"]);
    // Every node got a position.
    expect(layout.nodes.length).toBe(fixtureNodes().length);
  });

  it("is deterministic — same input twice yields identical geometry", () => {
    const a = layoutStarGraph(fixtureNodes(), fixtureRelations(), { seed: 7, version: "v" });
    const b = layoutStarGraph(fixtureNodes(), fixtureRelations(), { seed: 7, version: "v" });
    const key = (n: { id: string }) => n.id;
    expect(a.nodes.map(key)).toEqual(b.nodes.map(key));
    for (const n of a.nodes) {
      const m = b.nodes.find((x) => x.id === n.id)!;
      expect(n.x).toBeCloseTo(m.x, 6);
      expect(n.y).toBeCloseTo(m.y, 6);
    }
  });

  it("spreads a sparse pair across the horizontal field", () => {
    const layout = layoutStarGraph([
      { id: "goal", tier: "xxl", clusterId: "main" },
      { id: "left", tier: "xl", clusterId: "result" },
      { id: "right", tier: "xl", clusterId: "result" },
    ]);
    const left = layout.nodes.find((node) => node.id === "left")!;
    const right = layout.nodes.find((node) => node.id === "right")!;

    expect(Math.sign(left.x)).not.toBe(Math.sign(right.x));
    expect(Math.abs(left.x - right.x)).toBeGreaterThan(
      Math.abs(left.y - right.y),
    );
  });

  it("separates a canonical goal origin from the XXL synthesis destination", () => {
    const layout = layoutStarGraph([
      { id: "origin", tier: "m", nodeKind: "goal" },
      { id: "synthesis", tier: "xxl", nodeKind: "finding", clusterId: "result" },
      { id: "result", tier: "xl", nodeKind: "finding", clusterId: "result" },
    ]);
    const origin = layout.nodes.find((node) => node.id === "origin")!;
    const synthesis = layout.nodes.find((node) => node.id === "synthesis")!;

    expect(layout.rootId).toBe("origin");
    expect(origin).toMatchObject({ x: 0, y: 0 });
    expect(synthesis.x).toBeGreaterThan(origin.x);
  });

  it("includes Agent satellites in cluster territory and renders canonical new directions", () => {
    const layout = layoutStarGraph(
      [
        { id: "goal", tier: "xxl", nodeKind: "goal" },
        { id: "result", tier: "xl", clusterId: "result" },
        { id: "agent", tier: "s", parentId: "result" },
        { id: "frontier", tier: "m" },
      ],
      [{ id: "new", fromNodeId: "goal", toNodeId: "frontier", kind: "newdir" }],
    );

    expect(layout.clusters[0]?.memberIds).toEqual(["agent", "result"]);
    expect(layout.frontiers?.[0]?.memberIds).toEqual(["frontier"]);
  });
});

describe("LRM-1514 quantitative hard gates", () => {
  it("node circle collision = 0", () => {
    const layout = layoutStarGraph(fixtureNodes(), fixtureRelations());
    const diag = diagnoseStarGraphLayout(layout);
    expect(diag.nodeCollisions).toBe(0);
  });

  it("core label collision = 0", () => {
    const layout = layoutStarGraph(fixtureNodes(), fixtureRelations());
    const diag = diagnoseStarGraphLayout(layout);
    expect(diag.labelCollisions).toBe(0);
  });

  it("edge endpoint-to-circle error <= 2px", () => {
    const layout = layoutStarGraph(fixtureNodes(), fixtureRelations());
    const diag = diagnoseStarGraphLayout(layout);
    expect(diag.maxEndpointError).toBeLessThanOrEqual(2);
    // verify each emitted edge endpoint is on its source/target circle
    for (const e of layout.edges) {
      const src = layout.nodes.find((n) => n.id === e.fromNodeId)!;
      const dst = layout.nodes.find((n) => n.id === e.toNodeId)!;
      expect(dist(e.from.x, e.from.y, src.x, src.y)).toBeCloseTo(src.radius, 6);
      expect(dist(e.to.x, e.to.y, dst.x, dst.y)).toBeCloseTo(dst.radius, 6);
    }
  });

  it("cluster boundary contains every member + label", () => {
    const layout = layoutStarGraph(fixtureNodes(), fixtureRelations());
    const diag = diagnoseStarGraphLayout(layout);
    expect(diag.clusterContainmentFailures).toBe(0);
  });

  it("cluster members are grouped into contiguous sectors, different clusters spaced", () => {
    const layout = layoutStarGraph(fixtureNodes(), fixtureRelations());
    // Members of the same cluster should be closer to their cluster centre than
    // to the origin, proving grouping (not scattered across the board).
    for (const cluster of layout.clusters) {
      for (const memberId of cluster.memberIds) {
        const node = layout.nodes.find((n) => n.id === memberId)!;
        const dCluster = dist(node.x, node.y, cluster.x, cluster.y);
        const dRoot = dist(node.x, node.y, 0, 0);
        expect(dCluster).toBeLessThan(dRoot);
      }
    }
  });

  it("S-tier agents orbit their parent result on a small radius", () => {
    const layout = layoutStarGraph(fixtureNodes(), fixtureRelations());
    for (const n of layout.nodes) {
      if (n.tier !== "s") continue;
      const parent = layout.nodes.find((p) => p.id === n.parentId)!;
      const d = dist(n.x, n.y, parent.x, parent.y);
      const expectMin = Math.max(parent.radius + STAR_GRAPH_RADIUS.s, 20);
      expect(d).toBeGreaterThanOrEqual(expectMin - 2);
      // Exploration ring: S stays in the parent's neighbourhood (a few radii),
      // it never escapes to the far side of the board.
      expect(d).toBeLessThanOrEqual(parent.radius + STAR_GRAPH_RADIUS.s * 3.5 + 30);
    }
  });
});

describe("LRM-1514 incremental stability", () => {
  it("adding a new cluster does not visibly move untouched clusters", () => {
    const base = layoutStarGraph(
      fixtureNodes().filter(
        (n) =>
          n.clusterId !== "B" &&
          n.id !== "b1" &&
          n.id !== "b2" &&
          n.id !== "b3" &&
          n.id !== "s3",
      ),
      fixtureRelations().filter((e) => e.toNodeId !== "b1"),
      { version: "v1" },
    );

    // Add the whole B cluster (and its S agent) on top of the base graph.
    const grown: StarGraphLayoutNode[] = [
      ...fixtureNodes().filter((n) => n.clusterId !== "B" && n.id !== "s3"),
      { id: "b1", tier: "xl", clusterId: "B" },
      { id: "b2", tier: "l", clusterId: "B" },
      { id: "b3", tier: "m", clusterId: "B" },
      { id: "s3", tier: "s", parentId: "b1" },
    ];

    const after = layoutStarGraph(grown, fixtureRelations(), {
      previous: base,
      version: "v1",
    });

    // Unaffected cluster A nodes should stay near their previous positions.
    const aIds = ["a1", "a2", "a3", "a4", "s1", "s2"];
    for (const id of aIds) {
      const prevNode = base.nodes.find((n) => n.id === id);
      const newNode = after.nodes.find((n) => n.id === id);
      if (!prevNode || !newNode) continue;
      const d = dist(newNode.x, newNode.y, prevNode.x, prevNode.y);
      // A re-layout of an added cluster must not visibly re-shuffle untouched
      // cluster A: bound its displacement to a small fraction of the board.
      expect(d).toBeLessThan(60);
    }
    // The new cluster B is actually present.
    expect(after.clusters.map((c) => c.clusterId)).toContain("B");
  });

  it("reuses previous positions for unchanged nodes when version matches", () => {
    const first = layoutStarGraph(fixtureNodes(), fixtureRelations(), { version: "v1" });
    const second = layoutStarGraph(fixtureNodes(), fixtureRelations(), {
      previous: first,
      version: "v1",
    });
    for (const n of first.nodes) {
      const m = second.nodes.find((x) => x.id === n.id)!;
      // Positions are stored rounded to 2 decimals, so reuse is exact within
      // that rounding precision (incremental stability, not bitwise identity).
      expect(Math.abs(m.x - n.x)).toBeLessThanOrEqual(0.02);
      expect(Math.abs(m.y - n.y)).toBeLessThanOrEqual(0.02);
    }
  });
});

describe("LRM-1514 refresh stability at scale", () => {
  it("lays out ~200 nodes / ~400 edges with zero collisions and no root occlusion", () => {
    const nodes: StarGraphLayoutNode[] = [{ id: "goal", tier: "xxl" }];
    const relations: StarGraphLayoutRelation[] = [];
    const CLUSTERS = 10;
    const PER_CLUSTER = 10; // 1 goal + 100 cluster nodes
    const S_AGENTS = 99; // total 200
    for (let c = 0; c < CLUSTERS; c++) {
      const cx = `c${c}`;
      for (let k = 0; k < PER_CLUSTER; k++) {
        const id = `${cx}-n${k}`;
        const tier =
          k === 0 ? "xl" : k === 1 ? "l" : k === 2 ? "m" : "m";
        nodes.push({ id, tier, clusterId: cx });
      }
    }
    for (let k = 0; k < S_AGENTS; k++) {
      const parent =
        k % 2 === 0
          ? `c${k % CLUSTERS}-n0`
          : `c${k % CLUSTERS}-n${k % PER_CLUSTER}`;
      nodes.push({ id: `s${k}`, tier: "s", parentId: parent });
    }

    // goal -> each cluster head
    for (let c = 0; c < CLUSTERS; c++) {
      relations.push({
        id: `r-${c}`,
        fromNodeId: "goal",
        toNodeId: `${c}-n0`,
        kind: "decompose",
      });
      // chain within cluster
      for (let k = 1; k < PER_CLUSTER; k++) {
        relations.push({
          id: `r-${c}-${k}`,
          fromNodeId: `${c}-n${k - 1}`,
          toNodeId: `${c}-n${k}`,
          kind: "support",
        });
      }
    }
    // extra cross edges to reach ~400 total (3 challenge edges per node)
    for (let c = 0; c < CLUSTERS; c++) {
      for (let k = 0; k < PER_CLUSTER; k++) {
        for (let hop = 1; hop <= 3; hop++) {
          relations.push({
            id: `x-${c}-${k}-${hop}`,
            fromNodeId: `${c}-n${k}`,
            toNodeId: `c${(c + hop) % CLUSTERS}-n${(k + hop) % PER_CLUSTER}`,
            kind: "challenge",
          });
        }
      }
    }
    // S agent edges
    for (let k = 0; k < S_AGENTS; k++) {
      const parentId = nodes.find((n) => n.id === `s${k}`)!.parentId!;
      relations.push({
        id: `rs-${k}`,
        fromNodeId: parentId,
        toNodeId: `s${k}`,
        kind: "decompose",
      });
    }

    expect(nodes.length).toBe(200);
    expect(relations.length).toBeGreaterThanOrEqual(400);

    const layout = layoutStarGraph(nodes, relations);
    expect(layout.nodes.length).toBe(200);

    const diag = diagnoseStarGraphLayout(layout);
    expect(diag.nodeCollisions).toBe(0);
    expect(diag.labelCollisions).toBe(0);
    expect(diag.maxEndpointError).toBeLessThanOrEqual(2);
    expect(diag.clusterContainmentFailures).toBe(0);
    expect(diag.hasRootOcclusion).toBe(false);
  });
});

describe("LRM-1514 geometry primitives", () => {
  it("circleEdgeEndpoints reports error ~0 (exact circle-line hit)", () => {
    const ep = circleEdgeEndpoints(0, 0, 50, 100, 0, 30);
    expect(dist(ep.from.x, ep.from.y, 0, 0)).toBeCloseTo(50, 6);
    expect(dist(ep.to.x, ep.to.y, 100, 0)).toBeCloseTo(30, 6);
  });

  it("labelBoxesOverlap detects crossing boxes", () => {
    expect(labelBoxesOverlap(0, 0, 10, 5, 24, 0, 10, 5)).toBe(false);
    expect(labelBoxesOverlap(0, 0, 10, 5, 15, 0, 10, 5)).toBe(true);
  });

  it("defaultLabelBox keeps labels inside the circle radius", () => {
    for (const tier of ["xxl", "xl", "l", "m", "s"] as const) {
      const box = defaultLabelBox(tier);
      const r = STAR_GRAPH_RADIUS[tier];
      expect(box.halfWidth + box.halfHeight).toBeLessThan(r);
    }
  });
});

function dist(ax: number, ay: number, bx: number, by: number): number {
  return Math.hypot(ax - bx, ay - by);
}
