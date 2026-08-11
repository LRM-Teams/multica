import { describe, expect, it } from "vitest";
import { parseWithFallback } from "../api/schema";
import {
  EMPTY_TYPED_GRAPH,
  TypedGraphResponseSchema,
  indexTypedGraphNodes,
} from "./graph-typed";
import type { TypedGraphResponse } from "./graph-typed";

/** A realistic LRM-1505 typed-graph payload with all typed fields present. */
function sampleTypedGraph(): Record<string, unknown> {
  return {
    session_id: "s1",
    graph_version: 7,
    nodes: [
      {
        id: "n1",
        node_type: "conclusion",
        title: "落地结论",
        summary: "第一轮融合的稳定结论",
        status: "done",
        actor_agent_id: "agent:lindberg",
        level: "xl",
        round: 2,
        cluster_id: "c1",
        confidence: 82,
        document_count: 5,
        conclusion_count: 2,
        goal_version_id: "gv1",
        derived_from: "n0",
        merged_from: ["n2", "n3"],
        superseded_by: null,
        restart_of: null,
        invalidated_by: null,
        parent_id: null,
        child_ids: [],
        children_of: [],
        created_at: "2026-08-10T00:00:00Z",
        updated_at: "2026-08-10T00:00:00Z",
      },
      {
        id: "n2",
        node_type: "claim",
        title: "分方向结论",
        status: "done",
        level: "m",
        round: 1,
        cluster_id: null,
        confidence: null,
        document_count: 2,
        conclusion_count: 1,
      },
    ],
    edges: [
      { id: "e1", from_node_id: "n2", to_node_id: "n1", edge_type: "derived" },
    ],
    clusters: [
      { id: "c1", name: "主题A", label: "主题A", level: "m", cluster_type: "topic" },
    ],
    lineage: {
      derived: { n1: "n0" },
      merged: { n1: ["n2", "n3"] },
      superseded: {},
      restarted: {},
      invalidated: {},
      supersedes: {},
    },
  };
}

describe("typed graph schema (LRM-1497 · fed by LRM-1505)", () => {
  it("parses a full typed-graph response with authoritative typed fields", () => {
    const parsed = parseWithFallback(
      sampleTypedGraph(),
      TypedGraphResponseSchema,
      EMPTY_TYPED_GRAPH,
      { endpoint: "test" },
    ) as TypedGraphResponse;

    expect(parsed.session_id).toBe("s1");
    expect(parsed.graph_version).toBe(7);
    expect(parsed.nodes).toHaveLength(2);
    expect(parsed.edges).toHaveLength(1);
    expect(parsed.clusters).toHaveLength(1);

    const n1 = parsed.nodes[0]!;
    // The typed LRM-1505 fields are authoritative and preserved verbatim.
    expect(n1.level).toBe("xl");
    expect(n1.round).toBe(2);
    expect(n1.cluster_id).toBe("c1");
    expect(n1.confidence).toBe(82);
    expect(n1.document_count).toBe(5);
    expect(n1.conclusion_count).toBe(2);
    expect(n1.derived_from).toBe("n0");
    expect(n1.merged_from).toEqual(["n2", "n3"]);
    expect(n1.actor_agent_id).toBe("agent:lindberg");

    expect(parsed.edges).toHaveLength(1);
    expect(parsed.edges[0]!.edge_type).toBe("derived");
    expect(parsed.clusters[0]!.cluster_type).toBe("topic");
    expect(parsed.lineage.merged.n1).toEqual(["n2", "n3"]);
  });

  it("defaults absent optional fields without fabricating values", () => {
    const parsed = parseWithFallback(
      { session_id: "s1", graph_version: 0, nodes: [], edges: [], clusters: [], lineage: {} },
      TypedGraphResponseSchema,
      EMPTY_TYPED_GRAPH,
      { endpoint: "test" },
    ) as TypedGraphResponse;

    expect(parsed.nodes).toEqual([]);
    expect(parsed.lineage.derived).toEqual({});
    // Defaults are inert, not invented confidence/rounds.
    expect(parsed.nodes).toHaveLength(0);
  });

  it("parses total_node_count when the graph is paginated", () => {
    const parsed = parseWithFallback(
      { ...sampleTypedGraph(), total_node_count: 10_042 },
      TypedGraphResponseSchema,
      EMPTY_TYPED_GRAPH,
      { endpoint: "test" },
    ) as TypedGraphResponse;
    expect(parsed.total_node_count).toBe(10_042);
  });

  it("falls back to EMPTY_TYPED_GRAPH on a non-object response", () => {
    const parsed = parseWithFallback(
      null,
      TypedGraphResponseSchema,
      EMPTY_TYPED_GRAPH,
      { endpoint: "test" },
    ) as TypedGraphResponse;
    expect(parsed.nodes).toEqual([]);
  });
});

describe("indexTypedGraphNodes", () => {
  it("indexes only real non-empty node ids", () => {
    const graph = parseWithFallback(
      sampleTypedGraph(),
      TypedGraphResponseSchema,
      EMPTY_TYPED_GRAPH,
      { endpoint: "test" },
    ) as TypedGraphResponse;
    const index = indexTypedGraphNodes(graph.nodes);
    expect(index.get("n1")?.level).toBe("xl");
    expect(index.get("n2")?.id).toBe("n2");
    expect(index.has("ghost")).toBe(false);
  });

  it("returns an empty map for an empty node list", () => {
    expect(indexTypedGraphNodes([]).size).toBe(0);
  });
});
