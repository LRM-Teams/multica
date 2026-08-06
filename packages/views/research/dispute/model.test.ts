import { describe, expect, it } from "vitest";
import { DISPUTE_EDGES, DISPUTE_NODES } from "./__fixtures__/dispute-contract.fixture";
import { buildDisputeModel, findDisputeRoot } from "./model";

describe("buildDisputeModel (LRM-1472 §9 fixture)", () => {
  const model = buildDisputeModel(DISPUTE_NODES, DISPUTE_EDGES);

  it("resolves the dispute root", () => {
    expect(findDisputeRoot(DISPUTE_NODES)?.id).toBe("dispute-1");
    expect(model.root?.node_type).toBe("dispute");
  });

  it("reads the delivery-blocking flag from payload, not from status guess", () => {
    expect(model.blocking).toBe(true);
  });

  it("derives exactly the 3 positions with typed-edge stances", () => {
    expect(model.positions).toHaveLength(3);
    const stanceById = new Map(model.positions.map((p) => [p.node.id, p.stance]));
    expect(stanceById.get("pos-1")).toBe("supports");
    expect(stanceById.get("pos-2")).toBe("contradicts");
    expect(stanceById.get("pos-3")).toBe("conditional");
  });

  it("attributes evidence by supports/contradicts typed edges per position", () => {
    const p2 = model.positions.find((p) => p.node.id === "pos-2");
    // ev-2 + ev-3 support pos-2; ev-3 contradicts pos-1 (not pos-2).
    expect(p2?.evidenceIds).toContain("ev-2");
    expect(p2?.evidenceIds).toContain("ev-3");
    const supportingPos1 = model.evidence.some(
      (e) => e.node.id === "ev-1" && e.role === "supports" && e.targetPositionId === "pos-1",
    );
    expect(supportingPos1).toBe(true);
  });

  it("keeps a ≥3-turn deliberation spine in order", () => {
    expect(model.deliberation?.id).toBe("delib-1");
    expect(model.turns.length).toBeGreaterThanOrEqual(3);
    expect(model.turns.map((t) => t.node.id)).toEqual([
      "turn-1",
      "turn-2",
      "turn-3",
      "turn-4",
    ]);
    // deadlock surfaces the escalation need.
    expect(model.escalation.requires).toBe(true);
    expect(model.escalation.target?.node_type).toBe("agent_activity");
  });

  it("retains decision history with a live current verdict + superseded prior", () => {
    expect(model.decision.current?.id).toBe("decision-1");
    expect(model.decision.current?.status).toBe("current");
    expect(model.decision.history.length).toBeGreaterThanOrEqual(1);
    const prior = model.decision.history.find((h) => h.node.id === "decision-2");
    expect(prior).toBeDefined();
    expect(prior?.node.status).toBe("superseded");
    expect(model.lifecycle).toBe("open"); // dispute reopens to investigating
  });

  it("reopen: the later contradicting evidence invalidates the settled decision", () => {
    const invalidating = DISPUTE_EDGES.find(
      (e) => e.edge_type === "invalidates" && e.from_node_id === "ev-3",
    );
    expect(invalidating).toBeDefined();
    expect(invalidating?.to_node_id).toBe("decision-1");
  });
});
