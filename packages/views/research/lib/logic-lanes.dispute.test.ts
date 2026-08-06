// @vitest-environment node
import { describe, expect, it } from "vitest";
import { createTestNode } from "../dispute/__fixtures__/dispute-test-node";
import { laneForNode } from "./logic-lanes";

describe("logic-lanes · dispute subgraph (LRM-1472 §1.1)", () => {
  it("maps every dispute-domain node onto the validate lane", () => {
    for (const node_type of [
      "dispute",
      "dispute_position",
      "decision",
      "deliberation",
      "deliberation_turn",
    ] as const) {
      expect(laneForNode(createTestNode({ node_type })), node_type).toBe("validate");
    }
  });

  it("keeps conflict/refuted on validate (contention co-located)", () => {
    expect(laneForNode(createTestNode({ node_type: "conflict" }))).toBe("validate");
    expect(laneForNode(createTestNode({ node_type: "refuted" }))).toBe("validate");
  });
});
