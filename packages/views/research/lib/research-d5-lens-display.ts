import type { TypedGraphResponse } from "@multica/core/research";
import type { StarCanvasViewModel } from "../star-graph/lib/star-canvas-view-model";
import type { ResearchD5Lens } from "./research-d5-lens";
import { RESEARCH_D5_LENSES } from "./research-d5-lens";

export interface D5LensDisplayHints {
  dimmedNodeIds: ReadonlySet<string>;
  emphasizedNodeIds: ReadonlySet<string>;
  dimmedRelationIds: ReadonlySet<string>;
  emphasizedRelationIds: ReadonlySet<string>;
}

const EMPTY_HINTS: D5LensDisplayHints = {
  dimmedNodeIds: new Set(),
  emphasizedNodeIds: new Set(),
  dimmedRelationIds: new Set(),
  emphasizedRelationIds: new Set(),
};

const LINEAGE_EDGE_TYPES = new Set([
  "merged",
  "merged_from",
  "merge",
  "restart",
  "restart_of",
  "superseded",
  "superseded_by",
  "invalidated",
  "invalidated_by",
  "derived",
  "lineage",
]);

const MERGE_RELATION_KINDS = new Set(["merge", "merged", "fusion"]);

export function isResearchD5Lens(value: string | null | undefined): value is ResearchD5Lens {
  return RESEARCH_D5_LENSES.includes(value as ResearchD5Lens);
}

function collectLineageNodeIds(typed: TypedGraphResponse): Set<string> {
  const ids = new Set<string>();
  for (const node of typed.nodes) {
    if (node.merged_from?.length) ids.add(node.id);
    if (node.restart_of) ids.add(node.id);
    if (node.superseded_by) ids.add(node.id);
    if (node.invalidated_by) ids.add(node.id);
    if (node.derived_from) ids.add(node.id);
    for (const sourceId of node.merged_from ?? []) ids.add(sourceId);
  }
  for (const merged of Object.values(typed.lineage?.merged ?? {})) {
    for (const id of merged) ids.add(id);
  }
  for (const derived of Object.values(typed.lineage?.derived ?? {})) {
    if (typeof derived === "string") ids.add(derived);
  }
  for (const restarted of Object.values(typed.lineage?.restarted ?? {})) {
    if (typeof restarted === "string") ids.add(restarted);
  }
  return ids;
}

function collectAgentNodeIds(typed: TypedGraphResponse): Set<string> {
  const ids = new Set<string>();
  for (const node of typed.nodes) {
    if (node.actor_agent_id) ids.add(node.id);
    if ((node.level || "").toLowerCase() === "s") ids.add(node.id);
  }
  for (const edge of typed.edges) {
    if (ids.has(edge.from_node_id) || ids.has(edge.to_node_id)) {
      ids.add(edge.from_node_id);
      ids.add(edge.to_node_id);
    }
  }
  return ids;
}

function collectConfidenceEmphasis(typed: TypedGraphResponse): {
  emphasized: Set<string>;
  dimmed: Set<string>;
} {
  const emphasized = new Set<string>();
  const dimmed = new Set<string>();
  const scored: { id: string; confidence: number }[] = [];

  for (const node of typed.nodes) {
    if (node.confidence == null || Number.isNaN(node.confidence)) {
      dimmed.add(node.id);
      continue;
    }
    scored.push({ id: node.id, confidence: node.confidence });
  }

  if (scored.length === 0) return { emphasized, dimmed };

  scored.sort((a, b) => b.confidence - a.confidence);
  const thresholdIndex = Math.max(0, Math.floor(scored.length * 0.35) - 1);
  const threshold = scored[thresholdIndex]?.confidence ?? 0;

  for (const entry of scored) {
    if (entry.confidence >= threshold) emphasized.add(entry.id);
    else dimmed.add(entry.id);
  }

  return { emphasized, dimmed };
}

export function buildD5LensDisplayHints(
  lens: ResearchD5Lens,
  typed: TypedGraphResponse | undefined,
  model: StarCanvasViewModel | null,
): D5LensDisplayHints {
  if (!typed || !model || lens === "relations") return EMPTY_HINTS;

  const allNodeIds = new Set(model.entities.map((entity) => entity.id));

  if (lens === "confidence") {
    const { emphasized, dimmed } = collectConfidenceEmphasis(typed);
    return {
      emphasizedNodeIds: emphasized,
      dimmedNodeIds: dimmed,
      emphasizedRelationIds: new Set(),
      dimmedRelationIds: new Set(),
    };
  }

  if (lens === "agent") {
    const emphasized = collectAgentNodeIds(typed);
    const dimmed = new Set<string>();
    for (const id of allNodeIds) {
      if (!emphasized.has(id)) dimmed.add(id);
    }
    return {
      emphasizedNodeIds: emphasized,
      dimmedNodeIds: dimmed,
      emphasizedRelationIds: new Set(),
      dimmedRelationIds: new Set(),
    };
  }

  const lineageNodes = collectLineageNodeIds(typed);
  const emphasizedRelations = new Set<string>();
  const dimmedRelations = new Set<string>();

  for (const relation of model.relations) {
    const lineageEdge =
      MERGE_RELATION_KINDS.has(relation.kind) ||
      LINEAGE_EDGE_TYPES.has(relation.edgeType.toLowerCase());
    const touchesLineage =
      lineageNodes.has(relation.fromNodeId) || lineageNodes.has(relation.toNodeId);
    if (lineageEdge || touchesLineage) emphasizedRelations.add(relation.id);
    else dimmedRelations.add(relation.id);
  }

  const dimmedNodes = new Set<string>();
  for (const id of allNodeIds) {
    if (!lineageNodes.has(id)) dimmedNodes.add(id);
  }

  return {
    emphasizedNodeIds: lineageNodes,
    dimmedNodeIds: dimmedNodes,
    emphasizedRelationIds: emphasizedRelations,
    dimmedRelationIds: dimmedRelations,
  };
}
