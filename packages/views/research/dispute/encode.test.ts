// @vitest-environment node
import { describe, expect, it } from "vitest";
import { createTestNode } from "./__fixtures__/dispute-test-node";
import {
  decisionIsSuperseded,
  disputeNodeGlyph,
  isDisputeDomainNodeType,
  stanceFromPayload,
  turnMarkerFromPayload,
  verdictFromPayload,
} from "./encode";

describe("dispute encode (LRM-1472 / UI-04)", () => {
  it("reads stance only from typed payload, returns null otherwise", () => {
    expect(stanceFromPayload(createTestNode({ node_type: "dispute_position", payload: { stance: "supports" } }))).toBe("supports");
    expect(stanceFromPayload(createTestNode({ node_type: "dispute_position", payload: { stance: "contradicts" } }))).toBe("contradicts");
    expect(stanceFromPayload(createTestNode({ node_type: "dispute_position", payload: { stance: "conditional" } }))).toBe("conditional");
    expect(stanceFromPayload(createTestNode({ node_type: "dispute_position", payload: {} }))).toBeNull();
    expect(stanceFromPayload(createTestNode({ node_type: "dispute_position", payload: { stance: "garbage" } }))).toBeNull();
  });

  it("reads deliberation turn marker from payload", () => {
    expect(turnMarkerFromPayload(createTestNode({ payload: { marker: "evidence_added" } }))).toBe("evidence_added");
    expect(turnMarkerFromPayload(createTestNode({ payload: { marker: "no_change" } }))).toBe("no_change");
    expect(turnMarkerFromPayload(createTestNode({ payload: {} }))).toBeNull();
    expect(turnMarkerFromPayload(createTestNode({ payload: { marker: "bogus" } }))).toBeNull();
  });

  it("reads decision verdict from payload", () => {
    expect(verdictFromPayload(createTestNode({ payload: { verdict: "conditionally_resolved" } }))).toBe("conditionally_resolved");
    expect(verdictFromPayload(createTestNode({ payload: { verdict: "obsolete" } }))).toBe("obsolete");
    expect(verdictFromPayload(createTestNode({ payload: {} }))).toBeNull();
  });

  it("assigns a stable glyph per dispute node type (never color-only)", () => {
    expect(disputeNodeGlyph("dispute", "open")).toBe("⚖");
    expect(disputeNodeGlyph("dispute_position", "proposed")).toBe("◆");
    expect(disputeNodeGlyph("deliberation", "deadlocked")).toBe("↻");
    expect(disputeNodeGlyph("deliberation_turn", "done")).toBe("·");
    expect(disputeNodeGlyph("decision", "current")).toBe("●");
    expect(disputeNodeGlyph("decision", "superseded")).toBe("↺");
    expect(disputeNodeGlyph("finding", "done")).toBe("");
  });

  it("classifies dispute-domain node types", () => {
    expect(isDisputeDomainNodeType("dispute")).toBe(true);
    expect(isDisputeDomainNodeType("decision")).toBe(true);
    expect(isDisputeDomainNodeType("finding")).toBe(false);
    expect(isDisputeDomainNodeType(undefined)).toBe(false);
  });

  it("flags superseded decision by canonical status", () => {
    expect(decisionIsSuperseded(createTestNode({ status: "superseded" }))).toBe(true);
    expect(decisionIsSuperseded(createTestNode({ status: "current" }))).toBe(false);
  });
});
