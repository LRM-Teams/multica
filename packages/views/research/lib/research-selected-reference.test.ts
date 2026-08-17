import { describe, expect, it } from "vitest";
import type { ResearchGraphNode } from "@multica/core/types";
import { researchSelectedReferenceFromNode } from "./research-selected-reference";

const ENTITY_ID = "00000000-0000-4000-8000-000000000123";

function node(canonicalRef: Record<string, unknown>): ResearchGraphNode {
  return {
    id: "projection-node",
    session_id: "run",
    node_type: "insight",
    title: "Verified synthesis",
    summary: "",
    status: "accepted",
    actor_agent_id: null,
    payload: { canonical_ref: canonicalRef },
    created_at: "",
    updated_at: "",
  };
}

describe("researchSelectedReferenceFromNode", () => {
  it("uses the exact immutable canonical identity", () => {
    expect(
      researchSelectedReferenceFromNode(
        node({
          kind: "insight",
          id: ENTITY_ID,
          revision: 3,
          content_hash: `sha256:${"a".repeat(64)}`,
        }),
      ),
    ).toEqual({
      stable_id: `insight:${ENTITY_ID}`,
      kind: "insight",
      entity_id: ENTITY_ID,
      revision: 3,
      content_hash: `sha256:${"a".repeat(64)}`,
      display_summary: "Verified synthesis",
    });
  });

  it("never fabricates a reference when revision or hash is absent", () => {
    expect(
      researchSelectedReferenceFromNode(
        node({ kind: "insight", id: ENTITY_ID }),
      ),
    ).toBeNull();
  });
});
