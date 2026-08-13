import type { TypedGraphNode } from "@multica/core/research";
import type { ExecutionRow } from "../execution-overlay";

/**
 * Resolve the Agent row shown for an S node.
 *
 * Execution rows are normally projected from the current fleet. Historical
 * nodes can still name a canonical actor after that member was archived or
 * omitted from a partial fleet projection. Keep the node inspectable in that
 * case, but use `unknown` rather than inventing live execution facts.
 */
export function resolveAgentInspectorRow(
  rows: readonly ExecutionRow[],
  node: TypedGraphNode | null | undefined,
): ExecutionRow | null {
  const actorId = node?.actor_agent_id?.trim();
  if (!actorId) return null;

  const projected = rows.find((row) => row.id === actorId);
  if (projected) return projected;

  return {
    id: actorId,
    name: actorId,
    role: "Agent",
    initials: actorId.slice(0, 2).toUpperCase(),
    status: "unknown",
    actionKey: "unknown",
    currentNodeId: node.id,
    locationLabel: node.title,
  };
}
