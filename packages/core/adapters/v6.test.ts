// @vitest-environment node
import { describe, expect, it } from "vitest";
import { adaptV6Delta, adaptV6Snapshot, v6NodeId } from "./v6";
import { v6FixtureDelta, v6FixtureSnapshot } from "./fixtures";

describe("adaptV6Snapshot — (§7.1) canonical projection contract", () => {
  const snapshot = adaptV6Snapshot(v6FixtureSnapshot());

  it("uses the canonical stable (run_id, entity_kind, entity_id) id", () => {
    const insight = snapshot.nodes.find((n) => n.kind === "insight")!;
    expect(insight.id).toBe(v6NodeId("run-v6-contract-fixture", "insight", "i1"));
    expect(insight.id).toBe("run-v6-contract-fixture:insight:i1");
    // Same canonical entity always maps to the same stable id.
    const again = adaptV6Snapshot(v6FixtureSnapshot());
    expect(again.nodes.find((n) => n.kind === "insight")!.id).toBe(insight.id);
  });

  it("copies importance and the freshness token verbatim from the projection", () => {
    const insight = snapshot.nodes.find((n) => n.kind === "insight")!;
    expect(insight.importance).toBe(0.9);
    // The canonical V6 freshness is an opaque server token (string) — carried
    // verbatim, never recomputed from prose.
    expect(insight.freshness).toBe("fresh:1");
    expect(snapshot.nodes.find((n) => n.kind === "question")!.importance).toBe(0.5);
  });

  it("degrades unknown future node_kind to generic without dropping it", () => {
    const future = snapshot.nodes.find((n) => n.id.includes("u1"))!;
    expect(future.kind).toBe("generic");
    expect(future.title).toBe("Future node");
    // It must still appear in the render layer (plan §7.1 — do not crash old clients).
    expect(snapshot.nodes.some((n) => n.id === future.id)).toBe(true);
  });

  it("preserves typed relations and resolves endpoints through canonical node ids", () => {
    const integrates = snapshot.edges.filter((e) => e.relation === "integrates");
    expect(integrates).toHaveLength(2);
    for (const e of integrates) {
      expect(e.to).toBe(v6NodeId("run-v6-contract-fixture", "insight", "i1"));
    }
    const contrad = snapshot.edges.find((e) => e.relation === "contradicts")!;
    expect(contrad.from).toContain("c1");
    expect(contrad.to).toContain("c2");
  });

  it("never guesses a research fact from a summary or title", () => {
    // detailRef is the canonical entity reference, never fabricated from prose.
    const claim = snapshot.nodes.find((n) => n.kind === "claim")!;
    expect(claim.detailRef).toBe("claim:c1");
  });
});

describe("adaptV6Delta — (§7.2) canonical delta framing", () => {
  it("maps sequence frame, upserts, tombstones and affected roots", () => {
    const delta = adaptV6Delta(v6FixtureDelta());
    expect(delta.fromSequenceExclusive).toBe(6);
    expect(delta.throughSequence).toBe(8);
    expect(delta.upsertNodes).toHaveLength(1);
    expect(delta.upsertNodes[0]!.id).toContain("c3");
    expect(delta.tombstoneNodeIds).toEqual([
      v6NodeId("run-v6-contract-fixture", "claim", "c1"),
    ]);
    expect(delta.tombstoneEdgeIds).toEqual(["e1", "e3", "e4", "e6"]);
    expect(delta.affectedRootIds).toEqual([
      v6NodeId("run-v6-contract-fixture", "question", "q1"),
      v6NodeId("run-v6-contract-fixture", "insight", "i1"),
    ]);
    expect(delta.transitionKind).toBe("insight_staled");
  });

  it("resolves new edge endpoints to canonical node ids", () => {
    const delta = adaptV6Delta(v6FixtureDelta());
    const refines = delta.upsertEdges.find((e) => e.relation === "refines")!;
    expect(refines.from).toContain("c3");
    expect(refines.to).toContain("i1");
  });
});
