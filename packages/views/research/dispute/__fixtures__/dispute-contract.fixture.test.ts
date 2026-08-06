// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { ResearchGraphNode } from "@multica/core/types";
import { disputeLifecycleBucket } from "../../lib/node-visuals";
import { DISPUTE_EDGES, DISPUTE_NODES } from "./dispute-contract.fixture";

const byType = (t: ResearchGraphNode["node_type"]) =>
  new Set(DISPUTE_NODES.filter((n) => n.node_type === t).map((n) => n.id));

const edgeTypes = new Set(DISPUTE_EDGES.map((e) => e.edge_type));

describe("dispute contract fixture (LRM-1472 / UI-04 §9)", () => {
  it("contains exactly 3 positions with stance payloads (supports/contradicts/conditional)", () => {
    const positions = DISPUTE_NODES.filter((n) => n.node_type === "dispute_position");
    expect(positions).toHaveLength(3);
    const stances = positions.map((p) => (p.payload as { stance?: string }).stance).sort();
    expect(stances).toEqual(["conditional", "contradicts", "supports"]);
  });

  it("has ≥3 deliberation turns with progress markers and a deadlocked spine", () => {
    const turns = DISPUTE_NODES.filter((n) => n.node_type === "deliberation_turn");
    expect(turns.length).toBeGreaterThanOrEqual(3);
    for (const t of turns) {
      expect(["position_changed", "evidence_added", "scope_refined", "no_change"]).toContain(
        (t.payload as { marker?: string }).marker,
      );
    }
    const delib = DISPUTE_NODES.find((n) => n.node_type === "deliberation");
    expect(delib?.status).toBe("deadlocked");
  });

  it("has an escalation branch to the Research Director via lead_adjudication", () => {
    expect(edgeTypes.has("escalated_to")).toBe(true);
    const escalated = DISPUTE_EDGES.filter((e) => e.edge_type === "escalated_to");
    expect(escalated.length).toBeGreaterThanOrEqual(1);
    const directorId = escalated[0]!.to_node_id;
    const director = DISPUTE_NODES.find((n) => n.id === directorId);
    expect(director?.node_type).toBe("agent_activity");
    expect((director?.payload as { task_kind?: string }).task_kind).toBe("lead_adjudication");
  });

  it("preserves decision history: a current decision resolved_by + a superseded one", () => {
    const decisions = DISPUTE_NODES.filter((n) => n.node_type === "decision");
    expect(decisions.length).toBeGreaterThanOrEqual(2);
    expect(decisions.some((d) => d.status === "current")).toBe(true);
    expect(decisions.some((d) => d.status === "superseded")).toBe(true);
    expect(edgeTypes.has("resolved_by")).toBe(true);
    expect(edgeTypes.has("supersedes")).toBe(true);
  });

  it("reopen scenario is marked via invalidates/supersedes back to investigating", () => {
    const dispute = DISPUTE_NODES.find((n) => n.node_type === "dispute");
    expect(dispute?.status).toBe("investigating");
    expect(disputeLifecycleBucket(dispute?.status ?? "")).toBe("open");
    expect(edgeTypes.has("invalidates")).toBe(true);
  });

  it("covers the full LRM-1472 typed edge set referenced by the design", () => {
    for (const type of [
      "supports",
      "contradicts",
      "refines",
      "challenged_by",
      "discussed_by",
      "escalated_to",
      "resolved_by",
      "supersedes",
      "invalidates",
    ]) {
      expect(edgeTypes.has(type), type).toBe(true);
    }
  });

  it("all edges reference existing nodes on both ends", () => {
    const ids = new Set(DISPUTE_NODES.map((n) => n.id));
    for (const e of DISPUTE_EDGES) {
      expect(ids.has(e.from_node_id), `from ${e.from_node_id}`).toBe(true);
      expect(ids.has(e.to_node_id), `to ${e.to_node_id}`).toBe(true);
    }
  });

  // guard: this fixture is intentionally small to keep node budget under canvas caps
  it("stays under a bounded node budget (desktop canvas DOM ≤220)", () => {
    expect(DISPUTE_NODES.length).toBeLessThan(220);
    expect(byType("dispute").size).toBe(1);
  });
});
