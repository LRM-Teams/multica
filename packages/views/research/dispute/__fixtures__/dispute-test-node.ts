import type { ResearchGraphNode } from "@multica/core/types";

/** Minimal factory for dispute encode/layout tests. */
export function createTestNode(
  overrides: Partial<ResearchGraphNode> = {},
): ResearchGraphNode {
  return {
    id: "n-test",
    session_id: "s-test",
    node_type: "finding",
    title: "node",
    summary: "",
    status: "done",
    actor_agent_id: null,
    payload: {},
    created_at: "2026-08-06T00:00:00Z",
    updated_at: "2026-08-06T00:00:00Z",
    ...overrides,
  };
}
