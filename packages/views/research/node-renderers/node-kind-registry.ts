/** Display-only node-kind grouping used by the active star-graph adapter. */

export type NodeKindFamily =
  | "structure"
  | "execution"
  | "evidence"
  | "cognition"
  | "collaboration"
  | "governance"
  | "generic";

const NODE_KIND_TO_FAMILY: ReadonlyMap<string, NodeKindFamily> = new Map<
  string,
  NodeKindFamily
>([
  ["goal", "structure"],
  ["task", "structure"],
  ["search_plan", "structure"],
  ["branch", "structure"],
  ["attempt", "execution"],
  ["query_execution", "execution"],
  ["result_artifact", "evidence"],
  ["source_candidate", "evidence"],
  ["source_snapshot", "evidence"],
  ["observation", "evidence"],
  ["screening_decision", "evidence"],
  ["claim", "cognition"],
  ["question", "cognition"],
  ["hypothesis", "cognition"],
  ["insight", "cognition"],
  ["insight_derivation", "cognition"],
  ["decision", "cognition"],
  ["deliberation", "cognition"],
  ["deliberation_turn", "cognition"],
  ["dispute", "cognition"],
  ["dispute_position", "cognition"],
  ["evaluation_defect", "cognition"],
  ["team_formation", "collaboration"],
  ["team_membership", "collaboration"],
  ["integration_round", "collaboration"],
  ["integration_contribution", "collaboration"],
  ["divergence_pass", "collaboration"],
  ["capability_observation", "collaboration"],
  ["monitoring_cycle", "governance"],
  ["report_revision", "governance"],
  ["episode", "governance"],
]);

export function familyForNodeKind(rawKind: string): NodeKindFamily {
  return NODE_KIND_TO_FAMILY.get(rawKind) ?? "generic";
}
