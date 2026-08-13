import { describe, expect, it } from "vitest";
import type { InfiniteData } from "@tanstack/react-query";
import { parseWithFallback } from "../api/schema";
import {
  EMPTY_TYPED_GRAPH,
  TypedGraphResponseSchema,
} from "./graph-typed";
import type { TypedGraphResponse } from "./graph-typed";
import {
  normalizeWsGraphNode,
  patchTypedGraphInfiniteData,
} from "./typed-graph-cache";

const emptyLineage = {
  derived: {},
  merged: {},
  superseded: {},
  restarted: {},
  invalidated: {},
  supersedes: {},
};

function makePage(partial: Record<string, unknown>): TypedGraphResponse {
  return parseWithFallback(
    {
      total_node_count: null,
      nodes: [],
      edges: [],
      clusters: [],
      lineage: emptyLineage,
      ...partial,
    },
    TypedGraphResponseSchema,
    EMPTY_TYPED_GRAPH,
    { endpoint: "test" },
  ) as TypedGraphResponse;
}

function makeInfinite(pages: TypedGraphResponse[]): InfiniteData<TypedGraphResponse> {
  return {
    pages,
    pageParams: pages.map((_, index) => index * 800),
  };
}

describe("typed-graph-cache", () => {
  it("normalizes legacy snapshot nodes into typed nodes", () => {
    const node = normalizeWsGraphNode({
      id: "n1",
      session_id: "s1",
      node_type: "finding",
      title: "Finding",
      summary: "bounded",
      status: "done",
      actor_agent_id: null,
      payload: { details: { result: "evidence-backed" } },
      created_at: "",
      updated_at: "",
    });
    expect(node?.id).toBe("n1");
    expect(node?.payload).toEqual({ details: { result: "evidence-backed" } });
  });

  it("rejects malformed partial fields instead of replacing them with defaults", () => {
    expect(
      normalizeWsGraphNode({
        id: "n1",
        title: "Finding",
        confidence: "not-a-number",
      }),
    ).toBeNull();
  });

  it("upserts nodes without duplicating ids", () => {
    const data = makeInfinite([
      makePage({
        session_id: "s1",
        graph_version: 3,
        total_node_count: 1,
        nodes: [{ id: "n1", title: "Old", node_type: "finding" }],
      }),
    ]);
    const result = patchTypedGraphInfiniteData(data, {
      node: { id: "n1", title: "New", node_type: "finding", payload: { details: { x: 1 } } } as never,
      graphVersion: 4,
    });
    expect(result.patched).toBe(true);
    expect(result.data?.pages[0]?.nodes).toHaveLength(1);
    expect(result.data?.pages[0]?.nodes[0]?.title).toBe("New");
    expect(result.data?.pages[0]?.graph_version).toBe(4);
  });

  it("preserves canonical fields omitted by a legacy partial patch", () => {
    const data = makeInfinite([
      makePage({
        session_id: "s1",
        graph_version: 3,
        nodes: [
          {
            id: "n1",
            title: "Old",
            status: "completed",
            level: "xl",
            confidence: 88,
            child_ids: ["child"],
          },
        ],
      }),
    ]);
    const result = patchTypedGraphInfiniteData(data, {
      node: { id: "n1", title: "Renamed" },
      graphVersion: 4,
    });

    expect(result.data?.pages[0]?.nodes[0]).toMatchObject({
      title: "Renamed",
      status: "completed",
      level: "xl",
      confidence: 88,
      child_ids: ["child"],
    });
  });

  it("applies explicit empty and null values from a canonical patch", () => {
    const data = makeInfinite([
      makePage({
        session_id: "s1",
        graph_version: 3,
        nodes: [
          {
            id: "n1",
            title: "Node",
            confidence: 88,
            child_ids: ["child"],
            merged_from: ["input"],
          },
        ],
      }),
    ]);
    const result = patchTypedGraphInfiniteData(data, {
      node: {
        id: "n1",
        confidence: null,
        child_ids: [],
        merged_from: [],
      },
      graphVersion: 4,
    });

    expect(result.data?.pages[0]?.nodes[0]).toMatchObject({
      confidence: null,
      child_ids: [],
      merged_from: [],
    });
  });

  it("updates fields on an existing edge instead of ignoring the patch", () => {
    const data = makeInfinite([
      makePage({
        session_id: "s1",
        graph_version: 3,
        edges: [
          {
            id: "e1",
            from_node_id: "n1",
            to_node_id: "n2",
            edge_type: "supports",
          },
        ],
      }),
    ]);
    const result = patchTypedGraphInfiniteData(data, {
      edge: {
        id: "e1",
        from_node_id: "n1",
        to_node_id: "n2",
        edge_type: "contradicts",
      },
      graphVersion: 4,
    });

    expect(result.data?.pages[0]?.edges).toHaveLength(1);
    expect(result.data?.pages[0]?.edges[0]?.edge_type).toBe("contradicts");
  });

  it("signals resync when graph_version skips a frame", () => {
    const data = makeInfinite([
      makePage({
        session_id: "s1",
        graph_version: 3,
        total_node_count: 0,
        nodes: [],
      }),
    ]);
    const result = patchTypedGraphInfiniteData(data, {
      node: { id: "n2", title: "Late", node_type: "probe" } as never,
      graphVersion: 10,
    });
    expect(result.needsResync).toBe(true);
    expect(result.patched).toBe(false);
  });

  it("ignores a delayed graph event without overwriting current facts", () => {
    const data = makeInfinite([
      makePage({
        session_id: "s1",
        graph_version: 8,
        nodes: [{ id: "n1", title: "Current", node_type: "finding" }],
      }),
    ]);
    const result = patchTypedGraphInfiniteData(data, {
      node: { id: "n1", title: "Stale", node_type: "finding" },
      graphVersion: 7,
      sessionId: "s1",
    });

    expect(result).toMatchObject({ patched: true, needsResync: false });
    expect(result.data?.pages[0]?.nodes[0]?.title).toBe("Current");
    expect(result.data?.pages[0]?.graph_version).toBe(8);
  });

  it("requests resync instead of applying a cross-session node", () => {
    const data = makeInfinite([
      makePage({ session_id: "s1", graph_version: 3, nodes: [] }),
    ]);
    const result = patchTypedGraphInfiniteData(data, {
      node: {
        id: "foreign",
        session_id: "s2",
        title: "Other session",
        node_type: "finding",
      },
      graphVersion: 4,
      sessionId: "s1",
    });

    expect(result).toEqual({ patched: false, needsResync: true });
  });

  it("requests resync instead of applying a cross-session edge", () => {
    const data = makeInfinite([
      makePage({ session_id: "s1", graph_version: 3, nodes: [] }),
    ]);
    const result = patchTypedGraphInfiniteData(data, {
      edge: {
        id: "foreign-edge",
        session_id: "s2",
        from_node_id: "n1",
        to_node_id: "n2",
        edge_type: "supports",
      },
      graphVersion: 4,
      sessionId: "s1",
    });

    expect(result).toEqual({ patched: false, needsResync: true });
  });
});
