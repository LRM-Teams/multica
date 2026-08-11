import { describe, expect, it } from "vitest";
import { diffTypedGraphLayout, scopeMotionEventsToLayoutDiff } from "./diff-typed-graph-layout";
import type { TypedGraphResponse } from "@multica/core/research";

const base = {
  session_id: "s1",
  graph_version: 1,
  nodes: [
    { id: "a", level: "l", cluster_id: "c1", parent_id: "root", status: "done" },
    { id: "b", level: "s", cluster_id: "c1", parent_id: "a", status: "running" },
  ],
  edges: [{ id: "e1", from_node_id: "a", to_node_id: "b", edge_type: "leads_to" }],
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

describe("diffTypedGraphLayout", () => {
  it("detects new nodes and neighbor roots", () => {
    const next = {
      ...base,
      graph_version: 2,
      nodes: [...base.nodes, { id: "c", level: "s", cluster_id: "c1", parent_id: "a", status: "queued" }],
      edges: [
        ...base.edges,
        { id: "e2", from_node_id: "a", to_node_id: "c", edge_type: "leads_to" },
      ],
    } as unknown as TypedGraphResponse;
    const diff = diffTypedGraphLayout(base, next);
    expect(diff.newNodeIds).toEqual(["c"]);
    expect(diff.affectedRootIds).toEqual(expect.arrayContaining(["a", "c"]));
  });

  it("detects layout signature changes", () => {
    const next = {
      ...base,
      nodes: [
        { ...base.nodes[0]!, cluster_id: "c2" },
        base.nodes[1]!,
      ],
    } as unknown as TypedGraphResponse;
    const diff = diffTypedGraphLayout(base, next);
    expect(diff.changedNodeIds).toEqual(["a"]);
    expect(diff.affectedRootIds).toEqual(expect.arrayContaining(["a", "b"]));
  });

  it("scopes motion events to the layout-affected subgraph", () => {
    const events = [
      {
        transition_kind: "branch_spawned" as const,
        related_ids: ["a", "far-away"],
        anchor_id: "a",
      },
      {
        transition_kind: "node_retired" as const,
        related_ids: ["removed"],
        anchor_id: null,
      },
    ];
    const scoped = scopeMotionEventsToLayoutDiff(events, {
      newNodeIds: ["c"],
      removedNodeIds: ["removed"],
      changedNodeIds: [],
      affectedRootIds: ["a", "c", "removed"],
    });
    expect(scoped).toHaveLength(2);
    expect(scoped[0]?.related_ids).toEqual(["a"]);
    expect(scoped[1]?.related_ids).toEqual(["removed"]);
  });
});
