import type { ResearchGraphNode } from "@multica/core/types";

/** System nodes open detail directly — no action ring (LRM-848). */
export const SYSTEM_NODE_TYPES = new Set([
  "roster_change",
  "stage_gate",
  "agent_activity",
]);

export type NodeRingAction =
  | "detail"
  | "locate_source"
  | "copy_prompt"
  | "retry"
  | "dig_deeper"
  | "more";

export function ringActionsForNode(node: ResearchGraphNode): {
  id: NodeRingAction;
  primary?: boolean;
  disabled?: boolean;
  candidate?: boolean;
}[] {
  const isDeadEnd = node.node_type === "dead_end";
  const hasSource =
    node.node_type === "finding" &&
    !!node.payload &&
    typeof node.payload === "object" &&
    "source_id" in (node.payload as object);

  return [
    { id: isDeadEnd ? "retry" : "detail", primary: true },
    { id: "locate_source", disabled: !hasSource },
    { id: "copy_prompt" },
    { id: isDeadEnd ? "detail" : "retry", disabled: !isDeadEnd },
    { id: "dig_deeper", candidate: true, disabled: true },
    { id: "more" },
  ];
}
