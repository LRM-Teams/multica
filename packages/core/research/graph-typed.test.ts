import { describe, expect, it } from "vitest";
import { parseWithFallback } from "../api/schema";
import {
  EMPTY_TYPED_GRAPH,
  TypedGraphResponseSchema,
  indexTypedGraphNodes,
  mergeTypedGraphPages,
  selectTypedGraphRootCandidateId,
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

describe("mergeTypedGraphPages", () => {
  it("dedupes nodes and merges lineage maps across pages", () => {
    const page1 = parseWithFallback(
      {
        session_id: "s1",
        graph_version: 3,
        total_node_count: 4,
        nodes: [
          { id: "n1", title: "A", level: "m" },
          { id: "n2", title: "B", level: "s" },
        ],
        edges: [{ from_node_id: "n1", to_node_id: "n2", edge_type: "derived" }],
        clusters: [{ id: "c1", name: "Cluster" }],
        lineage: { merged: { n1: ["n2"] }, derived: {}, superseded: {}, restarted: {}, invalidated: {}, supersedes: {} },
      },
      TypedGraphResponseSchema,
      EMPTY_TYPED_GRAPH,
      { endpoint: "test" },
    ) as TypedGraphResponse;
    const page2 = parseWithFallback(
      {
        session_id: "s1",
        graph_version: 4,
        total_node_count: 4,
        nodes: [
          { id: "n2", title: "B-updated", level: "s" },
          { id: "n3", title: "C", level: "l" },
        ],
        edges: [{ from_node_id: "n2", to_node_id: "n3", edge_type: "derived" }],
        clusters: [{ id: "c2", name: "Cluster 2" }],
        lineage: { merged: { n3: ["n2"] }, derived: {}, superseded: {}, restarted: {}, invalidated: {}, supersedes: {} },
      },
      TypedGraphResponseSchema,
      EMPTY_TYPED_GRAPH,
      { endpoint: "test" },
    ) as TypedGraphResponse;

    const merged = mergeTypedGraphPages([page1, page2]);
    expect(merged.graph_version).toBe(4);
    expect(merged.total_node_count).toBe(4);
    expect(merged.nodes.map((n) => n.id).sort()).toEqual(["n1", "n2", "n3"]);
    expect(merged.nodes.find((n) => n.id === "n2")?.title).toBe("B");
    expect(merged.edges).toHaveLength(2);
    expect(merged.clusters.map((c) => c.id).sort()).toEqual(["c1", "c2"]);
    expect(merged.lineage.merged.n1).toEqual(["n2"]);
    expect(merged.lineage.merged.n3).toEqual(["n2"]);
  });

  it("evicts oldest page nodes when merged cache exceeds the node budget", () => {
    // Partial fixtures for merge/budget behavior only — cast via unknown so
    // strict tsc does not require a full TypedGraphNode for every stub row.
    const makePage = (offset: number, count: number): TypedGraphResponse =>
      ({
        session_id: "s1",
        graph_version: 1,
        total_node_count: 1600,
        nodes: Array.from({ length: count }, (_, i) => ({
          id: `n${offset + i}`,
          title: `N${offset + i}`,
          level: "s",
        })),
        edges: [],
        clusters: [],
        lineage: {
          derived: {},
          merged: {},
          superseded: {},
          restarted: {},
          invalidated: {},
          supersedes: {},
        },
      }) as unknown as TypedGraphResponse;

    const merged = mergeTypedGraphPages([makePage(0, 800), makePage(800, 800)], {
      nodeBudget: 1000,
    });
    expect(merged.nodes.length).toBeLessThanOrEqual(1000);
    // The same root candidate used by layout stays mounted across page eviction.
    expect(merged.nodes.some((node) => node.id === "n0")).toBe(true);
    expect(merged.nodes.some((node) => node.id === "n1")).toBe(false);
    expect(merged.nodes.some((node) => node.id === "n1599")).toBe(true);
    expect(merged.total_node_count).toBe(1600);
  });

  it("keeps pinned nodes when trimming the merged cache", () => {
    const page = {
      session_id: "s1",
      graph_version: 1,
      total_node_count: 5,
      nodes: Array.from({ length: 5 }, (_, i) => ({
        id: `n${i}`,
        title: `N${i}`,
        level: "s",
      })),
      edges: [],
      clusters: [],
      lineage: {
        derived: {},
        merged: {},
        superseded: {},
        restarted: {},
        invalidated: {},
        supersedes: {},
      },
    } as unknown as TypedGraphResponse;

    const merged = mergeTypedGraphPages([page], {
      nodeBudget: 2,
      pinNodeIds: ["n0"],
    });
    expect(merged.nodes.map((node) => node.id)).toContain("n0");
    expect(merged.nodes.length).toBeLessThanOrEqual(2);
  });

  it("never lets protected ids exceed the hard cache budget", () => {
    const page = {
      session_id: "s1",
      graph_version: 1,
      total_node_count: 5,
      nodes: Array.from({ length: 5 }, (_, i) => ({
        id: `n${i}`,
        title: `N${i}`,
        level: "s",
      })),
      edges: [],
      clusters: [],
      lineage: EMPTY_TYPED_GRAPH.lineage,
    } as unknown as TypedGraphResponse;

    const merged = mergeTypedGraphPages([page], {
      nodeBudget: 2,
      pinNodeIds: ["n1", "n2", "n3"],
    });
    expect(merged.nodes.map((node) => node.id)).toEqual(["n0", "n1"]);
  });
});

describe("selectTypedGraphRootCandidateId", () => {
  it("uses the same XXL-first precedence as the star layout", () => {
    const nodes = [
      {
        id: "a",
        level: "m",
        cluster_id: null,
        parent_id: null,
        derived_from: null,
      },
      { id: "z", level: "xxl", cluster_id: "c1", parent_id: "a" },
    ] as unknown as TypedGraphResponse["nodes"];
    expect(selectTypedGraphRootCandidateId(nodes)).toBe("z");
  });

  it("falls back to an ungrouped structural root, then stable id order", () => {
    const structural = [
      { id: "b", level: "m", cluster_id: "c1", parent_id: "a" },
      {
        id: "a",
        level: "m",
        cluster_id: null,
        parent_id: null,
        derived_from: null,
      },
    ] as unknown as TypedGraphResponse["nodes"];
    expect(selectTypedGraphRootCandidateId(structural)).toBe("a");

    const cyclic = [
      { id: "z", level: "m", cluster_id: "c1", parent_id: "a" },
      { id: "a", level: "m", cluster_id: "c1", parent_id: "z" },
    ] as unknown as TypedGraphResponse["nodes"];
    expect(selectTypedGraphRootCandidateId(cyclic)).toBe("a");
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

describe("typed graph strict client boundary", () => {
  it("rejects malformed graph_version so GET /graph/typed can surface query errors", () => {
    const result = TypedGraphResponseSchema.safeParse({
      session_id: "s1",
      graph_version: "bad",
      nodes: [],
      edges: [],
      clusters: [],
      lineage: {},
    });
    expect(result.success).toBe(false);
  });
});
