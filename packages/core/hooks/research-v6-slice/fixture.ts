/**
 * Research V6 · Projection Slice contract fixture (LRM-1465 / FE-03).
 *
 * Deterministic, in-memory Projection Slice server. Serves bounded slices that
 * honour every request field the plan §7.2 lists — root, direction, relation
 * types, max depth, status, importance floor, cursor, limit — with stable
 * ordering fixed to a snapshot. Used to develop and verify the frontend slice
 * pipeline while the backend Slice endpoint is not yet live. It is an explicit
 * injected gateway (see ProjectionSliceGateway): production wiring never
 * constructs it silently, so no mock reaches a production path.
 *
 * The fixture also exposes a 10_000-node builder used by the scale/behaviour
 * tests to prove the browser never requests the whole graph.
 */

import type {
  ResearchGraphEdge,
  ResearchGraphNode,
} from "../../types/research";
import {
  type ProjectionSliceRequest,
  type ProjectionSliceResponse,
  type SliceNode,
} from "./types";

/** Observable request record — lets tests assert "Network" parameters. */
export interface SliceWireRequest {
  path: string;
  params: Readonly<Record<string, string | number | undefined>>;
  signal: AbortSignal | null;
}

/**
 * The network abstraction a slice loader talks to. `request` returns a
 * cancellable, cursor-paginated page. `observe` receives every wire request
 * (both real backend and fixture) for diagnostics / Network-recording tests.
 */
export interface ProjectionSliceGateway {
  request(
    req: ProjectionSliceRequest,
    options?: { signal?: AbortSignal },
  ): Promise<ProjectionSliceResponse>;
  observe(listener: (wire: SliceWireRequest) => void): () => void;
}

export interface SliceFixtureGraph {
  nodes: ResearchGraphNode[];
  edges: ResearchGraphEdge[];
  /** nodeId -> importance in [0,1]. Absent defaults to 0. */
  importance?: Readonly<Record<string, number>>;
  snapshotId?: string;
  throughEventSequence?: number;
}

const DESCENDANT_COUNT_CAP = 999;

interface Adjacency {
  out: Map<string, string[]>;
  inMap: Map<string, string[]>;
  edgeTypes: Map<string, Map<string, string>>;
  byId: Map<string, ResearchGraphNode>;
  statusById: Map<string, string>;
}

function buildAdjacency(graph: SliceFixtureGraph): Adjacency {
  const out = new Map<string, string[]>();
  const inMap = new Map<string, string[]>();
  const edgeTypes = new Map<string, Map<string, string>>();
  const byId = new Map<string, ResearchGraphNode>();
  for (const n of graph.nodes) {
    byId.set(n.id, n);
    if (!out.has(n.id)) out.set(n.id, []);
    if (!inMap.has(n.id)) inMap.set(n.id, []);
  }
  for (const e of graph.edges) {
    if (!byId.has(e.from_node_id) || !byId.has(e.to_node_id)) continue;
    out.get(e.from_node_id)!.push(e.to_node_id);
    inMap.get(e.to_node_id)!.push(e.from_node_id);
    let t = edgeTypes.get(e.from_node_id);
    if (!t) {
      t = new Map();
      edgeTypes.set(e.from_node_id, t);
    }
    t.set(e.to_node_id, e.edge_type);
  }
  const statusById = new Map<string, string>();
  for (const n of graph.nodes) statusById.set(n.id, n.status);
  return { out, inMap, edgeTypes, byId, statusById };
}

function importanceOf(graph: SliceFixtureGraph, nodeId: string): number {
  const v = graph.importance?.[nodeId];
  return typeof v === "number" && Number.isFinite(v) ? Math.max(0, Math.min(1, v)) : 0;
}

function shortHash(input: string): string {
  let h = 2166136261;
  for (let i = 0; i < input.length; i += 1) {
    h ^= input.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return (h >>> 0).toString(36);
}

function assertSignal(signal: AbortSignal | null | undefined): void {
  if (signal?.aborted) {
    const e = new Error("Projection slice request aborted");
    e.name = "AbortError";
    throw e;
  }
}

function decodeCursor(cursor: string | null | undefined): number {
  if (cursor && /^p\d+$/.test(cursor)) return Number(cursor.slice(1));
  return 0;
}

/** BFS reachable set honoring direction + relation types + maxDepth, then filters. */
function reachableOrdered(
  adjacency: Adjacency,
  graph: SliceFixtureGraph,
  req: ProjectionSliceRequest,
): string[] {
  const { out, inMap, edgeTypes, statusById } = adjacency;
  const maxDepth = Math.max(0, Math.floor(req.maxDepth));
  const relationSet = req.relationTypes && req.relationTypes.length
    ? new Set(req.relationTypes)
    : null;
  const statusSet = req.status && req.status.length ? new Set(req.status) : null;
  const importanceFloor = req.importanceFloor || 0;

  const visited = new Set<string>([req.root]);
  const depth = new Map<string, number>([[req.root, 0]]);
  const reachable: string[] = [req.root];

  // BFS (iterative to stay deterministic and avoid deep recursion at 10k nodes).
  const queue: string[] = [req.root];
  while (queue.length > 0) {
    const cur = queue.shift()!;
    const d = depth.get(cur)!;
    if (d >= maxDepth) continue;
    const fwd = out.get(cur)!;
    const targets =
      req.direction === "in"
        ? inMap.get(cur)!
        : req.direction === "both"
          ? fwd.concat(inMap.get(cur)!)
          : fwd;
    const seenTargets = new Set<string>();
    for (const nb of targets) {
      if (seenTargets.has(nb)) continue;
      seenTargets.add(nb);
      let edgeType: string | undefined;
      if (req.direction === "in") {
        edgeType = edgeTypes.get(nb)?.get(cur);
      } else if (req.direction === "both" && fwd.indexOf(nb) === -1) {
        edgeType = edgeTypes.get(nb)?.get(cur);
      } else {
        edgeType = edgeTypes.get(cur)?.get(nb);
      }
      if (relationSet && (!edgeType || !relationSet.has(edgeType))) continue;
      if (visited.has(nb)) continue;
      visited.add(nb);
      depth.set(nb, d + 1);
      queue.push(nb);
      reachable.push(nb);
    }
  }

  // Deterministic stable order: root first (BFS discovery order). This never
  // re-sorts across pages, so repeated requests produce identical pages.
  return reachable.filter((id) => {
    const st = statusById.get(id);
    if (statusSet && st && !statusSet.has(st)) return false;
    if (importanceFloor > 0 && importanceOf(graph, id) < importanceFloor) return false;
    return true;
  });
}

/**
 * Create a deterministic slice gateway backed by the supplied graph.
 * Repeated identical requests return identical pages in identical order.
 */
export function createProjectionSliceFixture(
  graph: SliceFixtureGraph,
): ProjectionSliceGateway {
  const snapshotId =
    graph.snapshotId ??
    `fixture-${shortHash(graph.nodes.map((n) => n.id).join(","))}`;
  const throughEventSequence = graph.throughEventSequence ?? graph.nodes.length;
  const listeners = new Set<(w: SliceWireRequest) => void>();

  const adjacency = buildAdjacency(graph);

  const contentHashFor = (nodeIds: readonly string[]): string =>
    shortHash(`${snapshotId}:${nodeIds.join("|")}`);

  const request = async (
    req: ProjectionSliceRequest,
    options?: { signal?: AbortSignal },
  ): Promise<ProjectionSliceResponse> => {
    const limit = Math.max(1, Math.floor(req.limit));
    const signal = options?.signal ?? null;
    assertSignal(signal);

    const wire: SliceWireRequest = {
      path: `/api/research/v6/slices/${req.root}`,
      params: {
        direction: req.direction,
        relation_types: req.relationTypes?.join(",") ?? undefined,
        max_depth: req.maxDepth,
        status: req.status?.join(",") ?? undefined,
        importance_floor: req.importanceFloor,
        limit,
        cursor: req.cursor ?? undefined,
      },
      signal,
    };
    for (const l of listeners) l(wire);

    const order = reachableOrdered(adjacency, graph, req);
    const totalNodes = order.length;
    const start = decodeCursor(req.cursor);
    const pageIds = order.slice(start, start + limit);
    const nextCursor = start + limit < order.length ? `p${start + limit}` : null;
    assertSignal(signal);

    const pageSet = new Set(pageIds);
    // Bounded "still unloaded" estimate shared across the page (O(page) not
    // O(page × reachable)) so a 10k request stays a single render-frame scan.
    const unloadedWorld = Math.min(totalNodes - pageIds.length, DESCENDANT_COUNT_CAP);

    const nodes: SliceNode[] = pageIds.map((id) => {
      const node = adjacency.byId.get(id)!;
      const fwd = adjacency.out.get(id) ?? [];
      const rev = adjacency.inMap.get(id) ?? [];
      const dirNeighbors =
        req.direction === "in" ? rev : req.direction === "both" ? fwd.concat(rev) : fwd;
      const neighborSet = new Set(dirNeighbors);
      let unloadedNeighborCount = 0;
      for (const nb of neighborSet) if (!pageSet.has(nb)) unloadedNeighborCount += 1;

      return {
        node: {
          ...node,
          payload: {
            ...(node.payload as Record<string, unknown> | undefined),
            importance: importanceOf(graph, id),
          },
        },
        discovery: {
          nodeId: id,
          unloadedNeighborCount,
          unloadedDescendantCount: unloadedWorld,
          canExpand: unloadedNeighborCount > 0 || unloadedWorld > 0,
        },
      };
    });

    const nodeIds = new Set(pageIds);
    const edges: ProjectionSliceResponse["edges"] = graph.edges
      .filter((e) => nodeIds.has(e.from_node_id) || nodeIds.has(e.to_node_id))
      .map((e) => ({ edge: e }));

    return {
      snapshotId,
      throughEventSequence,
      contentHash: contentHashFor(pageIds),
      nodes,
      edges,
      hasMore: nextCursor !== null,
      nextCursor,
      totalNodes,
      danglingCount: 0,
    };
  };

  return {
    request,
    observe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
  };
}

function ts(iso: string, i: number): string {
  return new Date(Date.parse(iso) + i * 1000).toISOString();
}

function makeNode(
  id: string,
  sessionId: string,
  nodeType: string,
  title: string,
  status: string,
): ResearchGraphNode {
  return {
    id,
    session_id: sessionId,
    node_type: nodeType,
    title,
    summary: title,
    status,
    actor_agent_id: null,
    payload: {},
    created_at: ts("2026-08-05T00:00:00Z", 0),
    updated_at: ts("2026-08-05T00:00:00Z", 0),
  };
}

function makeEdge(
  id: string,
  sessionId: string,
  from: string,
  to: string,
  edgeType: string,
): ResearchGraphEdge {
  return {
    id,
    session_id: sessionId,
    from_node_id: from,
    to_node_id: to,
    edge_type: edgeType,
    created_at: ts("2026-08-05T00:00:00Z", 0),
  };
}

/**
 * Build a deterministic graph with exactly `count` nodes: a root that fans out
 * to `branches` chains of `perBranch` nodes each, plus a deep tail to exercise
 * depth bounds. Used by the 10k-node protection tests.
 */
export function buildScalingFixture(options: {
  sessionId: string;
  totalNodes: number;
  branches?: number;
  maxDepth?: number;
}): SliceFixtureGraph {
  const { sessionId, totalNodes } = options;
  const branches = Math.max(1, options.branches ?? 32);
  const nodes: ResearchGraphNode[] = [];
  const edges: ResearchGraphEdge[] = [];
  const importance: Record<string, number> = {};

  const rootId = "root";
  nodes.push(makeNode(rootId, sessionId, "goal", "root", "done"));
  importance[rootId] = 1;
  let edgeSeq = 0;

  // distribute remaining nodes across branches, each a chain
  const perBranch = Math.max(1, Math.floor((totalNodes - 1) / branches));
  for (let b = 0; b < branches && nodes.length < totalNodes; b += 1) {
    const branchRoot = `${sessionId}-branch-${b}-n0`;
    nodes.push(makeNode(branchRoot, sessionId, "branch", `branch ${b}`, "active"));
    importance[branchRoot] = 0.8 - (b % 5) * 0.1;
    edges.push(makeEdge(`e${edgeSeq++}`, sessionId, rootId, branchRoot, "decomposes"));
    let prev = branchRoot;
    for (let i = 0; i < perBranch && nodes.length < totalNodes; i += 1) {
      const id = `${sessionId}-b${b}-n${i + 1}`;
      nodes.push(makeNode(id, sessionId, "task", `branch ${b} step ${i + 1}`, "active"));
      importance[id] = 0.5 + ((i + b) % 5) * 0.1;
      edges.push(makeEdge(`e${edgeSeq++}`, sessionId, prev, id, "produces"));
      prev = id;
    }
    if (nodes.length >= totalNodes) break;
  }

  return { nodes, edges, importance, snapshotId: `scale-${totalNodes}` };
}
