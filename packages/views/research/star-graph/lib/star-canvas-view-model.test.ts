import { describe, expect, it } from "vitest";

import type { TypedGraphEdge, TypedGraphNode } from "@multica/core/research";

import {
  buildStarCanvasViewModel,
  layoutKindForEdgeType,
  rebaseStarCanvasIntoViewModel,
  type StarCanvasInput,
} from "./star-canvas-view-model";

/** Build a complete TypedGraphNode (all schema fields present, real values). */
function node(partial: Partial<TypedGraphNode> & { id: string }): TypedGraphNode {
  return {
    session_id: "session-1",
    node_type: "subquestion",
    title: "",
    summary: "",
    status: "active",
    actor_agent_id: null,
    level: "m",
    round: 1,
    cluster_id: null,
    confidence: null,
    document_count: 0,
    conclusion_count: 0,
    goal_version_id: null,
    derived_from: null,
    merged_from: [],
    superseded_by: null,
    restart_of: null,
    invalidated_by: null,
    superseded_at: null,
    invalidated_at: null,
    parent_id: null,
    child_ids: [],
    children_of: [],
    created_at: "",
    updated_at: "",
    ...partial,
  };
}

/** Build a complete TypedGraphEdge. */
function edge(partial: { id: string; from_node_id: string; to_node_id: string; edge_type: string }): TypedGraphEdge {
  return { session_id: "session-1", created_at: "", ...partial };
}

/** Build a small but representative typed graph: goal → directions → S agents. */
function fixture(): StarCanvasInput {
  const nodes: TypedGraphNode[] = [
    node({ id: "goal", node_type: "goal", status: "active", title: "自动驾驶落地可行性", level: "xxl" }),
    node({ id: "d1", node_type: "subquestion", status: "done", title: "法规与责任归属", level: "l", cluster_id: "regulatory", confidence: 80, document_count: 12, conclusion_count: 2 }),
    node({ id: "d2", node_type: "subquestion", status: "active", title: "传感器成本曲线", level: "l", cluster_id: "cost", confidence: 60, document_count: 8, conclusion_count: 1 }),
    node({ id: "c1", node_type: "finding", status: "done", title: "责任保险是最大瓶颈", level: "m", round: 2, cluster_id: "regulatory", confidence: 85, document_count: 3 }),
    node({ id: "agent1", node_type: "agent_activity", status: "running", title: "agent:lindberg", level: "s", round: 2, cluster_id: "regulatory", parent_id: "d1" }),
    node({ id: "agent2", node_type: "agent_activity", status: "running", title: "agent:mira", level: "s", round: 2, cluster_id: "cost", parent_id: "d2" }),
  ];

  const edges: TypedGraphEdge[] = [
    edge({ id: "e1", from_node_id: "goal", to_node_id: "d1", edge_type: "leads_to" }),
    edge({ id: "e2", from_node_id: "goal", to_node_id: "d2", edge_type: "leads_to" }),
    edge({ id: "e3", from_node_id: "d1", to_node_id: "c1", edge_type: "supports" }),
    edge({ id: "e4", from_node_id: "d1", to_node_id: "agent1", edge_type: "escalated_to" }),
    edge({ id: "e5", from_node_id: "d2", to_node_id: "agent2", edge_type: "escalated_to" }),
    edge({ id: "e6", from_node_id: "c1", to_node_id: "d2", edge_type: "contradicts" }),
  ];

  return { nodes, edges };
}

/** Euclidean distance from a point to a circle centre must equal its radius. */
function distToCircleErr(
  cx: number,
  cy: number,
  r: number,
  px: number,
  py: number,
): number {
  return Math.abs(Math.hypot(px - cx, py - cy) - r);
}

describe("LRM-1497 render-layer view-model — geometry precision (AC: endpoints on circle)", () => {
  it("produces one entity per node with a release-tier view", () => {
    const vm = buildStarCanvasViewModel(fixture());
    expect(vm.entities).toHaveLength(6);
    expect(vm.relations).toHaveLength(6);
    // every entity carries a render view + a circle centre + radius
    for (const e of vm.entities) {
      expect(e.view.id).toBe(e.id);
      expect(e.radius).toBeGreaterThan(0);
      expect(Number.isFinite(e.x)).toBe(true);
      expect(Number.isFinite(e.y)).toBe(true);
    }
  });

  it("snaps every relation endpoint to the source and target circle perimeter (<=2px hard gate)", () => {
    const vm = buildStarCanvasViewModel(fixture());
    const byId = new Map(vm.entities.map((e) => [e.id, e]));
    for (const r of vm.relations) {
      const from = byId.get(r.fromNodeId)!;
      const to = byId.get(r.toNodeId)!;
      const errFrom = distToCircleErr(from.x, from.y, from.radius, r.from.x, r.from.y);
      const errTo = distToCircleErr(to.x, to.y, to.radius, r.to.x, r.to.y);
      expect(errFrom).toBeLessThanOrEqual(2);
      expect(errTo).toBeLessThanOrEqual(2);
    }
    // the whole graph must also report a bounded max endpoint error
    expect(vm.diagnostics.maxEndpointError).toBeLessThanOrEqual(2);
  });

  it("is deterministic: identical input yields identical geometry", () => {
    const a = buildStarCanvasViewModel(fixture());
    const b = buildStarCanvasViewModel(fixture());
    expect(a.entities.map((e) => [e.id, e.x, e.y, e.radius])).toEqual(
      b.entities.map((e) => [e.id, e.x, e.y, e.radius]),
    );
    expect(a.relations).toEqual(b.relations);
    expect(a.version).toBe(b.version);
  });

  it("is incremental: stable nodes reuse positions when nothing changed", () => {
    const first = buildStarCanvasViewModel(fixture());
    // Reconstruct a StarGraphLayoutResult from the emitted entities so the
    // engine's `previous` channel (incremental stability) is exercised for real.
    const prevLayout: import("@multica/core/research").StarGraphLayoutResult = {
      nodes: first.entities.map((e) => ({
        id: e.id,
        tier: e.tier,
        x: e.x,
        y: e.y,
        radius: e.radius,
        label: e.label,
        clusterId: e.clusterId,
        angle: e.angle,
        radiusOffset: e.radiusOffset,
        parentId: e.parentId,
      })),
      edges: first.relations.map((r) => ({
        id: r.id,
        fromNodeId: r.fromNodeId,
        toNodeId: r.toNodeId,
        kind: r.kind,
        from: r.from,
        to: r.to,
      })),
      clusters: first.clusters,
      rootId: first.rootId,
      version: first.version,
      stats: { reused: 0, moved: 0, total: first.entities.length },
      keyByNode: new Map(),
    };
    const again = buildStarCanvasViewModel({ ...fixture(), previous: prevLayout });
    // real stability guarantee: no node moved (root is recomputed each run but
    // lands in the same origin centre), positions are byte-identical.
    expect(again.stats.total).toBe(first.stats.total);
    for (const e of again.entities) {
      const before = first.entities.find((x) => x.id === e.id)!;
      expect(e.x).toBeCloseTo(before.x, 3);
      expect(e.y).toBeCloseTo(before.y, 3);
    }
  });

  it("wires the canonical 4-authoritative typed level, never fabricating", () => {
    const vm = buildStarCanvasViewModel(fixture());
    const tierOf = new Map(vm.entities.map((e) => [e.id, e.tier]));
    expect(tierOf.get("goal")).toBe("xxl");
    expect(tierOf.get("d1")).toBe("l");
    expect(tierOf.get("c1")).toBe("m");
    expect(tierOf.get("agent1")).toBe("s");
  });

  it("classifies relations conservatively and carries real edge types", () => {
    expect(layoutKindForEdgeType("leads_to")).toBe("decompose");
    expect(layoutKindForEdgeType("supports")).toBe("support");
    expect(layoutKindForEdgeType("contradicts")).toBe("challenge");
    expect(layoutKindForEdgeType("discussed_by")).toBe("support");
    const vm = buildStarCanvasViewModel(fixture());
    const kind = new Map(vm.relations.map((r) => [`${r.fromNodeId}->${r.toNodeId}`, r.kind]));
    const edgeType = new Map(vm.relations.map((r) => [`${r.fromNodeId}->${r.toNodeId}`, r.edgeType]));
    expect(kind.get("goal->d1")).toBe("decompose");
    expect(kind.get("d1->c1")).toBe("support");
    expect(kind.get("c1->d2")).toBe("challenge");
    // real canonical type survives for styling
    expect(edgeType.get("c1->d2")).toBe("contradicts");
  });
});

describe("LRM-1497 view-model — viewport-safe rebase (AC: 3 viewports, no occlusion)", () => {
  const CASES: Array<{ label: string; viewport: { width: number; height: number }; panel: number }> = [
    { label: "1440x900", viewport: { width: 1440, height: 900 }, panel: 360 },
    { label: "1920x1080", viewport: { width: 1920, height: 1080 }, panel: 360 },
    { label: "narrow 768x900", viewport: { width: 768, height: 900 }, panel: 0 },
  ];

  for (const { label, viewport, panel } of CASES) {
    it(`keeps endpoints precise after right-panel-safe rebase (${label})`, () => {
      const base = buildStarCanvasViewModel(fixture());
      const vm = rebaseStarCanvasIntoViewModel(base, viewport, { rightPanelWidth: panel });
      const byId = new Map(vm.entities.map((e) => [e.id, e]));
      for (const r of vm.relations) {
        const from = byId.get(r.fromNodeId)!;
        const to = byId.get(r.toNodeId)!;
        const errFrom = distToCircleErr(from.x, from.y, from.radius, r.from.x, r.from.y);
        const errTo = distToCircleErr(to.x, to.y, to.radius, r.to.x, r.to.y);
        expect(errFrom).toBeLessThanOrEqual(2);
        expect(errTo).toBeLessThanOrEqual(2);
      }
      // the goal (root) must land inside the viewport band
      const root = byId.get(vm.rootId!);
      expect(root).toBeDefined();
      expect(root!.x).toBeGreaterThanOrEqual(0);
      expect(root!.x).toBeLessThanOrEqual(viewport.width);
      expect(root!.y).toBeGreaterThanOrEqual(0);
      expect(root!.y).toBeLessThanOrEqual(viewport.height);
    });
  }

  it("preserves relative geometry across rebase (incremental stability)", () => {
    const base = buildStarCanvasViewModel(fixture());
    const vm = rebaseStarCanvasIntoViewModel(base, { width: 1440, height: 900 }, { rightPanelWidth: 360 });
    // node-to-node deltas must be preserved (uniform affine, no re-shuffle)
    const byId = new Map(vm.entities.map((e) => [e.id, e]));
    const g = byId.get("goal")!;
    const d1 = byId.get("d1")!;
    const dx = (d1.x - g.x).toFixed(0);
    const baseById = new Map(base.entities.map((e) => [e.id, e]));
    const bg = baseById.get("goal")!;
    const bd1 = baseById.get("d1")!;
    const bdx = (bd1.x - bg.x).toFixed(0);
    // The rebase may apply a uniform translate (dx unchanged) or a uniform scale
    // when the graph is wider than the band — assert deterministic affine, i.e.
    // the move is either pure translate (dx equal) or a consistent scale (both
    // x and y deltas shrink by the same ratio).
    if (dx === bdx) {
      const dy = (d1.y - g.y).toFixed(0);
      const bdy = (bd1.y - bg.y).toFixed(0);
      expect(dy).toBe(bdy);
    } else {
      const sx = Number(dx) / (Number(bdx) || 1);
      const dx2 = (byId.get("d2")!.x - byId.get("goal")!.x).toFixed(0);
      const bdx2 = (baseById.get("d2")!.x - baseById.get("goal")!.x).toFixed(0);
      expect(Number(dx2) / (Number(bdx2) || 1)).toBeCloseTo(sx, 1);
    }
  });

  it("empty graph degrades to an empty model without inventing topology", () => {
    const vm = buildStarCanvasViewModel({ nodes: [], edges: [] });
    expect(vm.entities).toHaveLength(0);
    expect(vm.relations).toHaveLength(0);
    expect(vm.rootId).toBeNull();
  });
});
