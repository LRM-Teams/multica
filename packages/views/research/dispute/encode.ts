/**
 * LRM-1472 / UI-04 — dispute subgraph encode helpers.
 * Pure display logic keyed by typed edges + canonical node status. Never infers
 * support/contradiction from text, proximity, or animation; never mutates the graph.
 */

import type { ResearchGraphNode } from "@multica/core/types";

/** Dispute stance read from the incoming typed edge role — not from position text. */
export type DisputeStance = "supports" | "contradicts" | "conditional";

export function stanceFromPayload(node: ResearchGraphNode): DisputeStance | null {
  const payload = node.payload;
  if (payload && typeof payload === "object" && !Array.isArray(payload)) {
    const stance = (payload as { stance?: unknown }).stance;
    if (stance === "supports" || stance === "contradicts" || stance === "conditional") {
      return stance;
    }
  }
  return null;
}

export type DeliberationTurnMarker =
  | "position_changed"
  | "evidence_added"
  | "scope_refined"
  | "no_change";

export function turnMarkerFromPayload(node: ResearchGraphNode): DeliberationTurnMarker | null {
  const payload = node.payload;
  if (payload && typeof payload === "object" && !Array.isArray(payload)) {
    const marker = (payload as { marker?: unknown }).marker;
    if (
      marker === "position_changed" ||
      marker === "evidence_added" ||
      marker === "scope_refined" ||
      marker === "no_change"
    ) {
      return marker;
    }
  }
  return null;
}

export type DecisionVerdict =
  | "resolved"
  | "conditionally_resolved"
  | "irreducible"
  | "obsolete";

export function verdictFromPayload(node: ResearchGraphNode): DecisionVerdict | null {
  const payload = node.payload;
  if (payload && typeof payload === "object" && !Array.isArray(payload)) {
    const verdict = (payload as { verdict?: unknown }).verdict;
    if (
      verdict === "resolved" ||
      verdict === "conditionally_resolved" ||
      verdict === "irreducible" ||
      verdict === "obsolete"
    ) {
      return verdict;
    }
  }
  return null;
}

/**
 * Glyph prefix for each dispute-domain node (LRM-1472 multi-encoding §6).
 * Always paired with a type chip + status label — never color-only.
 */
export function disputeNodeGlyph(nodeType: string, status: string): string {
  switch (nodeType) {
    case "dispute":
      return "⚖";
    case "dispute_position":
      return "◆";
    case "deliberation":
      return "↻";
    case "deliberation_turn":
      return "·";
    case "decision":
      return normalizeDecisionGlyph(status);
    default:
      return "";
  }
}

function normalizeDecisionGlyph(status: string): string {
  const key = (status || "").toLowerCase();
  return key === "superseded" ? "↺" : "●";
}

const DISPUTE_NODE_TYPES = new Set([
  "dispute",
  "dispute_position",
  "deliberation",
  "deliberation_turn",
  "decision",
]);

export function isDisputeDomainNodeType(nodeType: string | undefined): boolean {
  return nodeType != null && DISPUTE_NODE_TYPES.has(nodeType);
}

/** Demand-only projection: a decision node is superseded when its status says so. */
export function decisionIsSuperseded(node: ResearchGraphNode): boolean {
  return (node.status || "").toLowerCase() === "superseded";
}
