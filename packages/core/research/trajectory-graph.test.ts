import { describe, expect, it } from "vitest";
import { trajectoryGraphFixture } from "./trajectory-fixture";
import { normalizeTrajectoryGraph } from "./trajectory-graph";

describe("normalizeTrajectoryGraph", () => {
  it("produces a stable topological DAG from shuffled lineage input", () => {
    const forward = normalizeTrajectoryGraph(trajectoryGraphFixture);
    const reverse = normalizeTrajectoryGraph({ ...trajectoryGraphFixture, nodes: [...trajectoryGraphFixture.nodes].reverse() });
    expect(forward).toEqual(reverse);
    expect(forward.commits.map((commit) => commit.id)).toEqual(["plan", "probe-a1", "probe-a2", "dead-end", "finding", "gate", "merge"]);
    expect(forward.commits.find((commit) => commit.id === "merge")?.parentIds).toEqual(["finding", "gate"]);
    expect(Object.fromEntries(forward.commits.map((commit) => [commit.id, commit.status]))).toMatchObject({
      "probe-a1": "failed", "probe-a2": "success", "dead-end": "detour",
      finding: "success", gate: "success", merge: "merged",
    });
  });

  it("deduplicates task/attempt/sequence while retaining every source id", () => {
    const finding = normalizeTrajectoryGraph(trajectoryGraphFixture).commits.find((commit) => commit.id === "finding");
    expect(finding?.sourceNodeIds).toEqual(["finding", "finding-shadow"]);
    expect(finding?.evidenceRefs).toEqual(["claim-1", "source-1"]);
  });

  it("keeps explicit, inferred and unknown relationships distinguishable", () => {
    const explicit = normalizeTrajectoryGraph(trajectoryGraphFixture);
    expect(explicit.commits.find((commit) => commit.id === "finding")?.parentRefs).toEqual([
      { id: "probe-a2", relationshipSource: "explicit", unknown: false },
      { id: "missing-upstream", relationshipSource: "explicit", unknown: true },
    ]);
    const legacy = normalizeTrajectoryGraph({ nodes: [
      { id: "b", actor_agent_id: "agent-a", created_at: "2026-01-01T00:00:01Z" },
      { id: "a", actor_agent_id: "agent-a", created_at: "2026-01-01T00:00:00Z" },
      { id: "other", actor_agent_id: "agent-b", created_at: "2026-01-01T00:00:00Z" },
    ], edges: [] });
    expect(legacy.relationshipIncomplete).toBe(true);
    expect(legacy.commits.find((commit) => commit.id === "b")?.parentRefs).toEqual([
      { id: "a", relationshipSource: "inferred_agent_sequence", unknown: false },
    ]);
    expect(legacy.commits.find((commit) => commit.id === "other")?.parentIds).toEqual([]);
  });

  it("degrades cycles, orphans, missing agents and equal timestamps without throwing", () => {
    const graph = normalizeTrajectoryGraph({ nodes: [
      { id: "b", parentIds: ["a"], timestamp: "bad", agentId: null },
      { id: "a", parentIds: ["b"], timestamp: "bad", agentId: null },
      { id: "orphan", parentIds: ["gone"], timestamp: "bad", agentId: null },
    ] });
    expect(graph.commits.map((commit) => commit.id)).toEqual(["orphan", "a", "b"]);
    expect(graph.warnings).toContain("cycle_detected");
    expect(graph.warnings).toContain("missing_agent");
    expect(graph.commits.find((commit) => commit.id === "orphan")?.unknownParentIds).toEqual(["gone"]);
  });

  it("returns an explicit fallback when schema parsing fails", () => {
    expect(normalizeTrajectoryGraph({ nodes: "not-an-array" })).toMatchObject({ commits: [], relationshipIncomplete: true, fallbackReason: "schema_parse_failed" });
  });
});
