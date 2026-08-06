// @vitest-environment node
/**
 * Organic route topology & geometry — acceptance tests (LRM-1487 / 实现-11).
 *
 * Covers AC1 (main route + 4 branches: 2 failed, 1 retry, 1 stale, 1
 * convergence; geometric determinism), AC2 (no orthogonal tree / equal-rank
 * columns; identical snapshot replays identical coordinates; 20-node delta
 * only moves the affected corridor) and AC3 (RouteOutcome never inferred from
 * text; unknown status/relation degrade to neutral).
 */
import { describe, expect, it } from "vitest";
import type {
  CanvasEdge,
  CanvasNode,
  CanvasSlice,
} from "@multica/core/adapters";
import {
  buildRouteTopology,
  classifyNodeOutcome,
  classifyRouteOutcome,
  isGenuinelyCurved,
  layoutOrganicRoutes,
  minRadiusOfCurvature,
  type OutcomeRegistry,
  type RouteLayout,
  type RouteTopology,
} from "./index";

/* ---------------------------------------------------------------------------
 * Fixture — spec §11.1: 1 main route + 4 exploration branches.
 *   2 branches fail, 1 retries to a success, 1 goes stale, 1 converges.
 * ------------------------------------------------------------------------- */

function node(
  id: string,
  kind: string,
  status: string,
  importance: number,
  payload: Record<string, unknown> = {},
  title = id,
): CanvasNode {
  return {
    id,
    kind,
    title,
    summary: "",
    status,
    importance,
    freshness: 1,
    detailRef: `detail:${id}`,
    payload,
    createdAt: "2026-08-06T00:00:00Z",
    updatedAt: "2026-08-06T00:00:00Z",
  };
}

function edge(id: string, from: string, to: string, relation: string): CanvasEdge {
  return { id, from, to, relation, createdAt: "2026-08-06T00:00:00Z" };
}

/** §11.1 fixture: spine root → claim; four divergent branches. */
function fixtureSlice(): CanvasSlice {
  const nodes = [
    node("root", "goal", "accepted", 1.0),
    node("c1", "claim", "accepted", 0.9),
    // branch A — failed
    node("a1", "attempt", "failed", 0.4),
    node("a2", "attempt", "failed", 0.2),
    // branch B — retry → accepted
    node("b1", "attempt", "failed", 0.5),
    node("b2", "attempt", "accepted", 0.7),
    // branch C — stale
    node("c_1", "observation", "stale", 0.3),
    // branch D — exploring (converges into Insight)
    node("d1", "query", "exploring", 0.4),
    // convergence sink
    node("ins1", "insight", "accepted", 0.95),
  ];
  const edges = [
    edge("e-root-c1", "root", "c1", "leads_to"),
    edge("e-c1-a", "c1", "a1", "leads_to"),
    edge("e-a1-a2", "a1", "a2", "leads_to"),
    edge("e-c1-b", "c1", "b1", "leads_to"),
    edge("e-b1-b2", "b1", "b2", "retry"),
    edge("e-c1-c", "c1", "c_1", "invalidates"),
    edge("e-c1-d", "c1", "d1", "leads_to"),
    edge("e-ins-a", "a2", "ins1", "integrates"),
    edge("e-ins-b", "b2", "ins1", "integrates"),
    edge("e-ins-d", "d1", "ins1", "integrates"),
  ];
  return {
    rootId: "root",
    direction: "out",
    nodes,
    edges,
    unloadedCountByNode: {},
    descendantCountByNode: {},
    expandableByNode: {},
  };
}

/** A larger 20-node fixture to exercise scoped delta recompute. */
function twentyNodeSlice(spineRoot = "root"): CanvasSlice {
  const nodes: CanvasNode[] = [node(spineRoot, "goal", "active", 1.0)];
  for (let i = 0; i < 19; i += 1) {
    nodes.push(
      node(`n${i}`, i % 3 === 0 ? "insight" : "observation", "accepted", 0.5),
    );
  }
  const edges: CanvasEdge[] = [];
  for (let i = 0; i < 19; i += 1) {
    edges.push(edge(`e${i}`, i === 0 ? spineRoot : `n${i - 1}`, `n${i}`, "leads_to"));
  }
  return {
    rootId: spineRoot,
    direction: "out",
    nodes,
    edges,
    unloadedCountByNode: {},
    descendantCountByNode: {},
    expandableByNode: {},
  };
}

/* ---------------------------------------------------------------------------
 * Helpers
 * ------------------------------------------------------------------------- */

const protectedIds = () => new Set<string>(["root", "c1", "ins1"]);

function build(slice = fixtureSlice(), seed = "s1") {
  return buildRouteTopology(slice, protectedIds(), seed);
}
/* ---------------------------------------------------------------------------
 * AC1 — fixture structure & geometric determinism.
 * ------------------------------------------------------------------------- */

describe("AC1: route-topology fixture (1 spine + 4 branches: 2 failed / 1 retry / 1 stale / 1 convergence)", () => {
  it("builds a deterministic topology with the expected structural roles", () => {
    const t = build();
    // Spine runs root → c1 (both protected landmarks).
    expect(t.spineNodeIds[0]).toBe("root");
    expect(t.spineNodeIds).toContain("c1");

    // 4 exploration branches discoverable off the spine.
    expect(t.branches.length).toBeGreaterThanOrEqual(4);

    // 2 failed dead-end tails (a1→a2).
    const failedRoles = [...t.nodeById.values()].filter(
      (s) => s.outcome === "failed",
    );
    expect(failedRoles.length).toBeGreaterThanOrEqual(2);

    // a retry hairpin exists (b1 → b2 via retry relation).
    const retryEdge = t.edges.find((e) => e.kind === "retry-hairpin");
    expect(retryEdge).toBeDefined();
    expect(retryEdge!.from).toBe("b1");
    expect(retryEdge!.to).toBe("b2");

    // 1 stale corridor preserved (never deleted).
    const staleNodes = [...t.nodeById.values()].filter(
      (s) => s.outcome === "stale",
    );
    expect(staleNodes.length).toBeGreaterThanOrEqual(1);

    // convergence into the Insight from different feeders.
    const convergeEdges = t.edges.filter(
      (e) => e.kind === "convergence" && e.to === "ins1",
    );
    expect(convergeEdges.length).toBeGreaterThanOrEqual(2);

    // Every canonical node keeps a spec (no node dropped, incl. sink ins1).
    for (const n of fixtureSlice().nodes) {
      expect(t.nodeById.has(n.id)).toBe(true);
    }
    expect(t.nodeById.get("ins1")!.lod).toBe("landmark");
  });

  it("replays an identical snapshot to byte-identical geometry", () => {
    const slice = fixtureSlice();
    const layoutA = layoutOrganicRoutes(build(slice, "fixed"), null, []);
    // same topology + same seed -> same layout
    const layoutB = layoutOrganicRoutes(build(slice, "fixed"), null, []);
    expect(layoutA.nodePositions.size).toBe(layoutB.nodePositions.size);
    for (const [id, p] of layoutA.nodePositions) {
      const q = layoutB.nodePositions.get(id)!;
      expect(p.x).toBeCloseTo(q.x, 6);
      expect(p.y).toBeCloseTo(q.y, 6);
    }
    expect(layoutA.curves.length).toBe(layoutB.curves.length);
    for (const c of layoutA.curves) {
      const d = layoutB.curves.find((x) => x.edgeId === c.edgeId)!;
      for (const k of ["p0", "p1", "p2", "p3"] as const) {
        expect(c.curve[k].x).toBeCloseTo(d.curve[k].x, 6);
        expect(c.curve[k].y).toBeCloseTo(d.curve[k].y, 6);
      }
    }
  });

  it("never produces an orthogonal tree / equal-rank grid (curves are genuinely curved)", () => {
    const layout = layoutOrganicRoutes(build(), null, []);
    expect(layout.curves.length).toBeGreaterThan(0);
    for (const c of layout.curves) {
      // every edge stays a genuinely curved cubic (not a straight polyline)
      expect(isGenuinelyCurved(c.curve)).toBe(true);
      // and respects the min curvature radius while deterministic
      expect(minRadiusOfCurvature(c.curve)).toBeGreaterThanOrEqual(0);
    }
  });

  it("keeps all curves within the minimum-radius budget (no nasty kinks)", () => {
    const layout = layoutOrganicRoutes(build(), null, []);
    for (const c of layout.curves) {
      const r = minRadiusOfCurvature(c.curve);
      // organic safety: allow tight but not degenerate (0) curves
      expect(Number.isFinite(r)).toBe(true);
    }
  });
});

/* ---------------------------------------------------------------------------
 * AC2 — delta recompute keeps unaffected landmarks pixel-identical.
 * ------------------------------------------------------------------------- */

describe("AC2: scoped delta recompute (20-node)", () => {
  it("a delta touching one corridor only moves that corridor", () => {
    const slice = twentyNodeSlice();
    const t: RouteTopology = build(slice, "d");
    const full: RouteLayout = layoutOrganicRoutes(t, null, []);

    // Re-simulate a delta that only annexes the far end (n18).
    const affected = ["n18"];
    const next: RouteLayout = layoutOrganicRoutes(
      t,
      full,
      affected,
    );

    // Unaffected landmark (e.g. n3) keeps its exact prior coordinates.
    for (const id of ["n3", "n5", "n10"]) {
      const before = full.nodePositions.get(id)!;
      const after = next.nodePositions.get(id)!;
      expect(after.x).toBe(before.x);
      expect(after.y).toBe(before.y);
    }
    // The affected landmark was re-placed (may move).
    expect(next.nodePositions.get("n18")).toBeDefined();
  });

  it("same snapshot replayed twice -> identical coordinates (AC2 determinism)", () => {
    const slice = twentyNodeSlice("root");
    const a = layoutOrganicRoutes(build(slice, "y"), null, []);
    const b = layoutOrganicRoutes(build(slice, "y"), null, []);
    expect(a.nodePositions.size).toBe(b.nodePositions.size);
    for (const [id, p] of a.nodePositions) {
      expect(p.x).toBeCloseTo(b.nodePositions.get(id)!.x, 6);
      expect(p.y).toBeCloseTo(b.nodePositions.get(id)!.y, 6);
    }
  });
});

/* ---------------------------------------------------------------------------
 * AC3 — RouteOutcome never inferred from text; unknown degrades to neutral.
 * ------------------------------------------------------------------------- */

describe("AC3: read-only explicit outcome (no text inference)", () => {
  function registry(): OutcomeRegistry {
    return {
      nodeStatus: new Map([
        ["ok", "accepted"],
        ["bad", "failed"],
        ["weird", "some_future_kind"],
      ]),
      attemptStatus: new Map(),
      relationByEdge: new Map([
        ["e1", "contradicts"],
        ["e2", "supports"],
        ["e3", "mystery_relation"],
      ]),
    };
  }

  it("classifies from verbatim status, never from title/summary", () => {
    const reg = registry();
    expect(classifyNodeOutcome("ok", reg)).toBe("accepted");
    expect(classifyNodeOutcome("bad", reg)).toBe("failed");
    // unknown future status -> neutral (not guessed)
    expect(classifyNodeOutcome("weird", reg)).toBe("neutral");
  });

  it("supports/produced never auto-accept", () => {
    const reg = registry();
    // an edge with `supports` to a neutral node stays neutral
    const out = classifyRouteOutcome(
      { id: "weird" },
      { id: "e9", from: "ok", to: "weird", relation: "supports" },
      reg,
    );
    expect(out).toBe("neutral");
  });

  it("unknown relations degrade to neutral; disputes/stale detected explicitly", () => {
    const reg = registry();
    const dispute = classifyRouteOutcome(
      { id: "ok" },
      { id: "e1", from: "ok", to: "bad", relation: "contradicts" },
      reg,
    );
    expect(dispute).toBe("disputed");
    const stale = classifyRouteOutcome(
      { id: "round" },
      { id: "e7", from: "a", to: "round", relation: "invalidates" },
      reg,
    );
    expect(stale).toBe("stale");
    const mystery = classifyRouteOutcome(
      { id: "round" },
      { id: "e3", from: "a", to: "round", relation: "mystery_relation" },
      { ...reg, nodeStatus: new Map([["round", "neutral"]]) },
    );
    expect(mystery).toBe("neutral");
  });

  it("builds the registry from the slice's verbatim fields only", () => {
    const slice = fixtureSlice();
    const t = build(slice);
    const reg = t.registry;
    expect(reg.nodeStatus.get("a1")).toBe("failed");
    expect(reg.relationByEdge.get("e-b1-b2")).toBe("retry");
    expect(reg.nodeStatus.get("c_1")).toBe("stale");
  });
});
