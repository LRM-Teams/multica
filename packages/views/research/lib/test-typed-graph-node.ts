import type { TypedGraphNode, TypedGraphCluster } from "@multica/core/research";

/** Minimal typed-graph node stub for unit tests (parsed fields filled by schema defaults at runtime). */
export function testTypedNode(
  partial: Record<string, unknown> & { id: string },
): TypedGraphNode {
  return partial as TypedGraphNode;
}

/** Minimal typed-graph cluster stub for unit tests. */
export function testTypedCluster(
  partial: Record<string, unknown> & { id: string },
): TypedGraphCluster {
  return partial as TypedGraphCluster;
}
