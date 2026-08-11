import { describe, expect, it } from "vitest";
import type { InfiniteData } from "@tanstack/react-query";
import {
  normalizeWsGraphNode,
  patchTypedGraphInfiniteData,
} from "./typed-graph-cache";
import type { TypedGraphResponse } from "./graph-typed";

const emptyLineage = {
  derived: {},
  merged: {},
  superseded: {},
  restarted: {},
  invalidated: {},
  supersedes: {},
};

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

  it("upserts nodes without duplicating ids", () => {
    const data = makeInfinite([
      {
        session_id: "s1",
        graph_version: 3,
        total_node_count: 1,
        nodes: [{ id: "n1", title: "Old", node_type: "finding" }],
        edges: [],
        clusters: [],
        lineage: emptyLineage,
      } as unknown as TypedGraphResponse,
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

  it("signals resync when graph_version skips a frame", () => {
    const data = makeInfinite([
      {
        session_id: "s1",
        graph_version: 3,
        total_node_count: 0,
        nodes: [],
        edges: [],
        clusters: [],
        lineage: emptyLineage,
      } as TypedGraphResponse,
    ]);
    const result = patchTypedGraphInfiniteData(data, {
      node: { id: "n2", title: "Late", node_type: "probe" } as never,
      graphVersion: 10,
    });
    expect(result.needsResync).toBe(true);
    expect(result.patched).toBe(false);
  });
});
