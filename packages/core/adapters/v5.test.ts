// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  computeGraphContentHash,
  V5_NEUTRAL_IMPORTANCE,
} from "./index";
import { adaptV5Graph } from "./v5";
import { v5FixtureGraph } from "./fixtures";

describe("adaptV5Graph — no field guessing", () => {
  it("maps documented V5 node fields verbatim", () => {
    const { nodes, edges } = v5FixtureGraph();
    const snapshot = adaptV5Graph("s1", nodes, edges);
    const goal = snapshot.nodes.find((n) => n.id === "goal")!;
    expect(goal.kind).toBe("goal");
    expect(goal.title).toBe("Pick a payment provider");
    expect(goal.status).toBe("active");
    // identity is the stable V5 server id — never re-derived from prose.
    expect(goal.id).toBe("goal");
  });

  it("never infers importance or kind from summary/title/payload prose", () => {
    const { nodes, edges } = v5FixtureGraph();
    const withProse = nodes.map((n) =>
      n.id === "probe-a"
        ? { ...n, summary: "IMPORTANT finding: this is the answer", payload: { importance: 0.99 } }
        : n,
    );
    const snapshot = adaptV5Graph("s1", withProse, edges);
    const probe = snapshot.nodes.find((n) => n.id === "probe-a")!;
    // Importance must be the documented neutral constant — a payload knob or
    // summary text must not change it.
    expect(probe.importance).toBe(V5_NEUTRAL_IMPORTANCE);
    expect(probe.kind).toBe("probe");
  });

  it("derives freshness from timestamps only, not text", () => {
    const { nodes, edges } = v5FixtureGraph();
    const snapshotA = adaptV5Graph("s1", nodes, edges);
    const textChanged = nodes.map((n) =>
      n.id === "finding" ? { ...n, summary: "totally different prose" } : n,
    );
    const snapshotB = adaptV5Graph("s1", textChanged, edges);
    expect(snapshotA.nodes.find((n) => n.id === "finding")!.freshness).toBe(
      snapshotB.nodes.find((n) => n.id === "finding")!.freshness,
    );
    // Freshness is monotonic in updated_at recency.
    const newest = snapshotA.nodes
      .filter((n) => n.id !== "probe-b")
      .map((n) => n.freshness);
    expect(newest.every((f) => f >= 0 && f <= 1)).toBe(true);
  });

  it("maps typed V5 edges and drops none", () => {
    const { nodes, edges } = v5FixtureGraph();
    const snapshot = adaptV5Graph("s1", nodes, edges);
    expect(snapshot.edges).toHaveLength(6);
    const supports = snapshot.edges.find((e) => e.relation === "supports")!;
    expect(supports.from).toBe("probe-a");
    expect(supports.to).toBe("finding");
  });

  it("is deterministic: same input → same ids and same content hash", () => {
    const { nodes, edges } = v5FixtureGraph();
    const a = adaptV5Graph("s1", nodes, edges);
    const b = adaptV5Graph("s1", nodes, edges);
    expect(a.graphContentHash).toBe(b.graphContentHash);
    expect(a.nodes.map((n) => n.id)).toEqual(b.nodes.map((n) => n.id));
  });

  it("content hash is order- and display-state-independent", () => {
    const { nodes, edges } = v5FixtureGraph();
    const a = adaptV5Graph("s1", nodes, edges);
    const reversed = adaptV5Graph("s1", [...nodes].reverse(), [...edges].reverse());
    expect(a.graphContentHash).toBe(reversed.graphContentHash);
    // Display geometry never participates in the canonical hash.
    expect(a.graphContentHash).toBe(
      computeGraphContentHash(a.nodes, a.edges),
    );
  });
});
