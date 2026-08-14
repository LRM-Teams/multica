/**
 * Deterministic content hash for the unified canvas graph (§7.1 / §7.2).
 *
 * The hash covers ONLY canonical projection fields — node/edge identity,
 * kind, title/status, importance/freshness and typed relations. Display state
 * (x/y positions, folding, selection, animation) is deliberately excluded so
 * that the same canonical state always yields the same hash regardless of how
 * the renderer laid it out. A client can therefore verify "same snapshot
 * recompute → same hash" without any layout involvement.
 */
import type { CanvasCluster, CanvasEdge, CanvasNode, CanvasSnapshot } from "./canvas-types";

function encodeSimple(value: unknown): string {
  if (value === null || value === undefined) return "";
  if (typeof value === "boolean" || typeof value === "number") return String(value);
  if (typeof value === "string") return JSON.stringify(value);
  return "";
}

function nodeToken(n: CanvasNode): string {
  const parts = [
    n.id,
    n.kind,
    n.subtype ?? "",
    n.schemaVersion ?? "",
    n.title,
    n.summary,
    n.status,
    String(n.importance),
    String(n.freshness),
    n.detailRef,
    n.actor ?? "",
    n.level ?? "",
    n.clusterId ?? "",
    n.parentId ?? "",
    n.round ?? "",
    n.confidence ?? "",
    n.documentCount ?? "",
    n.conclusionCount ?? "",
    n.derivedFrom ?? "",
    (n.mergedFrom ?? []).slice().sort().join(","),
    n.supersededBy ?? "",
    n.restartOf ?? "",
    n.invalidatedBy ?? "",
  ];
  return parts.map(encodeSimple).join("\u0001");
}

function clusterToken(cluster: CanvasCluster): string {
  return [
    cluster.id,
    cluster.label,
    cluster.clusterType,
    cluster.memberNodeIds.slice().sort().join(","),
    cluster.confidence ?? "",
    cluster.documentCount ?? "",
    cluster.conclusionCount ?? "",
  ].map(encodeSimple).join("\u0001");
}

function edgeToken(e: CanvasEdge): string {
  return [e.id, e.from, e.to, e.relation, e.createdAt]
    .map(encodeSimple)
    .join("\u0001");
}

function fnv1a(input: string): string {
  let hash = 0x811c9dc5;
  for (let i = 0; i < input.length; i += 1) {
    hash ^= input.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return (hash >>> 0).toString(16).padStart(8, "0");
}

/**
 * Stable FNV-1a hash over the canonical node/edge set.
 * Order independent for nodes, order independent for edges, but recomputed in
 * one pass so identical canonical states always produce identical output.
 */
export function computeGraphContentHash(
  nodes: Iterable<CanvasNode>,
  edges: Iterable<CanvasEdge>,
  clusters: Iterable<CanvasCluster> = [],
): string {
  const nodeTokens: string[] = [];
  for (const n of nodes) nodeTokens.push(nodeToken(n));
  const edgeTokens: string[] = [];
  for (const e of edges) edgeTokens.push(edgeToken(e));
  const clusterTokens: string[] = [];
  for (const cluster of clusters) clusterTokens.push(clusterToken(cluster));

  const nodeSink = nodeTokens.sort().join("\u0002");
  const edgeSink = edgeTokens.sort().join("\u0002");
  const clusterSink = clusterTokens.sort().join("\u0002");
  // Salt each section with its length so an empty edge-set never collapses
  // into a prefix of a non-empty one.
  return `${fnv1a(`N:${nodeSink.length}:${nodeSink}`)}${fnv1a(`E:${edgeSink.length}:${edgeSink}`)}${fnv1a(`C:${clusterSink.length}:${clusterSink}`)}`;
}

/** Recompute the content hash of a snapshot's current canonical state. */
export function snapshotContentHash(snapshot: CanvasSnapshot): string {
  return computeGraphContentHash(snapshot.nodes, snapshot.edges, snapshot.clusters ?? []);
}
