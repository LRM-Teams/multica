// @vitest-environment node
import { describe, expect, it } from "vitest";
import { adaptV6Snapshot } from "@multica/core/adapters";
import {
  twoComponentSnapshot,
  v6FixtureSnapshot,
} from "@multica/core/adapters/fixtures";
import { deterministicPositions, recomputeScoped } from "./positioner";

const baseView = () => {
  const snap = adaptV6Snapshot(v6FixtureSnapshot());
  return { nodes: snap.nodes, edges: snap.edges };
};

function afterTombstoneFixture() {
  const snap = twoComponentSnapshot();
  const a3 = snap.nodes.find((n) => n.id === "a3")!;
  const afterNodes = snap.nodes
    .filter((n) => n.id !== "a2")
    .concat({
      ...a3,
      id: "a4",
      kind: "claim",
      title: "A4 replacement",
      detailRef: "a4",
    });
  const afterEdges = snap.edges
    .filter((e) => e.id !== "ea1" && e.id !== "ea2")
    .concat({
      id: "ea3",
      from: "a1",
      to: "a4",
      relation: "produces",
      createdAt: "2026-08-01T00:00:00Z",
    });
  return { nodes: afterNodes, edges: afterEdges };
}

describe("deterministicPositions — (AC1) stable identity & position on recompute", () => {
  it("returns identical positions for an identical view", () => {
    const a = deterministicPositions(baseView());
    const b = deterministicPositions(baseView());
    expect(a.size).toBe(b.size);
    for (const [id, pa] of a) {
      expect(b.get(id)).toEqual(pa);
    }
  });

  it("positions every node (no node is left without geometry)", () => {
    const view = baseView();
    const positions = deterministicPositions(view);
    for (const n of view.nodes) expect(positions.has(n.id)).toBe(true);
  });

  it("positions depend only on the canonical node set, not on history", () => {
    const first = deterministicPositions(baseView());
    const second = deterministicPositions(baseView());
    expect(first).toEqual(second);
  });
});

describe("recomputeScoped — (AC2) visibility tombstone triggers local recompute only", () => {
  it("keeps unaffected component B at its exact prior positions", () => {
    const snap = twoComponentSnapshot();
    const prior = deterministicPositions({ nodes: snap.nodes, edges: snap.edges });
    const after = afterTombstoneFixture();
    const next = recomputeScoped(prior, after, ["a1", "a3"], ["a4"]);
    // Component B keeps positions verbatim — only component A was recomputed.
    expect(next.get("b1")).toEqual(prior.get("b1"));
    expect(next.get("b2")).toEqual(prior.get("b2"));
  });

  it("repositions the affected region deterministically", () => {
    const snap = twoComponentSnapshot();
    const prior = deterministicPositions({ nodes: snap.nodes, edges: snap.edges });
    const after = afterTombstoneFixture();
    const next = recomputeScoped(prior, after, ["a1", "a3"], ["a4"]);
    const nextAgain = recomputeScoped(prior, after, ["a1", "a3"], ["a4"]);
    expect(next).toEqual(nextAgain);
    expect(next.has("a4")).toBe(true);
  });
});
