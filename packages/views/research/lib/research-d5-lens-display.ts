import type { TypedGraphResponse, ResearchD5Lens } from "@multica/core/research";
import type { StarCanvasViewModel } from "../star-graph/lib/star-canvas-view-model";

export { isResearchD5Lens } from "@multica/core/research";

export interface D5LensDisplayHints {
  dimmedNodeIds: ReadonlySet<string>;
  emphasizedNodeIds: ReadonlySet<string>;
  dimmedRelationIds: ReadonlySet<string>;
  emphasizedRelationIds: ReadonlySet<string>;
}

export interface D5LensDisplayOptions {
  /** Display filter round; when set, the lineage lens emphasizes that round. */
  filterRound?: string | null;
}

const EMPTY_HINTS: D5LensDisplayHints = {
  dimmedNodeIds: new Set(),
  emphasizedNodeIds: new Set(),
  dimmedRelationIds: new Set(),
  emphasizedRelationIds: new Set(),
};

function parseFilterRound(round: string | null | undefined): number | null {
  if (round == null || round.trim() === "") return null;
  const parsed = Number(round);
  if (!Number.isFinite(parsed) || parsed <= 0) return null;
  return Math.trunc(parsed);
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

/** Round-spectrum emphasis for the 轮次谱系 lens (latest round by default). */
function collectRoundEmphasis(
  typed: TypedGraphResponse,
  focusRound: number | null,
): { emphasized: Set<string>; dimmed: Set<string> } {
  const emphasized = new Set<string>();
  const dimmed = new Set<string>();

  const rounds = typed.nodes
    .map((node) => node.round)
    .filter((round): round is number => round != null && !Number.isNaN(round) && round > 0);

  if (rounds.length === 0) return { emphasized, dimmed };

  const targetRound = focusRound ?? Math.max(...rounds);

  for (const node of typed.nodes) {
    if (node.round == null || Number.isNaN(node.round) || node.round <= 0) {
      dimmed.add(node.id);
      continue;
    }
    if (node.round === targetRound) emphasized.add(node.id);
    else dimmed.add(node.id);
  }

  return { emphasized, dimmed };
}

export function buildD5LensDisplayHints(
  lens: ResearchD5Lens,
  typed: TypedGraphResponse | undefined,
  model: StarCanvasViewModel | null,
  options: D5LensDisplayOptions = {},
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

  const focusRound = parseFilterRound(options.filterRound);
  const { emphasized: roundNodes, dimmed: dimmedNodes } = collectRoundEmphasis(
    typed,
    focusRound,
  );

  if (roundNodes.size === 0 && dimmedNodes.size === 0) return EMPTY_HINTS;

  const emphasizedRelations = new Set<string>();
  const dimmedRelations = new Set<string>();

  for (const relation of model.relations) {
    const fromEmphasized = roundNodes.has(relation.fromNodeId);
    const toEmphasized = roundNodes.has(relation.toNodeId);
    if (fromEmphasized && toEmphasized) emphasizedRelations.add(relation.id);
    else if (fromEmphasized || toEmphasized) emphasizedRelations.add(relation.id);
    else dimmedRelations.add(relation.id);
  }

  return {
    emphasizedNodeIds: roundNodes,
    dimmedNodeIds: dimmedNodes,
    emphasizedRelationIds: emphasizedRelations,
    dimmedRelationIds: dimmedRelations,
  };
}
