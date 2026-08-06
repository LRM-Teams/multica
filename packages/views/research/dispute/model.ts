/**
 * LRM-1472 / UI-04 — dispute subgraph derived model.
 * Pure projections from typed edges + canonical node status. Facts about
 * support/contradiction NEVER come from position text or animation; they come
 * only from the typed `supports` / `contradicts` / `refines` edge roles.
 * These are display projections — they never mutate the graph.
 */

import type { ResearchGraphEdge, ResearchGraphNode } from "@multica/core/types";
import {
  disputeLifecycleBucket,
  nodeConfidence,
} from "../lib/node-visuals";
import type { DisputeStance } from "./encode";
import { isDisputeDomainNodeType, stanceFromPayload, turnMarkerFromPayload, verdictFromPayload } from "./encode";

export const DISPUTE_STANCE_TYPES = new Set(["supports", "contradicts", "refines"]);

/** A dispute position with its stance (from the typed edge into the dispute). */
export type PositionView = {
  node: ResearchGraphNode;
  stance: DisputeStance;
  /** Direct evidence node ids (via supports edges into the position). */
  evidenceIds: string[];
  /** Contradicting evidence ids (via contradicts edges into the position). */
  contradictsIds: string[];
  confidence: number | null;
};

export type EvidenceView = {
  node: ResearchGraphNode;
  /** Relation to the focused dispute root: supports a position / contradicts one. */
  role: "supports" | "contradicts";
  targetPositionId: string | null;
};

export type TurnView = {
  node: ResearchGraphNode;
  marker: NonNullable<ReturnType<typeof import("./encode").turnMarkerFromPayload>> | null;
};

export type EscalationView = {
  target: ResearchGraphNode | null; // the Director / lead_adjudication activity
  requires: boolean; // whether the escalation arrow should render
};

export type DecisionView = {
  current: ResearchGraphNode | null;
  /** Prior decisions, newest first, with their supersede/invalidate relation. */
  history: Array<{ node: ResearchGraphNode; supersededBy: string | null; invalidatedBy: string | null }>;
};

export type DisputeSubgraphModel = {
  root: ResearchGraphNode | null;
  lifecycle: ReturnType<typeof disputeLifecycleBucket>;
  blocking: boolean;
  positions: PositionView[];
  overflowPositions: PositionView[];
  evidence: EvidenceView[];
  deliberation: ResearchGraphNode | null;
  turns: TurnView[];
  escalation: EscalationView;
  decision: DecisionView;
};

function indexById(nodes: ResearchGraphNode[]): Map<string, ResearchGraphNode> {
  const map = new Map<string, ResearchGraphNode>();
  for (const n of nodes) map.set(n.id, n);
  return map;
}

/**
 * Resolve the dispute root: the single `dispute` node in the subgraph. When a
 * session contains multiple dispute roots we only ever show one at a time
 * (callers pass their focused subgraph's node set), so a stable first-match is
 * acceptable and never conflates distinct disputes.
 */
export function findDisputeRoot(nodes: ResearchGraphNode[]): ResearchGraphNode | null {
  return nodes.find((n) => n.node_type === "dispute") ?? null;
}

/** Edges touching the given set of node ids. */
function edgesWithin(edges: ResearchGraphEdge[], idSet: Set<string>): ResearchGraphEdge[] {
  return edges.filter((e) => idSet.has(e.from_node_id) && idSet.has(e.to_node_id));
}

/**
 * Build the full display model for a dispute subgraph (nodes+edges = bounded
 * Projection Slice from the canonical store). The root determines which
 * positions/turns/decisions belong to THIS dispute via typed edges.
 */
export function buildDisputeModel(
  nodes: ResearchGraphNode[],
  edges: ResearchGraphEdge[],
): DisputeSubgraphModel {
  const index = indexById(nodes);
  const root = findDisputeRoot(nodes);
  if (!root) {
    return {
      root: null,
      lifecycle: "open",
      blocking: false,
      positions: [],
      overflowPositions: [],
      evidence: [],
      deliberation: null,
      turns: [],
      escalation: { target: null, requires: false },
      decision: { current: null, history: [] },
    };
  }

  const idSet = new Set(nodes.map((n) => n.id));
  const within = edgesWithin(edges, idSet);

  const positionEdges = within.filter(
    (e) =>
      (e.from_node_id === root.id || e.to_node_id === root.id) &&
      DISPUTE_STANCE_TYPES.has(e.edge_type),
  );
  const positionIds: string[] = [];
  const stanceByPosition = new Map<string, DisputeStance>();
  for (const e of positionEdges) {
    // Arrow points toward the entity it annotates: position → dispute.
    const positionId = e.from_node_id === root.id ? e.to_node_id : e.from_node_id;
    const position = index.get(positionId);
    if (!position || position.node_type !== "dispute_position") continue;
    const stance = stanceFromPayload(position) ?? (e.edge_type as DisputeStance);
    if (!stanceByPosition.has(positionId)) stanceByPosition.set(positionId, stance);
    if (!positionIds.includes(positionId)) positionIds.push(positionId);
  }

  const positions: PositionView[] = [];
  const evidence: EvidenceView[] = [];
  for (const positionId of positionIds) {
    const node = index.get(positionId);
    if (!node) continue;
    const stance = stanceByPosition.get(positionId) ?? "supports";
    // Evidence into a position via supports = its positive evidence; contradicts = its counter-evidence.
    const supports = within.filter(
      (e) => e.to_node_id === positionId && e.edge_type === "supports",
    );
    const contradicts = within.filter(
      (e) => e.to_node_id === positionId && e.edge_type === "contradicts",
    );
    for (const e of supports) {
      const ev = index.get(e.from_node_id);
      if (ev) {
        evidence.push({ node: ev, role: "supports", targetPositionId: positionId });
      }
    }
    for (const e of contradicts) {
      const ev = index.get(e.from_node_id);
      if (ev) {
        evidence.push({ node: ev, role: "contradicts", targetPositionId: positionId });
      }
    }
    positions.push({
      node,
      stance,
      evidenceIds: supports.map((e) => e.from_node_id),
      contradictsIds: contradicts.map((e) => e.from_node_id),
      confidence: nodeConfidence(node),
    });
  }

  // Deliberation spine (root → deliberation via discussed_by).
  const deliberationEdge = within.find(
    (e) => e.edge_type === "discussed_by" && (e.from_node_id === root.id || e.to_node_id === root.id),
  );
  const deliberationId = deliberationEdge
    ? deliberationEdge.from_node_id === root.id
      ? deliberationEdge.to_node_id
      : deliberationEdge.from_node_id
    : null;
  const deliberation = deliberationId ? index.get(deliberationId) ?? null : null;

  const turns: TurnView[] = [];
  if (deliberation) {
    const turnEdges = within
      .filter((e) => e.edge_type === "discussed_by" && (e.from_node_id === deliberation.id || e.to_node_id === deliberation.id))
      .map((e) => (e.from_node_id === deliberation.id ? e.to_node_id : e.from_node_id));
    for (const turnId of turnEdges) {
      const node = index.get(turnId);
      if (node && node.node_type === "deliberation_turn") {
        turns.push({ node, marker: turnMarkerFromPayload(node) });
      }
    }
    // Stable order: keep fixture/creation order.
    turns.sort((a, b) => a.node.created_at.localeCompare(b.node.created_at));
  }

  // Escalation: deliberation (or dispute) → Director via escalated_to.
  let escalationTarget: ResearchGraphNode | null = null;
  let escalationRequires = false;
  const escalationEdge = within.find((e) => e.edge_type === "escalated_to");
  if (escalationEdge) {
    const target = index.get(escalationEdge.to_node_id);
    if (target) escalationTarget = target;
    escalationRequires = true;
  }

  // Decisions resolving this dispute (resolved_by/supersedes/invalidates).
  const decisions = nodes
    .filter((n) => n.node_type === "decision")
    .sort((a, b) => a.created_at.localeCompare(b.created_at));
  const current =
    decisions.find((d) => (d.status || "").toLowerCase() !== "superseded") ??
    decisions.at(-1) ??
    null;
  const history: DecisionView["history"] = decisions
    .filter((d) => d.id !== current?.id)
    .slice()
    .reverse()
    .map((d) => {
      const supersededBy = within.find(
        (e) => e.edge_type === "supersedes" && e.from_node_id === d.id,
      )?.to_node_id ?? null;
      const invalidatedBy = within.find(
        (e) => e.edge_type === "invalidates" && (e.from_node_id === d.id || e.to_node_id === d.id),
      )?.to_node_id ?? null;
      return { node: d, supersededBy, invalidatedBy };
    });

  return {
    root,
    lifecycle: disputeLifecycleBucket(root.status),
    blocking: (root.payload as { blocking?: unknown } | null)?.blocking === true,
    positions,
    // Fan overflow: >5 per row wraps; remainder collapses to +N (design §4/§8).
    overflowPositions: positions.length > 5 ? positions.slice(5) : [],
    evidence: dedupeEvidence(evidence),
    deliberation,
    turns,
    escalation: { target: escalationTarget, requires: escalationRequires },
    decision: { current, history },
  };
}

function dedupeEvidence(evidence: EvidenceView[]): EvidenceView[] {
  const seen = new Set<string>();
  const out: EvidenceView[] = [];
  for (const ev of evidence) {
    const key = ev.node.id;
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(ev);
  }
  return out;
}

/**
 * Non-color status/glyph for a node — always paired with a chip + label,
 * never color-only (design §6).
 */
export { isDisputeDomainNodeType, verdictFromPayload };
