import type {
  ResearchV6DirectorProjectionEdgeKind,
  ResearchV6DirectorProjectionNodeKind,
} from "../types/research-v6-director";

export interface ResearchV6DirectorKindDiagnostic {
  contract: "research-v6-director";
  field: "node.kind" | "edge.kind";
  received: string;
}

const NODE_KINDS = new Set<ResearchV6DirectorProjectionNodeKind>([
  "goal",
  "work_s",
  "result_s",
  "insight",
]);
const EDGE_KINDS = new Set<ResearchV6DirectorProjectionEdgeKind>([
  "derived_from",
  "absorbed_into",
  "produced_by",
  "belongs_to",
  "challenges",
  "collapsed_path",
]);

/** Lenient visual boundary; strict wire parsing remains fail-closed. */
export function resolveResearchV6DirectorNodeVisualKind(
  value: string,
): { kind: ResearchV6DirectorProjectionNodeKind | "generic"; diagnostic?: ResearchV6DirectorKindDiagnostic } {
  if (NODE_KINDS.has(value as ResearchV6DirectorProjectionNodeKind)) {
    return { kind: value as ResearchV6DirectorProjectionNodeKind };
  }
  return {
    kind: "generic",
    diagnostic: { contract: "research-v6-director", field: "node.kind", received: value },
  };
}

export function resolveResearchV6DirectorEdgeVisualKind(
  value: string,
): { kind: ResearchV6DirectorProjectionEdgeKind | "generic"; diagnostic?: ResearchV6DirectorKindDiagnostic } {
  if (EDGE_KINDS.has(value as ResearchV6DirectorProjectionEdgeKind)) {
    return { kind: value as ResearchV6DirectorProjectionEdgeKind };
  }
  return {
    kind: "generic",
    diagnostic: { contract: "research-v6-director", field: "edge.kind", received: value },
  };
}
