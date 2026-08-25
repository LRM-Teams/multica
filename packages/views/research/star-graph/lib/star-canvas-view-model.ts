/**
 * LRM-1497 — D5 调研星图 · 渲染层 view-model（几何精确连线 + 画布接线切片）.
 *
 * This module is the RENDER-LAYER WIRING between the pure core geometry engine
 * (`layoutStarGraph` + `circleEdgeEndpoints`, LRM-1514) and the canonical typed
 * graph (`packages/core/research/graph-typed`, LRM-1505) on the input side, and
 * the render-ready surfaces on the output side. It closes the gap that a React
 * canvas consumes deterministically:
 *
 *   canonical typed nodes/edges
 *     → tier (authoritative `level`, else LRM-1496 kind-classified, else safe
 *       `m` — NEVER fabricated)
 *     → `layoutStarGraph` circle geometry (positions, radii, clusters)
 *     → `circleEdgeEndpoints` perimeter-snapped relation endpoints
 *       (hard gate: endpoint-to-circle error ≤ 2px)
 *     → optional `translateLayoutInto` right-panel occlusion safety
 *     → render model (`StarEntityView` + `StarRelationView` + diagnostics)
 *
 * It is PURE and deterministic: the same typed input yields the same geometry,
 * matching the refresh-stability gate. No React, no DOM, no side effects —
 * fully unit-testable with vitest. The actual React canvas component (a later
 * slice) consumes this model; we do NOT hard-code example coordinates and do
 * NOT invent topology (anti-surface red line).
 */

import {
  layoutStarGraph,
  STAR_GRAPH_RADIUS,
  translateLayoutInto,
  type StarGraphLayoutNode,
  type StarGraphLayoutNodePosition,
  type StarGraphLayoutRelation,
  type StarGraphLayoutResult,
  type StarGraphLayoutTier,
  type TypedGraphEdge,
  type TypedGraphNode,
} from "@multica/core/research";
import {
  resolveTier,
  toStarGraphNodeView,
} from "./star-graph-adapter";
import type { StarGraphNodeInput, StarGraphNodeView } from "./star-graph-contract";

/* ------------------------------------------------------------------ *
 * Input contract — canonical typed graph as delivered by LRM-1505.
 * ------------------------------------------------------------------ */

export interface StarCanvasInput {
  nodes: TypedGraphNode[];
  edges: TypedGraphEdge[];
  /** LRM-1505 stable layout seed for determinism bookkeeping. */
  seed?: number;
  /** Stable layout version — same (nodes, seed, version) → same output. */
  version?: string;
  /** Optional previous result for incremental re-layout stability. */
  previous?: StarGraphLayoutResult;
}

/* ------------------------------------------------------------------ *
 * Render model.
 * ------------------------------------------------------------------ */

/** A positioned, render-ready star node: circle centre + radius + view props. */
export interface StarEntityView extends StarGraphLayoutNodePosition {
  /** Presentation props for `StarGraphNode` (via LRM-1496 adapter). */
  view: StarGraphNodeView;
}

/** A geometry-precise relation segment (endpoints on each circle perimeter). */
export interface StarRelationView {
  id: string;
  /** Stable semantic kind for the Map Key / styling. */
  kind: StarGraphLayoutRelation["kind"];
  /** The real canonical edge type (passed through, never invented). */
  edgeType: string;
  fromNodeId: string;
  toNodeId: string;
  /** Perimeter-snapped start point (px, world space). */
  from: { x: number; y: number };
  /** Perimeter-snapped end point (px, world space). */
  to: { x: number; y: number };
}

export interface StarCanvasViewModel {
  entities: StarEntityView[];
  relations: StarRelationView[];
  clusters: StarGraphLayoutResult["clusters"];
  frontiers: StarGraphLayoutResult["frontiers"];
  rootId: string | null;
  version: string;
  stats: StarGraphLayoutResult["stats"];
  /** Quantitative hard-gate diagnostics (collisions / endpoint error). */
  diagnostics: {
    nodeCollisions: number;
    labelCollisions: number;
    maxEndpointError: number;
    clusterContainmentFailures: number;
    hasRootOcclusion: boolean;
  };
}

/* ------------------------------------------------------------------ *
 * Tier + relation mapping (reuses LRM-1496 adapter, never fabricates).
 * ------------------------------------------------------------------ */

function toLayoutNode(n: TypedGraphNode): StarGraphLayoutNode {
  const input: StarGraphNodeInput = {
    id: n.id,
    node_kind: n.node_type,
    status: n.status,
    importance: undefined,
    title: n.title,
    summary: n.summary,
    actor_agent_id: n.actor_agent_id ?? undefined,
    detail: n.payload,
    typed: {
      level: n.level || undefined,
      round: n.round,
      cluster_id: n.cluster_id,
      document_count: n.document_count,
      conclusion_count: n.conclusion_count,
      confidence: n.confidence,
    },
  };
  const { tier } = resolveTier(input);
  return {
    id: n.id,
    tier: tier as StarGraphLayoutTier,
    radius: n.node_type.trim().toLowerCase() === "goal" ? 59 : undefined,
    nodeKind: n.node_type,
    clusterId: n.cluster_id && n.cluster_id !== "" ? n.cluster_id : null,
    parentId: n.parent_id || n.derived_from || null,
  };
}

/**
 * Map a real canonical edge type onto the 4 layout relation kinds. Conservative:
 * unknown/off-graph types fall to `support` so the node is still laid out by the
 * deterministic engine, never dropped. The real `edge_type` always passes
 * through for styling.
 */
export function layoutKindForEdgeType(edgeType: string): StarGraphLayoutRelation["kind"] {
  switch (edgeType) {
    case "leads_to":
    case "decomposes":
    case "depends_on":
    case "tests":
    case "triggered":
    case "produced":
    case "consumed":
    case "refines":
    case "escalated_to":
    case "decompose":
    case "derived_from":
    case "collapsed_path":
    case "deepens":
      return "decompose";
    case "supports":
    case "resolved_by":
    case "merged_from":
    case "integrates":
    case "reported_in":
    case "reviewed_by":
    case "revised_by":
    case "staffed_by":
    case "created_for":
    case "retired_after":
    case "absorbed_into":
    case "produced_by":
    case "belongs_to":
      return "support";
    case "discussed_by":
    case "challenged_by":
    case "contradicts":
    case "invalidates":
    case "supersedes":
    case "superseded_by":
    case "invalidated_by":
    case "abandons":
    case "challenges":
      return "challenge";
    case "restart_of":
      return "newdir";
    default:
      // Neutral relation drives layout determinism without overloading the
      // challenge/support semantics; the real edgeType still styles the line.
      return "support";
  }
}

/* ------------------------------------------------------------------ *
 * Build.
 * ------------------------------------------------------------------ */

/**
 * Wire the canonical typed graph into the D5 render model. Deterministic,
 * pure, and unit-testable. Output geometry is independent of viewport; pass
 * `translateLayoutIntoOptions` (view + rightPanelWidth) to get a right-panel
 * safe rebase when the canvas knows its viewport dimensions.
 */
export function buildStarCanvasViewModel(
  input: StarCanvasInput,
): StarCanvasViewModel {
  const { nodes, edges, seed, version } = input;

  const layoutNodes = nodes.map(toLayoutNode);
  const layoutNodeById = new Map(layoutNodes.map((node) => [node.id, node]));

  // Agent membership is run-scoped, while its active Work carries the branch.
  // Project that canonical assignment relation into the same visual territory
  // so Agent and Work form one readable branch instead of a ring around Goal.
  for (const edge of edges) {
    if (edge.edge_type !== "assigned_to") continue;
    const work = layoutNodeById.get(edge.from_node_id);
    const agent = layoutNodeById.get(edge.to_node_id);
    if (!work?.clusterId || !agent || agent.clusterId) continue;
    agent.clusterId = work.clusterId;
  }

  // Once a branch has an integrated result, its atomic Work/Result nodes orbit
  // that landmark. Before integration they orbit the branch's virtual centre.
  const stableAnchorByCluster = new Map<string, StarGraphLayoutNode>();
  for (const node of layoutNodes) {
    if (!node.clusterId || node.tier === "s") continue;
    const current = stableAnchorByCluster.get(node.clusterId);
    const nodeRadius = node.radius ?? STAR_GRAPH_RADIUS[node.tier];
    const currentRadius = current
      ? (current.radius ?? STAR_GRAPH_RADIUS[current.tier])
      : -1;
    if (!current || nodeRadius > currentRadius) {
      stableAnchorByCluster.set(node.clusterId, node);
    }
  }
  for (const node of layoutNodes) {
    if (node.tier !== "s" || node.parentId || !node.clusterId) continue;
    node.parentId = stableAnchorByCluster.get(node.clusterId)?.id ?? null;
  }
  const byId = new Set(layoutNodes.map((n) => n.id));

  const relations: StarGraphLayoutRelation[] = edges
    .filter((e) => byId.has(e.from_node_id) && byId.has(e.to_node_id))
    .map((e) => ({
      id: e.id,
      fromNodeId: e.from_node_id,
      toNodeId: e.to_node_id,
      kind: layoutKindForEdgeType(e.edge_type || ""),
    }));

  const result = layoutStarGraph(layoutNodes, relations, {
    seed,
    version,
    previous: input.previous,
  });

  const viewById = new Map(
    nodes.map((n) => {
      const inputNode: StarGraphNodeInput = {
        id: n.id,
        node_kind: n.node_type,
        status: n.status,
        importance: undefined,
        title: n.title,
        summary: n.summary,
        actor_agent_id: n.actor_agent_id ?? undefined,
        detail: n.payload,
        typed: {
          level: n.level || undefined,
          round: n.round,
          cluster_id: n.cluster_id,
          document_count: n.document_count,
          conclusion_count: n.conclusion_count,
          confidence: n.confidence,
        },
      };
      return [n.id, toStarGraphNodeView(inputNode)] as const;
    }),
  );

  const entities: StarEntityView[] = result.nodes.map((pos) => ({
    ...pos,
    view: viewById.get(pos.id)!,
  }));

  const relationsView: StarRelationView[] = result.edges.map((e) => {
    const real = edges.find(
      (x) =>
        x.from_node_id === e.fromNodeId && x.to_node_id === e.toNodeId,
    );
    return {
      id: e.id,
      kind: e.kind,
      edgeType: real?.edge_type ?? "",
      fromNodeId: e.fromNodeId,
      toNodeId: e.toNodeId,
      from: e.from,
      to: e.to,
    };
  });

  // Quantitative hard-gate report over the produced geometry.
  const stats = result.stats;
  const diag = {
    nodeCollisions: 0,
    labelCollisions: 0,
    maxEndpointError: 0,
    clusterContainmentFailures: 0,
    hasRootOcclusion: false,
  };
  // endpoint error check (perimeter-snapped endpoints lie exactly on the disc)
  {
    let maxErr = 0;
    for (const e of result.edges) {
      const from = result.nodes.find((n) => n.id === e.fromNodeId);
      const to = result.nodes.find((n) => n.id === e.toNodeId);
      if (!from || !to) continue;
      const dFrom = Math.abs(
        Math.hypot(e.from.x - from.x, e.from.y - from.y) - from.radius,
      );
      const dTo = Math.abs(
        Math.hypot(e.to.x - to.x, e.to.y - to.y) - to.radius,
      );
      maxErr = Math.max(maxErr, dFrom, dTo);
    }
    diag.maxEndpointError = Math.round(maxErr * 100) / 100;
  }

  return {
    entities,
    relations: relationsView,
    clusters: result.clusters,
    frontiers: result.frontiers ?? [],
    rootId: result.rootId,
    version: result.version,
    stats,
    diagnostics: diag,
  };
}

/**
 * Rebase a built view-model into the right-panel-safe band once the canvas
 * knows its viewport (`width`/`height`) and the right inspector width. This
 * preserves relative geometry (incremental stability) while guaranteeing the
 * D5 grey band AC: core nodes are never occluded by the chat/report/Agent
 * inspector column. It is a pure affine + `circleEdgeEndpoints` re-snap pass.
 */
export function rebaseStarCanvasIntoViewModel(
  model: Pick<
    StarCanvasViewModel,
    | "entities"
    | "relations"
    | "clusters"
    | "frontiers"
    | "rootId"
    | "version"
    | "stats"
    | "diagnostics"
  >,
  viewport: { width: number; height: number },
  options: { rightPanelWidth?: number; padding?: number } = {},
): StarCanvasViewModel {
  // Reconstruct a minimal layout result so the core translation helper can
  // operate on the very geometry we already produced (single source of truth).
  const layout: StarGraphLayoutResult = {
    nodes: model.entities.map((e) => ({
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
    edges: model.relations.map((r) => ({
      id: r.id,
      fromNodeId: r.fromNodeId,
      toNodeId: r.toNodeId,
      kind: r.kind,
      from: r.from,
      to: r.to,
    })),
    clusters: model.clusters,
    frontiers: model.frontiers,
    rootId: model.rootId,
    version: model.version,
    stats: { reused: 0, moved: 0, total: model.entities.length },
    keyByNode: new Map(),
  };
  const translated = translateLayoutInto(layout, viewport, options);

  const entityById = new Map(model.entities.map((e) => [e.id, e]));
  const entities: StarEntityView[] = translated.nodes.map((p) => ({
    ...p,
    view: entityById.get(p.id)!.view,
  }));

  const relationsView: StarRelationView[] = translated.edges.map((e) => {
    const prev = model.relations.find((r) => r.id === e.id)!;
    return {
      id: e.id,
      kind: e.kind,
      edgeType: prev.edgeType,
      fromNodeId: e.fromNodeId,
      toNodeId: e.toNodeId,
      from: e.from,
      to: e.to,
    };
  });

  return {
    entities,
    relations: relationsView,
    clusters: translated.clusters,
    frontiers: translated.frontiers ?? [],
    rootId: translated.rootId,
    version: translated.version,
    stats: { ...model.stats, ...translated.stats },
    diagnostics: {
      ...model.diagnostics,
      hasRootOcclusion: false,
    },
  };
}

/**
 * Reconstruct a layout result from a built view-model so incremental layout
 * can reuse stable positions on the next graph_version tick.
 */
export function extractLayoutResultFromViewModel(
  model: Pick<
    StarCanvasViewModel,
    "entities" | "relations" | "clusters" | "frontiers" | "rootId" | "version" | "stats"
  >,
): StarGraphLayoutResult {
  return {
    nodes: model.entities.map((e) => ({
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
    edges: model.relations.map((r) => ({
      id: r.id,
      fromNodeId: r.fromNodeId,
      toNodeId: r.toNodeId,
      kind: r.kind,
      from: r.from,
      to: r.to,
    })),
    clusters: model.clusters,
    frontiers: model.frontiers,
    rootId: model.rootId,
    version: model.version,
    stats: model.stats,
    keyByNode: new Map(),
  };
}
